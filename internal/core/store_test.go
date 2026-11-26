package core

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"strings"
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
	bin, err := extractBytes(t, art, "tar.gz", "thing")
	if err != nil || string(bin) != "ELF" {
		t.Fatalf("extract: %q %v", bin, err)
	}
	if _, err := extractBytes(t, art, "tar.gz", "missing"); err == nil {
		t.Fatal("missing binary not reported")
	}
	a := CatalogApp{ID: "thing", Name: "Thing", Version: "v1.0.0", Binary: "thing", Kind: "service", Needs: []string{"postgres"},
		Files: map[string]string{"run": "#!/bin/sh\nexec ./thing --port $PORT\n"}}
	pkg, err := packageBytes(t, a, bin)
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
	bin, err := extractBytes(t, buf.Bytes(), "zip", "pocketbase")
	if err != nil || string(bin) != "\x7fELF binary" {
		t.Fatalf("got %q %v", bin, err)
	}
	if _, err := extractBytes(t, buf.Bytes(), "zip", "missing"); err == nil {
		t.Fatal("found a file that isn't there")
	}
	if b, _ := extractBytes(t, buf.Bytes(), "zip", "passwd"); string(b) == "nope" {
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

func TestCatalogTokenAuth(t *testing.T) {
	a := CatalogApp{ID: "push", Name: "push", Version: "v0.1.0", Binary: "push", Auth: "nook-token", Open: []string{"/v1/health", "/v1/send/*"},
		Artifacts: map[string]Artifact{}}
	pkg, err := packageBytes(t, a, []byte("ELF"))
	if err != nil {
		t.Fatal(err)
	}
	zr, _ := gzip.NewReader(bytes.NewReader(pkg))
	tr := tar.NewReader(zr)
	for {
		h, err := tr.Next()
		if err != nil {
			break
		}
		b, _ := io.ReadAll(tr)
		if h.Name == "nook.json" && (!bytes.Contains(b, []byte(`"auth": "nook-token"`)) || !bytes.Contains(b, []byte(`"/v1/send/*"`))) {
			t.Fatalf("nook.json: %s", b)
		}
	}
	for _, bad := range []CatalogApp{
		{Auth: "basic"},
		{Auth: "nook-token", Kind: "service"},
		{Open: []string{"/x"}},
		{Auth: "nook-token", Open: []string{"/x/*/y"}},
		{Auth: "nook-login", Kind: "service"},
	} {
		bad.ID, bad.Name, bad.Version, bad.Binary = "push", "push", "v0.1.0", "push"
		if err := bad.check(); err == nil || !strings.Contains(err.Error(), "auth") && !strings.Contains(err.Error(), "open") {
			t.Errorf("%+v: %v", bad, err)
		}
	}
}

func TestCatalogLogin(t *testing.T) {
	a := CatalogApp{ID: "files", Name: "Files", Version: "v0.2.0", Binary: "files", Auth: "nook-login", Open: []string{"/s/*", "/health"}}
	if err := a.check(); err != nil {
		t.Fatal(err)
	}
}

func TestBindRules(t *testing.T) {
	if got := bindRules(20001, false, 20000, 29999); got != "SocketBindAllow=tcp:20001\nSocketBindDeny=any\n" {
		t.Fatalf("default: %q", got)
	}
	got := bindRules(20001, true, 20000, 29999)
	for _, want := range []string{"SocketBindAllow=tcp:20001\n", "SocketBindAllow=tcp:1024-19999\n", "SocketBindAllow=tcp:30000-65535\n", "SocketBindAllow=udp:1024-19999\n", "SocketBindDeny=any\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("listen any: missing %q in %q", want, got)
		}
	}
	if strings.Contains(got, "20000-29999") || strings.Contains(got, ":1-") {
		t.Errorf("listen any must keep out of nook's app ports and privileged ones: %q", got)
	}
}

// extractBytes runs extractBinary on an artifact held in memory, through temp files as pkgFor does.
func extractBytes(t *testing.T, art []byte, format, name string) ([]byte, error) {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "art")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	f.Write(art)
	var out bytes.Buffer
	err = extractBinary(f, format, name, &out)
	return out.Bytes(), err
}

func packageBytes(t *testing.T, a CatalogApp, bin []byte) ([]byte, error) {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "bin")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	f.Write(bin)
	var out bytes.Buffer
	err = buildSitePackage(a, f, &out)
	return out.Bytes(), err
}

func TestCatalogRuntimeAndPorts(t *testing.T) {
	art := map[string]Artifact{"amd64": {URL: "https://example.com/frankenphp", SHA256: strings.Repeat("a", 64), Format: "raw"}}
	rt := CatalogApp{ID: "frankenphp", Name: "FrankenPHP", Version: "v1.4.0", Kind: "runtime", Binary: "frankenphp", Artifacts: art, Provides: map[string]ProvideSpec{"frankenphp": {Version: "1.4"}}}
	if err := rt.check(); err != nil {
		t.Fatal(err)
	}
	bad := rt
	bad.Files = map[string]string{"run": "#!/bin/sh\n"}
	if bad.check() == nil {
		t.Fatal("a runtime with a run file would be a web app")
	}
	mail := CatalogApp{ID: "mail", Name: "Mail", Version: "v1", Kind: "service", Binary: "mail", Artifacts: art, Ports: []int{25, 465, 587, 993}}
	if err := mail.check(); err != nil {
		t.Fatal(err)
	}
	for _, ports := range [][]int{{22}, {443}, {0}} {
		m := mail
		m.Ports = ports
		if m.check() == nil {
			t.Errorf("accepted ports %v", ports)
		}
	}
	cron := CatalogApp{ID: "job", Name: "Job", Version: "v1", Kind: "cron", Schedule: "daily", Binary: "job", Artifacts: art, Ports: []int{25}}
	if cron.check() == nil {
		t.Fatal("a cron job doesn't listen")
	}
}

func TestAdoptCheck(t *testing.T) {
	for _, c := range []struct {
		exists, managed, adopt, ok bool
	}{
		{false, false, false, true}, // a new install
		{true, false, false, false}, // the name is taken
		{true, false, true, true},   // taking over a hand-made app
		{false, false, true, false}, // nothing to take over
		{true, true, true, false},   // already the store's
	} {
		if err := adoptCheck("subduction", c.exists, c.managed, c.adopt); (err == nil) != c.ok {
			t.Errorf("%+v: %v", c, err)
		}
	}
}
