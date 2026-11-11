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
	return createdText(o, tok, DeployBase(h.S)), nil
}

// DeployBase is the deploy API's base URL: the dashboard's address, or the IP fallback (self-signed).
func DeployBase(s config.Settings) string {
	d := config.LoadDashboard()
	if d.Host != "" {
		return "https://" + d.Host + "/api/v1/sites/"
	}
	return "https://" + s.SSHHost + ":" + d.IPPort + "/api/v1/sites/"
}

func createdText(o CreateOpts, token, base string) string {
	warn := ""
	if strings.Contains(strings.TrimPrefix(base, "https://"), ":") {
		warn = "\n(The dashboard has no address yet, so this uses the IP with a self-signed certificate, which curl refuses.\nSet the dashboard's address on its Domains page, then use https://<that address>/api/v1/sites/ instead.)\n"
	}
	return fmt.Sprintf(`
================ 1. GitHub secret ================

On your laptop, run this and paste the token below when it asks:

  gh secret set NOOK_TOKEN -R %[1]s

%[2]s

(Shown once; the server keeps only its hash. Make more or revoke on the dashboard: %[3]s → Deploy.)

================ 2. Workflow: .github/workflows/deploy.yml ================

name: deploy
on:
  push:
    branches: [main]
  pull_request:
    types: [opened, synchronize, reopened, closed]
concurrency:
  group: deploy-${{ github.event.pull_request.number || github.ref }}
env:
  NOOK: %[4]s%[3]s        # site: "%[3]s", or a hostname like client.com (needs a grant)
  NOOK_TOKEN: ${{ secrets.NOOK_TOKEN }}
  PR: ${{ github.event.pull_request.number }}
jobs:
  deploy:
    runs-on: ubuntu-latest
    steps:
      - if: github.event.action == 'closed'
        run: curl -fsS -X DELETE -H "Authorization: Bearer $NOOK_TOKEN" "$NOOK?pr=$PR"
      - if: github.event.action != 'closed'
        uses: actions/checkout@v4
      - if: github.event.action != 'closed'
        run: npm ci && npm run build      # your build; skip for plain files
      - if: github.event.action != 'closed'
        run: |
          tar -C dist -czf site.tgz .     # folder to publish; an executable "run" makes it a running app
          curl --fail-with-body -sS -H "Authorization: Bearer $NOOK_TOKEN" -F site=@site.tgz "$NOOK${PR:+?pr=$PR}"
%[5]s
Pushes to main deploy the site; pull requests deploy a preview and remove it when closed.
Any CI works the same way: it's one HTTPS POST of a .tar.gz.

================ 3. Runtime environment ================

Set it on the server (dashboard → %[3]s → Environment, or nook-admin env set %[3]s KEY), or send it with
the deploy from GitHub secrets, which replaces that site's "sent by deploys" values each time:

  curl ... -F site=@site.tgz -F "env.DATABASE_URL=$DATABASE_URL" "$NOOK"     (or -F env=@prod.env)

Running apps also get $PORT, $SITE and $SITE_HOST.
`, o.Repo, token, o.Name, base, warn)
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
