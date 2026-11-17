package core

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func pgEntry() CatalogApp {
	return CatalogApp{ID: "postgres", Name: "PostgreSQL", Version: "17", Kind: "service", Versions: []string{"16", "17"},
		Apt:      &AptSource{Packages: []string{"postgresql-{version}"}, Repo: "pgdg"},
		Mask:     []string{"postgresql@{version}-main"},
		Provides: map[string]ProvideSpec{"postgres": {Env: map[string]string{"DATABASE_URL": ""}, Provision: "./provision.sh"}},
		Files:    map[string]string{"run": "#!/bin/sh\nexec /usr/lib/postgresql/{version}/bin/postgres\n", "provision.sh": "#!/bin/sh\n"}}
}

func TestAptEntry(t *testing.T) {
	a := pgEntry()
	if err := a.check(); err != nil {
		t.Fatal(err)
	}
	v := a.withVersion("16")
	if v.Apt.Packages[0] != "postgresql-16" || v.Mask[0] != "postgresql@16-main" || !strings.Contains(v.Files["run"], "/postgresql/16/") || v.Provides["postgres"].Version != "16" {
		t.Fatalf("withVersion: %+v %v %q %+v", v.Apt, v.Mask, v.Files["run"], v.Provides)
	}
	if a.Apt.Packages[0] != "postgresql-{version}" {
		t.Fatal("withVersion changed the entry it was given")
	}
	for _, bad := range []func(*CatalogApp){
		func(a *CatalogApp) { a.Apt.Packages = []string{"postgresql; rm -rf /"} },
		func(a *CatalogApp) { a.Binary = "postgres" },
		func(a *CatalogApp) { a.Versions = []string{"17-beta"} },
		func(a *CatalogApp) { a.Mask = []string{"x y"} },
		func(a *CatalogApp) { delete(a.Files, "run") },
	} {
		b := pgEntry()
		bad(&b)
		if b.check() == nil {
			t.Errorf("accepted %+v", b)
		}
	}
	for _, r := range []AptRepo{
		{URL: "http://apt.example", Suite: "x", Components: []string{"main"}, Key: strings.Repeat("A", 40), KeyURL: "https://k"},
		{URL: "https://apt.example", Suite: "x", Components: []string{"main"}, Key: "ACCC4CF8", KeyURL: "https://k"},
		{URL: "https://apt.example\nURIs: https://evil", Suite: "x", Components: []string{"main"}, Key: strings.Repeat("A", 40), KeyURL: "https://k"},
	} {
		if r.check() == nil {
			t.Errorf("accepted repo %+v", r)
		}
	}
}

func TestAptPackage(t *testing.T) {
	a := pgEntry().withVersion("17")
	pkg, done, err := aptPackage(a)
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	b, _ := os.ReadFile(pkg)
	zr, _ := gzip.NewReader(bytes.NewReader(b))
	tr := tar.NewReader(zr)
	got := map[string]int64{}
	var man string
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		body, _ := io.ReadAll(tr)
		got[h.Name] = h.Mode
		if h.Name == "nook.json" {
			man = string(body)
		}
	}
	if len(got) != 3 || got["run"] != 0o755 || got["provision.sh"] != 0o755 || got["nook.json"] != 0o644 {
		t.Fatalf("package: %v", got)
	}
	if !strings.Contains(man, `"provides"`) || !strings.Contains(man, `"provision": "./provision.sh"`) || !strings.Contains(man, `"version": "17"`) {
		t.Fatalf("nook.json: %s", man)
	}
}

func TestAptInstallSteps(t *testing.T) {
	var calls []string
	old, oldSrc, oldKey, oldFetch := aptRun, aptSourceDir, aptKeyDir, aptFetchKey
	aptFetchKey = func(string) ([]byte, error) { return []byte("KEY"), nil }
	dir := t.TempDir()
	aptSourceDir, aptKeyDir = filepath.Join(dir, "sources"), filepath.Join(dir, "keys")
	fpr := strings.Repeat("B", 40)
	aptRun = func(env []string, name string, args ...string) (string, error) {
		calls = append(calls, name+" "+strings.Join(args, " "))
		if name == "gpg" {
			return "pub:-:4096:1:7FCC7D46ACCC4CF8:::\nfpr:::::::::" + fpr + ":\n", nil
		}
		return "", nil
	}
	t.Cleanup(func() { aptRun, aptSourceDir, aptKeyDir, aptFetchKey = old, oldSrc, oldKey, oldFetch })
	keySrv := "https://www.postgresql.org/media/keys/ACCC4CF8.asc"
	repos := map[string]AptRepo{"pgdg": {URL: "https://apt.postgresql.org/pub/repos/apt", Suite: "{codename}-pgdg", Components: []string{"main"}, Key: fpr, KeyURL: keySrv}}
	h := New(&strings.Builder{})
	a := pgEntry().withVersion("17")
	if err := h.aptInstall(a, repos, false); err == nil {
		t.Fatal("installed an apt app that isn't from the official catalog")
	}
	a.Official = true
	if err := h.aptInstall(a, repos, false); err != nil {
		t.Fatal(err)
	}
	src, _ := os.ReadFile(filepath.Join(aptSourceDir, "nook-pgdg.sources"))
	if !strings.Contains(string(src), "URIs: https://apt.postgresql.org/pub/repos/apt\n") || !strings.Contains(string(src), "Signed-By: "+filepath.Join(aptKeyDir, "nook-pgdg.asc")) || strings.Contains(string(src), "{codename}") {
		t.Fatalf("sources: %s", src)
	}
	joined := strings.Join(calls, "\n")
	for _, want := range []string{"apt-get update -q", "--no-install-recommends", "postgresql-17", "systemctl mask postgresql@17-main"} {
		if !strings.Contains(joined, want) {
			t.Errorf("no %q in\n%s", want, joined)
		}
	}
	// a key that isn't the pinned one is refused, and nothing is installed
	calls = nil
	os.Remove(filepath.Join(aptKeyDir, "nook-pgdg.asc"))
	r := repos["pgdg"]
	r.Key = strings.Repeat("C", 40)
	if err := h.aptInstall(a, map[string]AptRepo{"pgdg": r}, false); err == nil || !strings.Contains(err.Error(), "refusing") {
		t.Fatalf("wrong key: %v", err)
	}
	if strings.Contains(strings.Join(calls, "\n"), "apt-get install") {
		t.Fatal("installed with a key that doesn't match")
	}
}
