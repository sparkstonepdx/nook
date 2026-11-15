package core

// Per-app disk use, measured once an hour by the helper and carried in every 5-minute sample, so each app's
// growth can be trended and projected like its memory. An app's disk is everything nook keeps for it:
//
//	sites   its sites folder and the copies Caddy serves (/var/lib/nook/www/APP)
//	builds  uploads waiting to go live, and what SSH deploys rsync into (kept, so the next rsync sends changes)
//	data    the rest of its home folder (data/ and anything else the app writes)
//	logs    /var/log/nook/APP
//	store   saved store packages, for rolling back (/var/lib/nook/store/APP)
//
// Space is counted the way du counts it (blocks on disk), and a file with several hard links once.

import (
	"io/fs"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/sparkstonepdx/nook/internal/config"
)

type AppDisk struct {
	Total  uint64 `json:"total"`
	Sites  uint64 `json:"sites"`
	Builds uint64 `json:"builds"`
	Data   uint64 `json:"data"`
	Logs   uint64 `json:"logs"`
	Store  uint64 `json:"store"`
	At     int64  `json:"at"` // when it was measured
}

const diskEvery = time.Hour

var appDisks struct {
	sync.Mutex
	m map[string]AppDisk
}

// diskReady closes after the first pass, so the first sample after a restart has disk figures too.
var (
	diskReady     = make(chan struct{})
	diskReadyOnce sync.Once
)

// lastAppDisk is the latest measurement for app, if there is one.
func lastAppDisk(app string) (AppDisk, bool) {
	appDisks.Lock()
	defer appDisks.Unlock()
	d, ok := appDisks.m[app]
	return d, ok
}

// RunDiskSampler measures every app's disk now and then hourly. Run inside the helper.
func RunDiskSampler() {
	for {
		m := map[string]AppDisk{}
		for _, a := range Apps() {
			m[a] = measureAppDisk(a)
		}
		appDisks.Lock()
		appDisks.m = m
		appDisks.Unlock()
		diskReadyOnce.Do(func() { close(diskReady) })
		time.Sleep(diskEvery)
	}
}

// diskRoots are where app's space lives, by part (a var for tests).
var diskRoots = func(app string) map[string][]string {
	home := filepath.Join(config.AppsDir, app)
	return map[string][]string{
		"sites":  {filepath.Join(home, "sites"), filepath.Join(config.WWWDir, app)},
		"builds": {filepath.Join(home, "builds")},
		"home":   {home}, // data: the home folder less what sites and builds counted
		"logs":   {filepath.Join(config.LogDir, app)},
		"store":  {storeDir(app)},
	}
}

func measureAppDisk(app string) AppDisk {
	roots := diskRoots(app)
	seen := map[[2]uint64]bool{} // dev, inode: hard links count once
	size := func(paths ...string) uint64 {
		var n uint64
		for _, p := range paths {
			filepath.WalkDir(p, func(path string, d fs.DirEntry, err error) error {
				if err != nil {
					return nil // a folder that isn't there, or one we can't read: count what we can
				}
				fi, err := d.Info()
				if err != nil {
					return nil
				}
				st, ok := fi.Sys().(*syscall.Stat_t)
				if !ok {
					return nil
				}
				k := [2]uint64{uint64(st.Dev), st.Ino}
				if seen[k] {
					return nil
				}
				seen[k] = true
				n += uint64(st.Blocks) * 512
				return nil
			})
		}
		return n
	}
	d := AppDisk{At: time.Now().Unix()}
	d.Sites = size(roots["sites"]...)
	d.Builds = size(roots["builds"]...)
	d.Data = size(roots["home"]...) // what's left of home once sites/ and builds/ were counted
	d.Logs = size(roots["logs"]...)
	d.Store = size(roots["store"]...)
	d.Total = d.Sites + d.Builds + d.Data + d.Logs + d.Store
	return d
}
