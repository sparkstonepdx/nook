package precompress

import (
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andybalholm/brotli"
)

func TestDir(t *testing.T) {
	root := t.TempDir()
	big := strings.Repeat("<p>hello wordbag</p>\n", 200)
	write := func(name, body string) {
		p := filepath.Join(root, name)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("index.html", big)
	write("assets/app.js", big)
	write("assets/app.js.gz", "stale sidecar")
	write("assets/app.js.br", "stale sidecar")
	write("small.css", "a{}")
	write("logo.png", big)
	write("data.tar.gz", "a download")

	n, err := Dir(root)
	if err != nil || n != 4 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	for _, name := range []string{"index.html", "assets/app.js"} {
		gz, err := os.ReadFile(filepath.Join(root, name+".gz"))
		if err != nil {
			t.Fatal(err)
		}
		r, err := gzip.NewReader(bytes.NewReader(gz))
		if err != nil {
			t.Fatal(name, err)
		}
		if got, _ := io.ReadAll(r); !bytes.Equal(got, []byte(big)) {
			t.Fatalf("%s.gz doesn't match the file", name)
		}
		br, err := os.ReadFile(filepath.Join(root, name+".br"))
		if err != nil {
			t.Fatal(err)
		}
		if got, _ := io.ReadAll(brotli.NewReader(bytes.NewReader(br))); !bytes.Equal(got, []byte(big)) {
			t.Fatalf("%s.br doesn't match the file", name)
		}
	}
	for _, name := range []string{"small.css.gz", "small.css.br", "logo.png.gz", "logo.png.br"} {
		if _, err := os.Stat(filepath.Join(root, name)); err == nil {
			t.Fatalf("%s shouldn't exist", name)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(root, "data.tar.gz")); string(b) != "a download" {
		t.Fatal("a .gz with no file beside it was touched")
	}
}
