package core

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/sparkstonepdx/nook/internal/config"
	"github.com/sparkstonepdx/nook/internal/grants"
	"github.com/sparkstonepdx/nook/internal/names"
)

var (
	memRE  = regexp.MustCompile(`^\d+[KMG]$`)
	cpuRE  = regexp.MustCompile(`^\d+%$`)
	numRE  = regexp.MustCompile(`^\d+$`)
	repoRE = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
)

type CreateOpts struct{ Name, Memory, CPU, Tasks, Repo string }

// Create makes the user, folders, limits, deploy key and grant, and returns what to paste into GitHub.
func (h *Host) Create(o CreateOpts) (string, error) {
	if o.Memory == "" {
		o.Memory = h.S.MemoryMax
	}
	if o.CPU == "" {
		o.CPU = h.S.CPUQuota
	}
	if o.Tasks == "" {
		o.Tasks = h.S.TasksMax
	}
	if o.Repo == "" {
		o.Repo = "OWNER/REPO"
	}
	switch {
	case !names.App(o.Name):
		return "", fmt.Errorf("name: a lowercase letter, then 1-23 of a-z 0-9 -")
	case !memRE.MatchString(o.Memory) || !cpuRE.MatchString(o.CPU) || !numRE.MatchString(o.Tasks):
		return "", fmt.Errorf("memory like 256M, cpu like 50%%, tasks a number")
	case !repoRE.MatchString(o.Repo):
		return "", fmt.Errorf("repo must look like owner/name")
	}
	if _, err := LookupApp(o.Name); err == nil {
		return "", fmt.Errorf("nook-%s already exists", o.Name)
	}
	unlock, err := Lock()
	if err != nil {
		return "", err
	}
	defer unlock()

	home := filepath.Join(config.AppsDir, o.Name)
	if out, err := Run("useradd", "--system", "--home-dir", home, "--create-home", "--shell", "/bin/bash", "--user-group", "nook-"+o.Name); err != nil {
		return "", fmt.Errorf("useradd: %s", out)
	}
	u, err := LookupApp(o.Name)
	if err != nil {
		return "", err
	}
	for _, d := range []string{"sites", "builds", "data"} {
		os.Mkdir(filepath.Join(home, d), 0o700)
	}
	// the user owns its home; nothing else (Caddy included) needs to read it
	for _, p := range []string{home, filepath.Join(home, "sites"), filepath.Join(home, "builds"), filepath.Join(home, "data")} {
		os.Chown(p, u.UID, u.GID)
		os.Chmod(p, 0o700)
	}

	slice := fmt.Sprintf("[Unit]\nDescription=Resource limits for app %s\n\n[Slice]\nMemoryMax=%s\nCPUQuota=%s\nTasksMax=%s\n", o.Name, o.Memory, o.CPU, o.Tasks)
	config.WriteAtomic(filepath.Join("/etc/systemd/system", "nook-"+o.Name+".slice"), []byte(slice), 0o644)
	systemctl("daemon-reload")
	if err := systemctl("enable", "--now", "nook-sync@"+o.Name+".path"); err != nil {
		h.say("warning: could not start the sites/ watcher")
	}

	gl, _ := grants.Load(config.GrantsFile)
	g := grants.Grant{App: o.Name, Kind: "sub", Name: h.S.BaseDomain}
	if !gl.Has(g) {
		config.WriteAtomic(config.GrantsFile, []byte((append(gl, g)).Text()), 0o600)
	}

	tok, err := newToken()
	if err != nil {
		return "", err
	}
	if _, err := addTokenLocked(o.Name, "created with the app", tok); err != nil {
		return "", err
	}
	if o.Repo != "OWNER/REPO" {
		setAppRepo(o.Name, o.Repo)
	}
	return Instructions(o.Name, tok, ""), nil
}

// DeployBase is the deploy API's base URL: the dashboard's address, or the IP fallback (self-signed).
func DeployBase(s config.Settings) string {
	d := config.LoadDashboard()
	if d.Host != "" {
		return "https://" + d.Host + "/api/v1/sites/"
	}
	return "https://" + s.SSHHost + ":" + d.IPPort + "/api/v1/sites/"
}

// Delete removes everything an app has. confirm must equal the name.
func (h *Host) Delete(app, confirm string) error {
	if confirm != app {
		return fmt.Errorf("type the app name to confirm")
	}
	if _, err := LookupApp(app); err != nil {
		return err
	}
	unlock, err := Lock()
	if err != nil {
		return err
	}
	defer unlock()
	systemctl("disable", "--now", "nook-sync@"+app+".path")
	for _, s := range RunningSites(app) {
		h.stopUnit(app, s)
	}
	os.Remove(filepath.Join("/etc/systemd/system", "nook-"+app+".slice"))
	systemctl("daemon-reload")
	Run("pkill", "-KILL", "-u", "nook-"+app)
	gl, _ := grants.Load(config.GrantsFile)
	var keep grants.List
	for _, g := range gl {
		if g.App != app {
			keep = append(keep, g)
		}
	}
	config.WriteAtomic(config.GrantsFile, []byte(keep.Text()), 0o600)
	tokensDropApp(app)
	h.dropAppBindings(app)
	storeForget(app)
	for _, p := range []string{
		filepath.Join(config.CaddyAppsDir, app+".caddy"), filepath.Join(config.EnvDir, app+".env"), filepath.Join(config.EnvDir, app+".user.env"),
		filepath.Join(config.SSHKeysDir, "nook-"+app),
	} {
		os.Remove(p)
	}
	for _, p := range []string{filepath.Join(config.WWWDir, app), filepath.Join(config.LogDir, app), filepath.Join(config.EnvDir, app)} {
		os.RemoveAll(p)
	}
	if states, _ := filepath.Glob(filepath.Join(config.StateDir, app+"--*")); states != nil {
		for _, s := range states {
			os.Remove(s)
		}
	}
	systemctl("reload", "caddy")
	if out, err := Run("userdel", "nook-"+app); err != nil {
		h.say("userdel: %s", strings.TrimSpace(out))
	}
	os.RemoveAll(filepath.Join(config.AppsDir, app)) // RemoveAll never follows symlinks
	h.say("deleted %s. Remove its NOOK_TOKEN secret from the GitHub repo too.", app)
	return nil
}

// Grant adds or removes a grant, then re-syncs the app.
func (h *Host) Grant(add bool, g grants.Grant) error {
	g.Name = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(g.Name)), "*.")
	if err := grants.Valid(g); err != nil {
		return err
	}
	if _, err := LookupApp(g.App); err != nil {
		return err
	}
	unlock, err := Lock()
	if err != nil {
		return err
	}
	gl, _ := grants.Load(config.GrantsFile)
	if add && !gl.Has(g) {
		gl = append(gl, g)
	} else if !add {
		gl = gl.Without(g)
	}
	err = config.WriteAtomic(config.GrantsFile, []byte(gl.Text()), 0o600)
	unlock()
	if err != nil {
		return err
	}
	return h.Sync(g.App)
}
