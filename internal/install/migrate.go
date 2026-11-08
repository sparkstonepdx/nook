package install

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"

	"github.com/sparkstonepdx/nook/internal/config"
)

// The layout before the rename to nook (both the bash and the first Go version used it).
const (
	oldEtc      = "/etc/apps"
	oldSrv      = "/srv/apps"
	oldLog      = "/var/log/apps"
	oldLib      = "/var/lib/app-host"
	oldWebState = "/var/lib/app-host-web"
	oldKeys     = "/etc/ssh/app-host-keys"
	oldWebUser  = "app-host-web"
)

// migrateFromAppHost renames app-host's users, folders and units to nook's. It does nothing on a fresh box or
// one that's already migrated, so it's safe on every install.
func migrateFromAppHost(st *step, out io.Writer) error {
	oldApps := oldAppUsers()
	_, webErr := user.Lookup(oldWebUser)
	if !fileExists(oldEtc) && !fileExists(oldSrv) && len(oldApps) == 0 && webErr != nil {
		return nil
	}
	st.say("moving the app-host layout to nook (%d apps)", len(oldApps))

	// stop everything that runs as an old user or holds the old paths open
	units := []string{"app-host-helper.service", "app-host-web.service", "app-host-update-check.timer", "app-host-logrotate.timer", "app-dashboard.service"}
	for _, pat := range []string{"site-*.service", "app-sync@*.path", "app-sync@*.service"} {
		if b, err := exec.Command("systemctl", "list-units", "--all", "--plain", "--no-legend", pat).Output(); err == nil {
			for _, l := range strings.Split(string(b), "\n") {
				if f := strings.Fields(l); len(f) > 0 {
					units = append(units, f[0])
				}
			}
		}
	}
	for _, u := range units {
		exec.Command("systemctl", "disable", "--now", u).Run()
	}
	for _, a := range oldApps { // anything else left running as the app (usermod refuses while it is)
		exec.Command("pkill", "-KILL", "-u", "app-"+a).Run()
	}
	exec.Command("pkill", "-KILL", "-u", oldWebUser).Run()

	// sshd: lock both old and new user names to the gate before any user is renamed
	if write("/etc/ssh/sshd_config.d/50-nook.conf", sshdConf, 0o644) {
		exec.Command("systemctl", "reload", "ssh").Run()
	}
	os.Remove("/etc/ssh/sshd_config.d/50-app-host.conf")

	// folders: move whole trees; a destination that already exists means a half-done run, so merge into it.
	// App folders are app-owned, so they're only ever renamed as a whole, never walked into as root.
	for _, m := range []struct {
		from, to string
		deep     bool
	}{
		{oldEtc, "/etc/nook", true}, {oldSrv, config.AppsDir, false}, {oldLog, config.LogDir, false},
		{oldLib, "/var/lib/nook", true}, {oldWebState, config.WebStateDir, false}, {oldKeys, config.SSHKeysDir, false},
	} {
		if err := moveTree(m.from, m.to, m.deep); err != nil {
			return fmt.Errorf("moving %s to %s: %v", m.from, m.to, err)
		}
		if fileExists(m.from) {
			fmt.Fprintf(out, "    left %s in place: parts of it already exist under %s (compare them by hand)\n", m.from, m.to)
		}
	}

	// users and groups
	for _, a := range oldApps {
		if err := renameUser("app-"+a, "nook-"+a, filepath.Join(config.AppsDir, a)); err != nil {
			return err
		}
		// deploy key file is named after the user
		os.Rename(filepath.Join(config.SSHKeysDir, "app-"+a), filepath.Join(config.SSHKeysDir, "nook-"+a))
		// resource limits keep their values
		if b, err := os.ReadFile(filepath.Join("/etc/systemd/system", "app-"+a+".slice")); err == nil {
			write(filepath.Join("/etc/systemd/system", "nook-"+a+".slice"), string(b), 0o644)
			os.Remove(filepath.Join("/etc/systemd/system", "app-"+a+".slice"))
		}
		fmt.Fprintf(out, "    app-%s -> nook-%s\n", a, a)
	}
	if webErr == nil {
		if err := renameUser(oldWebUser, config.WebUser, config.WebStateDir); err != nil {
			return err
		}
	}

	// old unit files, configs and binaries (sync and the rest of install write the nook versions)
	for _, g := range []string{
		"/etc/systemd/system/site-*--*.service", "/etc/systemd/system/app-sync@.*",
		"/etc/systemd/system/app-host-*", "/etc/systemd/system/*.wants/site-*--*.service",
		"/etc/systemd/system/*.wants/app-sync@*", "/etc/systemd/system/*.wants/app-host-*",
	} {
		ms, _ := filepath.Glob(g)
		for _, f := range ms {
			os.Remove(f)
		}
	}
	for _, f := range []string{
		"/etc/logrotate.d/apps", "/etc/update-motd.d/90-app-host",
		"/etc/systemd/system/caddy.service.d/app-host.conf", "/etc/caddy/admin.caddy",
		"/usr/local/sbin/app-host", "/usr/local/bin/app",
	} {
		os.Remove(f)
	}
	os.RemoveAll("/etc/caddy/apps")
	dropImports("/etc/caddy/apps/*.caddy", "/etc/caddy/admin.caddy")
	exec.Command("systemctl", "daemon-reload").Run()
	return nil
}

// oldAppUsers lists app names that still have an app-NAME user.
func oldAppUsers() []string {
	b, err := os.ReadFile("/etc/passwd")
	if err != nil {
		return nil
	}
	var apps []string
	for _, l := range strings.Split(string(b), "\n") {
		f := strings.Split(l, ":")
		if len(f) < 6 || !strings.HasPrefix(f[0], "app-") || f[0] == oldWebUser {
			continue
		}
		name := strings.TrimPrefix(f[0], "app-")
		if f[5] == filepath.Join(oldSrv, name) || f[5] == filepath.Join(config.AppsDir, name) {
			apps = append(apps, name)
		}
	}
	return apps
}

func renameUser(from, to, home string) error {
	if _, err := user.Lookup(to); err == nil {
		return fmt.Errorf("can't rename %s: user %s already exists", from, to)
	}
	if err := run("usermod", "-l", to, "-d", home, from); err != nil {
		return err
	}
	if _, err := user.LookupGroup(from); err == nil {
		return run("groupmod", "-n", to, from)
	}
	return nil
}

// moveTree renames src to dst, or, when dst already exists, moves src's entries that dst lacks (recursing into
// directories both have only when deep). src is removed only once it's empty; nothing is ever deleted.
func moveTree(src, dst string, deep bool) error {
	fi, err := os.Lstat(src)
	if err != nil || !fi.IsDir() {
		return nil
	}
	if _, err := os.Lstat(dst); os.IsNotExist(err) {
		os.MkdirAll(filepath.Dir(dst), 0o755)
		return os.Rename(src, dst)
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		s, d := filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())
		if di, err := os.Lstat(d); err == nil {
			if deep && e.Type().IsDir() && di.IsDir() {
				if err := moveTree(s, d, true); err != nil {
					return err
				}
			}
			continue
		}
		if err := os.Rename(s, d); err != nil {
			return err
		}
	}
	os.Remove(src) // only succeeds when everything moved
	return nil
}

func dropImports(paths ...string) {
	b, err := os.ReadFile(config.Caddyfile)
	if err != nil {
		return
	}
	var keep []string
	changed := false
	for _, l := range strings.Split(string(b), "\n") {
		drop := false
		for _, p := range paths {
			if strings.TrimSpace(l) == "import "+p {
				drop = true
			}
		}
		if drop {
			changed = true
			continue
		}
		keep = append(keep, l)
	}
	if changed {
		config.WriteAtomic(config.Caddyfile, []byte(strings.Join(keep, "\n")), 0o644)
	}
}
