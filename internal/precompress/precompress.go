// Package precompress writes Brotli and gzip sidecars (name.br, name.gz) beside a static site's text files, so
// Caddy's `file_server { precompressed br gzip }` sends them without compressing on each request.
package precompress

import (
	"bytes"
	"compress/gzip"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/andybalholm/brotli"
)

// MinSize is the smallest file worth compressing: below it the saving doesn't cover the gzip header.
const MinSize = 1024

// Types that compress well. Images, fonts, video and archives are compressed already.
var types = map[string]bool{
	".html": true, ".htm": true, ".css": true, ".js": true, ".mjs": true, ".json": true, ".map": true,
	".svg": true, ".xml": true, ".txt": true, ".md": true, ".csv": true, ".wasm": true, ".webmanifest": true,
}

// Sidecar is one precompressed copy: its suffix and how to write it at the best (slowest) level.
type Sidecar struct {
	Ext string
	New func(io.Writer) io.WriteCloser
}

// Sidecars, in the order Caddy should prefer them: Brotli is smaller, and every browser that takes gzip over
// HTTPS takes Brotli too.
var Sidecars = []Sidecar{
	{".br", func(w io.Writer) io.WriteCloser { return brotli.NewWriterLevel(w, brotli.BestCompression) }},
	{".gz", func(w io.Writer) io.WriteCloser { z, _ := gzip.NewWriterLevel(w, gzip.BestCompression); return z }},
}

// Dir writes name.br and name.gz beside each compressible file under root, each when it saves at least a tenth.
// Sidecars the upload brought for a file that exists are removed first, so what's served always matches the
// file; a .br or .gz with no file beside it (a download) stays. Returns how many sidecars it wrote and the first
// error, carrying on past it.
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
		for _, sc := range Sidecars {
			if strings.HasSuffix(p, sc.Ext) {
				if _, err := os.Lstat(strings.TrimSuffix(p, sc.Ext)); err == nil {
					if err := os.Remove(p); err != nil {
						keep(err)
					}
				}
				return nil
			}
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
		for _, sc := range Sidecars {
			var buf bytes.Buffer
			w := sc.New(&buf)
			w.Write(b)
			if err := w.Close(); err != nil {
				keep(err)
				continue
			}
			if buf.Len() > len(b)*9/10 {
				continue
			}
			if err := os.WriteFile(p+sc.Ext, buf.Bytes(), 0o644); err != nil {
				keep(err)
				continue
			}
			n++
		}
	}
	return n, first
}
