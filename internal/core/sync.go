package core

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"github.com/sparkstonepdx/nook/internal/caddyfile"
	"github.com/sparkstonepdx/nook/internal/config"
	"github.com/sparkstonepdx/nook/internal/envfile"
	"github.com/sparkstonepdx/nook/internal/grants"
	"github.com/sparkstonepdx/nook/internal/names"
	"github.com/sparkstonepdx/nook/internal/precompress"
	"github.com/sparkstonepdx/nook/internal/redirects"
	"github.com/sparkstonepdx/nook/internal/tarx"
)

// dirFD opens a directory without following a symlink at its last component.
type dirFD struct{ fd int }

func openDirNoFollow(path string) (*dirFD, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	return &dirFD{fd}, nil
}

func (d *dirFD) close() { syscall.Close(d.fd) }

const oPath = 0x200000 // O_PATH (same value on amd64 and arm64)

// fstatat(fd, name, AT_SYMLINK_NOFOLLOW) via openat(O_PATH|O_NOFOLLOW) + fstat: describes a symlink itself, never its target.
func lstatAt(dirfd int, name string) (*syscall.Stat_t, error) {
	fd, err := syscall.Openat(dirfd, name, oPath|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	defer syscall.Close(fd)
	var st syscall.Stat_t
	if err := syscall.Fstat(fd, &st); err != nil {
		return nil, err
	}
	return &st, nil
}

func (d *dirFD) lstat(name string) (*syscall.Stat_t, error) { return lstatAt(d.fd, name) }

// lstatIn checks name/child relative to d without following either component.
func (d *dirFD) lstatIn(name, child string) (*syscall.Stat_t, error) {
	fd, err := syscall.Openat(d.fd, name, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	defer syscall.Close(fd)
	return lstatAt(fd, child)
}

func (d *dirFD) names() []string {
	f := os.NewFile(uintptr(dupFD(d.fd)), "dir")
	defer f.Close()
	list, _ := f.Readdirnames(-1)
	sort.Strings(list)
	return list
}

func dupFD(fd int) int { n, _ := syscall.Dup(fd); return n }

func (d *dirFD) unlink(name string) { syscall.Unlinkat(d.fd, name) }

func isDir(st *syscall.Stat_t) bool  { return st.Mode&syscall.S_IFMT == syscall.S_IFDIR }
func isFile(st *syscall.Stat_t) bool { return st.Mode&syscall.S_IFMT == syscall.S_IFREG }

// SyncAll syncs every app.
func (h *Host) SyncAll() error {
	unlock, err := Lock()
	if err != nil {
		return err
	}
	defer unlock()
	var firstErr error
	for _, a := range Apps() {
		if err := h.syncLocked(a); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// Sync regenerates one app's routes and services.
func (h *Host) Sync(app string) error {
	unlock, err := Lock()
	if err != nil {
		return err
	}
	defer unlock()
	return h.syncLocked(app)
}

func otherCaddyFiles(app string) map[string]string {
	out := map[string]string{}
	entries, _ := os.ReadDir(config.CaddyAppsDir)
	for _, e := range entries {
		n := strings.TrimSuffix(e.Name(), ".caddy")
		if n == e.Name() || n == app {
			continue
		}
		if b, err := os.ReadFile(filepath.Join(config.CaddyAppsDir, e.Name())); err == nil {
			out[n] = string(b)
		}
	}
	return out
}

func claimedByOther(others map[string]string, host string) string {
	for app, text := range others {
		live, _ := caddyfile.Hosts(text)
		for _, l := range live {
			if l == host {
				return app
			}
		}
	}
	return ""
}

func (h *Host) syncLocked(app string) error {
	u, err := LookupApp(app)
	if err != nil {
		h.say("%s: %v", app, err)
		return err
	}
	h.S = config.Load()
	dash := config.LoadDashboard()
	gl, _ := grants.Load(config.GrantsFile)
	for _, d := range []string{config.EnvDir, config.StateDir} {
		os.MkdirAll(d, 0o700)
	}
	os.MkdirAll(filepath.Join(config.WWWDir, app), 0o755)
	logDir := filepath.Join(config.LogDir, app)
	os.MkdirAll(logDir, 0o750)
	os.Chown(logDir, 0, u.GID)
	os.Chmod(logDir, 0o750)

	envChanged := h.importUserEnv(u)

	sitesPath := filepath.Join(u.Home, "sites")
	sites, err := openDirNoFollow(sitesPath)
	if err != nil {
		h.say("%s: can't open sites/ (%v); nothing published", app, err)
		sites = nil
	}
	if sites != nil {
		defer sites.close()
	}

	others := otherCaddyFiles(app)
	var published []caddyfile.Site
	hosts := map[string]bool{}
	seen, cronSeen := map[string]bool{}, map[string]bool{}
	svc := appServices{Needs: map[string][]string{}, Provides: map[string]map[string]ProvideSpec{}}
	svcKeep := false // a manifest couldn't be read: keep the last record rather than forget its services
	var cronChanged, cronRunNow []string
	reload := false
	var restartUnits []string

	var entries []string
	if sites != nil {
		entries = sites.names()
	}
	restartMarks := map[string]bool{}
	for _, name := range entries {
		if strings.HasPrefix(name, ".restart-") {
			restartMarks[strings.TrimPrefix(name, ".restart-")] = true
		}
	}
	for _, s := range entries {
		if strings.HasPrefix(s, ".") {
			continue
		}
		st, err := sites.lstat(s)
		if err != nil {
			continue
		}
		if !isDir(st) {
			if st.Mode&syscall.S_IFMT == syscall.S_IFLNK {
				h.say("%s: sites/%s is a symlink, skipped", app, s)
			}
			continue
		}
		if !names.Site(s) {
			h.say("%s: '%s' is not a valid subdomain or hostname, skipped", app, s)
			continue
		}
		run, rerr := sites.lstatIn(s, "run")
		runs := rerr == nil && isFile(run) && run.Mode&0o111 != 0
		kind := "static"
		var man Manifest
		if runs {
			m, err := u.readManifest(s)
			if err != nil {
				h.say("%s: %s: %v; left as it was", app, s, err)
				seen[s], cronSeen[s] = true, true // keep whatever runs now
				svcKeep = true
				continue
			}
			man, kind = m, m.Kind
			if len(m.Needs) > 0 {
				svc.Needs[s] = m.Needs
			}
			if len(m.Provides) > 0 {
				svc.Provides[s] = m.Provides
			}
		}
		if kind != "cron" && fileExists(filepath.Join("/etc/systemd/system", CronUnit(app, s)+".timer")) {
			h.stopCron(app, s)
			reload = true
		}
		switch kind {
		case "cron":
			if _, err := os.Stat(filepath.Join("/etc/systemd/system", UnitName(app, s))); err == nil {
				h.stopUnit(app, s)
			}
			changed, err := h.writeCron(u, s, man)
			if err != nil {
				h.say("%s: %s: %v", app, s, err)
				continue
			}
			seen[s], cronSeen[s] = true, true
			if changed {
				cronChanged = append(cronChanged, s)
			}
			if restartMarks[s] {
				cronRunNow = append(cronRunNow, s)
			}
			h.say("%s: cron %s (%s)", app, s, man.calendar)
			continue
		case "service":
			port, err := h.allocPort(app, s)
			if err != nil {
				h.say("%s: %s: %v", app, s, err)
				continue
			}
			changed, err := h.writeUnit(u, s, "", port, man.Health)
			if err != nil {
				h.say("%s: %s: %v", app, s, err)
				continue
			}
			seen[s] = true
			reload = reload || changed
			idFile := filepath.Join(config.StateDir, app+"--"+s+".id")
			id := strconv.FormatUint(st.Ino, 10)
			old, _ := os.ReadFile(idFile)
			if changed || string(old) != id || restartMarks[s] || envChanged {
				restartUnits = append(restartUnits, UnitName(app, s))
				os.WriteFile(idFile, []byte(id), 0o600)
			}
			h.say("%s: service %s on port %d (no hostname)", app, s, port)
			continue
		}
		host := names.HostOf(s, h.S.BaseDomain)
		if why := h.refuse(app, host, dash.Host, gl, others); why != "" {
			h.say("%s: %s, skipped", app, why)
			continue
		}
		// a real site beats another app's automatic redirect
		for other, text := range others {
			if t, found := caddyfile.WithoutRedirect(text, host); found {
				config.WriteAtomic(filepath.Join(config.CaddyAppsDir, other+".caddy"), []byte(t), 0o644)
				others[other] = t
			}
		}
		seen[s], hosts[host] = true, true

		if runs {
			port, err := h.allocPort(app, s)
			if err != nil {
				h.say("%s: %s: %v", app, s, err)
				continue
			}
			changed, err := h.writeUnit(u, s, host, port, man.Health)
			if err != nil {
				h.say("%s: %s: %v", app, s, err)
				continue
			}
			reload = reload || changed
			idFile := filepath.Join(config.StateDir, app+"--"+s+".id")
			id := strconv.FormatUint(st.Ino, 10)
			old, _ := os.ReadFile(idFile)
			if changed || string(old) != id || restartMarks[s] || envChanged {
				restartUnits = append(restartUnits, UnitName(app, s))
				os.WriteFile(idFile, []byte(id), 0o600)
			}
			published = append(published, caddyfile.Site{Host: host, Port: port, Folder: s, Version: id})
		} else {
			if _, err := os.Stat(filepath.Join("/etc/systemd/system", UnitName(app, s))); err == nil {
				h.stopUnit(app, s)
				reload = true
			}
			root := filepath.Join(config.WWWDir, app, s)
			if err := h.snapshot(u, s, st); err != nil {
				h.say("%s: %s: %v", app, s, err)
				if _, e := os.Stat(root); e != nil {
					continue
				}
			}
			// static sites read nook.json only for cors; a bad file publishes the site without it
			m, err := u.readManifest(s)
			if err != nil {
				h.say("%s: %s: %v; published without cors", app, s, err)
			}
			rules := readRules(root, func(why string) { h.say("%s: %s: _redirects %s, skipped", app, s, why) })
			published = append(published, caddyfile.Site{Host: host, Root: root, Folder: s, Version: strconv.FormatUint(st.Ino, 10), CORS: err == nil && m.CORS, Rules: rules})
		}
	}
	if sites != nil {
		for s := range restartMarks {
			sites.unlink(".restart-" + s)
		}
	}

	// root domains without their own www folder: www.<domain> redirects to <domain>
	var redirects [][2]string
	for host := range hosts {
		w := "www." + host
		if !gl.IsApex(app, host) || hosts[w] || !gl.Allowed(app, w) || claimedByOther(others, w) != "" {
			continue
		}
		redirects = append(redirects, [2]string{w, host})
	}
	sort.Slice(redirects, func(i, j int) bool { return redirects[i][0] < redirects[j][0] })

	// running sites whose folder is gone
	for _, s := range RunningSites(app) {
		if !seen[s] {
			h.stopUnit(app, s)
			reload = true
		}
	}
	if !svcKeep && recordServices(app, svc) {
		// a provider's offer changed: refresh what its consumers see
		for _, bd := range readBindings() {
			if pa, _ := splitAppProvider(bd.Provider); pa == app {
				writeConsumerEnv(bd.App)
			}
		}
		applyFirewall()
	}
	for s := range svc.Provides {
		writeProviderEnv(app, s)
	}
	// cron jobs whose folder is gone
	for _, s := range CronJobs(app) {
		if !cronSeen[s] {
			h.stopCron(app, s)
			reload = true
		}
	}
	// static copies whose site is gone or now runs
	if copies, err := os.ReadDir(filepath.Join(config.WWWDir, app)); err == nil {
		for _, c := range copies {
			if !seen[c.Name()] || PortOf(app, c.Name()) > 0 || cronSeen[c.Name()] {
				os.RemoveAll(filepath.Join(config.WWWDir, app, c.Name()))
				os.Remove(filepath.Join(config.StateDir, app+"--"+c.Name()+".static"))
			}
		}
	}
	if reload || len(cronChanged) > 0 {
		systemctl("daemon-reload")
	}
	for _, s := range cronChanged {
		systemctl("enable", CronUnit(app, s)+".timer")
		systemctl("restart", CronUnit(app, s)+".timer")
	}
	for _, s := range cronRunNow {
		systemctl("start", "--no-block", CronUnit(app, s)+".service")
	}
	for _, unit := range restartUnits {
		systemctl("enable", unit)
		systemctl("restart", unit)
	}

	sort.Slice(published, func(i, j int) bool { return published[i].Host < published[j].Host })
	out := filepath.Join(config.CaddyAppsDir, app+".caddy")
	text := caddyfile.Render(sitesPath, published, redirects)
	old, oldErr := os.ReadFile(out)
	os.MkdirAll(config.CaddyAppsDir, 0o755)
	if err := config.WriteAtomic(out, []byte(text), 0o644); err != nil {
		return err
	}
	if msg, err := Run("caddy", "validate", "--config", config.Caddyfile); err != nil {
		if oldErr == nil {
			config.WriteAtomic(out, old, 0o644)
		} else {
			os.Remove(out)
		}
		h.say("%s: generated Caddy config failed to validate; kept the previous one\n%s", app, lastLines(msg, 5))
		return fmt.Errorf("caddy validate failed")
	}
	if !bytes.Equal(old, []byte(text)) {
		systemctl("reload", "caddy")
	}
	live, red := caddyfile.Hosts(text)
	if len(live) == 0 {
		h.say("%s: no sites", app)
	}
	for _, l := range live {
		h.say("%s: live https://%s", app, l)
	}
	for _, r := range red {
		h.say("%s: redirect https://%s", app, r)
	}
	return nil
}

func (h *Host) refuse(app, host, dashHost string, gl grants.List, others map[string]string) string {
	if dashHost != "" && host == dashHost {
		return host + " is the dashboard's address"
	}
	for _, r := range h.S.Reserved {
		if host == r+"."+h.S.BaseDomain {
			return host + " is reserved"
		}
	}
	if !gl.Allowed(app, host) {
		return "not allowed to use " + host + " (add a grant on the dashboard or with nook-admin grant)"
	}
	if other := claimedByOther(others, host); other != "" {
		return host + " is already used by another app"
	}
	return ""
}

// importUserEnv copies the app's own secrets.env (written by its deploys) into a root-owned file,
// reading it as the app user. Returns true when it changed.
func (h *Host) importUserEnv(u *AppUser) bool {
	data, _ := u.AsApp(64<<10, "internal-read-env")
	v := envfile.Filter(envfile.Parse(string(data)))
	path := filepath.Join(config.EnvDir, u.Name+".user.env")
	old, oldErr := os.ReadFile(path)
	if v.Len() == 0 {
		if oldErr == nil {
			os.Remove(path)
			return true
		}
		return false
	}
	text := envfile.Format(v, "copied by nook-admin from "+u.Home+"/secrets.env; edit that file (or use nook-admin env), not this one")
	if oldErr == nil && string(old) == text {
		return false
	}
	config.WriteAtomic(path, []byte(text), 0o600)
	return true
}

// snapshot makes the root-owned copy Caddy serves. `nook internal-pack` runs as the app user and resolves
// symlinks with the app's own permissions; root only unpacks plain files and folders.
// readRules reads _redirects from a site's published copy (root-owned: tarx unpacked it, and it refuses links).
func readRules(root string, skipped func(string)) []redirects.Rule {
	p := filepath.Join(root, "_redirects")
	fi, err := os.Lstat(p)
	if err != nil || !fi.Mode().IsRegular() {
		return nil
	}
	if fi.Size() > redirects.MaxBytes {
		skipped(fmt.Sprintf("is over %d KB", redirects.MaxBytes>>10))
		return nil
	}
	b, err := os.ReadFile(p)
	if err != nil {
		skipped(err.Error())
		return nil
	}
	rules, why := redirects.Parse(string(b))
	for _, w := range why {
		skipped(w)
	}
	return rules
}

func (h *Host) snapshot(u *AppUser, site string, st *syscall.Stat_t) error {
	app := u.Name
	dst := filepath.Join(config.WWWDir, app, site)
	idFile := filepath.Join(config.StateDir, app+"--"+site+".static")
	id := fmt.Sprintf("%d:%d", st.Ino, st.Mtim.Sec)
	if old, err := os.ReadFile(idFile); err == nil && string(old) == id {
		if _, err := os.Stat(dst); err == nil {
			return nil
		}
	}
	data, err := u.AsApp(h.S.SiteMaxBytes+(64<<20), "internal-pack", filepath.Join(u.Home, "sites", site), strconv.FormatInt(h.S.SiteMaxBytes, 10))
	if err != nil {
		return fmt.Errorf("couldn't copy the site (%v); kept the previous copy", err)
	}
	tmp := filepath.Join(config.WWWDir, app, ".new-"+site)
	os.RemoveAll(tmp)
	if err := tarx.Extract(bytes.NewReader(data), tmp, h.S.SiteMaxBytes); err != nil {
		os.RemoveAll(tmp)
		return fmt.Errorf("couldn't copy the site (%v); kept the previous copy", err)
	}
	// nook.json is settings, not content: it stays in the app's folder and is never served
	os.Remove(filepath.Join(tmp, "nook.json"))
	// gzip sidecars, so Caddy serves text files compressed without compressing them on each request
	if _, err := precompress.Dir(tmp); err != nil {
		h.say("%s: %s: compressing: %v; served as they are where it failed", app, site, err)
	}
	os.RemoveAll(dst + ".old")
	os.Rename(dst, dst+".old")
	if err := os.Rename(tmp, dst); err != nil {
		return err
	}
	os.RemoveAll(dst + ".old")
	return os.WriteFile(idFile, []byte(id), 0o600)
}

func (h *Host) writeUnit(u *AppUser, site, host string, port int, health string) (bool, error) {
	app := u.Name
	path := filepath.Join("/etc/systemd/system", UnitName(app, site))
	log := filepath.Join(config.LogDir, app, site+".log")
	desc, siteHost := host, " SITE_HOST="+host
	if host == "" { // a service: no hostname
		desc, siteHost = site+" (service)", ""
	}
	healthLine := ""
	if health != "" { // read back by the dashboard and the deploy API (SiteHealth)
		healthLine = "X-Nook-Health=" + health + "\n"
	}
	text := fmt.Sprintf(`[Unit]
Description=%[3]s (app %[1]s)
After=nook-firewall.service
%[10]s
[Service]
User=nook-%[1]s
Group=nook-%[1]s
Slice=nook-%[1]s.slice
WorkingDirectory=%[4]s/sites/%[2]s
ExecStart=%[4]s/sites/%[2]s/run
Environment=PORT=%[5]d HOST=127.0.0.1 SITE=%[2]s%[6]s
%[8]sRestart=always
RestartSec=2
StandardOutput=append:%[7]s
StandardError=append:%[7]s
# a single file can't fill the disk
LimitFSIZE=2G
%[9]s
[Install]
WantedBy=multi-user.target
`, app, site, desc, u.Home, port, siteHost, log, envLines(app, site),
		sandbox(u.Home, app, fmt.Sprintf("SocketBindAllow=tcp:%d\nSocketBindDeny=any\n", port)), healthLine)
	old, _ := os.ReadFile(path)
	if string(old) == text {
		return false, nil
	}
	return true, config.WriteAtomic(path, []byte(text), 0o644)
}

// sandbox is the confinement every app process gets; bind says which ports it may listen on.
func sandbox(home, app, bind string) string {
	return `# network: may only listen on its own port (if any); ordinary sockets only
` + bind + `RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6
# sandbox: only its own app folder is visible and writable
TemporaryFileSystem=` + config.AppsDir + `:ro
BindPaths=` + home + `
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
SystemCallArchitectures=native
CapabilityBoundingSet=
`
}

// envLines is the unit's EnvironmentFile lines, general to specific (see env.go).
func envLines(app, site string) string {
	var b strings.Builder
	for _, f := range envFiles(app, site) {
		b.WriteString("EnvironmentFile=-" + f + "\n")
	}
	return b.String()
}

func (h *Host) stopUnit(app, site string) {
	unit := UnitName(app, site)
	systemctl("disable", "--now", unit)
	os.Remove(filepath.Join("/etc/systemd/system", unit))
	os.Remove(filepath.Join(config.StateDir, app+"--"+site+".id"))
	dropPort(app, site)
	dropSiteEnv(app, site)
}

func lastLines(s string, n int) string {
	l := strings.Split(strings.TrimSpace(s), "\n")
	if len(l) > n {
		l = l[len(l)-n:]
	}
	return strings.Join(l, "\n")
}
