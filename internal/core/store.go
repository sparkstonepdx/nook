package core

// The store (#15): install apps from a signed catalog, keep them updated by policy, roll back a broken update.
//
//	catalogs:  the official one (sparkstonepdx/nook-catalog) and any community ones added on the dashboard,
//	           each signed with its own pinned key (catalogs.go)
//	installed: /etc/nook/store.json   (app -> catalog, id, version, site, policy)
//	packages:  /var/lib/nook/store/APP/VERSION.tgz (root-only; the last two, for rollback)
//
// A catalog artifact is downloaded, checked against the sha256 the signed catalog pins, and read in
// memory: root picks the executable out and builds a fresh site package (binary + run + nook.json),
// which is unpacked as the app user through the same path as the deploy API. Root never writes a path
// that came from the archive.

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/sparkstonepdx/nook/internal/config"
	"github.com/sparkstonepdx/nook/internal/names"
	"github.com/sparkstonepdx/nook/internal/update"
)

var (
	catalogIDRE = regexp.MustCompile(`^[a-z][a-z0-9-]{0,30}$`)
	fileNameRE  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	sha256RE    = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

func storeDir(app string) string { return filepath.Join(config.LibDir, "store", app) }

type Installed struct {
	App       string `json:"app"`
	Catalog   string `json:"catalog,omitempty"` // empty: the official catalog
	ID        string `json:"id"`
	Version   string `json:"version"`
	Site      string `json:"site"`
	Policy    string `json:"policy"` // off, notify, patch, minor, all
	Installed string `json:"installed"`
	Updated   string `json:"updated,omitempty"`
	Notified  string `json:"notified,omitempty"`  // last version we told you about
	LastError string `json:"lastError,omitempty"` // last failed (rolled back) update
	Failed    string `json:"failed,omitempty"`    // version that failed: not retried automatically
	Major     string `json:"major,omitempty"`     // an apt app's major (17): its identity; apt updates within it
}

func readInstalled() map[string]Installed {
	m := map[string]Installed{}
	if b, err := os.ReadFile(config.StoreFile); err == nil {
		json.Unmarshal(b, &m)
	}
	return m
}

func writeInstalled(m map[string]Installed) error {
	b, _ := json.MarshalIndent(m, "", "  ")
	return config.WriteAtomic(config.StoreFile, b, 0o600)
}

var policies = map[string]bool{"off": true, "notify": true, "patch": true, "minor": true, "all": true}

// ---- packages ----

// healthy waits until the site answers: its health path under 400, or (no path / cron) its unit is active.
func healthy(app, site string, a CatalogApp) error {
	if a.kind() == "cron" {
		return nil
	}
	if a.kind() == "runtime" { // nothing runs: it's ready once it's published for other apps
		deadline := time.Now().Add(60 * time.Second)
		for time.Now().Before(deadline) {
			if runtimePublished(app, site) {
				return nil
			}
			time.Sleep(2 * time.Second)
		}
		return fmt.Errorf("%s wasn't published", site)
	}
	if a.Health == "" {
		deadline := time.Now().Add(60 * time.Second)
		for time.Now().Before(deadline) {
			time.Sleep(2 * time.Second)
			if UnitProps(UnitName(app, site), "ActiveState")["ActiveState"] == "active" {
				return nil
			}
		}
		return fmt.Errorf("%s didn't stay running", site)
	}
	if err := waitHealthy(app, site, a.Health, a.kind() == "web", 60*time.Second); err != nil {
		return fmt.Errorf("%s isn't healthy after 60 s: %v", site, err)
	}
	return nil
}

// ---- install / update / remove ----

type InstallOpts struct {
	Catalog, ID, App, Site, Policy string // Catalog: empty for the official one
	Version                        string // a major from the entry's versions (an apt app); empty for the newest
	Env                            map[string]string
	// prepare runs after the app is made and before the first deploy starts it (subductionmove.go fills its data)
	prepare func(app string) error
}

// StoreInstall installs a catalog app as a new nook app.
func (h *Host) StoreInstall(o InstallOpts) (err error) {
	if o.Catalog == "" {
		o.Catalog = OfficialCatalog
	}
	a, err := findCatalogApp(o.Catalog, o.ID)
	if err != nil {
		return err
	}
	major := ""
	if len(a.Versions) > 0 { // each major is its own app: postgres-17 beside postgres-16
		major = o.Version
		if major == "" {
			major = a.Versions[len(a.Versions)-1]
		}
		if !slices.Contains(a.Versions, major) {
			return fmt.Errorf("%s comes in %s, not %s", a.Name, strings.Join(a.Versions, ", "), major)
		}
		a = a.withVersion(major)
		if o.App == "" {
			o.App = a.ID + "-" + strings.ReplaceAll(major, ".", "-")
		}
		if o.Site == "" {
			o.Site = o.App
		}
	} else if o.Version != "" {
		return fmt.Errorf("%s has no versions to pick from", a.Name)
	}
	if o.App == "" {
		o.App = a.ID
	}
	if o.Site == "" {
		o.Site = a.site()
	}
	if o.Policy == "" {
		o.Policy = "minor"
	}
	if !policies[o.Policy] {
		return fmt.Errorf("policy must be off, notify, patch, minor or all")
	}
	if !names.App(o.App) || !names.Site(o.Site) {
		return fmt.Errorf("bad app or site name")
	}
	if _, err := LookupApp(o.App); err == nil {
		return fmt.Errorf("app %s already exists; pick another name", o.App)
	}
	for _, other := range Apps() { // a second copy of an app needs its own site too
		if slices.Contains(SiteNames(other), o.Site) {
			return fmt.Errorf("the site %s is app %s's already; pick another, like %s-2", o.Site, other, o.Site)
		}
	}
	env := map[string]string{}
	for _, f := range a.Env {
		v, ok := o.Env[f.Key]
		if !ok || v == "" {
			v = f.Default
		}
		if v == "" && f.Required {
			return fmt.Errorf("%s needs %s (%s)", a.Name, f.Key, f.Desc)
		}
		if v != "" {
			dash := ""
			if d := config.LoadDashboard(); d.Host != "" {
				dash = "https://" + d.Host
			}
			env[f.Key] = strings.NewReplacer("{site_host}", names.HostOf(o.Site, h.S.BaseDomain), "{site}", o.Site, "{dashboard_url}", dash).Replace(v)
		}
	}
	if !a.Official {
		h.say("from the community catalog %s, not the official one", a.CatalogName)
	}
	h.say("getting %s %s", a.Name, cmp.Or(major, a.Version))
	j := startJob(o.App, "install", a.Name, "", cmp.Or(major, a.Version))
	defer func() { j.finish(err) }()
	if a.Apt != nil {
		j.step(StepDownload)
		if err := h.aptInstall(a, catalogRepos(o.Catalog), false); err != nil {
			return err
		}
	}
	pkg, done, err := pkgFor(a, j)
	if err != nil {
		return err
	}
	defer done()
	if _, err := h.Create(CreateOpts{Name: o.App, Memory: a.Memory, CPU: a.CPU}); err != nil {
		return err
	}
	// a failed install removes the app it just made, so the same name can be tried again
	defer func() {
		if err != nil {
			h.say("install failed; removing app %s", o.App)
			if derr := h.Delete(o.App, o.App); derr != nil {
				h.say("couldn't remove %s: %v (delete it from the Apps page)", o.App, derr)
			}
		}
	}()
	if o.prepare != nil {
		if err := o.prepare(o.App); err != nil {
			return err
		}
	}
	if len(env) > 0 {
		if err := h.EnvApply(o.App, Scope{"site", o.Site}, env, nil, false); err != nil {
			return err
		}
	}
	if len(a.Ports) > 0 { // installing it is the admin's yes to the ports the entry lists
		if err := h.GrantPorts(o.App, a.Ports, false); err != nil {
			return err
		}
	}
	h.say("installing %s as app %s, site %s", a.Name, o.App, o.Site)
	j.step(StepDeploy)
	out, err := deployPackage(o.App, o.Site, pkg)
	h.say("%s", out)
	if err != nil && !strings.Contains(out, "(cron job)") && !strings.Contains(out, "(service)") && !strings.Contains(out, "(runtime)") {
		return fmt.Errorf("deploy: %v", err)
	}
	savePackage(o.App, a.Version, pkg)
	m := readInstalled()
	now := time.Now().UTC().Format(time.RFC3339)
	rec := Installed{App: o.App, Catalog: o.Catalog, ID: a.ID, Version: a.Version, Site: o.Site, Policy: o.Policy, Installed: now, Major: major}
	if a.Apt != nil {
		rec.Version = cmp.Or(aptVersion(a), major)
	}
	m[o.App] = rec
	if err := writeInstalled(m); err != nil {
		return err
	}
	for _, s := range a.Needs {
		n, _ := parseNeed(s) // checked with the catalog entry
		if p := h.defaultProvider(n.Name, consumerNeeds(o.App, n.Name)); p != "" {
			if err := h.Bind(o.App, n.Name, p, nil); err != nil {
				h.say("couldn't bind %s: %v (grant it on the Services page)", n.Name, err)
			}
		} else if n.Optional {
			h.say("%s can use %s: nothing provides it yet; it works without it", a.Name, n.Name)
		} else {
			h.say("%s needs %s: nothing provides it yet; grant it on the Services page", a.Name, s)
		}
	}
	j.step(StepHealth)
	if err := healthy(o.App, o.Site, a); err != nil {
		h.say("warning: %v (it's installed; check its logs)", err)
	} else {
		h.say("%s %s is up", a.Name, a.Version)
	}
	return nil
}

// StoreUpdate moves an installed app to the catalog's version, rolling back if it doesn't come up.
// force reinstalls even when the version is the same.
func (h *Host) StoreUpdate(app string, force bool) (err error) {
	m := readInstalled()
	rec, ok := m[app]
	if !ok {
		return fmt.Errorf("%s wasn't installed from the store", app)
	}
	a, err := findCatalogApp(rec.Catalog, rec.ID)
	if err != nil {
		return err
	}
	if a.Apt != nil {
		return h.aptUpdate(app, rec, a)
	}
	if !force && !versionNewer(a.Version, rec.Version) {
		h.say("%s is up to date (%s)", app, rec.Version)
		return nil
	}
	h.say("updating %s: %s → %s", app, rec.Version, a.Version)
	j := startJob(app, "update", a.Name, rec.Version, a.Version)
	defer func() { j.finish(err) }()
	pkg, done, err := pkgFor(a, j)
	if err != nil {
		return err
	}
	defer done()
	j.step(StepDeploy)
	out, derr := deployPackage(app, rec.Site, pkg)
	h.say("%s", out)
	herr := derr
	if herr == nil {
		j.step(StepHealth)
		herr = healthy(app, rec.Site, a)
	}
	if herr != nil {
		h.say("%s %s didn't come up (%v); rolling back to %s", app, a.Version, herr, rec.Version)
		j.step(StepRollback)
		old, lerr := loadPackage(app, rec.Version)
		if lerr != nil {
			return fmt.Errorf("update failed (%v) and there's no saved %s to roll back to: %v", herr, rec.Version, lerr)
		}
		if out, err := deployPackage(app, rec.Site, old); err != nil {
			return fmt.Errorf("update failed (%v) and the rollback failed too: %s %v", herr, out, err)
		}
		rec.LastError, rec.Failed = fmt.Sprintf("%s: %v (rolled back)", a.Version, herr), a.Version
		m[app] = rec
		writeInstalled(m)
		update.Notify("nook: "+app+" update rolled back", fmt.Sprintf("%s %s didn't come up (%v); it's back on %s.", app, a.Version, herr, rec.Version))
		return fmt.Errorf("%s %s didn't come up; rolled back to %s", app, a.Version, rec.Version)
	}
	prev := rec.Version
	savePackage(app, a.Version, pkg)
	prunePackages(app, a.Version, prev)
	rec.Version, rec.Updated, rec.LastError, rec.Failed = a.Version, time.Now().UTC().Format(time.RFC3339), "", ""
	m[app] = rec
	writeInstalled(m)
	h.say("%s is on %s", app, a.Version)
	return nil
}

// StoreSetPolicy sets how an installed app is updated.
func StoreSetPolicy(app, policy string) error {
	if !policies[policy] {
		return fmt.Errorf("policy must be off, notify, patch, minor or all")
	}
	m := readInstalled()
	rec, ok := m[app]
	if !ok {
		return fmt.Errorf("%s wasn't installed from the store", app)
	}
	rec.Policy = policy
	m[app] = rec
	return writeInstalled(m)
}

// storeForget drops a deleted app's record and packages.
func storeForget(app string) {
	m := readInstalled()
	if _, ok := m[app]; ok {
		delete(m, app)
		writeInstalled(m)
	}
	os.RemoveAll(storeDir(app))
}

// StoreCheck refreshes the catalog and, per app policy, updates or notifies. Run nightly. A version that
// already failed and rolled back isn't retried automatically; you're told about it instead.
func (h *Host) StoreCheck() error {
	var errs []string
	for _, src := range CatalogSources() {
		if _, err := loadFrom(src, true); err != nil {
			errs = append(errs, err.Error())
		}
	}
	for app, rec := range readInstalled() {
		a, err := findCatalogApp(rec.Catalog, rec.ID)
		if err == nil && a.Apt != nil { // apt's updates within the major, if the policy takes updates at all
			if rec.Policy != "off" && rec.Policy != "notify" {
				if err := h.StoreUpdate(app, false); err != nil {
					h.say("%s: %v", app, err)
				}
			}
			continue
		}
		if err != nil || !versionNewer(a.Version, rec.Version) {
			continue
		}
		if allowed(rec.Policy, rec.Version, a.Version) && rec.Failed != a.Version {
			if err := h.StoreUpdate(app, false); err != nil {
				h.say("%s: %v", app, err)
			}
			continue
		}
		if rec.Policy == "off" || rec.Notified == a.Version {
			continue
		}
		msg := fmt.Sprintf("%s %s is available (installed: %s). Update it on the dashboard's Store page.", a.Name, a.Version, rec.Version)
		if rec.Failed == a.Version {
			msg = fmt.Sprintf("%s %s failed its health check and was rolled back to %s; it won't be retried automatically. %s", a.Name, a.Version, rec.Version, rec.LastError)
		}
		update.Notify("nook: update for "+app, msg)
		m := readInstalled() // fresh: StoreUpdate may have written in the meantime
		if r, ok := m[app]; ok {
			r.Notified = a.Version
			m[app] = r
			writeInstalled(m)
		}
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

// allowed: may policy move from old to new without asking?
func allowed(policy, old, new string) bool {
	oM, om, _, ok1 := semver(old)
	nM, nm, _, ok2 := semver(new)
	switch policy {
	case "all":
		return true
	case "minor":
		return ok1 && ok2 && oM == nM
	case "patch":
		return ok1 && ok2 && oM == nM && om == nm
	}
	return false
}

func semver(v string) (int, int, int, bool) {
	p := strings.SplitN(strings.TrimPrefix(v, "v"), ".", 3)
	if len(p) != 3 {
		return 0, 0, 0, false
	}
	if f := strings.FieldsFunc(p[2], func(r rune) bool { return r == '-' || r == '+' }); len(f) > 0 {
		p[2] = f[0]
	}
	a, e1 := strconv.Atoi(p[0])
	b, e2 := strconv.Atoi(p[1])
	c, e3 := strconv.Atoi(p[2])
	return a, b, c, e1 == nil && e2 == nil && e3 == nil
}

func versionNewer(latest, current string) bool {
	lM, lm, lp, ok1 := semver(latest)
	cM, cm, cp, ok2 := semver(current)
	if !ok1 || !ok2 {
		return latest != current
	}
	if lM != cM {
		return lM > cM
	}
	if lm != cm {
		return lm > cm
	}
	return lp > cp
}

// ---- for the dashboard ----

type StoreView struct {
	Catalog   []CatalogApp  `json:"catalog"` // every catalog's apps, official first
	Catalogs  []CatalogInfo `json:"catalogs"`
	FetchedAt string        `json:"fetchedAt"` // the official catalog's
	Error     string        `json:"error,omitempty"`
	Installed []StoreRow    `json:"installed"`
	Taken     []string      `json:"taken"` // app names and site names in use, so a second install picks free ones
}

type CatalogInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Repo        string `json:"repo,omitempty"`
	URL         string `json:"url"`
	Official    bool   `json:"official"`
	Fingerprint string `json:"fingerprint"`
	Apps        int    `json:"apps"`
	FetchedAt   string `json:"fetchedAt,omitempty"`
	Error       string `json:"error,omitempty"`
}

type StoreRow struct {
	Installed
	Name        string `json:"name"`
	CatalogName string `json:"catalogName"`
	Official    bool   `json:"official"`
	Latest      string `json:"latest"`
	Available   bool   `json:"available"`
	URL         string `json:"url,omitempty"`
}

func StoreOverview(refresh bool) StoreView {
	v := StoreView{Catalog: []CatalogApp{}, Catalogs: []CatalogInfo{}, Installed: []StoreRow{}, Taken: []string{}}
	for _, a := range Apps() {
		v.Taken = append(v.Taken, "app:"+a)
		for _, s := range SiteNames(a) {
			v.Taken = append(v.Taken, "site:"+s)
		}
	}
	byKey := map[string]CatalogApp{}
	names_ := map[string]string{}
	for _, src := range CatalogSources() {
		info := CatalogInfo{ID: src.ID, Name: src.Name, Repo: src.Repo, URL: src.URL, Official: src.Official(), Fingerprint: update.KeyFingerprint(src.Key)}
		names_[src.ID] = src.Name
		c, err := loadFrom(src, refresh)
		if err != nil {
			info.Error = err.Error()
			if src.Official() {
				v.Error = err.Error()
			}
		}
		if c != nil {
			info.Apps, info.FetchedAt = len(c.Apps), c.FetchedAt
			if src.Official() {
				v.FetchedAt = c.FetchedAt
			}
			for _, a := range c.Apps {
				v.Catalog = append(v.Catalog, a)
				byKey[src.ID+"/"+a.ID] = a
			}
		}
		v.Catalogs = append(v.Catalogs, info)
	}
	base := config.Load().BaseDomain
	for _, rec := range readInstalled() {
		cat := rec.Catalog
		if cat == "" {
			cat = OfficialCatalog
		}
		row := StoreRow{Installed: rec, Name: rec.ID, CatalogName: names_[cat], Official: cat == OfficialCatalog}
		if row.CatalogName == "" {
			row.CatalogName = cat + " (removed)"
		}
		if a, ok := byKey[cat+"/"+rec.ID]; ok {
			row.Name, row.Latest, row.Available = a.Name, a.Version, versionNewer(a.Version, rec.Version)
			if a.kind() == "web" {
				row.URL = "https://" + names.HostOf(rec.Site, base)
			}
		}
		v.Installed = append(v.Installed, row)
	}
	sort.Slice(v.Installed, func(i, j int) bool { return v.Installed[i].App < v.Installed[j].App })
	return v
}

// aptUpdate takes apt's updates within an apt app's major, refreshes its files (run, scripts) from the catalog, and
// restarts it. apt can't reliably go back a version, so a failed health check is reported, not rolled back.
func (h *Host) aptUpdate(app string, rec Installed, a CatalogApp) error {
	if rec.Major == "" {
		return fmt.Errorf("%s has no recorded major version", app)
	}
	a = a.withVersion(rec.Major)
	if err := h.aptInstall(a, catalogRepos(rec.Catalog), true); err != nil {
		return err
	}
	pkg, done, err := pkgFor(a, &job{})
	if err != nil {
		return err
	}
	defer done()
	out, err := deployPackage(app, rec.Site, pkg)
	h.say("%s", out)
	if err != nil && !strings.Contains(out, "(service)") && !strings.Contains(out, "(cron job)") && !strings.Contains(out, "(runtime)") {
		return fmt.Errorf("deploy: %v", err)
	}
	h.restartSites(app, RunningSites(app))
	v := cmp.Or(aptVersion(a), rec.Version)
	herr := healthy(app, rec.Site, a)
	m := readInstalled()
	r := m[app]
	if v != r.Version {
		r.Updated = time.Now().UTC().Format(time.RFC3339)
	}
	r.Version, r.LastError = v, ""
	if herr != nil {
		r.LastError = fmt.Sprintf("%s: %v (apt can't roll back; check its logs)", v, herr)
		update.Notify("nook: "+app+" isn't healthy after its update", r.LastError)
	}
	m[app] = r
	writeInstalled(m)
	if herr != nil {
		return fmt.Errorf("%s %s didn't come up: %v", app, v, herr)
	}
	h.say("%s is on %s", app, v)
	return nil
}
