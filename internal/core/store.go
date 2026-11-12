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
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/sparkstonepdx/nook/internal/config"
	"github.com/sparkstonepdx/nook/internal/names"
	"github.com/sparkstonepdx/nook/internal/update"
)

type EnvField struct {
	Key      string `json:"key"`
	Desc     string `json:"desc,omitempty"`
	Default  string `json:"default,omitempty"`
	Secret   bool   `json:"secret,omitempty"`
	Required bool   `json:"required,omitempty"`
}

type Artifact struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
	Format string `json:"format"` // tar.gz, zip, gz (one gzipped file) or raw
}

type CatalogApp struct {
	ID          string              `json:"id"`
	Name        string              `json:"name"`
	Description string              `json:"description"`
	Homepage    string              `json:"homepage,omitempty"`
	License     string              `json:"license,omitempty"`
	Version     string              `json:"version"`
	Kind        string              `json:"kind,omitempty"` // web (default), service, cron
	Site        string              `json:"site,omitempty"` // default site name (default: the id)
	Schedule    string              `json:"schedule,omitempty"`
	Health      string              `json:"health,omitempty"` // path answering < 400 when it's up
	Memory      string              `json:"memory,omitempty"`
	CPU         string              `json:"cpu,omitempty"`
	Needs       []string            `json:"needs,omitempty"`
	Env         []EnvField          `json:"env,omitempty"`
	Binary      string              `json:"binary"`          // executable's file name inside the artifact
	Files       map[string]string   `json:"files,omitempty"` // more files for the site folder (run, config ...)
	Artifacts   map[string]Artifact `json:"artifacts"`       // by GOARCH

	// Set by nook from the catalog it was read from; whatever a catalog says here is overwritten.
	Catalog     string `json:"catalog,omitempty"`
	CatalogName string `json:"catalogName,omitempty"`
	Official    bool   `json:"official,omitempty"`
}

type Catalog struct {
	Generated string       `json:"generated"`
	Apps      []CatalogApp `json:"apps"`
	FetchedAt string       `json:"fetchedAt,omitempty"`
}

var (
	catalogIDRE = regexp.MustCompile(`^[a-z][a-z0-9-]{0,30}$`)
	fileNameRE  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	sha256RE    = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

func storeDir(app string) string { return filepath.Join(config.WWWDir, "..", "store", app) }

func (a CatalogApp) check() error {
	if !catalogIDRE.MatchString(a.ID) || a.Name == "" || a.Version == "" || len(a.Version) > 40 {
		return fmt.Errorf("catalog entry %q: bad id, name or version", a.ID)
	}
	if !fileNameRE.MatchString(a.Binary) {
		return fmt.Errorf("catalog entry %s: bad binary name", a.ID)
	}
	for name, body := range a.Files {
		if !fileNameRE.MatchString(name) || name == a.Binary || name == "nook.json" || len(body) > 64<<10 {
			return fmt.Errorf("catalog entry %s: bad file %q", a.ID, name)
		}
	}
	for arch, art := range a.Artifacts {
		test, isTest := update.TestServer() // UPDATE_API (root-set, for tests) may serve plain http
		okURL := strings.HasPrefix(art.URL, "https://") || (isTest && strings.HasPrefix(art.URL, test))
		if !okURL || !sha256RE.MatchString(art.SHA256) || !artifactFormats[art.Format] {
			return fmt.Errorf("catalog entry %s/%s: bad artifact", a.ID, arch)
		}
	}
	if a.Site != "" && !names.Site(a.Site) {
		return fmt.Errorf("catalog entry %s: bad site", a.ID)
	}
	switch a.Kind {
	case "", "web", "service", "cron":
	default:
		return fmt.Errorf("catalog entry %s: bad kind", a.ID)
	}
	for _, f := range a.Env {
		if !envNameRE.MatchString(f.Key) || names.ReservedEnv[f.Key] {
			return fmt.Errorf("catalog entry %s: bad env %q", a.ID, f.Key)
		}
	}
	m := Manifest{Kind: a.kind(), Needs: a.Needs}
	return checkServices(&m)
}

// CheckCatalogApp validates an entry the way servers will (used by the catalog builder).
func CheckCatalogApp(a CatalogApp) error { return a.check() }

func (a CatalogApp) kind() string {
	if a.Kind == "" {
		return "web"
	}
	return a.Kind
}

func (a CatalogApp) site() string {
	if a.Site != "" {
		return a.Site
	}
	return a.ID
}

func findCatalogApp(catalog, id string) (CatalogApp, error) {
	src, ok := catalogSource(catalog)
	if !ok {
		return CatalogApp{}, fmt.Errorf("the catalog it came from (%s) was removed", catalog)
	}
	c, err := loadFrom(src, false)
	if c == nil {
		return CatalogApp{}, err
	}
	for _, a := range c.Apps {
		if a.ID == id {
			return a, nil
		}
	}
	return CatalogApp{}, fmt.Errorf("%q isn't in the %s catalog", id, src.Name)
}

// ---- installed apps ----

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

// pkgFor downloads a's artifact for this machine, checks its hash and builds the site package.
func pkgFor(a CatalogApp) ([]byte, error) {
	art, ok := a.Artifacts[runtime.GOARCH]
	if !ok {
		return nil, fmt.Errorf("%s has no build for %s", a.ID, runtime.GOARCH)
	}
	data, err := update.Download(art.URL)
	if err != nil {
		return nil, fmt.Errorf("download: %v", err)
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != art.SHA256 {
		return nil, errors.New("download doesn't match the catalog's sha256: refusing it")
	}
	bin, err := extractBinary(data, art.Format, a.Binary)
	if err != nil {
		return nil, err
	}
	return buildSitePackage(a, bin)
}

// extractBinary reads the executable out of the artifact in memory.
var artifactFormats = map[string]bool{"tar.gz": true, "zip": true, "gz": true, "raw": true}

func extractBinary(data []byte, format, name string) ([]byte, error) {
	switch format {
	case "zip":
		zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return nil, err
		}
		for _, f := range zr.File {
			if f.Mode().IsRegular() && path.Base(f.Name) == name {
				rc, err := f.Open()
				if err != nil {
					return nil, err
				}
				defer rc.Close()
				return io.ReadAll(io.LimitReader(rc, 500<<20))
			}
		}
		return nil, fmt.Errorf("no %s in the archive", name)
	case "raw":
		return data, nil
	case "gz":
		zr, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		return io.ReadAll(io.LimitReader(zr, 500<<20))
	case "tar.gz":
		zr, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		tr := tar.NewReader(zr)
		for {
			h, err := tr.Next()
			if err == io.EOF {
				return nil, fmt.Errorf("no %s in the archive", name)
			}
			if err != nil {
				return nil, err
			}
			if h.Typeflag == tar.TypeReg && path.Base(h.Name) == name {
				return io.ReadAll(io.LimitReader(tr, 500<<20))
			}
		}
	}
	return nil, fmt.Errorf("unknown artifact format %q", format)
}

// buildSitePackage makes the tar.gz that gets deployed: the binary, the catalog's files, and nook.json.
func buildSitePackage(a CatalogApp, bin []byte) ([]byte, error) {
	man := map[string]any{"kind": a.kind()}
	if a.Schedule != "" {
		man["schedule"] = a.Schedule
	}
	if len(a.Needs) > 0 {
		man["needs"] = a.Needs
	}
	if a.Health != "" && a.kind() != "cron" { // so the dashboard checks it too
		man["health"] = a.Health
	}
	manJSON, _ := json.MarshalIndent(man, "", "  ")
	files := map[string][]byte{a.Binary: bin, "nook.json": manJSON}
	for n, body := range a.Files {
		files[n] = []byte(body)
	}
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	keys := make([]string, 0, len(files))
	for n := range files {
		keys = append(keys, n)
	}
	sort.Strings(keys)
	for _, n := range keys {
		mode := int64(0o644)
		if n == a.Binary || n == "run" {
			mode = 0o755
		}
		if err := tw.WriteHeader(&tar.Header{Name: n, Typeflag: tar.TypeReg, Mode: mode, Size: int64(len(files[n])), ModTime: time.Now()}); err != nil {
			return nil, err
		}
		tw.Write(files[n])
	}
	tw.Close()
	zw.Close()
	return buf.Bytes(), nil
}

// deployPackage publishes pkg as app's site, as the app user (the deploy API's path).
func deployPackage(app, site string, pkg []byte) (string, error) {
	u, err := LookupApp(app)
	if err != nil {
		return "", err
	}
	s := config.Load()
	if out, err := u.asAppIn(bytes.NewReader(pkg), 64<<10, "internal-unpack", site, strconv.FormatInt(s.SiteMaxBytes, 10)); err != nil {
		return "", fmt.Errorf("%s", firstNonEmpty(strings.TrimSpace(string(out)), err.Error()))
	}
	out, err := u.asAppIn(nil, 64<<10, "site", "deploy", filepath.Join(u.Home, "builds", site), site)
	return strings.TrimSpace(string(out)), err
}

func savePackage(app, version string, pkg []byte) {
	dir := storeDir(app)
	os.MkdirAll(dir, 0o700)
	config.WriteAtomic(filepath.Join(dir, safeVersion(version)+".tgz"), pkg, 0o600)
}

func loadPackage(app, version string) ([]byte, error) {
	return os.ReadFile(filepath.Join(storeDir(app), safeVersion(version)+".tgz"))
}

// prunePackages keeps the packages of keep (current and previous version).
func prunePackages(app string, keep ...string) {
	want := map[string]bool{}
	for _, k := range keep {
		want[safeVersion(k)+".tgz"] = true
	}
	entries, _ := os.ReadDir(storeDir(app))
	for _, e := range entries {
		if !want[e.Name()] {
			os.Remove(filepath.Join(storeDir(app), e.Name()))
		}
	}
}

func safeVersion(v string) string {
	return regexp.MustCompile(`[^A-Za-z0-9._-]`).ReplaceAllString(v, "_")
}

// healthy waits until the site answers: its health path under 400, or (no path / cron) its unit is active.
func healthy(app, site string, a CatalogApp) error {
	if a.kind() == "cron" {
		return nil
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
	Env                            map[string]string
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
			env[f.Key] = strings.NewReplacer("{site_host}", names.HostOf(o.Site, h.S.BaseDomain), "{site}", o.Site).Replace(v)
		}
	}
	if !a.Official {
		h.say("from the community catalog %s, not the official one", a.CatalogName)
	}
	h.say("downloading %s %s", a.Name, a.Version)
	pkg, err := pkgFor(a)
	if err != nil {
		return err
	}
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
	if len(env) > 0 {
		if err := h.EnvApply(o.App, Scope{"site", o.Site}, env, nil, false); err != nil {
			return err
		}
	}
	h.say("installing %s as app %s, site %s", a.Name, o.App, o.Site)
	out, err := deployPackage(o.App, o.Site, pkg)
	h.say("%s", out)
	if err != nil && !strings.Contains(out, "(cron job)") && !strings.Contains(out, "(service)") {
		return fmt.Errorf("deploy: %v", err)
	}
	savePackage(o.App, a.Version, pkg)
	m := readInstalled()
	now := time.Now().UTC().Format(time.RFC3339)
	m[o.App] = Installed{App: o.App, Catalog: o.Catalog, ID: a.ID, Version: a.Version, Site: o.Site, Policy: o.Policy, Installed: now}
	if err := writeInstalled(m); err != nil {
		return err
	}
	for _, need := range a.Needs {
		if p := h.defaultProvider(need); p != "" {
			if err := h.Bind(o.App, need, p, nil); err != nil {
				h.say("couldn't bind %s: %v (grant it on the Services page)", need, err)
			}
		} else {
			h.say("%s needs %s: nothing provides it yet; grant it on the Services page", a.Name, need)
		}
	}
	if err := healthy(o.App, o.Site, a); err != nil {
		h.say("warning: %v (it's installed; check its logs)", err)
	} else {
		h.say("%s %s is up", a.Name, a.Version)
	}
	return nil
}

// StoreUpdate moves an installed app to the catalog's version, rolling back if it doesn't come up.
// force reinstalls even when the version is the same.
func (h *Host) StoreUpdate(app string, force bool) error {
	m := readInstalled()
	rec, ok := m[app]
	if !ok {
		return fmt.Errorf("%s wasn't installed from the store", app)
	}
	a, err := findCatalogApp(rec.Catalog, rec.ID)
	if err != nil {
		return err
	}
	if !force && !versionNewer(a.Version, rec.Version) {
		h.say("%s is up to date (%s)", app, rec.Version)
		return nil
	}
	h.say("updating %s: %s → %s", app, rec.Version, a.Version)
	pkg, err := pkgFor(a)
	if err != nil {
		return err
	}
	out, derr := deployPackage(app, rec.Site, pkg)
	h.say("%s", out)
	herr := derr
	if herr == nil {
		herr = healthy(app, rec.Site, a)
	}
	if herr != nil {
		h.say("%s %s didn't come up (%v); rolling back to %s", app, a.Version, herr, rec.Version)
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
	v := StoreView{Catalog: []CatalogApp{}, Catalogs: []CatalogInfo{}, Installed: []StoreRow{}}
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
