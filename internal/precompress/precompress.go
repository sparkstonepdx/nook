// Package precompress writes gzip sidecars (name.gz) beside a static site's text files, so Caddy's
// `file_server { precompressed gzip }` sends them without compressing on each request. Standard library only,
// so gzip and nothing else.
package precompress

import (
	"compress/gzip"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// MinSize is the smallest file worth compressing: below it the saving doesn't cover the gzip header.
const MinSize = 1024

// Types that compress well. Images, fonts, video and archives are compressed already.
var types = map[string]bool{
	".html": true, ".htm": true, ".css": true, ".js": true, ".mjs": true, ".json": true, ".map": true,
	".svg": true, ".xml": true, ".txt": true, ".md": true, ".csv": true, ".wasm": true, ".webmanifest": true,
}

// Dir writes name.gz beside each compressible file under root when it saves at least a tenth. Sidecars the
// upload brought for a file that exists are removed first, so what's served always matches the file; a .gz with
// no file beside it (a download) stays. Returns how many sidecars it wrote and the first error, carrying on past it.
func Dir(root string) (int, error) {
	var files []string
	var first error
	keep := func(err error) {
		if first == nil {
			first = err
		}
	}
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			keep(err)
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		if strings.HasSuffix(p, ".gz") {
			if _, err := os.Lstat(strings.TrimSuffix(p, ".gz")); err == nil {
				if err := os.Remove(p); err != nil {
					keep(err)
				}
			}
			return nil
		}
		if types[strings.ToLower(filepath.Ext(p))] {
			files = append(files, p)
		}
		return nil
	})
	n := 0
	for _, p := range files {
		wrote, err := sidecar(p)
		if err != nil {
			keep(err)
		}
		if wrote {
			n++
		}
	}
	return n, first
}

// sidecar gzips p into p.gz, streaming file to file, and keeps it only if it saves at least a tenth.
func sidecar(p string) (bool, error) {
	in, err := os.Open(p)
	if err != nil {
		return false, err
	}
	defer in.Close()
	fi, err := in.Stat()
	if err != nil || fi.Size() < MinSize {
		return false, err
	}
	out, err := os.CreateTemp(filepath.Dir(p), ".gz-*")
	if err != nil {
		return false, err
	}
	defer os.Remove(out.Name()) // a no-op once renamed
	w, _ := gzip.NewWriterLevel(out, gzip.BestCompression)
	_, err = io.Copy(w, in)
	if cerr := w.Close(); err == nil {
		err = cerr
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return false, err
	}
	gz, err := os.Stat(out.Name())
	if err != nil || gz.Size() > fi.Size()*9/10 {
		return false, err
	}
	os.Chmod(out.Name(), 0o644)
	return true, os.Rename(out.Name(), p+".gz")
}
