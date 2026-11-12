// catalog-build turns a catalog repo's apps/*.json into the store's index.json: for each app it finds the
// release (the latest, or a pinned tag) on GitHub, picks the asset for each architecture by pattern,
// downloads it and pins its sha256. Catalog repos (sparkstonepdx/nook-catalog, and community ones copied
// from it) run it from their workflow, then sign and publish the index to their dist branch.
//
//	go run github.com/sparkstonepdx/nook/cmd/catalog-build@latest apps > index.json   (GITHUB_TOKEN raises the API rate limit)
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/sparkstonepdx/nook/internal/core"
)

type source struct {
	core.CatalogApp
	Source struct {
		GitHub  string            `json:"github"`  // owner/repo
		Tag     string            `json:"tag"`     // pin a release; default: the latest
		Assets  map[string]string `json:"assets"`  // GOARCH -> regexp for the asset name
		Format  string            `json:"format"`  // tar.gz, zip, gz or raw
		Version string            `json:"version"` // regexp with one group to take the version from the tag (default: the tag)
	} `json:"source"`
}

type release struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

var client = &http.Client{Timeout: 10 * time.Minute}

func get(url string, accept string) ([]byte, error) {
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Accept", accept)
	req.Header.Set("User-Agent", "nook-catalog-build")
	if t := os.Getenv("GITHUB_TOKEN"); t != "" && strings.HasPrefix(url, "https://api.github.com/") {
		req.Header.Set("Authorization", "Bearer "+t)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("%s: %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 500<<20))
}

func build(path string) (core.CatalogApp, error) {
	var s source
	b, err := os.ReadFile(path)
	if err != nil {
		return core.CatalogApp{}, err
	}
	if err := json.Unmarshal(b, &s); err != nil {
		return core.CatalogApp{}, fmt.Errorf("%s: %v", path, err)
	}
	a := s.CatalogApp
	if s.Source.GitHub == "" || len(s.Source.Assets) == 0 {
		return a, fmt.Errorf("%s: source.github and source.assets are required", path)
	}
	url := "https://api.github.com/repos/" + s.Source.GitHub + "/releases/latest"
	if s.Source.Tag != "" {
		url = "https://api.github.com/repos/" + s.Source.GitHub + "/releases/tags/" + s.Source.Tag
	}
	rb, err := get(url, "application/vnd.github+json")
	if err != nil {
		return a, err
	}
	var r release
	if err := json.Unmarshal(rb, &r); err != nil {
		return a, err
	}
	a.Version = r.TagName
	if s.Source.Version != "" {
		if m := regexp.MustCompile(s.Source.Version).FindStringSubmatch(r.TagName); len(m) > 1 {
			a.Version = m[1]
		}
	}
	format := s.Source.Format
	if format == "" {
		format = "tar.gz"
	}
	a.Artifacts = map[string]core.Artifact{}
	for arch, pattern := range s.Source.Assets {
		re := regexp.MustCompile(pattern)
		found := false
		for _, as := range r.Assets {
			if !re.MatchString(as.Name) {
				continue
			}
			data, err := get(as.URL, "application/octet-stream")
			if err != nil {
				return a, err
			}
			sum := sha256.Sum256(data)
			a.Artifacts[arch] = core.Artifact{URL: as.URL, SHA256: hex.EncodeToString(sum[:]), Format: format}
			fmt.Fprintf(os.Stderr, "%s %s %s: %s %x\n", a.ID, a.Version, arch, as.Name, sum[:6])
			found = true
			break
		}
		if !found {
			return a, fmt.Errorf("%s %s: no asset matches %q for %s", a.ID, r.TagName, pattern, arch)
		}
	}
	return a, core.CheckCatalogApp(a)
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: catalog-build DIR > index.json")
		os.Exit(2)
	}
	files, _ := filepath.Glob(filepath.Join(os.Args[1], "*.json"))
	sort.Strings(files)
	c := core.Catalog{Generated: time.Now().UTC().Format(time.RFC3339)}
	failed := false
	for _, f := range files {
		a, err := build(f)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			failed = true
			continue
		}
		c.Apps = append(c.Apps, a)
	}
	if failed {
		os.Exit(1)
	}
	out, _ := json.MarshalIndent(c, "", "  ")
	os.Stdout.Write(append(out, '\n'))
}
