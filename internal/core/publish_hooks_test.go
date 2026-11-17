package core

import (
	"archive/tar"
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/sparkstonepdx/nook/internal/tarx"
)

// fakeHook stands in for a provider's publish script: it reads the tar nook sends and answers with edit's tar.
func fakeHook(t *testing.T, edit func(name, body string) (string, string, bool)) func() {
	old := publishHookRun
	publishHookRun = func(p publisher, extra map[string]string, in io.Reader, read func(io.Reader) error) (string, error) {
		var out bytes.Buffer
		tw := tar.NewWriter(&out)
		tr := tar.NewReader(in)
		var seen []string
		for {
			h, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return "", err
			}
			if h.Typeflag != tar.TypeReg {
				continue
			}
			b, _ := io.ReadAll(tr)
			seen = append(seen, h.Name)
			if name, body, ok := edit(h.Name, string(b)); ok {
				tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: name, Mode: 0o644, Size: int64(len(body))})
				tw.Write([]byte(body))
			}
		}
		tw.Close()
		sort.Strings(seen)
		return "saw " + strings.Join(seen, " ") + "\n", read(&out)
	}
	return func() { publishHookRun = old }
}

func stageSite(t *testing.T) string {
	dir := filepath.Join(t.TempDir(), "site")
	os.MkdirAll(filepath.Join(dir, "contact"), 0o755)
	os.WriteFile(filepath.Join(dir, "index.html"), []byte(`<form netlify name="hi"></form>`), 0o644)
	os.WriteFile(filepath.Join(dir, "contact", "index.html"), []byte(`<p>no form</p>`), 0o644)
	os.WriteFile(filepath.Join(dir, "app.js"), []byte(`x`), 0o644)
	return dir
}

func TestPublishHookRewrites(t *testing.T) {
	defer fakeHook(t, func(name, body string) (string, string, bool) {
		if !strings.Contains(body, "netlify") {
			return "", "", false // unchanged files aren't sent back
		}
		return name, strings.Replace(body, " netlify", "", 1), true
	})()
	dir := stageSite(t)
	var out bytes.Buffer
	h := &Host{Out: &out}
	p := publisher{Binding: Binding{App: "blog", Interface: "formcatch", Provider: "app:catch/catch"}, Spec: ProvideSpec{Publish: "./publish.sh"}}
	if err := h.runPublishHooks("blog", "www", dir, []publisher{p}, 1<<20); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "index.html")); string(b) != `<form name="hi"></form>` {
		t.Fatalf("not rewritten: %s", b)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "contact", "index.html")); string(b) != `<p>no form</p>` {
		t.Fatalf("untouched file changed: %s", b)
	}
	if !strings.Contains(out.String(), "blog: www: saw contact/index.html index.html") {
		t.Fatalf("hook should see only the HTML, and its log goes to the deploy: %q", out.String())
	}
	if _, err := os.Stat(dir + ".hook"); !os.IsNotExist(err) {
		t.Fatal("left its scratch folder")
	}
}

func TestPublishHookCantAddFiles(t *testing.T) {
	defer fakeHook(t, func(name, body string) (string, string, bool) { return "app.js", "evil", name == "index.html" })()
	dir := stageSite(t)
	h := &Host{Out: io.Discard}
	p := publisher{Binding: Binding{Interface: "formcatch", Provider: "app:catch/catch"}, Spec: ProvideSpec{Publish: "./publish.sh"}}
	err := h.runPublishHooks("blog", "www", dir, []publisher{p}, 1<<20)
	if err == nil || !strings.Contains(err.Error(), "wasn't sent") {
		t.Fatalf("handing back a file it wasn't sent should fail the publish: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "app.js")); string(b) != "x" {
		t.Fatal("changed a file it wasn't sent")
	}
}

func TestPostTarget(t *testing.T) {
	a := publisher{Binding: Binding{Provider: "app:a/a"}, Spec: ProvideSpec{Posts: true}, Port: 20001}
	b := publisher{Binding: Binding{Provider: "app:b/b"}, Spec: ProvideSpec{Posts: true}, Port: 20002}
	down := publisher{Binding: Binding{Provider: "app:c/c"}, Spec: ProvideSpec{Posts: true}}
	if port, note := postTarget([]publisher{down, a}); port != 20001 || note != "" {
		t.Fatalf("%d %q", port, note)
	}
	if port, note := postTarget([]publisher{a, b}); port != 20001 || !strings.Contains(note, "app:b/b also wants them") {
		t.Fatalf("%d %q", port, note)
	}
	if publishID([]publisher{a}) != "" {
		t.Fatal("a provider that only takes posts doesn't change what's published")
	}
}

func TestProvidesPublishChecked(t *testing.T) {
	ok := Manifest{Kind: "service", Provides: map[string]ProvideSpec{"formcatch": {Publish: "./publish.sh", Posts: true, PublishFiles: []string{"*.html"}}}}
	if err := checkServices(&ok); err != nil {
		t.Fatal(err)
	}
	for _, spec := range []ProvideSpec{
		{Publish: "../publish.sh"},
		{Publish: "./p.sh", PublishFiles: []string{"a/*.html"}},
		{Publish: "./p.sh", PublishFiles: []string{"[x"}},
		{}, // nothing to offer
	} {
		m := Manifest{Kind: "service", Provides: map[string]ProvideSpec{"formcatch": spec}}
		if err := checkServices(&m); err == nil {
			t.Errorf("accepted %+v", spec)
		}
	}
}

func TestPipeHook(t *testing.T) {
	dir := stageSite(t)
	in, err := packFiles(dir, []string{"contact/index.html", "index.html"})
	if err != nil {
		t.Fatal(err)
	}
	back := filepath.Join(t.TempDir(), "back")
	logs, err := pipeHook(exec.Command("sh", "-c", "echo looked >&2; cat"), bytes.NewReader(in),
		func(r io.Reader) error { return tarx.Extract(r, back, 1<<20) }, time.Minute)
	if err != nil || logs != "looked\n" {
		t.Fatalf("%v %q", err, logs)
	}
	if b, _ := os.ReadFile(filepath.Join(back, "contact", "index.html")); string(b) != "<p>no form</p>" {
		t.Fatalf("round trip: %q", b)
	}
	if _, err := pipeHook(exec.Command("sh", "-c", "echo bad >&2; exit 3"), bytes.NewReader(in), func(r io.Reader) error { _, err := io.ReadAll(r); return err }, time.Minute); err == nil {
		t.Fatal("a failing hook must fail")
	}
	if _, err := pipeHook(exec.Command("sleep", "5"), bytes.NewReader(in), func(r io.Reader) error { _, err := io.ReadAll(r); return err }, 100*time.Millisecond); err == nil {
		t.Fatal("a stuck hook must fail")
	}
}
