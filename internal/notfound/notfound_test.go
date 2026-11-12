package notfound

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadWrite(t *testing.T) {
	Dir = filepath.Join(t.TempDir(), "nook-404")
	if html, custom, err := Read(); err != nil || custom || html != string(Default) {
		t.Fatalf("missing file: custom=%v err=%v", custom, err)
	}
	if err := Ensure(); err != nil {
		t.Fatal(err)
	}
	if err := Write("<h1>Lost</h1>"); err != nil {
		t.Fatal(err)
	}
	if html, custom, _ := Read(); html != "<h1>Lost</h1>" || !custom {
		t.Fatalf("after write: %q custom=%v", html, custom)
	}
	if err := Ensure(); err != nil {
		t.Fatal(err)
	}
	if html, _, _ := Read(); html != "<h1>Lost</h1>" {
		t.Fatal("Ensure replaced an edited page")
	}
	if err := Write("  \n"); err != nil {
		t.Fatal(err)
	}
	if _, custom, _ := Read(); custom {
		t.Fatal("an empty page didn't restore the default")
	}
	if err := Write(strings.Repeat("x", Max+1)); err == nil {
		t.Fatal("an oversized page was accepted")
	}
	if fi, _ := os.Stat(filepath.Join(Dir, "404.html")); fi.Mode().Perm() != 0o644 {
		t.Fatalf("mode %v: Caddy must be able to read it", fi.Mode())
	}
}
