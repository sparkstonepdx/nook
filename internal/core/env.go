package core

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sparkstonepdx/nook/internal/config"
	"github.com/sparkstonepdx/nook/internal/envfile"
	"github.com/sparkstonepdx/nook/internal/names"
)

// Env scopes, from general to specific. A running site loads, in order (later wins):
//
//	app's own ~/secrets.env  <  sent by CI (app-wide)  <  app (dashboard)  <  service bindings (#12)  <  previews of its base site
//	(previews only)  <  its deploy env (sent with the deploy)  <  its clients, if it provides a service  <  site
//
// Previews never load their base site's own env, so a PR build doesn't get production secrets unless you
// put them in the app scope or the previews scope.
type Scope struct {
	Kind string // "app", "ci", "site", "previews" or "deploy"
	Name string // site (or base site for "previews"); empty for "app"
}

// ParseScope reads "" / "app", "site:NAME", "previews:NAME" or "deploy:NAME".
func ParseScope(s string) (Scope, error) {
	if s == "" || s == "app" {
		return Scope{Kind: "app"}, nil
	}
	if s == "ci" {
		return Scope{Kind: "ci"}, nil
	}
	kind, name, ok := strings.Cut(s, ":")
	if !ok || (kind != "site" && kind != "previews" && kind != "deploy") || !names.Site(name) {
		return Scope{}, fmt.Errorf("bad env scope %q: use app, site:NAME, previews:NAME or deploy:NAME", s)
	}
	return Scope{Kind: kind, Name: name}, nil
}

func (s Scope) String() string {
	if s.Kind == "app" || s.Kind == "ci" {
		return s.Kind
	}
	return s.Kind + ":" + s.Name
}

func (s Scope) path(app string) string {
	if s.Kind == "app" {
		return filepath.Join(config.EnvDir, app+".env")
	}
	if s.Kind == "ci" {
		return filepath.Join(config.EnvDir, app, "ci.env")
	}
	return filepath.Join(config.EnvDir, app, s.Kind, s.Name+".env")
}

// envFiles is the EnvironmentFile list for one site, in load order.
func envFiles(app, site string) []string {
	files := []string{filepath.Join(config.EnvDir, app+".user.env"), Scope{Kind: "ci"}.path(app), Scope{Kind: "app"}.path(app), servicesEnvPath(app)}
	if base, _, ok := names.PreviewOf(site); ok {
		files = append(files, Scope{"previews", base}.path(app))
	}
	return append(files, Scope{"deploy", site}.path(app), providerEnvPath(app, site), Scope{"site", site}.path(app))
}

// EnvList returns the values in one scope (root-owned files under /etc/nook/env).
func EnvList(app string, scope Scope) (*envfile.Vars, error) {
	if _, err := LookupApp(app); err != nil {
		return nil, err
	}
	b, _ := os.ReadFile(scope.path(app))
	return envfile.Parse(string(b)), nil
}

// EnvScopes lists the scopes that have values, for the dashboard.
func EnvScopes(app string) []string {
	out := []string{}
	if fileExists(Scope{Kind: "ci"}.path(app)) {
		out = append(out, "ci")
	}
	for _, kind := range []string{"site", "previews", "deploy"} {
		files, _ := filepath.Glob(filepath.Join(config.EnvDir, app, kind, "*.env"))
		for _, f := range files {
			out = append(out, kind+":"+strings.TrimSuffix(filepath.Base(f), ".env"))
		}
	}
	return out
}

// sitesUsing lists the running sites that load scope.
func sitesUsing(app string, scope Scope) []string {
	var out []string
	for _, s := range RunningSites(app) {
		base, _, isPreview := names.PreviewOf(s)
		switch {
		case scope.Kind == "app", scope.Kind == "ci",
			(scope.Kind == "site" || scope.Kind == "deploy") && s == scope.Name,
			scope.Kind == "previews" && isPreview && base == scope.Name:
			out = append(out, s)
		}
	}
	return out
}

// EnvApply sets and unsets values in one scope, then restarts the running sites that load it.
func (h *Host) EnvApply(app string, scope Scope, set map[string]string, unset []string, restart bool) error {
	if scope.Kind == "deploy" {
		return fmt.Errorf("deploy env is replaced by each deploy; set the value in the site scope (--site %s) to override it", scope.Name)
	}
	if scope.Kind == "ci" {
		return fmt.Errorf("the \"sent by CI\" values are set by the env API; set the value in the app scope to override it")
	}
	v, err := EnvList(app, scope)
	if err != nil {
		return err
	}
	for k, val := range set {
		if err := envfile.Check(k, val); err != nil {
			return err
		}
	}
	for _, k := range unset {
		if err := envfile.Check(k, ""); err != nil {
			return err
		}
	}
	for k, val := range set {
		v.Set(k, val)
	}
	for _, k := range unset {
		v.Unset(k)
	}
	if err := writeEnv(app, scope, v); err != nil {
		return err
	}
	var changed []string
	for k := range set {
		changed = append(changed, "set "+k)
	}
	for _, k := range unset {
		changed = append(changed, "unset "+k)
	}
	h.say("%s (%s)", strings.Join(changed, ", "), scope)
	if restart {
		h.restartSites(app, sitesUsing(app, scope))
	}
	return nil
}

// EnvReplace makes a scope hold exactly vars (used for env sent with a deploy). It doesn't restart anything.
func EnvReplace(app string, scope Scope, vars map[string]string) error {
	v := envfile.Parse("")
	for k, val := range vars {
		if err := envfile.Check(k, val); err != nil {
			return err
		}
		v.Set(k, val)
	}
	return writeEnv(app, scope, v)
}

func writeEnv(app string, scope Scope, v *envfile.Vars) error {
	path := scope.path(app)
	if v.Len() == 0 {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	os.MkdirAll(filepath.Dir(path), 0o700)
	os.MkdirAll(config.EnvDir, 0o700)
	note := map[string]string{
		"app":      "loaded into every running site of this app",
		"site":     "loaded into this site only; overrides the app's values",
		"previews": "loaded into every preview of this site (not the site itself)",
		"deploy":   "sent with the latest deploy of this site (or by the env API for this site)",
		"ci":       "sent by CI through the env API, for every site of this app",
	}[scope.Kind]
	return config.WriteAtomic(path, []byte(envfile.Format(v, "managed by nook ("+scope.String()+"): "+note)), 0o600)
}

// dropSiteEnv removes a preview's own env when the preview goes away.
func dropSiteEnv(app, site string) {
	if _, _, ok := names.PreviewOf(site); !ok {
		return
	}
	os.Remove(Scope{"deploy", site}.path(app))
	os.Remove(Scope{"site", site}.path(app))
}

func (h *Host) restartSites(app string, sites []string) {
	var done []string
	for _, s := range sites {
		if systemctl("restart", UnitName(app, s)) == nil {
			done = append(done, s)
		}
	}
	if len(done) > 0 {
		h.say("restarted: %s", strings.Join(done, ", "))
	} else {
		h.say("no running sites to restart")
	}
}

// SiteNames lists the app's site folders (root never follows a symlink here).
func SiteNames(app string) []string {
	out := []string{}
	u, err := LookupApp(app)
	if err != nil {
		return out
	}
	sites, err := openDirNoFollow(filepath.Join(u.Home, "sites"))
	if err != nil {
		return out
	}
	defer sites.close()
	for _, s := range sites.names() {
		if st, err := sites.lstat(s); err == nil && isDir(st) && names.Site(s) {
			out = append(out, s)
		}
	}
	return out
}

// CIEnv is the env API (POST /api/v1/env): CI sets values in the app's "sent by CI" scope, or in one site's
// deploy scope. replace=true makes the scope hold exactly set; otherwise set is merged and unset removed.
// It restarts the running sites that load the scope and returns the key names it now holds.
func (h *Host) CIEnv(app string, scope Scope, set map[string]string, unset []string, replace bool) ([]string, []string, error) {
	if scope.Kind != "ci" && scope.Kind != "deploy" {
		return nil, nil, fmt.Errorf("the env API writes only the ci and deploy scopes")
	}
	v := envfile.Parse("")
	if !replace {
		var err error
		if v, err = EnvList(app, scope); err != nil {
			return nil, nil, err
		}
	}
	for k, val := range set {
		if err := envfile.Check(k, val); err != nil {
			return nil, nil, err
		}
		v.Set(k, val)
	}
	for _, k := range unset {
		v.Unset(k)
	}
	if err := writeEnv(app, scope, v); err != nil {
		return nil, nil, err
	}
	affected := sitesUsing(app, scope)
	h.restartSites(app, affected)
	return v.Keys(), affected, nil
}
