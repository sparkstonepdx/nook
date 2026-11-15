package core

// The directory: which of this server's sites are listed publicly. The list is in /etc/nook/directory.json
// (hosts, in the order shown); everything else about a listed site (its name, icon, colors, screenshots) comes from
// its own web manifest, which nook-web reads. GET /api/v1/directory is the public list a Directory app shows.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/sparkstonepdx/nook/internal/caddyfile"
	"github.com/sparkstonepdx/nook/internal/config"
)

var directoryFile = "/etc/nook/directory.json"

type DirectoryEntry struct {
	Host    string `json:"host"`
	App     string `json:"app"`
	Listed  bool   `json:"listed"`
	Preview bool   `json:"preview"` // a PR preview (blog-pr-12.example.com): left out of the launcher
}

var previewRE = regexp.MustCompile(`^([^.]*-)?pr-\d+\.`)

func readListed() []string {
	var d struct {
		Listed []string `json:"listed"`
	}
	if b, err := os.ReadFile(directoryFile); err == nil {
		json.Unmarshal(b, &d)
	}
	return d.Listed
}

// Directory is every live site: listed ones first, in their order, then the rest by host.
func Directory() []DirectoryEntry {
	listed := readListed()
	var out []DirectoryEntry
	files, _ := filepath.Glob(filepath.Join(config.CaddyAppsDir, "*.caddy"))
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		app := strings.TrimSuffix(filepath.Base(f), ".caddy")
		live, _ := caddyfile.Hosts(string(b))
		for _, h := range live {
			out = append(out, DirectoryEntry{Host: h, App: app, Listed: slices.Contains(listed, h), Preview: previewRE.MatchString(h)})
		}
	}
	rank := func(h string) int {
		if i := slices.Index(listed, h); i >= 0 {
			return i
		}
		return len(listed)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if ri, rj := rank(out[i].Host), rank(out[j].Host); ri != rj {
			return ri < rj
		}
		return out[i].Host < out[j].Host
	})
	if out == nil {
		out = []DirectoryEntry{}
	}
	return out
}

// SetListed lists host in the directory (at the end) or takes it off.
func (h *Host) SetListed(host string, on bool) error {
	host = strings.ToLower(strings.TrimSpace(host))
	listed := readListed()
	if on {
		found := false
		for _, e := range Directory() {
			found = found || e.Host == host
		}
		if !found {
			return fmt.Errorf("%s isn't one of this server's sites", host)
		}
		if !slices.Contains(listed, host) {
			listed = append(listed, host)
		}
	} else {
		listed = slices.DeleteFunc(listed, func(x string) bool { return x == host })
	}
	b, _ := json.MarshalIndent(map[string]any{"listed": listed}, "", "  ")
	if err := config.WriteAtomic(directoryFile, b, 0o644); err != nil {
		return err
	}
	if on {
		h.say("%s is listed in the directory", host)
	} else {
		h.say("%s is no longer listed", host)
	}
	return nil
}
