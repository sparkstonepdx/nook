package core

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeRestic stands in for restic: it logs each call (args, then the repo and password it was given) and
// answers backup with a summary line and snapshots with one snapshot.
func fakeRestic(t *testing.T) (log string) {
	t.Helper()
	dir := t.TempDir()
	log = filepath.Join(dir, "calls")
	script := `#!/bin/sh
echo "$* | repo=$RESTIC_REPOSITORY pw=$RESTIC_PASSWORD key=$AWS_ACCESS_KEY_ID" >> ` + log + `
case "$1" in
  version) echo "restic 0.19.1 compiled with go1.24 on linux/amd64";;
  backup) echo '{"message_type":"status","percent_done":0.5}'
          echo '{"message_type":"summary","snapshot_id":"4f2a9c1e0011223344556677889900aabbccddeeff00112233445566778899","data_added":1048576,"total_bytes_processed":5242880,"total_files_processed":12}';;
  snapshots) echo '[{"id":"aa11","short_id":"aa11","time":"2026-11-14T03:17:00Z","hostname":"srv-1","tags":["nook","app:wren"],"paths":["/srv/nook/wren"]},{"id":"bb22","short_id":"bb22","time":"2026-11-15T03:17:00Z","hostname":"srv-1","tags":["nook","app:wren"],"paths":["/srv/nook/wren"]}]';;
esac
exit 0
`
	bin := filepath.Join(dir, "restic")
	os.WriteFile(bin, []byte(script), 0o755)
	old := []any{resticBin, backupEnv, backupJSON, serverIDFile, backupDir, backupUnitDir}
	resticBin, backupEnv, backupJSON, serverIDFile, backupDir, backupUnitDir =
		bin, filepath.Join(dir, "backup.env"), filepath.Join(dir, "backup.json"), filepath.Join(dir, "server-id"), filepath.Join(dir, "state"), dir
	os.WriteFile(serverIDFile, []byte("srv-1\n"), 0o644)
	oldCtl := backupSystemctl
	backupSystemctl = func(...string) error { return nil }
	t.Cleanup(func() {
		resticBin, backupEnv, backupJSON, serverIDFile, backupDir, backupUnitDir = old[0].(string), old[1].(string), old[2].(string), old[3].(string), old[4].(string), old[5].(string)
		backupSystemctl = oldCtl
	})
	return log
}

func TestSetBackup(t *testing.T) {
	log := fakeRestic(t)
	h := New(&strings.Builder{})
	for _, bad := range []BackupSetup{
		{Repo: "https://bucket.example"},
		{Repo: "s3:https://r2.example/b/nook"}, // no key
		{Repo: "s3:https://r2.example/b/nook", KeyID: "k", Secret: "s", Time: "25:00"},
	} {
		if _, err := h.SetBackup(bad); err == nil {
			t.Errorf("%+v: no error", bad)
		}
	}
	pw, err := h.SetBackup(BackupSetup{Repo: "s3:https://acct.r2.cloudflarestorage.com/backups/nook", KeyID: "KEY", Secret: "SECRET", Time: "04:05"})
	if err != nil || len(pw) < 30 {
		t.Fatalf("setup: %q %v", pw, err)
	}
	calls, _ := os.ReadFile(log)
	if !strings.Contains(string(calls), "init | repo=s3:https://acct.r2.cloudflarestorage.com/backups/nook pw="+pw+" key=KEY") {
		t.Fatalf("init call: %s", calls)
	}
	if strings.Contains(string(calls), "SECRET") {
		t.Fatal("the secret must never be on restic's command line")
	}
	st, _ := os.Stat(backupEnv)
	b, _ := os.ReadFile(backupEnv)
	if st.Mode().Perm() != 0o600 || !strings.Contains(string(b), "RESTIC_PASSWORD="+pw) || !strings.Contains(string(b), "AWS_SECRET_ACCESS_KEY=SECRET") {
		t.Fatalf("backup.env %v: %s", st.Mode(), b)
	}
	timer, _ := os.ReadFile(filepath.Join(backupUnitDir, "nook-backup.timer"))
	if !strings.Contains(string(timer), "OnCalendar=*-*-* 04:05:00") {
		t.Fatalf("timer: %s", timer)
	}
	c := LoadBackupConfig()
	if !c.Configured() || c.ServerID != "srv-1" || c.Keep != "7,4,6" {
		t.Fatalf("config: %+v", c)
	}
	// an existing repo: given its password, opened, not made
	os.Remove(log)
	if pw2, err := h.SetBackup(BackupSetup{Repo: "s3:https://acct.r2.cloudflarestorage.com/backups/nook", KeyID: "KEY", Secret: "SECRET", Password: "old-pw"}); err != nil || pw2 != "" {
		t.Fatalf("existing: %q %v", pw2, err)
	}
	if calls, _ := os.ReadFile(log); strings.Contains(string(calls), "init") || !strings.Contains(string(calls), "cat config | repo=s3:https://acct.r2.cloudflarestorage.com/backups/nook pw=old-pw") {
		t.Fatalf("existing repo calls: %s", calls)
	}
}

func TestBackupPaths(t *testing.T) {
	log := fakeRestic(t)
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, "data"), 0o755)
	c := BackupConfig{Repo: "s3:https://x/b", password: "pw", ServerID: "srv-1"}
	r, err := backupPaths(c, []string{"nook", "app:wren"}, []string{home, "/nope/missing"}, []string{filepath.Join(home, "builds")})
	if err != nil || r.Snapshot[:8] != "4f2a9c1e" || r.Added != 1<<20 || r.Size != 5<<20 || r.Files != 12 {
		t.Fatalf("%+v %v", r, err)
	}
	calls, _ := os.ReadFile(log)
	want := "backup --json --host srv-1 --one-file-system --tag nook --tag app:wren --exclude " + filepath.Join(home, "builds") + " " + home + " |"
	if !strings.Contains(string(calls), want) || strings.Contains(string(calls), "/nope/missing") {
		t.Fatalf("call: %s\nwant: %s", calls, want)
	}
	if _, err := backupPaths(c, []string{"nook"}, []string{"/nope/missing"}, nil); err == nil {
		t.Fatal("no paths at all should fail")
	}
}

func TestBackupSnapshotsAndSkip(t *testing.T) {
	log := fakeRestic(t)
	New(&strings.Builder{}).SetBackup(BackupSetup{Repo: "s3:https://x/b/nook", KeyID: "K", Secret: "S"})
	s, err := BackupSnapshots("wren", "")
	if err != nil || len(s) != 2 || s[0].ID != "bb22" {
		t.Fatalf("newest first: %+v %v", s, err)
	}
	BackupSnapshots("wren", "old-server")
	calls, _ := os.ReadFile(log)
	if !strings.Contains(string(calls), "snapshots --json --host srv-1 --tag nook,app:wren") || !strings.Contains(string(calls), "--host old-server") {
		t.Fatalf("calls: %s", calls)
	}
}

func TestBackupAdvice(t *testing.T) {
	now := time.Now()
	cases := []struct {
		s     BackupStatus
		level string
	}{
		{BackupStatus{}, ""},
		{BackupStatus{Configured: true, Running: true}, ""},
		{BackupStatus{Configured: true, Finished: now.Unix(), OK: false, Error: "restic backup: no space"}, "tight"},
		{BackupStatus{Configured: true, Finished: now.Unix(), OK: true, LastOK: now.Add(-48 * time.Hour).Unix()}, "watch"},
		{BackupStatus{Configured: true, Finished: now.Unix(), OK: true, LastOK: now.Add(-2 * time.Hour).Unix()}, ""},
		{BackupStatus{Configured: true, OK: true, LastOK: now.Unix(), CheckError: "pack 3f… is damaged"}, "tight"},
	}
	for i, c := range cases {
		a, ok := backupAdvice(c.s, now)
		if (c.level == "") == ok || (ok && a.Level != c.level) {
			t.Errorf("%d: %v %+v", i, ok, a)
		}
	}
}

// A download that doesn't match GitHub's digest is refused and nothing is installed.
func TestEnsureResticRefusesBadChecksum(t *testing.T) {
	dir := t.TempDir()
	oldBin, oldRel, oldGet, oldGetTo := resticBin, resticRelease, resticGet, resticGetTo
	resticBin = filepath.Join(dir, "restic")
	defer func() { resticBin, resticRelease, resticGet, resticGetTo = oldBin, oldRel, oldGet, oldGetTo }()
	resticGet = func(u string) ([]byte, error) {
		resp, err := http.Get(u)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		return io.ReadAll(resp.Body)
	}
	resticGetTo = func(u string, w io.Writer, _ int64, _ func(int64)) error {
		resp, err := http.Get(u)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		_, err = io.Copy(w, resp.Body)
		return err
	}
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/dl") {
			w.Write([]byte("not the real thing"))
			return
		}
		sum := sha256.Sum256([]byte("the real thing"))
		json.NewEncoder(w).Encode(map[string]any{"assets": []map[string]string{{"name": "restic_" + resticVersion + "_linux_" + goarch() + ".bz2", "browser_download_url": srv.URL + "/dl", "digest": "sha256:" + hex.EncodeToString(sum[:])}}})
	}))
	defer srv.Close()
	resticRelease = srv.URL + "/repos/restic/restic/releases/tags/v"
	err := EnsureRestic(&strings.Builder{})
	if err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("%v", err)
	}
	if _, err := os.Stat(resticBin); err == nil {
		t.Fatal("a bad download was installed")
	}
}
