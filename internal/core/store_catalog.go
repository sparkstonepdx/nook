package core

// What a catalog entry is (CatalogApp, its images and artifacts), checking one, and finding one by id.

import (
	"fmt"
	"regexp"
	"strings"

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
	Repo        string              `json:"repo,omitempty"` // the original project's source, set by catalog-build from source.github
	Icon        *Image              `json:"icon,omitempty"`
	Screenshots []Image             `json:"screenshots,omitempty"`
	License     string              `json:"license,omitempty"`
	Version     string              `json:"version"`
	Kind        string              `json:"kind,omitempty"` // web (default), service, cron
	Site        string              `json:"site,omitempty"` // default site name (default: the id)
	Schedule    string              `json:"schedule,omitempty"`
	Health      string              `json:"health,omitempty"` // path answering < 400 when it's up
	Auth        string              `json:"auth,omitempty"`   // "nook-token": only requests with a deploy token from this server; "nook-login": those or a nook login
	Open        []string            `json:"open,omitempty"`   // with auth: paths that skip the token check
	Listen      string              `json:"listen,omitempty"` // "any": may listen on ports outside nook's app range (CI runners)
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

// Image is a picture in the catalog repo, published next to index.json and pinned by its hash. Servers
// fetch it only through the helper, which checks the hash, so the dashboard shows it from its own origin.
type Image struct {
	Path   string `json:"path"` // relative to the catalog's base URL: images/<app>/<file>.png
	SHA256 string `json:"sha256"`
	Alt    string `json:"alt,omitempty"`
	Width  int    `json:"width,omitempty"`
	Height int    `json:"height,omitempty"`
}

const (
	MaxImageBytes  = 1 << 20
	MaxScreenshots = 6
)

var imagePathRE = regexp.MustCompile(`^images/[a-z0-9-]+/[A-Za-z0-9_-][A-Za-z0-9._-]{0,63}\.(png|jpe?g)$`)

func (i Image) check() error {
	if !imagePathRE.MatchString(i.Path) || !sha256RE.MatchString(i.SHA256) || len(i.Alt) > 300 || i.Width < 0 || i.Height < 0 {
		return fmt.Errorf("bad image %q", i.Path)
	}
	return nil
}

type Catalog struct {
	Generated string       `json:"generated"`
	Apps      []CatalogApp `json:"apps"`
	FetchedAt string       `json:"fetchedAt,omitempty"`
}

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
	if a.Repo != "" && !strings.HasPrefix(a.Repo, "https://") {
		return fmt.Errorf("catalog entry %s: repo must be an https URL", a.ID)
	}
	if a.Icon != nil {
		if err := a.Icon.check(); err != nil {
			return fmt.Errorf("catalog entry %s: %v", a.ID, err)
		}
	}
	if len(a.Screenshots) > MaxScreenshots {
		return fmt.Errorf("catalog entry %s: at most %d screenshots", a.ID, MaxScreenshots)
	}
	for _, i := range a.Screenshots {
		if err := i.check(); err != nil {
			return fmt.Errorf("catalog entry %s: %v", a.ID, err)
		}
	}
	if a.Site != "" && !names.Site(a.Site) {
		return fmt.Errorf("catalog entry %s: bad site", a.ID)
	}
	if a.Auth != "" && (a.Auth != "nook-token" && a.Auth != "nook-login" || a.kind() != "web") {
		return fmt.Errorf(`catalog entry %s: auth can only be "nook-token" or "nook-login", on a web app`, a.ID)
	}
	if len(a.Open) > 0 && a.Auth == "" || len(a.Open) > 20 {
		return fmt.Errorf("catalog entry %s: open needs auth, and at most 20 paths", a.ID)
	}
	if a.Listen != "" && a.Listen != "any" {
		return fmt.Errorf(`catalog entry %s: listen can only be "any"`, a.ID)
	}
	for _, o := range a.Open {
		if !openPathRE.MatchString(o) {
			return fmt.Errorf("catalog entry %s: bad open path %q", a.ID, o)
		}
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
