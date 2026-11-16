package core

// Restoring a whole server (#68 part 2): on a fresh install, after `nook-admin backup setup --existing` has
// connected to the same repository, put back nook's settings, the dashboard's users, Caddy's certificates, the
// deploy keys, every app's user (same uid where it's free) and every app's folder, then sync.
//
// The new server takes over the old one's id (its restic host), so the daily backups carry on the same history.
// With NewID it keeps its own: two servers both serving, the old one still running (adopting, moving).

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/sparkstonepdx/nook/internal/config"
	"github.com/sparkstonepdx/nook/internal/names"
)

type ServerRestore struct {
	From     string // the old server's id; empty when the repository holds only one server
	Snapshot string // its server snapshot; empty for the latest
	NewID    bool   // keep this server's own id instead of taking over the old one's
	Force    bool   // restore onto a server that already has apps (theirs are set aside, never mixed)
}

// restoreRoot prefixes every path written, for tests. "" on a server.
var restoreRoot = ""

// restoreAddUser makes app's user and group, with uid and gid when they're free. Returns what it got.
var restoreAddUser = func(app string, uid, gid int) (int, int, error) {
	home := filepath.Join(config.AppsDir, app)
	args := []string{"--system", "--home-dir", home, "--no-create-home", "--shell", "/bin/bash"}
	if _, err := user.LookupId(strconv.Itoa(uid)); err != nil {
		if _, err := user.LookupGroupId(strconv.Itoa(gid)); err != nil {
			if out, err := Run("groupadd", "--system", "--gid", strconv.Itoa(gid), "nook-"+app); err != nil {
				return 0, 0, fmt.Errorf("groupadd: %s", out)
			}
			args = append(args, "--uid", strconv.Itoa(uid), "--gid", strconv.Itoa(gid))
		} else {
			args = append(args, "--uid", strconv.Itoa(uid), "--user-group")
		}
	} else {
		args = append(args, "--user-group")
	}
	if out, err := Run("useradd", append(args, "nook-"+app)...); err != nil {
		return 0, 0, fmt.Errorf("useradd: %s", out)
	}
	u, err := LookupApp(app)
	if err != nil {
		return 0, 0, err
	}
	return u.UID, u.GID, nil
}

// restoreFinish starts it all: units, the sites/ watchers, the firewall, Caddy and the dashboard.
var restoreFinish = func(h *Host, apps []string) error {
	systemctl("daemon-reload")
	for _, a := range apps {
		if err := systemctl("enable", "--now", "nook-sync@"+a+".path"); err != nil {
			h.say("%s: couldn't start its sites/ watcher", a)
		}
	}
	if err := Firewall(); err != nil {
		h.say("firewall: %v", err)
	}
	h.S = config.Load()
	// wildcard certificates: Caddy here needs the DNS plugin the old one had, then the same settings
	if c := loadCerts(); len(c.Wildcards) > 0 {
		for _, w := range c.Wildcards {
			if err := h.AddDNSPlugin(w.Provider); err != nil {
				h.say("Caddy's %s plugin: %v", w.Provider, err)
			}
		}
		if err := h.applyCerts(c, readDNSEnv()); err != nil {
			h.say("wildcard certificates: %v (save them again on the Domains page)", err)
		}
	}
	if err := h.AdminCaddy(); err != nil {
		h.say("dashboard's Caddy config: %v", err)
	}
	err := h.SyncAll()
	systemctl("restart", "nook-web")
	systemctl("reload-or-restart", "caddy")
	return err
}

// server paths put back over the fresh install's, and who owns them here (uids differ between servers)
var restoreOwners = map[string]string{config.WebStateDir: config.WebUser, "/var/lib/caddy/.local/share/caddy": "caddy"}

func (h *Host) RestoreServer(r ServerRestore) error {
	c := LoadBackupConfig()
	if !c.Configured() {
		return errors.New("connect to the backups first: nook-admin backup setup --existing (the same repository and password)")
	}
	if apps := Apps(); len(apps) > 0 && !r.Force {
		return fmt.Errorf("this server already has apps (%s): restore onto a fresh install, or pass --force", strings.Join(apps, ", "))
	}
	if r.Snapshot != "" && !regexp.MustCompile(`^[0-9a-f]{8,64}$`).MatchString(r.Snapshot) {
		return errors.New("a snapshot is an id like 4f2a9c1e")
	}
	if err := EnsureRestic(h.Out); err != nil {
		return err
	}

	// which server, and which of its snapshots
	out, err := restic(c, "snapshots", "--json", "--tag", "nook,server")
	if err != nil {
		return err
	}
	var all []Snapshot
	if err := json.Unmarshal(out, &all); err != nil {
		return fmt.Errorf("reading the snapshot list: %v", err)
	}
	hosts := map[string]bool{}
	for _, s := range all {
		hosts[s.Host] = true
	}
	from := r.From
	if from == "" {
		var list []string
		for h := range hosts {
			list = append(list, h)
		}
		sort.Strings(list)
		switch len(list) {
		case 0:
			return errors.New("that repository has no server snapshots")
		case 1:
			from = list[0]
		default:
			return fmt.Errorf("that repository holds several servers (%s): say which with --from", strings.Join(list, ", "))
		}
	} else if !hosts[from] {
		return fmt.Errorf("no server snapshots from %s", from)
	}
	var snap *Snapshot
	for i, s := range all {
		if s.Host == from && (r.Snapshot == "" && (snap == nil || s.Time.After(snap.Time)) || r.Snapshot != "" && (strings.HasPrefix(s.ID, r.Snapshot) || s.Short == r.Snapshot)) {
			snap = &all[i]
		}
	}
	if snap == nil {
		return fmt.Errorf("%s has no server snapshot %s", from, r.Snapshot)
	}
	h.say("restoring server %s from %s (%s)", from, snap.Short, snap.Time.Local().Format(time.DateTime))

	stage := filepath.Join(restoreRoot+config.AppsDir, ".restore", "server-"+strconv.FormatInt(time.Now().Unix(), 10))
	if err := os.MkdirAll(stage, 0o700); err != nil {
		return err
	}
	os.Chmod(filepath.Dir(stage), 0o700)
	defer os.RemoveAll(stage)
	if _, err := restic(c, "restore", snap.ID, "--target", stage); err != nil {
		return err
	}

	// this server's own: its address, and its id unless it takes over the old one's
	keep := config.Read(restoreRoot + config.ConfigFile)
	ownID := c.ServerID
	aside := restoreRoot + "/etc/nook.before-restore-" + time.Now().Format("20060102-150405")
	if out, err := Run("cp", "-a", restoreRoot+"/etc/nook", aside); err != nil {
		return fmt.Errorf("setting this server's settings aside: %s", out)
	}
	h.say("this server's own settings are in %s", aside)

	var paths []string
	for _, p := range serverPaths() {
		paths = append(paths, p)
	}
	units, _ := filepath.Glob(filepath.Join(stage, "etc/systemd/system/nook-*.slice"))
	for _, u := range units {
		paths = append(paths, strings.TrimPrefix(u, stage))
	}
	for _, p := range slices.Compact(paths) {
		src := filepath.Join(stage, p)
		fi, err := os.Lstat(src)
		if err != nil {
			continue // not on the old server either
		}
		dst := restoreRoot + p
		os.MkdirAll(filepath.Dir(dst), 0o755)
		if fi.IsDir() {
			os.MkdirAll(dst, fi.Mode().Perm())
			src += "/."
		}
		if out, err := Run("cp", "-a", src, dst); err != nil {
			return fmt.Errorf("putting back %s: %s", p, out)
		}
		if owner, ok := restoreOwners[p]; ok {
			Run("chown", "-R", owner+":"+owner, dst)
		}
	}
	if v := keep["SSH_HOST"]; v != "" {
		config.Set(restoreRoot+config.ConfigFile, "SSH_HOST", v)
	}
	if r.NewID {
		config.WriteAtomic(restoreRoot+"/etc/nook/server-id", []byte(ownID+"\n"), 0o644)
		h.say("this server keeps its own id, %s", ownID)
	} else {
		config.WriteAtomic(restoreRoot+"/etc/nook/server-id", []byte(from+"\n"), 0o644)
		h.say("this server takes over %s's id, so its backups carry on the same history", from)
	}

	// the apps: their users first (files come back with their old uid), then each folder
	var users map[string]map[string]int
	if b, err := os.ReadFile(filepath.Join(stage, config.LibDir, "backup", "users.json")); err == nil {
		json.Unmarshal(b, &users)
	}
	var apps []string
	for a := range users {
		if names.App(a) {
			apps = append(apps, a)
		}
	}
	sort.Strings(apps)
	var restored, failed []string
	for _, app := range apps {
		if err := h.restoreServerApp(c, from, app, users[app]["uid"], users[app]["gid"]); err != nil {
			h.say("%s: %v", app, err)
			failed = append(failed, app)
			continue
		}
		restored = append(restored, app)
	}
	h.say("starting everything")
	if err := restoreFinish(h, restored); err != nil {
		h.say("sync: %v", err)
	}
	h.say("restored %d apps. Point DNS for your domains at this server's address; Caddy reuses the certificates it had.", len(restored))
	if len(failed) > 0 {
		return fmt.Errorf("couldn't restore %s", strings.Join(failed, ", "))
	}
	return nil
}

func (h *Host) restoreServerApp(c BackupConfig, from, app string, uid, gid int) error {
	home := filepath.Join(config.AppsDir, app)
	dst := restoreRoot + home
	if _, err := os.Lstat(dst); err == nil {
		moved := dst + ".before-restore-" + time.Now().Format("20060102-150405")
		if err := os.Rename(dst, moved); err != nil {
			return err
		}
		h.say("%s: the folder that was here is %s", app, filepath.Base(moved))
	}
	stage := filepath.Join(restoreRoot+config.AppsDir, ".restore", app+"-"+strconv.FormatInt(time.Now().UnixNano(), 10))
	if err := os.MkdirAll(stage, 0o700); err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	h.say("%s: restoring its folder", app)
	if _, err := restic(c, "restore", "latest", "--host", from, "--tag", "nook,app:"+app, "--target", stage, "--include", home); err != nil {
		return err
	}
	got := filepath.Join(stage, home)
	if fi, err := os.Lstat(got); err != nil || !fi.IsDir() {
		return errors.New("no snapshot of its folder")
	}
	nu, ng := uid, gid
	if _, err := LookupApp(app); err != nil || restoreRoot != "" {
		var err error
		if nu, ng, err = restoreAddUser(app, uid, gid); err != nil {
			return err
		}
	} else if u, _ := LookupApp(app); u != nil {
		nu, ng = u.UID, u.GID
	}
	if nu != uid || ng != gid { // the old uid was taken here: hand its files to the new one, links not followed
		h.say("%s: uid %d is taken here, so it's %d now", app, uid, nu)
		filepath.Walk(got, func(p string, fi os.FileInfo, err error) error {
			if err == nil {
				os.Lchown(p, nu, ng)
			}
			return nil
		})
	}
	for _, d := range []string{"sites", "builds", "data"} {
		if err := os.Mkdir(filepath.Join(got, d), 0o700); err == nil {
			os.Chown(filepath.Join(got, d), nu, ng)
		}
	}
	os.MkdirAll(filepath.Dir(dst), 0o755)
	return os.Rename(got, dst)
}
