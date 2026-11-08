// Package tarx moves a static site from an app's folder into a root-owned copy without root ever opening
// a path the app controls. Pack runs as the app user (inside `app pack`) and follows symlinks with the
// app's own permissions; Extract runs as root and writes only plain files and folders.
package tarx

import (
	"archive/tar"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

var ErrTooBig = errors.New("site is over the size limit")

// Pack writes dir as a tar stream with symlinks resolved, skipping what can't be read. Stops at max bytes of file data.
func Pack(dir string, w io.Writer, max int64) error {
	tw := tar.NewWriter(w)
	var total int64
	var walk func(rel string, depth int) error
	walk = func(rel string, depth int) error {
		if depth > 40 {
			return nil // symlink loops
		}
		entries, err := os.ReadDir(filepath.Join(dir, rel))
		if err != nil {
			return nil
		}
		for _, e := range entries {
			name := path.Join(rel, e.Name())
			full := filepath.Join(dir, name)
			st, err := os.Stat(full) // follows symlinks, as this user
			if err != nil {
				continue
			}
			switch {
			case st.IsDir():
				if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeDir, Name: name + "/", Mode: 0o755}); err != nil {
					return err
				}
				if err := walk(name, depth+1); err != nil {
					return err
				}
			case st.Mode().IsRegular():
				f, err := os.Open(full)
				if err != nil {
					continue
				}
				total += st.Size()
				if total > max {
					f.Close()
					return ErrTooBig
				}
				err = tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: name, Mode: 0o644, Size: st.Size(), ModTime: st.ModTime()})
				if err == nil {
					_, err = io.CopyN(tw, f, st.Size())
				}
				f.Close()
				if err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk("", 0); err != nil {
		return err
	}
	return tw.Close()
}

// Extract unpacks a Pack stream into dst (which must not exist yet). Anything but plain files and folders,
// and any name that isn't a clean relative path, is rejected.
func Extract(r io.Reader, dst string, max int64) error {
	if err := os.Mkdir(dst, 0o755); err != nil {
		return err
	}
	tr := tar.NewReader(r)
	var total int64
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		name := strings.TrimSuffix(h.Name, "/")
		if name == "" || path.IsAbs(name) || path.Clean(name) != name || name == ".." || strings.HasPrefix(name, "../") || strings.Contains(name, "/../") {
			return fmt.Errorf("unsafe name in site: %q", h.Name)
		}
		target := filepath.Join(dst, filepath.FromSlash(name))
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.Mkdir(target, 0o755); err != nil && !errors.Is(err, fs.ErrExist) {
				return err
			}
		case tar.TypeReg:
			total += h.Size
			if total > max {
				return ErrTooBig
			}
			// O_EXCL + parent created by us: never follows anything
			f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
			if err != nil {
				return err
			}
			_, err = io.CopyN(f, tr, h.Size)
			if cerr := f.Close(); err == nil {
				err = cerr
			}
			if err != nil {
				return err
			}
			os.Chtimes(target, h.ModTime, h.ModTime)
		default:
			return fmt.Errorf("unexpected entry type in site: %q", h.Name)
		}
	}
}
