package core

// Store jobs: what an install or update is doing right now, for the dashboard's progress. Each one is a file in
// /var/lib/nook/store-jobs/APP.json, written as it goes, so updates the daily check starts on its own show up
// too. A finished job is shown for 10 minutes; one not touched for 10 minutes without finishing died with its
// process and reads as failed.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/sparkstonepdx/nook/internal/config"
)

// The steps, in order. A failed update that rolls back adds "rollback".
const (
	StepDownload = "download" // Bytes of Total
	StepCheck    = "check"    // the catalog's sha256
	StepUnpack   = "unpack"   // the binary out of the download, into a site package
	StepDeploy   = "deploy"
	StepHealth   = "health"
	StepRollback = "rollback"
)

type StoreJob struct {
	App     string    `json:"app"`
	Action  string    `json:"action"` // install, update
	Name    string    `json:"name"`   // the catalog app's name
	From    string    `json:"from,omitempty"`
	To      string    `json:"to"`
	Step    string    `json:"step"`
	Bytes   int64     `json:"bytes,omitempty"`
	Total   int64     `json:"total,omitempty"` // 0 when the server didn't say
	Started time.Time `json:"started"`
	Updated time.Time `json:"updated"`
	Done    bool      `json:"done,omitempty"`
	Error   string    `json:"error,omitempty"`
}

const jobKeep = 10 * time.Minute

// jobsDir is a var for tests.
var jobsDir = func() string { return filepath.Join(config.LibDir, "store-jobs") }

type job struct {
	mu    sync.Mutex
	j     StoreJob
	wrote time.Time
}

func startJob(app, action, name, from, to string) *job {
	now := time.Now().UTC()
	j := &job{j: StoreJob{App: app, Action: action, Name: name, From: from, To: to, Step: StepDownload, Started: now}}
	j.save(true)
	return j
}

// save writes the job; unforced writes are at most every 250 ms (download progress).
func (j *job) save(force bool) {
	if j == nil {
		return
	}
	now := time.Now()
	if !force && now.Sub(j.wrote) < 250*time.Millisecond {
		return
	}
	j.wrote = now
	j.j.Updated = now.UTC()
	os.MkdirAll(jobsDir(), 0o755)
	b, _ := json.Marshal(j.j)
	config.WriteAtomic(filepath.Join(jobsDir(), j.j.App+".json"), b, 0o644)
}

func (j *job) step(s string) {
	if j == nil {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	j.j.Step = s
	j.save(true)
}

// progress is the download's running count (a writer to tee into).
func (j *job) Write(p []byte) (int, error) {
	if j != nil {
		j.mu.Lock()
		j.j.Bytes += int64(len(p))
		j.save(false)
		j.mu.Unlock()
	}
	return len(p), nil
}

func (j *job) total(n int64) {
	if j == nil {
		return
	}
	j.mu.Lock()
	j.j.Total = n
	j.save(true)
	j.mu.Unlock()
}

func (j *job) finish(err error) {
	if j == nil {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	j.j.Done = true
	if err != nil {
		j.j.Error = err.Error()
	}
	j.save(true)
}

// StoreJobs is every job running now or finished in the last 10 minutes, oldest first.
func StoreJobs() []StoreJob {
	out := []StoreJob{}
	es, _ := os.ReadDir(jobsDir())
	for _, e := range es {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		p := filepath.Join(jobsDir(), e.Name())
		b, err := os.ReadFile(p)
		var j StoreJob
		if err != nil || json.Unmarshal(b, &j) != nil {
			continue
		}
		if time.Since(j.Updated) > jobKeep {
			if j.Done {
				os.Remove(p)
				continue
			}
			j.Done, j.Error = true, "stopped without finishing (nook restarted?)"
		}
		out = append(out, j)
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Started.Before(out[b].Started) })
	return out
}

// SweepStoreScratch removes pkgFor working folders a crash or restart left behind (older than an hour, so a
// running install's is never touched).
func SweepStoreScratch() {
	es, _ := os.ReadDir(storeScratch)
	for _, e := range es {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), ".store-") {
			continue
		}
		if fi, err := e.Info(); err == nil && time.Since(fi.ModTime()) > time.Hour {
			os.RemoveAll(filepath.Join(storeScratch, e.Name()))
		}
	}
}
