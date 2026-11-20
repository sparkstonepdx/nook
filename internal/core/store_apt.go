package core

// Apps from apt (#42 step 2): Postgres, Collabora and the like come as Debian packages, not single binaries. apt
// supplies only the files; nook masks the distro's own units and runs the program as an ordinary nook app (its
// own user, slice, limits, firewall rules, logs). Official catalog only: apt runs package scripts as root.
//
// A third-party repository is defined once in the signed catalog ("repos") and pinned by its key's fingerprint:
// the key is fetched, checked against the fingerprint, and written as that repository's own keyring (Signed-By),
// so it can't vouch for anything else. Majors are part of an app's identity: postgres 17 installs as postgres-17,
// beside postgres-16 if you have it; within a major, apt's own updates apply.

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/sparkstonepdx/nook/internal/config"
	"github.com/sparkstonepdx/nook/internal/update"
)

type AptSource struct {
	Packages []string `json:"packages"`       // may contain {version}: postgresql-{version}
	Repo     string   `json:"repo,omitempty"` // a key of the catalog's repos; empty for Debian's own
}

type AptRepo struct {
	URL        string   `json:"url"`                  // https://apt.postgresql.org/pub/repos/apt
	Suite      string   `json:"suite"`                // may contain {codename}: {codename}-pgdg; "./" for a flat repository
	Components []string `json:"components,omitempty"` // main; none for a flat repository
	Key        string   `json:"key"`                  // the signing key's full fingerprint
	Keys       []string `json:"keys,omitempty"`       // more fingerprints the key file holds (a rotated key next to its successor)
	KeyURL     string   `json:"keyUrl"`               // where to fetch it
}

var (
	aptPkgRE     = regexp.MustCompile(`^[a-z0-9][a-z0-9+.-]{0,60}$`)
	aptRepoIDRE  = regexp.MustCompile(`^[a-z][a-z0-9-]{0,30}$`)
	aptSuiteRE   = regexp.MustCompile(`^[a-z0-9{}][a-z0-9{}._-]{0,40}$`)
	aptCompRE    = regexp.MustCompile(`^[a-z][a-z0-9-]{0,30}$`)
	fingerprRE   = regexp.MustCompile(`^[0-9A-F]{40}$`)
	majorRE      = regexp.MustCompile(`^[0-9]{1,4}(\.[0-9]{1,4})?$`)
	unitNameRE   = regexp.MustCompile(`^[A-Za-z0-9@{}._-]{1,80}$`)
	aptSourceDir = "/etc/apt/sources.list.d"
	aptKeyDir    = "/etc/apt/keyrings"
)

func (r AptRepo) check() error {
	flat := r.Suite == "./"
	if !strings.HasPrefix(r.URL, "https://") || strings.ContainsAny(r.URL, " \n") || !flat && !aptSuiteRE.MatchString(r.Suite) ||
		!fingerprRE.MatchString(r.Key) || !strings.HasPrefix(r.KeyURL, "https://") || flat != (len(r.Components) == 0) || len(r.Keys) > 4 {
		return errors.New("bad apt repository")
	}
	for _, k := range r.Keys {
		if !fingerprRE.MatchString(k) {
			return errors.New("bad apt repository key")
		}
	}
	for _, c := range r.Components {
		if !aptCompRE.MatchString(c) {
			return errors.New("bad apt component")
		}
	}
	return nil
}

func (a CatalogApp) checkApt() error {
	if len(a.Apt.Packages) == 0 || len(a.Apt.Packages) > 20 || (a.Apt.Repo != "" && !aptRepoIDRE.MatchString(a.Apt.Repo)) {
		return fmt.Errorf("catalog entry %s: apt needs 1-20 packages and a repo name", a.ID)
	}
	for _, p := range a.Apt.Packages {
		if !aptPkgRE.MatchString(strings.ReplaceAll(p, "{version}", "0")) {
			return fmt.Errorf("catalog entry %s: bad apt package %q", a.ID, p)
		}
	}
	if len(a.Artifacts) > 0 || a.Binary != "" {
		return fmt.Errorf("catalog entry %s: an apt app has no binary or artifacts", a.ID)
	}
	if a.Files["run"] == "" {
		return fmt.Errorf("catalog entry %s: an apt app needs a run file", a.ID)
	}
	for _, v := range a.Versions {
		if !majorRE.MatchString(v) {
			return fmt.Errorf("catalog entry %s: bad version %q", a.ID, v)
		}
	}
	for _, u := range a.Mask {
		if !unitNameRE.MatchString(u) {
			return fmt.Errorf("catalog entry %s: bad unit to mask %q", a.ID, u)
		}
	}
	return nil
}

// withVersion fills {version} in an apt app's packages, units and files.
func (a CatalogApp) withVersion(v string) CatalogApp {
	r := strings.NewReplacer("{version}", v)
	out := a
	if a.Apt != nil {
		ap := *a.Apt
		ap.Packages = nil
		for _, p := range a.Apt.Packages {
			ap.Packages = append(ap.Packages, r.Replace(p))
		}
		out.Apt = &ap
	}
	out.Mask = nil
	for _, u := range a.Mask {
		out.Mask = append(out.Mask, r.Replace(u))
	}
	out.Files = map[string]string{}
	for n, b := range a.Files {
		out.Files[n] = r.Replace(b)
	}
	if len(a.Provides) > 0 {
		out.Provides = map[string]ProvideSpec{}
		for k, p := range a.Provides {
			if p.Version == "" {
				p.Version = v
			}
			if len(p.Links) > 0 { // php -> /usr/bin/php{version}
				links := map[string]string{}
				for n, t := range p.Links {
					links[n] = r.Replace(t)
				}
				p.Links = links
			}
			out.Provides[k] = p
		}
	}
	return out
}

// aptFetchKey downloads a repository's key (a var for tests).
var aptFetchKey = update.Download

// aptRun runs apt and its helpers (a var for tests).
var aptRun = func(env []string, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Env = append([]string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "DEBIAN_FRONTEND=noninteractive", "LANG=C"}, env...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func codename() string {
	return config.Read("/etc/os-release")["VERSION_CODENAME"]
}

// aptEnsureRepo writes repo id's keyring and source once its key matches the pinned fingerprint.
func (h *Host) aptEnsureRepo(id string, r AptRepo) error {
	if err := r.check(); err != nil {
		return err
	}
	keyring := filepath.Join(aptKeyDir, "nook-"+id+".asc")
	if b, err := os.ReadFile(keyring); err != nil || r.keysMismatch(keyFingerprints(b)) != "" {
		h.say("fetching the %s repository's key", id)
		key, err := aptFetchKey(r.KeyURL)
		if err != nil {
			return fmt.Errorf("the %s repository's key: %v", id, err)
		}
		if why := r.keysMismatch(keyFingerprints(key)); why != "" {
			return fmt.Errorf("the %s repository's key file %s: refusing it", id, why)
		}
		os.MkdirAll(aptKeyDir, 0o755)
		if err := config.WriteAtomic(keyring, key, 0o644); err != nil {
			return err
		}
	}
	suite := strings.ReplaceAll(r.Suite, "{codename}", codename())
	comps := ""
	if len(r.Components) > 0 { // a flat repository ("./") has none
		comps = "Components: " + strings.Join(r.Components, " ") + "\n"
	}
	src := fmt.Sprintf("# written by nook from its signed catalog: do not edit\nTypes: deb\nURIs: %s\nSuites: %s\n%sSigned-By: %s\n",
		r.URL, suite, comps, keyring)
	path := filepath.Join(aptSourceDir, "nook-"+id+".sources")
	if old, _ := os.ReadFile(path); string(old) != src {
		os.MkdirAll(aptSourceDir, 0o755)
		if err := config.WriteAtomic(path, []byte(src), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// keysMismatch says what's wrong with a key file holding these primary keys ("" when it's right): it must hold
// the pinned key, and nothing the catalog doesn't pin, since apt trusts every key in it for this repository.
func (r AptRepo) keysMismatch(got []string) string {
	pinned := map[string]bool{r.Key: true}
	for _, k := range r.Keys {
		pinned[k] = true
	}
	has := false
	for _, g := range got {
		if !pinned[g] {
			return "holds " + g + ", which the catalog doesn't pin"
		}
		has = has || g == r.Key
	}
	if !has {
		return "doesn't hold " + r.Key + ", the key the catalog pins"
	}
	return ""
}

// keyFingerprints is the primary keys' fingerprints in an armored or binary OpenPGP key file (none if gpg can't
// read it).
func keyFingerprints(key []byte) []string {
	f, err := os.CreateTemp("", "nook-key-")
	if err != nil {
		return nil
	}
	defer os.Remove(f.Name())
	f.Write(key)
	f.Close()
	out, err := aptRun(nil, "gpg", "--batch", "--show-keys", "--with-colons", f.Name())
	if err != nil {
		return nil
	}
	var out2 []string
	pub := false
	for _, l := range strings.Split(out, "\n") {
		fields := strings.Split(l, ":")
		switch {
		case fields[0] == "pub":
			pub = true
		case fields[0] == "sub":
			pub = false
		case fields[0] == "fpr" && pub && len(fields) > 9:
			out2 = append(out2, fields[9])
			pub = false
		}
	}
	return out2
}

// aptInstall installs (or, with upgrade, updates) an apt app's packages and masks the distro's own units.
func (h *Host) aptInstall(a CatalogApp, repos map[string]AptRepo, upgrade bool) error {
	if a.Apt == nil {
		return nil
	}
	if !a.Official {
		return errors.New("apps from apt come from the official catalog only")
	}
	if a.Apt.Repo != "" {
		r, ok := repos[a.Apt.Repo]
		if !ok {
			return fmt.Errorf("the catalog doesn't define the apt repository %q", a.Apt.Repo)
		}
		if err := h.aptEnsureRepo(a.Apt.Repo, r); err != nil {
			return err
		}
	}
	h.say("apt: updating the package lists")
	if out, err := aptRun(nil, "apt-get", "update", "-q"); err != nil {
		return fmt.Errorf("apt-get update: %v\n%s", err, lastLines(out, 6))
	}
	args := []string{"install", "-y", "-q", "--no-install-recommends", "-o", "Dpkg::Options::=--force-confold"}
	if upgrade {
		args = append(args, "--only-upgrade")
	}
	h.say("apt: %s %s", map[bool]string{false: "installing", true: "upgrading"}[upgrade], strings.Join(a.Apt.Packages, " "))
	if out, err := aptRun(nil, "apt-get", append(args, a.Apt.Packages...)...); err != nil {
		return fmt.Errorf("apt-get install: %v\n%s", err, lastLines(out, 8))
	}
	// the distro starts its own copy (a default cluster on 5432): nook runs it instead
	for _, u := range a.Mask {
		aptRun(nil, "systemctl", "disable", "--now", u)
		aptRun(nil, "systemctl", "mask", u)
	}
	return nil
}

// aptVersion is what apt has installed of an app's first package ("" if it can't tell).
func aptVersion(a CatalogApp) string {
	if a.Apt == nil || len(a.Apt.Packages) == 0 {
		return ""
	}
	out, err := aptRun(nil, "dpkg-query", "-W", "-f=${Version}", a.Apt.Packages[0])
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// VerifyAptRepo is catalog-build's check of one repos.json entry: its fields, that its key file holds exactly the
// pinned keys, and that the repository answers for Debian 13 (trixie). fetch reads a URL. It's the check the first
// install would otherwise make, run when the catalog is built so a typo fails CI instead of an install.
func VerifyAptRepo(id string, r AptRepo, fetch func(url string) ([]byte, error)) error {
	if !aptRepoIDRE.MatchString(id) {
		return fmt.Errorf("repos.json: %q isn't a repository name (a-z, 0-9, -)", id)
	}
	if err := r.check(); err != nil {
		return fmt.Errorf("repos.json: %s: %v", id, err)
	}
	key, err := fetch(r.KeyURL)
	if err != nil {
		return fmt.Errorf("repos.json: %s: its key: %v", id, err)
	}
	if why := r.keysMismatch(keyFingerprints(key)); why != "" {
		return fmt.Errorf("repos.json: %s: the key file at %s %s", id, r.KeyURL, why)
	}
	base := strings.TrimSuffix(r.URL, "/") + "/"
	if r.Suite == "./" {
		base += "./"
	} else {
		base += "dists/" + strings.ReplaceAll(r.Suite, "{codename}", "trixie") + "/"
	}
	if _, err := fetch(base + "InRelease"); err != nil {
		if _, err2 := fetch(base + "Release"); err2 != nil {
			return fmt.Errorf("repos.json: %s: no InRelease or Release at %s: %v", id, base, err)
		}
	}
	return nil
}
