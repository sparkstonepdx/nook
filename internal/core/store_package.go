package core

// Store packages: downloading an app's release artifact, taking its binary out in memory, building the site
// package (binary, files, nook.json), deploying it, and keeping past versions for rollbacks.

import (
	"archive/tar"
	"archive/zip"
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

// storeScratch holds pkgFor's working folders (a var for tests).
var storeScratch = config.LibDir

// maxArtifact caps a catalog download and the binary taken out of it.
const maxArtifact = 500 << 20

// pkgFor downloads a's artifact for this machine, checks its hash and builds the site package, all through
// files in a scratch folder, so memory stays flat whatever the size. done removes the folder.
func pkgFor(a CatalogApp, j *job) (pkg string, done func(), err error) {
	if a.Apt != nil { // the program came from apt: the package is just its files and nook.json
		return aptPackage(a)
	}
	art, ok := a.Artifacts[runtime.GOARCH]
	if !ok {
		return "", nil, fmt.Errorf("%s has no build for %s", a.ID, runtime.GOARCH)
	}
	os.MkdirAll(storeScratch, 0o755)
	dir, err := os.MkdirTemp(storeScratch, ".store-")
	if err != nil {
		return "", nil, err
	}
	// the cleanup is its own variable: a failed return sets the named done to nil, and calling that would panic
	cleanup := func() { os.RemoveAll(dir) }
	defer func() {
		if err != nil {
			cleanup()
		}
	}()
	dl, err := os.Create(filepath.Join(dir, "download"))
	if err != nil {
		return "", nil, err
	}
	defer dl.Close()
	h := sha256.New()
	j.step(StepDownload)
	if err := update.DownloadTo(art.URL, io.MultiWriter(dl, h, j), maxArtifact, j.total); err != nil {
		return "", nil, fmt.Errorf("download: %v", err)
	}
	j.step(StepCheck)
	if hex.EncodeToString(h.Sum(nil)) != art.SHA256 {
		return "", nil, errors.New("download doesn't match the catalog's sha256: refusing it")
	}
	j.step(StepUnpack)
	bin, err := os.Create(filepath.Join(dir, "binary"))
	if err != nil {
		return "", nil, err
	}
	defer bin.Close()
	if err := extractBinary(dl, art.Format, a.Binary, bin); err != nil {
		return "", nil, err
	}
	pkg = filepath.Join(dir, "package.tgz")
	out, err := os.Create(pkg)
	if err != nil {
		return "", nil, err
	}
	if err := buildSitePackage(a, bin, out); err != nil {
		out.Close()
		return "", nil, err
	}
	return pkg, cleanup, out.Close()
}

// copyCapped copies r to w, failing past maxArtifact instead of cutting the file short.
func copyCapped(w io.Writer, r io.Reader) error {
	n, err := io.Copy(w, io.LimitReader(r, maxArtifact+1))
	if err == nil && n > maxArtifact {
		err = fmt.Errorf("the binary is over %d MB", maxArtifact>>20)
	}
	return err
}

// extractBinary copies the executable named name out of the artifact in f into w, streaming.
var artifactFormats = map[string]bool{"tar.gz": true, "zip": true, "gz": true, "raw": true}

func extractBinary(f *os.File, format, name string, w io.Writer) error {
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	switch format {
	case "zip":
		st, err := f.Stat()
		if err != nil {
			return err
		}
		zr, err := zip.NewReader(f, st.Size())
		if err != nil {
			return err
		}
		for _, zf := range zr.File {
			if zf.Mode().IsRegular() && path.Base(zf.Name) == name {
				rc, err := zf.Open()
				if err != nil {
					return err
				}
				defer rc.Close()
				return copyCapped(w, rc)
			}
		}
		return fmt.Errorf("no %s in the archive", name)
	case "raw":
		return copyCapped(w, f)
	case "gz":
		zr, err := gzip.NewReader(f)
		if err != nil {
			return err
		}
		return copyCapped(w, zr)
	case "tar.gz":
		zr, err := gzip.NewReader(f)
		if err != nil {
			return err
		}
		tr := tar.NewReader(zr)
		for {
			h, err := tr.Next()
			if err == io.EOF {
				return fmt.Errorf("no %s in the archive", name)
			}
			if err != nil {
				return err
			}
			if h.Typeflag == tar.TypeReg && path.Base(h.Name) == name {
				return copyCapped(w, tr)
			}
		}
	}
	return fmt.Errorf("unknown artifact format %q", format)
}

// buildSitePackage writes the tar.gz that gets deployed to w: the binary (streamed from bin), the catalog's
// files, and nook.json.
func buildSitePackage(a CatalogApp, bin *os.File, w io.Writer) error {
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
	if len(a.Ports) > 0 {
		man["ports"] = a.Ports
	}
	if len(a.Provides) > 0 {
		man["provides"] = a.Provides
	}
	manJSON, _ := json.MarshalIndent(man, "", "  ")
	files := map[string][]byte{"nook.json": manJSON}
	for n, body := range a.Files {
		files[n] = []byte(body)
	}
	var size0 int64
	if bin != nil {
		st, err := bin.Stat()
		if err != nil {
			return err
		}
		size0 = st.Size()
	}
	zw := gzip.NewWriter(w)
	tw := tar.NewWriter(zw)
	var keys []string
	if bin != nil {
		keys = append(keys, a.Binary)
	}
	for n := range files {
		if bin == nil || n != a.Binary {
			keys = append(keys, n)
		}
	}
	sort.Strings(keys)
	for _, n := range keys {
		mode := int64(0o644)
		isBin := bin != nil && n == a.Binary
		if isBin || n == "run" || strings.HasSuffix(n, ".sh") {
			mode = 0o755
		}
		size := int64(len(files[n]))
		if isBin {
			size = size0
		}
		if err := tw.WriteHeader(&tar.Header{Name: n, Typeflag: tar.TypeReg, Mode: mode, Size: size, ModTime: time.Now()}); err != nil {
			return err
		}
		if isBin {
			if _, err := bin.Seek(0, io.SeekStart); err != nil {
				return err
			}
			if _, err := io.CopyN(tw, bin, size); err != nil {
				return err
			}
		} else if _, err := tw.Write(files[n]); err != nil {
			return err
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return zw.Close()
}

// deployPackage publishes the package file pkg as app's site, as the app user (the deploy API's path).
func deployPackage(app, site, pkg string) (string, error) {
	u, err := LookupApp(app)
	if err != nil {
		return "", err
	}
	f, err := os.Open(pkg)
	if err != nil {
		return "", err
	}
	defer f.Close()
	s := config.Load()
	if out, err := u.asAppIn(f, 64<<10, "internal-unpack", site, strconv.FormatInt(s.SiteMaxBytes, 10)); err != nil {
		return "", fmt.Errorf("%s", cmp.Or(strings.TrimSpace(string(out)), err.Error()))
	}
	out, err := u.asAppIn(nil, 64<<10, "site", "deploy", "--move", filepath.Join(u.Home, "builds", site), site)
	return strings.TrimSpace(string(out)), err
}

// savePackage keeps a copy of the package for rollbacks.
func savePackage(app, version, pkg string) {
	dir := storeDir(app)
	os.MkdirAll(dir, 0o700)
	in, err := os.Open(pkg)
	if err != nil {
		return
	}
	defer in.Close()
	tmp, err := os.CreateTemp(dir, ".save-")
	if err != nil {
		return
	}
	defer os.Remove(tmp.Name())
	_, err = io.Copy(tmp, in)
	if cerr := tmp.Close(); err == nil && cerr == nil {
		os.Chmod(tmp.Name(), 0o600)
		os.Rename(tmp.Name(), filepath.Join(dir, safeVersion(version)+".tgz"))
	}
}

// loadPackage is the saved package file for version, if there is one.
func loadPackage(app, version string) (string, error) {
	p := filepath.Join(storeDir(app), safeVersion(version)+".tgz")
	_, err := os.Stat(p)
	return p, err
}

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

// aptPackage builds an apt app's site package: its files (run, provision.sh ...) and nook.json, no binary.
func aptPackage(a CatalogApp) (pkg string, done func(), err error) {
	os.MkdirAll(storeScratch, 0o755)
	dir, err := os.MkdirTemp(storeScratch, ".store-")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { os.RemoveAll(dir) }
	pkg = filepath.Join(dir, "package.tgz")
	out, err := os.Create(pkg)
	if err != nil {
		cleanup()
		return "", nil, err
	}
	if err := buildSitePackage(a, nil, out); err != nil {
		out.Close()
		cleanup()
		return "", nil, err
	}
	return pkg, cleanup, out.Close()
}
