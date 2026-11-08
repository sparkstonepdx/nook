// Package install sets up (or upgrades) a Debian box: binaries, users, units, Caddy, sshd, migrations.
// Everything here is idempotent; `app-host update` runs it again after installing a new version.
package install

import (
	_ "embed"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/sparkstonepdx/app-host/internal/config"
	"github.com/sparkstonepdx/app-host/internal/core"
	"github.com/sparkstonepdx/app-host/internal/grants"
	"github.com/sparkstonepdx/app-host/internal/web"
)

//go:embed catchall.html
var catchallHTML []byte

type Opts struct {
	BaseDomain, SSHHost string
	Upgrade, NoApt      bool
	Out                 io.Writer
}

type step struct {
	out io.Writer
	n   int
}

func (s *step) say(format string, a ...any) { s.n++; fmt.Fprintf(s.out, "==> "+format+"\n", a...) }

func run(name string, args ...string) error {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
	return nil
}

func have(bin string) bool { _, err := exec.LookPath(bin); return err == nil }

func write(path, text string, mode os.FileMode) bool {
	old, err := os.ReadFile(path)
	if err == nil && string(old) == text {
		return false
	}
	os.MkdirAll(filepath.Dir(path), 0o755)
	config.WriteAtomic(path, []byte(text), mode)
	return true
}

// publicIPv4 is the address this box uses to reach the internet (what a VPS's public IP usually is).
func publicIPv4() string {
	c, err := net.Dial("udp4", "1.1.1.1:53")
	if err != nil {
		return ""
	}
	defer c.Close()
	return c.LocalAddr().(*net.UDPAddr).IP.String()
}

func Run(o Opts) error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("run as root")
	}
	st := &step{out: o.Out}

	if !o.Upgrade && !o.NoApt {
		var missing []string
		for bin, pkg := range map[string]string{"caddy": "caddy", "rsync": "rsync", "ssh-keygen": "openssh-client", "sshd": "openssh-server"} {
			if !have(bin) && !(bin == "sshd" && fileExists("/usr/sbin/sshd")) {
				missing = append(missing, pkg)
			}
		}
		if len(missing) > 0 {
			st.say("installing %s", strings.Join(missing, " "))
			if err := run("apt-get", "update", "-qq"); err != nil {
				return err
			}
			if err := run("apt-get", append([]string{"install", "-y", "-qq"}, missing...)...); err != nil {
				return err
			}
		}
	}

	// --- binaries: install this one and its `app` sibling ---
	self, _ := os.Executable()
	self, _ = filepath.EvalSymlinks(self)
	if self != config.HostBin {
		st.say("installing binaries")
		if err := copyFile(self, config.HostBin, 0o700); err != nil {
			return err
		}
		sibling := filepath.Join(filepath.Dir(self), "app")
		if err := copyFile(sibling, config.AppBin, 0o755); err != nil {
			return fmt.Errorf("the `app` binary must sit next to app-host: %v", err)
		}
	}
	// --- config ---
	st.say("config")
	for _, d := range []string{"/etc/apps", config.StateDir, config.EnvDir} {
		os.MkdirAll(d, 0o700)
		os.Chmod(d, 0o700)
	}
	for _, d := range []string{config.SSHKeysDir, config.CaddyAppsDir, config.AppsDir, config.LogDir, config.WWWDir, "/var/lib/app-host"} {
		os.MkdirAll(d, 0o755)
	}
	if !fileExists(config.ConfigFile) {
		base := o.BaseDomain
		if base == "" {
			base = "sparkstonepdx.com"
		}
		ip := o.SSHHost
		if ip == "" {
			ip = publicIPv4()
		}
		write(config.ConfigFile, fmt.Sprintf(`BASE_DOMAIN=%s
SSH_HOST=%s
# running apps get ports from this range
PORT_MIN=20000
PORT_MAX=29999
# subdomains of the default domain no app can claim
RESERVED_SUBDOMAINS="www mail admin"
# default limits for everything one app runs
APP_MEMORY_MAX=256M
APP_CPU_QUOTA=50%%
APP_TASKS_MAX=256
# largest static site, in bytes
SITE_MAX_BYTES=1073741824
`, base, ip), 0o600)
	}
	for _, f := range []string{config.PortsFile, config.GrantsFile} {
		if !fileExists(f) {
			write(f, "", 0o600)
		}
	}
	if !fileExists(config.DashboardEnv) {
		write(config.DashboardEnv, "DASHBOARD_PORT=19999\n", 0o600)
	}
	if !fileExists(config.UpdateEnv) {
		write(config.UpdateEnv, "# GITHUB_TOKEN=<read-only token for the app-host repo, only needed while it's private>\n# UPDATE_NOTIFY_URL=https://ntfy.sh/<your-topic>   (or a Discord/Slack webhook)\n", 0o600)
	}
	s := config.Load()

	// grants for apps made before grants existed
	gl, _ := grants.Load(config.GrantsFile)
	if len(gl) == 0 {
		for _, a := range core.Apps() {
			gl = append(gl, grants.Grant{App: a, Kind: "sub", Name: s.BaseDomain})
		}
		config.WriteAtomic(config.GrantsFile, []byte(gl.Text()), 0o600)
	}

	// --- dashboard's own user, and its data moved out of /etc/apps ---
	st.say("dashboard user")
	if _, err := user.Lookup(config.WebUser); err != nil {
		if err := run("useradd", "--system", "--no-create-home", "--home-dir", config.WebStateDir, "--shell", "/usr/sbin/nologin", config.WebUser); err != nil {
			return err
		}
	}
	wu, _ := user.Lookup(config.WebUser)
	wuid, _ := strconv.Atoi(wu.Uid)
	wgid, _ := strconv.Atoi(wu.Gid)
	os.MkdirAll(config.WebStateDir, 0o700)
	os.Chown(config.WebStateDir, wuid, wgid)
	for _, f := range []string{"users.json", "secret"} {
		oldPath, newPath := filepath.Join("/etc/apps/dashboard", f), filepath.Join(config.WebStateDir, f)
		if fileExists(oldPath) && !fileExists(newPath) {
			copyFile(oldPath, newPath, 0o600)
			fmt.Fprintf(o.Out, "    moved dashboard %s to %s\n", f, config.WebStateDir)
		}
	}
	if _, err := web.OpenStore(config.WebStateDir); err != nil { // makes the secret if missing
		return err
	}
	chownTree(config.WebStateDir, wuid, wgid)
	// app-host: root and the dashboard user may run it; app users can't
	os.Chown(config.HostBin, 0, wgid)
	os.Chmod(config.HostBin, 0o750)

	// --- units ---
	st.say("services")
	units := map[string]string{
		"app-host-helper.service": helperUnit, "app-host-web.service": webUnit,
		"app-sync@.path": syncPath, "app-sync@.service": syncService,
		"app-host-update-check.service": updateService, "app-host-update-check.timer": updateTimer,
		"app-host-logrotate.service": logrotateService, "app-host-logrotate.timer": logrotateTimer,
	}
	for name, text := range units {
		write(filepath.Join("/etc/systemd/system", name), text, 0o644)
	}
	write("/etc/logrotate.d/apps", logrotateConf, 0o644)
	write("/etc/update-motd.d/90-app-host", "#!/bin/sh\n[ -f /var/lib/app-host/update-available ] && cat /var/lib/app-host/update-available\nexit 0\n", 0o755)
	// the old bash/Node version
	exec.Command("systemctl", "disable", "--now", "app-dashboard.service").Run()
	os.Remove("/etc/systemd/system/app-dashboard.service")
	for _, old := range []string{"app-create", "app-sync", "app-delete", "app-grant", "app-env", "app-admin-caddy", "app-dashboard-invite"} {
		os.Remove(filepath.Join("/usr/local/sbin", old))
	}
	os.RemoveAll("/usr/local/lib/app-host")

	// --- sshd: app users can only deploy ---
	st.say("ssh: deploy keys can only upload and run `app site`")
	for _, a := range core.Apps() {
		keyFile := filepath.Join(config.SSHKeysDir, "app-"+a)
		if fileExists(keyFile) {
			continue
		}
		if old := filepath.Join("/etc/apps/ssh", "app-"+a); fileExists(old) { // an earlier pre-release spot
			copyFile(old, keyFile, 0o644)
			continue
		}
		u, err := core.LookupApp(a)
		if err != nil {
			continue
		}
		// the old layout kept the key in the app's ~/.ssh; read it as the app user
		if b, err := u.AsApp(64<<10, "internal-read-keys"); err == nil && len(strings.TrimSpace(string(b))) > 0 {
			write(keyFile, string(b), 0o644)
		}
	}
	sshdChanged := write("/etc/ssh/sshd_config.d/50-app-host.conf", sshdConf, 0o644)
	if sshdChanged {
		if err := run("sshd", "-t"); err != nil {
			os.Remove("/etc/ssh/sshd_config.d/50-app-host.conf")
			fmt.Fprintf(o.Out, "    sshd rejected the app-host settings, left them out: %v\n", err)
		} else {
			exec.Command("systemctl", "reload", "ssh").Run()
		}
	}

	// --- Caddy ---
	st.say("caddy")
	caddyRestart := hardenCaddy(o.Out)
	if !fileExists("/etc/caddy/catchall/index.html") {
		write("/etc/caddy/catchall/index.html", string(catchallHTML), 0o644)
	}
	write("/etc/caddy/catchall.caddy", catchallCaddy, 0o644)
	ensureImport("/etc/caddy/catchall.caddy")
	ensureImport(config.CaddyAppsDir + "/*.caddy")

	// --- lock down app folders from the old layout ---
	for _, a := range core.Apps() {
		home := filepath.Join(config.AppsDir, a)
		os.Chmod(home, 0o700)
		if fi, err := os.Lstat(filepath.Join(home, "sites")); err == nil && fi.IsDir() {
			os.Chmod(filepath.Join(home, "sites"), 0o700)
		}
	}

	exec.Command("systemctl", "daemon-reload").Run()
	if caddyRestart {
		run("systemctl", "restart", "caddy")
	} else {
		exec.Command("systemctl", "reload", "caddy").Run()
	}
	for _, u := range []string{"app-host-helper.service", "app-host-web.service", "app-host-update-check.timer", "app-host-logrotate.timer"} {
		exec.Command("systemctl", "enable", u).Run()
		if !o.Upgrade || strings.HasSuffix(u, ".timer") {
			exec.Command("systemctl", "restart", u).Run()
		}
	}
	for _, a := range core.Apps() {
		exec.Command("systemctl", "enable", "--now", "app-sync@"+a+".path").Run()
	}
	h := core.New(o.Out)
	if err := h.AdminCaddy(); err != nil {
		fmt.Fprintf(o.Out, "    %v\n", err)
	}
	h.Out = io.Discard
	h.SyncAll()

	if o.Upgrade {
		return nil
	}
	fmt.Fprintf(o.Out, "\napp-host is installed (default domain %s, server address %s).\n", s.BaseDomain, s.SSHHost)
	store, _ := web.OpenStore(config.WebStateDir)
	chownTree(config.WebStateDir, wuid, wgid)
	if !store.HasUsers() {
		d := config.LoadDashboard()
		fmt.Fprintf(o.Out, "\nOpen this to create your admin login (one use, 24 hours). The certificate is self-signed, so accept the warning:\n  https://%s:%s/invite?t=%s\nThen set the dashboard's address on its Domains page. Lost it? Run: app-host invite\n", s.SSHHost, d.IPPort, store.MakeInvite(""))
	}
	sshdAdvice(o.Out)
	return nil
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }

func copyFile(src, dst string, mode os.FileMode) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	tmp := dst + ".new"
	os.Remove(tmp)
	if err := os.WriteFile(tmp, b, mode); err != nil {
		return err
	}
	os.Chmod(tmp, mode)
	return os.Rename(tmp, dst)
}

func chownTree(dir string, uid, gid int) {
	filepath.Walk(dir, func(p string, fi os.FileInfo, err error) error {
		if err == nil && fi.Mode()&os.ModeSymlink == 0 {
			os.Chown(p, uid, gid)
		}
		return nil
	})
}

func ensureImport(path string) {
	b, _ := os.ReadFile(config.Caddyfile)
	for _, l := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(l) == "import "+path {
			return
		}
	}
	config.WriteAtomic(config.Caddyfile, []byte(strings.TrimRight(string(b), "\n")+"\n\nimport "+path+"\n"), 0o644)
}

var debianDefault = regexp.MustCompile(`(?ms)^:80\s*\{.*?^\}\n?`)

// hardenCaddy moves the admin API to a unix socket and drops Debian's :80 welcome page. Returns true when
// Caddy must restart (a reload can't move the admin endpoint).
func hardenCaddy(out io.Writer) bool {
	write("/etc/systemd/system/caddy.service.d/app-host.conf", "[Service]\nRuntimeDirectory=caddy\nRuntimeDirectoryMode=0750\n", 0o644)
	b, err := os.ReadFile(config.Caddyfile)
	if err != nil {
		b = []byte{}
	}
	text := string(b)
	changed, restart := false, false
	if debianDefault.MatchString(text) {
		text = debianDefault.ReplaceAllString(text, "")
		changed = true
		fmt.Fprintln(out, "    removed Debian's default :80 site")
	}
	if !strings.Contains(text, "admin unix//run/caddy/admin.sock") {
		lines := strings.Split(text, "\n")
		inserted := false
		for i, l := range lines {
			t := strings.TrimSpace(l)
			if t == "" || strings.HasPrefix(t, "#") {
				continue
			}
			if t == "{" { // existing global options block
				lines = append(lines[:i+1], append([]string{"\tadmin unix//run/caddy/admin.sock"}, lines[i+1:]...)...)
				inserted = true
			}
			break
		}
		if inserted {
			text = strings.Join(lines, "\n")
		} else {
			text = "{\n\tadmin unix//run/caddy/admin.sock\n}\n\n" + text
		}
		changed, restart = true, true
		fmt.Fprintln(out, "    moved Caddy's admin API off localhost:2019 to a root-only socket")
	}
	if changed {
		config.WriteAtomic(config.Caddyfile+".bak", b, 0o644)
		config.WriteAtomic(config.Caddyfile, []byte(text), 0o644)
	}
	return restart
}

func sshdAdvice(out io.Writer) {
	b, err := exec.Command("sshd", "-T").Output()
	if err != nil {
		return
	}
	t := string(b)
	if strings.Contains(t, "\npasswordauthentication yes") || strings.Contains(t, "\npermitrootlogin yes") {
		fmt.Fprint(out, `
Heads up: SSH still allows password logins. Once your key works, turn them off:
  printf 'PasswordAuthentication no\nPermitRootLogin prohibit-password\n' > /etc/ssh/sshd_config.d/10-no-passwords.conf
  sshd -t && systemctl reload ssh
`)
	}
}

const sshdConf = `# app-host: app users' keys live in /etc/ssh/app-host-keys (root-owned), and every login goes through the gate,
# which only allows an rsync upload into ~/builds/<name>/ and "app site ..." commands.
Match User app-*
	AuthorizedKeysFile /etc/ssh/app-host-keys/%u
	ForceCommand /usr/local/bin/app ssh-gate
	AllowTcpForwarding no
	AllowStreamLocalForwarding no
	AllowAgentForwarding no
	X11Forwarding no
	PermitTunnel no
	PermitTTY no
	PermitUserRC no
	PasswordAuthentication no
`

const helperUnit = `[Unit]
Description=app-host root helper (the dashboard's fixed set of admin operations)
After=network.target

[Service]
ExecStart=/usr/local/sbin/app-host helper
Restart=always
RuntimeDirectory=app-host
RuntimeDirectoryMode=0750

[Install]
WantedBy=multi-user.target
`

const webUnit = `[Unit]
Description=app-host dashboard (unprivileged)
After=app-host-helper.service
Wants=app-host-helper.service

[Service]
User=app-host-web
Group=app-host-web
ExecStart=/usr/local/sbin/app-host web
Restart=always
ReadWritePaths=/var/lib/app-host-web
ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes
PrivateDevices=yes
NoNewPrivileges=yes
ProtectKernelTunables=yes
ProtectKernelModules=yes
ProtectKernelLogs=yes
ProtectControlGroups=yes
ProtectClock=yes
ProtectHostname=yes
RestrictSUIDSGID=yes
RestrictNamespaces=yes
RestrictRealtime=yes
LockPersonality=yes
MemoryDenyWriteExecute=yes
SystemCallArchitectures=native
SystemCallFilter=@system-service
CapabilityBoundingSet=
RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6
MemoryMax=96M

[Install]
WantedBy=multi-user.target
`

const syncPath = `[Unit]
Description=Watch /srv/apps/%i/sites for deploys

[Path]
PathModified=/srv/apps/%i/sites
Unit=app-sync@%i.service

[Install]
WantedBy=multi-user.target
`

const syncService = `[Unit]
Description=Sync app %i

[Service]
Type=oneshot
ExecStart=/usr/local/sbin/app-host sync %i
`

const updateService = `[Unit]
Description=Check for app-host updates

[Service]
Type=oneshot
ExecStart=/usr/local/sbin/app-host update --check --notify
`

const updateTimer = `[Unit]
Description=Check for app-host updates daily

[Timer]
OnCalendar=daily
RandomizedDelaySec=4h
Persistent=true

[Install]
WantedBy=timers.target
`

const logrotateService = `[Unit]
Description=Rotate app logs

[Service]
Type=oneshot
ExecStart=/usr/sbin/logrotate /etc/logrotate.d/apps
`

const logrotateTimer = `[Unit]
Description=Rotate app logs hourly (an app can't fill the disk with logs between daily runs)

[Timer]
OnCalendar=hourly
Persistent=true

[Install]
WantedBy=timers.target
`

const logrotateConf = `/var/log/apps/*/*.log {
	size 10M
	rotate 3
	compress
	missingok
	notifempty
	copytruncate
}
`

const catchallCaddy = `# generated by app-host: unknown hostnames on port 80 get /etc/caddy/catchall/index.html (404).
# Known sites still redirect to HTTPS; edit the page, not this file.
:80 {
	error 404
	handle_errors {
		root * /etc/caddy/catchall
		rewrite * /index.html
		file_server
	}
}
`
