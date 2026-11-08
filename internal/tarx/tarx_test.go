package tarx

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestPackExtract(t *testing.T) {
	src := t.TempDir()
	os.MkdirAll(filepath.Join(src, "a/b"), 0o755)
	os.WriteFile(filepath.Join(src, "index.html"), []byte("hi"), 0o644)
	os.WriteFile(filepath.Join(src, "a/b/c.txt"), []byte("deep"), 0o644)
	outside := filepath.Join(t.TempDir(), "outside.txt")
	os.WriteFile(outside, []byte("followed"), 0o644)
	os.Symlink(outside, filepath.Join(src, "link.txt"))
	os.Symlink("/nonexistent", filepath.Join(src, "dangling"))
	os.Symlink(".", filepath.Join(src, "loop"))
	var buf bytes.Buffer
	if err := Pack(src, &buf, 1<<20); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "out")
	if err := Extract(&buf, dst, 1<<20); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dst, "a/b/c.txt")); string(b) != "deep" {
		t.Fatal("deep file")
	}
	// the symlink became a plain copy of what this user could read (Pack runs as the app user in real use)
	if st, err := os.Lstat(filepath.Join(dst, "link.txt")); err != nil || st.Mode()&os.ModeSymlink != 0 {
		t.Fatal("link.txt should be a plain file")
	}
	filepath.Walk(dst, func(p string, i os.FileInfo, _ error) error {
		if i.Mode()&os.ModeSymlink != 0 {
			t.Errorf("symlink in output: %s", p)
		}
		return nil
	})
}

func TestExtractRejects(t *testing.T) {
	for _, h := range []*tar.Header{
		{Typeflag: tar.TypeReg, Name: "../evil", Size: 1},
		{Typeflag: tar.TypeReg, Name: "/etc/passwd", Size: 1},
		{Typeflag: tar.TypeSymlink, Name: "x", Linkname: "/etc/shadow"},
		{Typeflag: tar.TypeLink, Name: "x", Linkname: "/etc/shadow"},
		{Typeflag: tar.TypeReg, Name: "a/../../b", Size: 1},
	} {
		var buf bytes.Buffer
		tw := tar.NewWriter(&buf)
		tw.WriteHeader(h)
		if h.Size > 0 {
			tw.Write([]byte("x"))
		}
		tw.Close()
		if err := Extract(&buf, filepath.Join(t.TempDir(), "o"), 100); err == nil {
			t.Errorf("accepted %q", h.Name)
		}
	}
}

func TestTooBig(t *testing.T) {
	src := t.TempDir()
	os.WriteFile(filepath.Join(src, "big"), bytes.Repeat([]byte("x"), 1000), 0o644)
	var buf bytes.Buffer
	if err := Pack(src, &buf, 500); err != ErrTooBig {
		t.Fatal("expected ErrTooBig")
	}
}
