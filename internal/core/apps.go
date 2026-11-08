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
	"github.com/sparkstonepdx/nook/internal/token"
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

	// deploy key: GitHub gets the private half; sshd reads the public half from a root-owned file
	tmp, err := os.MkdirTemp("", "nook-key-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	if out, err := Run("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", "github-actions-"+o.Name, "-f", filepath.Join(tmp, "key")); err != nil {
		return "", fmt.Errorf("ssh-keygen: %s", out)
	}
	pub, _ := os.ReadFile(filepath.Join(tmp, "key.pub"))
	priv, _ := os.ReadFile(filepath.Join(tmp, "key"))
	os.MkdirAll(config.SSHKeysDir, 0o755)
	if err := config.WriteAtomic(filepath.Join(config.SSHKeysDir, "nook-"+o.Name), []byte("restrict "+strings.TrimSpace(string(pub))+"\n"), 0o644); err != nil {
		return "", err
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

	hostKey, _ := os.ReadFile("/etc/ssh/ssh_host_ed25519_key.pub")
	f := strings.Fields(string(hostKey))
	known := h.S.SSHHost
	if len(f) >= 2 {
		known += " " + f[0] + " " + f[1]
	}
	tok := token.Token{V: 1, Host: h.S.SSHHost, Port: 22, User: "nook-" + o.Name, Dir: home, Domain: h.S.BaseDomain, KnownHosts: known, Key: string(priv)}
	return createdText(o, tok.Encode()), nil
}

func createdText(o CreateOpts, token string) string {
	return fmt.Sprintf(`
================ 1. GitHub secret (one) ================

On your laptop, run this and paste the value below when it asks:

  gh secret set NOOK_TOKEN -R %[1]s

%[2]s

(It holds the deploy key; it's shown once and not stored on the server.)

================ 2. Workflow: .github/workflows/deploy.yml ================

name: deploy
on:
  push:
    branches: [main]
  pull_request:
    types: [opened, synchronize, reopened, closed]
permissions:
  contents: read
  pull-requests: write
concurrency:
  group: deploy-${{ github.event.pull_request.number || github.ref }}
jobs:
  deploy:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        if: github.event.action != 'closed'
      - run: npm ci && npm run build      # your build; skip for plain files
        if: github.event.action != 'closed'
      - uses: sparkstonepdx/nook/deploy@v1
        with:
          token: ${{ secrets.NOOK_TOKEN }}
          dir: dist                       # folder to publish; add an executable "run" for a running app
          site: %[3]s                     # or a hostname like client.com (needs a grant)

Pushes to main deploy to the site. Pull requests get a preview, a comment with its URL,
and are removed when the PR closes.

================ 3. Runtime environment ================

Set env vars and secrets for running apps on the server, not in GitHub:
  dashboard → %[3]s → Environment,  or  nook-admin env set %[3]s KEY
Running sites restart with the new values. Running apps also get $PORT, $SITE, $SITE_HOST.
`, o.Repo, token, o.Name)
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
	for _, p := range []string{
		filepath.Join(config.CaddyAppsDir, app+".caddy"), filepath.Join(config.EnvDir, app+".env"), filepath.Join(config.EnvDir, app+".user.env"),
		filepath.Join(config.SSHKeysDir, "nook-"+app),
	} {
		os.Remove(p)
	}
	for _, p := range []string{filepath.Join(config.WWWDir, app), filepath.Join(config.LogDir, app)} {
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
