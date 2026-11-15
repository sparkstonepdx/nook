package core

// Store packages: downloading an app's release artifact, taking its binary out in memory, building the site
// package (binary, files, nook.json), deploying it, and keeping past versions for rollbacks.

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"cmp"
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
	"github.com/sparkstonepdx/nook/internal/update"
)

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
	if a.Auth != "" {
		man["auth"] = a.Auth
	}
	if len(a.Open) > 0 {
		man["open"] = a.Open
	}
	if a.Listen != "" {
		man["listen"] = a.Listen
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
		return "", fmt.Errorf("%s", cmp.Or(strings.TrimSpace(string(out)), err.Error()))
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
