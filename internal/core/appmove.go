package core

// Moving one app to a linked server (#111, step C). Both servers back up to the same repository, so the app's
// folder goes across as a snapshot, and its settings (limits, env, grants, login and ports switches, store entry)
// as a small bundle over the link.
//
//	1. out    the old server stops the app, backs up just its folder, and hands over the bundle
//	2. in     the new server restores the folder from that snapshot, writes the settings, makes a deploy token
//	          (tokens name their server, so the old one can't carry over) and starts it
//	3. done   the old server deletes its copy; if the new server failed instead, the old one starts it again
//
// An app that uses or provides a service (a database) doesn't move this way: its provider is on this server.

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/sparkstonepdx/nook/internal/config"
	"github.com/sparkstonepdx/nook/internal/grants"
	"github.com/sparkstonepdx/nook/internal/names"
)

// AppBundle is an app's settings, moving to another server.
type AppBundle struct {
	App      string            `json:"app"`
	From     string            `json:"from"` // the old server's id: the snapshot's restic host
	Repo     string            `json:"repo"` // the backup repository, which must be the new server's too
	Snapshot string            `json:"snapshot"`
	UID      int               `json:"uid"`
	GID      int               `json:"gid"`
	Files    map[string]string `json:"files"` // absolute path: base64 contents (all root-owned, 0600 or 0644)
	Modes    map[string]uint32 `json:"modes"`
	Grants   []grants.Grant    `json:"grants"`
	Store    *Installed        `json:"store,omitempty"`
	GitHub   string            `json:"github,omitempty"` // the repo that deploys it
}

// appFiles is app's own settings files that exist.
func appFiles(app string) []string {
	var out []string
	add := func(p string) {
		if fi, err := os.Lstat(p); err == nil && fi.Mode().IsRegular() {
			out = append(out, p)
		}
	}
	add(filepath.Join("/etc/systemd/system", "nook-"+app+".slice"))
	add(filepath.Join(config.EnvDir, app+".env"))
	add(filepath.Join(config.EnvDir, app+".user.env"))
	add(filepath.Join(config.SSHKeysDir, "nook-"+app))
	for _, p := range []string{loginPath(app), portsPath(app), askedPath(app), servicesStatePath(app)} {
		add(p)
	}
	filepath.WalkDir(filepath.Join(config.EnvDir, app), func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			out = append(out, p)
		}
		return nil
	})
	return out
}

// appFileAllowed says whether p is a settings file of app, so a bundle can't write anywhere else.
func appFileAllowed(app, p string) bool {
	p = filepath.Clean(p)
	for _, f := range []string{filepath.Join("/etc/systemd/system", "nook-"+app+".slice"), filepath.Join(config.EnvDir, app+".env"),
		filepath.Join(config.EnvDir, app+".user.env"), filepath.Join(config.SSHKeysDir, "nook-"+app),
		loginPath(app), portsPath(app), askedPath(app), servicesStatePath(app)} {
		if p == f {
			return true
		}
	}
	rel, err := filepath.Rel(filepath.Join(config.EnvDir, app), p)
	return err == nil && rel != "." && !strings.HasPrefix(rel, "..") && !strings.Contains(rel, "/")
}

func appUsesServices(app string) error {
	for _, b := range readBindings() {
		papp, _ := splitAppProvider(b.Provider)
		if b.App == app {
			return fmt.Errorf("%s uses %s from %s: unbind it first (Services page), since that stays on this server", app, b.Interface, b.Provider)
		}
		if papp == app {
			return fmt.Errorf("%s provides %s to %s: move or unbind that first", app, b.Interface, b.App)
		}
	}
	return nil
}

// AppMoveOut stops app and backs up its folder, and returns what the new server needs.
func (h *Host) AppMoveOut(app string) (AppBundle, error) {
	c := LoadBackupConfig()
	if !c.Configured() {
		return AppBundle{}, errors.New("this server has no backups set up: an app moves through them")
	}
	u, err := LookupApp(app)
	if err != nil {
		return AppBundle{}, err
	}
	if err := appUsesServices(app); err != nil {
		return AppBundle{}, err
	}
	b := AppBundle{App: app, From: c.ServerID, Repo: c.Repo, UID: u.UID, GID: u.GID, Files: map[string]string{}, Modes: map[string]uint32{}, GitHub: AppRepo(app)}
	for _, p := range appFiles(app) {
		data, err := os.ReadFile(p)
		if err != nil {
			return AppBundle{}, err
		}
		fi, _ := os.Stat(p)
		b.Files[p] = base64.StdEncoding.EncodeToString(data)
		b.Modes[p] = uint32(fi.Mode().Perm())
	}
	gl, _ := grants.Load(config.GrantsFile)
	b.Grants = gl.For(app)
	if in, ok := readInstalled()[app]; ok {
		b.Store = &in
	}

	h.say("%s: stopping it", app)
	h.stopApp(app)
	r, err := h.backupOne(c, app)
	if err != nil {
		h.startApp(app)
		return AppBundle{}, fmt.Errorf("backing up %s failed, so it's running here again: %v", app, err)
	}
	b.Snapshot = r.Snapshot
	h.say("%s: backed up as %s", app, r.Snapshot)
	return b, nil
}

// backupOne backs up app alone, under the same lock as the daily run.
func (h *Host) backupOne(c BackupConfig, app string) (AppBackup, error) {
	os.MkdirAll(backupDir, 0o700)
	lock, err := os.OpenFile(lockPath(), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return AppBackup{}, err
	}
	defer lock.Close()
	if syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		return AppBackup{}, errors.New("a backup is running: try again when it's done")
	}
	if err := EnsureRestic(h.Out); err != nil {
		return AppBackup{}, err
	}
	writeUsers()
	r := h.backupApp(c, app)
	if r.Snapshot == "" {
		return r, errors.New(r.Error)
	}
	return r, nil
}

func (h *Host) stopApp(app string) {
	systemctl("stop", "nook-sync@"+app+".path")
	for _, s := range RunningSites(app) {
		systemctl("stop", UnitName(app, s))
	}
	if timers, _ := filepath.Glob("/etc/systemd/system/" + CronUnit(app, "*") + ".timer"); timers != nil {
		for _, t := range timers {
			systemctl("stop", strings.TrimPrefix(t, "/etc/systemd/system/"))
		}
	}
}

func (h *Host) startApp(app string) {
	systemctl("start", "nook-sync@"+app+".path")
	for _, s := range RunningSites(app) {
		systemctl("start", UnitName(app, s))
	}
	if timers, _ := filepath.Glob("/etc/systemd/system/" + CronUnit(app, "*") + ".timer"); timers != nil {
		for _, t := range timers {
			systemctl("start", strings.TrimPrefix(t, "/etc/systemd/system/"))
		}
	}
}

// AppMoveBack starts app here again when its move was called off.
func (h *Host) AppMoveBack(app string) error {
	if _, err := LookupApp(app); err != nil {
		return err
	}
	h.startApp(app)
	h.say("%s is running here again", app)
	return nil
}

// AppMoveIn sets up an app moving here from a linked server, and returns its new deploy token.
func (h *Host) AppMoveIn(b AppBundle) (string, error) {
	c := LoadBackupConfig()
	switch {
	case !names.App(b.App):
		return "", errors.New("that isn't an app name")
	case !c.Configured():
		return "", errors.New("this server has no backups set up: an app moves through them (the same repository as the other server's)")
	case b.Repo != c.Repo:
		return "", fmt.Errorf("the servers back up to different places (%s there, %s here): an app moves through a repository both use", b.Repo, c.Repo)
	}
	if _, err := LookupApp(b.App); err == nil {
		return "", fmt.Errorf("this server already has an app called %s", b.App)
	}
	if err := EnsureRestic(h.Out); err != nil {
		return "", err
	}
	for p := range b.Files {
		if !appFileAllowed(b.App, p) {
			return "", fmt.Errorf("the bundle has a file that isn't %s's settings: %s", b.App, p)
		}
	}
	for _, g := range b.Grants {
		if g.App != b.App || grants.Valid(g) != nil {
			return "", errors.New("the bundle has a grant that isn't " + b.App + "'s")
		}
	}
	tok, err := h.appMoveInLocked(b, c)
	if err != nil {
		return "", err
	}
	if err := h.Sync(b.App); err != nil {
		return tok, fmt.Errorf("%s is here, but starting it failed: %v", b.App, err)
	}
	h.say("%s is running here", b.App)
	return tok, nil
}

func (h *Host) appMoveInLocked(b AppBundle, c BackupConfig) (string, error) {
	unlock, err := Lock()
	if err != nil {
		return "", err
	}
	defer unlock()
	h.say("%s: restoring its folder from %s's snapshot", b.App, b.From)
	if err := h.restoreServerApp(c, b.From, b.App, b.UID, b.GID); err != nil {
		return "", err
	}
	for p, enc := range b.Files {
		data, err := base64.StdEncoding.DecodeString(enc)
		if err != nil {
			return "", err
		}
		mode := os.FileMode(b.Modes[p]) & 0o644
		os.MkdirAll(filepath.Dir(p), 0o755)
		if strings.HasPrefix(p, config.EnvDir) {
			os.MkdirAll(filepath.Dir(p), 0o700)
		}
		if err := config.WriteAtomic(p, data, mode); err != nil {
			return "", err
		}
	}
	gl, _ := grants.Load(config.GrantsFile)
	for _, g := range b.Grants {
		if !gl.Has(g) {
			gl = append(gl, g)
		}
	}
	config.WriteAtomic(config.GrantsFile, []byte(gl.Text()), 0o600)
	if b.Store != nil {
		m := readInstalled()
		in := *b.Store
		in.App = b.App
		m[b.App] = in
		writeInstalled(m)
	}
	if b.GitHub != "" && repoRE.MatchString(b.GitHub) {
		setAppRepo(b.App, b.GitHub)
	}
	tok, err := newToken()
	if err != nil {
		return "", err
	}
	if _, err := addTokenLocked(b.App, "moved here", tok); err != nil {
		return "", err
	}
	systemctl("daemon-reload")
	if err := systemctl("enable", "--now", "nook-sync@"+b.App+".path"); err != nil {
		h.say("%s: couldn't start its sites/ watcher", b.App)
	}
	if err := Firewall(); err != nil {
		h.say("firewall: %v", err)
	}
	return tok, nil
}
