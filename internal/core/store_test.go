package core

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"io"
	"testing"
)

func TestVersionPolicy(t *testing.T) {
	cases := []struct {
		policy, old, new string
		want             bool
	}{
		{"patch", "v1.2.3", "v1.2.4", true}, {"patch", "v1.2.3", "v1.3.0", false},
		{"minor", "v1.2.3", "v1.3.0", true}, {"minor", "v1.2.3", "v2.0.0", false},
		{"all", "v1.2.3", "v2.0.0", true}, {"notify", "v1.2.3", "v1.2.4", false}, {"off", "1.0.0", "1.0.1", false},
		{"minor", "2.11.0", "v2.12.1", true},
	}
	for _, c := range cases {
		if got := allowed(c.policy, c.old, c.new); got != c.want {
			t.Errorf("%s %s→%s = %v", c.policy, c.old, c.new, got)
		}
	}
	if !versionNewer("v1.10.0", "v1.9.9") || versionNewer("v1.2.0", "v1.2.0") || !versionNewer("v2.0.0-rc1", "v1.9.0") {
		t.Error("versionNewer")
	}
	semver("1.2.") // must not panic
}

func tgz(t *testing.T, files map[string]string) []byte {
	var b bytes.Buffer
	zw := gzip.NewWriter(&b)
	tw := tar.NewWriter(zw)
	for n, body := range files {
		tw.WriteHeader(&tar.Header{Name: n, Typeflag: tar.TypeReg, Mode: 0o755, Size: int64(len(body))})
		tw.Write([]byte(body))
	}
	tw.Close()
	zw.Close()
	return b.Bytes()
}

func TestSitePackage(t *testing.T) {
	art := tgz(t, map[string]string{"pkg-1.0/README": "x", "pkg-1.0/bin/thing": "ELF", "../../etc/passwd": "evil"})
	bin, err := extractBinary(art, "tar.gz", "thing")
	if err != nil || string(bin) != "ELF" {
		t.Fatalf("extract: %q %v", bin, err)
	}
	if _, err := extractBinary(art, "tar.gz", "missing"); err == nil {
		t.Fatal("missing binary not reported")
	}
	a := CatalogApp{ID: "thing", Name: "Thing", Version: "v1.0.0", Binary: "thing", Kind: "service", Needs: []string{"postgres"},
		Files: map[string]string{"run": "#!/bin/sh\nexec ./thing --port $PORT\n"}}
	pkg, err := buildSitePackage(a, bin)
	if err != nil {
		t.Fatal(err)
	}
	zr, _ := gzip.NewReader(bytes.NewReader(pkg))
	tr := tar.NewReader(zr)
	got := map[string]int64{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		b, _ := io.ReadAll(tr)
		got[h.Name] = h.Mode
		if h.Name == "nook.json" && !bytes.Contains(b, []byte(`"service"`)) {
			t.Errorf("nook.json: %s", b)
		}
	}
	if len(got) != 3 || got["thing"] != 0o755 || got["run"] != 0o755 || got["nook.json"] != 0o644 {
		t.Fatalf("package: %v", got)
	}
}

func TestCatalogEntryChecks(t *testing.T) {
	good := CatalogApp{ID: "ntfy", Name: "ntfy", Version: "v2.11.0", Binary: "ntfy", Files: map[string]string{"run": "x"},
		Artifacts: map[string]Artifact{"amd64": {URL: "https://example.com/a.tar.gz", SHA256: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", Format: "tar.gz"}}}
	if err := good.check(); err != nil {
		t.Fatal(err)
	}
	bad := []func(a *CatalogApp){
		func(a *CatalogApp) { a.ID = "Bad ID" },
		func(a *CatalogApp) { a.Binary = "../x" },
		func(a *CatalogApp) { a.Files = map[string]string{"../run": "x"} },
		func(a *CatalogApp) { a.Files = map[string]string{"nook.json": "{}"} },
		func(a *CatalogApp) {
			a.Artifacts = map[string]Artifact{"amd64": {URL: "http://x", SHA256: good.Artifacts["amd64"].SHA256, Format: "tar.gz"}}
		},
		func(a *CatalogApp) {
			a.Artifacts = map[string]Artifact{"amd64": {URL: "https://x", SHA256: "nothex", Format: "tar.gz"}}
		},
		func(a *CatalogApp) { a.Env = []EnvField{{Key: "PORT"}} },
		func(a *CatalogApp) { a.Kind = "daemon" },
	}
	for i, f := range bad {
		a := good
		f(&a)
		if a.check() == nil {
			t.Errorf("bad entry %d accepted", i)
		}
	}
}

func TestExtractZip(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range map[string]string{"CHANGELOG.md": "notes", "pocketbase": "\x7fELF binary", "../../etc/passwd": "nope"} {
		w, _ := zw.Create(name)
		w.Write([]byte(body))
	}
	zw.Close()
	bin, err := extractBinary(buf.Bytes(), "zip", "pocketbase")
	if err != nil || string(bin) != "\x7fELF binary" {
		t.Fatalf("got %q %v", bin, err)
	}
	if _, err := extractBinary(buf.Bytes(), "zip", "missing"); err == nil {
		t.Fatal("found a file that isn't there")
	}
	if b, _ := extractBinary(buf.Bytes(), "zip", "passwd"); string(b) == "nope" {
		t.Log("a path's base name matches, but nothing is ever written to that path") // the binary is only ever read into memory
	}
}

func TestCatalogImages(t *testing.T) {
	sum := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	base := CatalogApp{ID: "thing", Name: "Thing", Version: "v1", Binary: "thing"}
	ok := base
	ok.Repo = "https://github.com/x/thing"
	ok.Icon = &Image{Path: "images/thing/icon.png", SHA256: sum}
	ok.Screenshots = []Image{{Path: "images/thing/home.jpg", SHA256: sum, Alt: "The home page", Width: 1280, Height: 800}}
	if err := ok.check(); err != nil {
		t.Fatal(err)
	}
	bad := []func(a *CatalogApp){
		func(a *CatalogApp) { a.Icon = &Image{Path: "images/../etc/passwd.png", SHA256: sum} },
		func(a *CatalogApp) { a.Icon = &Image{Path: "/images/thing/icon.png", SHA256: sum} },
		func(a *CatalogApp) { a.Icon = &Image{Path: "images/thing/icon.svg", SHA256: sum} },
		func(a *CatalogApp) { a.Icon = &Image{Path: "images/thing/icon.png", SHA256: "abc"} },
		func(a *CatalogApp) { a.Screenshots = make([]Image, MaxScreenshots+1) },
		func(a *CatalogApp) { a.Repo = "javascript:alert(1)" },
	}
	for i, f := range bad {
		a := base
		f(&a)
		if a.check() == nil {
			t.Errorf("case %d passed", i)
		}
	}
	if _, _, err := imageType([]byte("<svg onload=x>")); err == nil {
		t.Error("svg served as an image")
	}
	if _, _, err := StoreImage("official", "../../etc/passwd"); err == nil {
		t.Error("bad hash accepted")
	}
}
