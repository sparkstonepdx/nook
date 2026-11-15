package core

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sparkstonepdx/nook/internal/config"
)

func TestMeasureAppDisk(t *testing.T) {
	root := t.TempDir()
	home, www, logs := filepath.Join(root, "home"), filepath.Join(root, "www"), filepath.Join(root, "logs")
	old := diskRoots
	diskRoots = func(string) map[string][]string {
		return map[string][]string{
			"sites":  {filepath.Join(home, "sites"), www},
			"builds": {filepath.Join(home, "builds")},
			"home":   {home},
			"logs":   {logs},
			"store":  {filepath.Join(root, "none")}, // missing folders count as nothing
		}
	}
	defer func() { diskRoots = old }()
	write := func(p string, n int) {
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(strings.Repeat("x", n)), 0o644)
	}
	write(filepath.Join(home, "sites", "app", "index.html"), 100<<10)
	write(filepath.Join(www, "app", "index.html"), 100<<10)
	write(filepath.Join(home, "data", "db.sqlite"), 1<<20)
	write(filepath.Join(logs, "app.log"), 50<<10)
	// an alias hard link (what Files does) counts once
	os.Link(filepath.Join(home, "data", "db.sqlite"), filepath.Join(home, "data", "latest.sqlite"))

	d := measureAppDisk("x")
	kb := func(n uint64) uint64 { return n >> 10 }
	if kb(d.Sites) < 200 || kb(d.Sites) > 260 {
		t.Errorf("sites: %d KB", kb(d.Sites))
	}
	if kb(d.Data) < 1024 || kb(d.Data) > 1100 {
		t.Errorf("data (the hard link once, sites not again): %d KB", kb(d.Data))
	}
	if kb(d.Logs) < 50 || d.Store != 0 || d.Total != d.Sites+d.Builds+d.Data+d.Logs+d.Store {
		t.Errorf("logs %d, store %d, total %d", d.Logs, d.Store, d.Total)
	}
}

func TestDiskTrend(t *testing.T) {
	now := time.Now()
	all := history(now, 7, func(age time.Duration, s *Sample) {
		days := 7 - age.Hours()/24
		s.Disks = []DiskSample{{Path: config.AppsDir, Total: 100 << 30, Free: 10 << 30}}
		// growing 500 MB a day: 10 GB free lasts 20 days
		s.Apps["files"] = AppSample{Mem: 20 * mib1, Disk: uint64((1000 + 500*days) * mib1)}
		// steady, and measured only from 2 days ago
		d := uint64(0)
		if age < 48*time.Hour {
			d = 300 * mib1
		}
		s.Apps["blog"] = AppSample{Mem: 20 * mib1, Disk: d}
		// never measured yet
		s.Apps["new"] = AppSample{Mem: 20 * mib1}
	})
	ts := appTrends(all, now, 7*24*time.Hour)
	f := find(ts, "files")
	if math.Abs(f.DiskPerDay/mib1-500) > 5 || math.Abs(f.DiskDaysLeft-20) > 0.5 || len(f.DiskProjection) != 2 || len(f.DiskPoints) < 50 {
		t.Fatalf("files: %.0f MB/day, %.1f days, %d points", f.DiskPerDay/mib1, f.DiskDaysLeft, len(f.DiskPoints))
	}
	if b := find(ts, "blog"); b.DiskPerDay != 0 || b.DiskDaysLeft != -1 || b.Disk != 300*mib1 {
		t.Fatalf("blog: %+v", b)
	}
	if n := find(ts, "new"); n.Disk != 0 || len(n.DiskPoints) != 0 || n.DiskDaysLeft != -1 {
		t.Fatalf("new: %+v", n)
	}
	adv := trendAdvice(ts, now)
	got := ""
	for _, a := range adv {
		if a.App == "files" {
			got = a.Title + " / " + a.Level + " / " + a.Detail
		}
	}
	if !strings.Contains(got, "filling the disk") || !strings.Contains(got, "watch") || !strings.Contains(got, "500 MB a day") {
		t.Fatalf("advice: %q", got)
	}
}

// The sample taken right after a restart, before the first disk pass, has no disk figure: the one before it is used.
func TestDiskTrendSkipsAReadingWithoutDisk(t *testing.T) {
	now := time.Now()
	all := history(now, 2, func(age time.Duration, s *Sample) {
		s.Disks = []DiskSample{{Path: config.AppsDir, Total: 100 << 30, Free: 10 << 30}}
		d := uint64(500 * mib1)
		if age == 0 {
			d = 0
		}
		s.Apps["files"] = AppSample{Mem: 20 * mib1, Disk: d}
	})
	if f := find(appTrends(all, now, 24*time.Hour), "files"); f.Disk != 500*mib1 || len(f.DiskPoints) < 10 {
		t.Fatalf("%d MB, %d points", f.Disk>>20, len(f.DiskPoints))
	}
}
