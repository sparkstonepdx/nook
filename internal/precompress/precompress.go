// Package precompress writes gzip sidecars (name.gz) beside a static site's text files, so Caddy's
// `file_server { precompressed gzip }` sends them without compressing on each request. Standard library only,
// so gzip and nothing else.
package precompress

import (
	"bytes"
	"compress/gzip"
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
		b, err := os.ReadFile(p)
		if err != nil {
			keep(err)
			continue
		}
		if len(b) < MinSize {
			continue
		}
		var buf bytes.Buffer
		w, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
		w.Write(b)
		if err := w.Close(); err != nil {
			keep(err)
			continue
		}
		if buf.Len() > len(b)*9/10 {
			continue
		}
		if err := os.WriteFile(p+".gz", buf.Bytes(), 0o644); err != nil {
			keep(err)
			continue
		}
		n++
	}
	return n, first
}
