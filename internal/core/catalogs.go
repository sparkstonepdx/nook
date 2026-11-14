package core

// Catalog sources. The official catalog is built in and signed with a key compiled into nook. Community
// catalogs are added by repo (or URL); the key they're signed with is read from the catalog when it's
// added and pinned, so a later change of key is refused rather than trusted.
//
// A catalog is three files at a base URL: index.json, index.json.sig and catalog.pub. A GitHub repo
// publishes them to its dist branch (see sparkstonepdx/nook-catalog), so "owner/repo" means
// https://raw.githubusercontent.com/owner/repo/dist/.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/sparkstonepdx/nook/internal/config"
	"github.com/sparkstonepdx/nook/internal/update"
)

const (
	OfficialCatalog     = "official"
	OfficialCatalogRepo = "sparkstonepdx/nook-catalog"
)

type CatalogSource struct {
	ID    string `json:"id"`   // "official", or owner-repo for a community catalog
	Name  string `json:"name"` // what the UI calls it: the repo, or the URL's host
	Repo  string `json:"repo,omitempty"`
	URL   string `json:"url"` // base: index.json, index.json.sig and catalog.pub live here
	Key   string `json:"key"` // base64 ed25519 public key, pinned when added
	Added string `json:"added,omitempty"`
}

func (s CatalogSource) Official() bool { return s.ID == OfficialCatalog }

var (
	ghRepoRE    = regexp.MustCompile(`^([A-Za-z0-9][A-Za-z0-9-]{0,38})/([A-Za-z0-9._-]{1,100})$`)
	catalogIdRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,80}$`)
)

func catalogsFile() string { return filepath.Join(filepath.Dir(config.StoreFile), "catalogs.json") }

func catalogCacheDir() string { return filepath.Join(config.LibDir, "catalogs") }

func officialSource() CatalogSource {
	url := "https://raw.githubusercontent.com/" + OfficialCatalogRepo + "/dist/"
	if test, ok := update.TestServer(); ok { // tests: UPDATE_API serves the catalog
		url = test + "/catalog/"
	}
	return CatalogSource{ID: OfficialCatalog, Name: "Official", Repo: OfficialCatalogRepo, URL: url, Key: update.OfficialCatalogKey()}
}

// CatalogSources lists the official catalog first, then the community ones by name.
func CatalogSources() []CatalogSource {
	var community []CatalogSource
	if b, err := os.ReadFile(catalogsFile()); err == nil {
		json.Unmarshal(b, &community)
	}
	sort.Slice(community, func(i, j int) bool { return community[i].Name < community[j].Name })
	return append([]CatalogSource{officialSource()}, community...)
}

func catalogSource(id string) (CatalogSource, bool) {
	if id == "" {
		id = OfficialCatalog // records from before community catalogs
	}
	for _, s := range CatalogSources() {
		if s.ID == id {
			return s, true
		}
	}
	return CatalogSource{}, false
}

func writeCommunity(list []CatalogSource) error {
	var community []CatalogSource
	for _, s := range list {
		if !s.Official() {
			community = append(community, s)
		}
	}
	b, _ := json.MarshalIndent(community, "", "  ")
	return config.WriteAtomic(catalogsFile(), b, 0o644)
}

// sourceFor turns what someone typed ("owner/repo", a GitHub URL, or any https base URL) into a source
// with no key yet.
func sourceFor(in string) (CatalogSource, error) {
	in = strings.TrimSpace(in)
	in = strings.TrimSuffix(strings.TrimPrefix(in, "https://github.com/"), "/")
	in = strings.TrimSuffix(in, ".git")
	if m := ghRepoRE.FindStringSubmatch(in); m != nil {
		id := strings.ToLower(m[1] + "-" + m[2])
		return CatalogSource{ID: id, Name: in, Repo: in, URL: "https://raw.githubusercontent.com/" + in + "/dist/"}, nil
	}
	test, isTest := update.TestServer()
	if strings.HasPrefix(in, "https://") || (isTest && strings.HasPrefix(in, test)) {
		base := strings.TrimSuffix(strings.TrimSuffix(in, "index.json"), "/") + "/"
		host := strings.SplitN(strings.TrimPrefix(strings.TrimPrefix(base, "https://"), "http://"), "/", 2)[0]
		id := strings.ToLower(regexp.MustCompile(`[^A-Za-z0-9._-]+`).ReplaceAllString(strings.Trim(strings.TrimPrefix(strings.TrimPrefix(base, "https://"), "http://"), "/"), "-"))
		if len(id) > 80 {
			id = id[:80]
		}
		return CatalogSource{ID: id, Name: host, URL: base}, nil
	}
	return CatalogSource{}, fmt.Errorf("give a GitHub repo (owner/name) or an https URL")
}

// AddCatalog adds a community catalog: it reads the catalog's key, checks the catalog is signed with it,
// and pins that key.
func AddCatalog(in string) (CatalogSource, error) {
	s, err := sourceFor(in)
	if err != nil {
		return s, err
	}
	if !catalogIdRE.MatchString(s.ID) || s.ID == OfficialCatalog {
		return s, fmt.Errorf("that name can't be used for a catalog")
	}
	list := CatalogSources()
	for _, x := range list {
		if x.ID == s.ID || x.URL == s.URL {
			return x, fmt.Errorf("%s is already added", x.Name)
		}
	}
	key, err := update.Download(s.URL + "catalog.pub")
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			return s, fmt.Errorf("there's no catalog at %s (no catalog.pub). A catalog repo publishes to its dist branch", s.URL)
		}
		return s, fmt.Errorf("couldn't read %scatalog.pub: %v", s.URL, err)
	}
	s.Key = strings.TrimSpace(string(key))
	if s.Key == update.OfficialCatalogKey() {
		return s, fmt.Errorf("that's the official catalog's key; it's already built in")
	}
	s.Added = time.Now().UTC().Format(time.RFC3339)
	if _, err := fetchFrom(s); err != nil {
		return s, err
	}
	if err := writeCommunity(append(list, s)); err != nil {
		return s, err
	}
	return s, nil
}

// RemoveCatalog drops a community catalog. Apps installed from it keep running but get no more updates.
func RemoveCatalog(id string) error {
	if id == OfficialCatalog || id == "" {
		return fmt.Errorf("the official catalog can't be removed")
	}
	list := CatalogSources()
	var keep []CatalogSource
	found := false
	for _, s := range list {
		if s.ID == id {
			found = true
			continue
		}
		keep = append(keep, s)
	}
	if !found {
		return fmt.Errorf("no catalog %q", id)
	}
	os.Remove(filepath.Join(catalogCacheDir(), id+".json"))
	return writeCommunity(keep)
}

// fetchFrom downloads one catalog, verifies it with the source's pinned key and caches it.
func fetchFrom(s CatalogSource) (*Catalog, error) {
	data, err := update.Download(s.URL + "index.json")
	if err != nil {
		return nil, fmt.Errorf("%s catalog: %v", s.Name, err)
	}
	sig, err := update.Download(s.URL + "index.json.sig")
	if err != nil {
		return nil, fmt.Errorf("%s catalog signature: %v", s.Name, err)
	}
	if err := update.VerifyWith(s.Key, data, sig); err != nil {
		return nil, fmt.Errorf("%s catalog: %v", s.Name, err)
	}
	var c Catalog
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("%s catalog: %v", s.Name, err)
	}
	var ok []CatalogApp
	for _, a := range c.Apps {
		if err := a.check(); err == nil {
			a.Catalog, a.CatalogName, a.Official = s.ID, s.Name, s.Official()
			ok = append(ok, a)
		}
	}
	c.Apps = ok
	c.FetchedAt = time.Now().UTC().Format(time.RFC3339)
	b, _ := json.Marshal(c)
	os.MkdirAll(catalogCacheDir(), 0o755)
	config.WriteAtomic(filepath.Join(catalogCacheDir(), s.ID+".json"), b, 0o644)
	return &c, nil
}

// loadFrom returns a catalog's cached copy, fetching it when it's missing, over an hour old, or refresh
// is set. Offline, the last verified copy is used.
func loadFrom(s CatalogSource, refresh bool) (*Catalog, error) {
	var c Catalog
	if b, err := os.ReadFile(filepath.Join(catalogCacheDir(), s.ID+".json")); err == nil && json.Unmarshal(b, &c) == nil {
		if t, err := time.Parse(time.RFC3339, c.FetchedAt); err == nil && time.Since(t) < time.Hour && !refresh {
			return &c, nil
		}
	}
	fresh, err := fetchFrom(s)
	if err != nil && c.Apps != nil {
		return &c, err // the last verified copy, with the error to show
	}
	return fresh, err
}

// StoreImage returns a catalog image by hash, for the dashboard. Only images a catalog lists can be fetched,
// each is checked against its pinned hash, and the verified copy is kept, named by its hash.
func StoreImage(catalog, sum string) (string, []byte, error) {
	if !sha256RE.MatchString(sum) {
		return "", nil, fmt.Errorf("bad image")
	}
	dir := filepath.Join(catalogCacheDir(), "images")
	cached := filepath.Join(dir, sum)
	if b, err := os.ReadFile(cached); err == nil {
		return imageType(b)
	}
	src, ok := catalogSource(catalog)
	if !ok {
		return "", nil, fmt.Errorf("no catalog %q", catalog)
	}
	c, _ := loadFrom(src, false)
	if c == nil {
		return "", nil, fmt.Errorf("couldn't read the %s catalog", src.Name)
	}
	path := ""
	for _, a := range c.Apps {
		for _, i := range append(a.Screenshots, derefImage(a.Icon)...) {
			if i.SHA256 == sum {
				path = i.Path
			}
		}
	}
	if path == "" {
		return "", nil, fmt.Errorf("the %s catalog has no such image", src.Name)
	}
	b, err := update.Download(src.URL + path)
	if err != nil {
		return "", nil, err
	}
	got := sha256.Sum256(b)
	if len(b) > MaxImageBytes || hex.EncodeToString(got[:]) != sum {
		return "", nil, fmt.Errorf("%s doesn't match the hash the catalog pinned", path)
	}
	ctype, b, err := imageType(b)
	if err != nil {
		return "", nil, err
	}
	os.MkdirAll(dir, 0o755)
	config.WriteAtomic(cached, b, 0o644)
	return ctype, b, nil
}

func derefImage(i *Image) []Image {
	if i == nil {
		return nil
	}
	return []Image{*i}
}

func imageType(b []byte) (string, []byte, error) {
	switch t := http.DetectContentType(b); t {
	case "image/png", "image/jpeg":
		return t, b, nil
	}
	return "", nil, fmt.Errorf("not a PNG or JPEG")
}
