package core

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestStoreJobs(t *testing.T) {
	dir := t.TempDir()
	old := jobsDir
	jobsDir = func() string { return dir }
	defer func() { jobsDir = old }()

	j := startJob("files", "update", "Files", "v0.2.0", "v0.3.0")
	j.total(1000)
	j.Write(make([]byte, 400))
	j.step(StepCheck) // a forced write carries the bytes counted since the last one
	got := StoreJobs()
	if len(got) != 1 || got[0].Step != StepCheck || got[0].Bytes != 400 || got[0].Total != 1000 || got[0].Done {
		t.Fatalf("running: %+v", got)
	}
	j.finish(errors.New("didn't come up"))
	if got := StoreJobs(); !got[0].Done || got[0].Error != "didn't come up" {
		t.Fatalf("finished: %+v", got)
	}

	// a job its process left behind reads as failed; a finished one past 10 minutes goes away
	stale := StoreJob{App: "push", Action: "install", To: "v0.1.0", Step: StepDeploy, Started: time.Now().Add(-time.Hour), Updated: time.Now().Add(-time.Hour)}
	b, _ := json.Marshal(stale)
	os.WriteFile(filepath.Join(dir, "push.json"), b, 0o644)
	old2 := stale
	old2.App, old2.Done = "runners", true
	b, _ = json.Marshal(old2)
	os.WriteFile(filepath.Join(dir, "runners.json"), b, 0o644)
	got = StoreJobs()
	if len(got) != 2 || got[0].App != "push" || !got[0].Done || got[0].Error == "" {
		t.Fatalf("stale: %+v", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "runners.json")); err == nil {
		t.Fatal("an old finished job should be removed")
	}
	var nilJob *job // installs before a job exists, and tests, pass nil
	nilJob.step(StepDeploy)
	nilJob.Write([]byte("x"))
	nilJob.finish(nil)
}

// A failed download must come back as an error, leave no scratch folder, and end its job with the reason
// (it once panicked on its own cleanup instead).
func TestPkgForFailedDownload(t *testing.T) {
	scratch, jobs := t.TempDir(), t.TempDir()
	oldS, oldJ := storeScratch, jobsDir
	storeScratch, jobsDir = scratch, func() string { return jobs }
	defer func() { storeScratch, jobsDir = oldS, oldJ }()

	a := CatalogApp{ID: "x", Name: "X", Version: "v1.0.0", Binary: "x",
		Artifacts: map[string]Artifact{runtime.GOARCH: {URL: "https://127.0.0.1:1/x.tar.gz", SHA256: strings.Repeat("0", 64), Format: "tar.gz"}}}
	j := startJob("x", "install", "X", "", "v1.0.0")
	_, done, err := pkgFor(a, j)
	j.finish(err)
	if err == nil || done != nil {
		t.Fatalf("want an error and no cleanup to call: %v", err)
	}
	if es, _ := os.ReadDir(scratch); len(es) != 0 {
		t.Fatalf("scratch folder left behind: %v", es)
	}
	if got := StoreJobs(); len(got) != 1 || !got[0].Done || !strings.Contains(got[0].Error, "download") {
		t.Fatalf("job: %+v", got)
	}
}

func TestSweepStoreScratch(t *testing.T) {
	scratch := t.TempDir()
	old := storeScratch
	storeScratch = scratch
	defer func() { storeScratch = old }()
	for _, n := range []string{".store-old", ".store-new", "keep"} {
		os.Mkdir(filepath.Join(scratch, n), 0o755)
	}
	hour := time.Now().Add(-2 * time.Hour)
	os.Chtimes(filepath.Join(scratch, ".store-old"), hour, hour)
	os.Chtimes(filepath.Join(scratch, "keep"), hour, hour)
	SweepStoreScratch()
	es, _ := os.ReadDir(scratch)
	var left []string
	for _, e := range es {
		left = append(left, e.Name())
	}
	if strings.Join(left, ",") != ".store-new,keep" {
		t.Fatalf("left: %v", left)
	}
}
