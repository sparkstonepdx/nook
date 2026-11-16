package core

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestRestoreServer(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root: cp -a keeps ownership")
	}
	log := fakeRestic(t)
	root := t.TempDir()
	bin := resticBin
	// restore: the server snapshot, or one app's folder with --include
	os.WriteFile(bin, []byte(`#!/bin/sh
echo "$*" >> `+log+`
t=""; inc=""; prev=""
for a in "$@"; do
  [ "$prev" = "--target" ] && t="$a"
  [ "$prev" = "--include" ] && inc="$a"
  prev="$a"
done
case "$1" in
  version) echo "restic 0.19.1 compiled with go1.24 on linux/amd64";;
  snapshots) echo '[{"id":"aa11aa11","short_id":"aa11aa11","time":"2026-11-14T03:17:00Z","hostname":"old-srv","tags":["nook","server"]},{"id":"bb22bb22","short_id":"bb22bb22","time":"2026-11-15T03:17:00Z","hostname":"old-srv","tags":["nook","server"]}]';;
  restore)
    if [ -n "$inc" ]; then
      mkdir -p "$t$inc/data" "$t$inc/sites/www"; echo db > "$t$inc/data/db"; echo hi > "$t$inc/sites/www/index.html"
    else
      mkdir -p "$t/etc/nook" "$t/var/lib/nook/backup" "$t/var/lib/nook-web" "$t/etc/systemd/system"
      printf 'BASE_DOMAIN=old.example\nSSH_HOST=1.1.1.1\n' > "$t/etc/nook/config"
      echo old-srv > "$t/etc/nook/server-id"
      echo '{"wren":{"uid":31001,"gid":31001},"files":{"uid":31002,"gid":31002},"../x":{"uid":1,"gid":1}}' > "$t/var/lib/nook/backup/users.json"
      echo '{}' > "$t/var/lib/nook-web/users.json"
      printf '[Slice]\nMemoryMax=512M\n' > "$t/etc/systemd/system/nook-wren.slice"
    fi;;
esac
exit 0
`), 0o755)
	os.WriteFile(backupEnv, []byte("BACKUP_REPO=s3:https://r2.example/b/nook\nAWS_ACCESS_KEY_ID=K\nAWS_SECRET_ACCESS_KEY=S\nRESTIC_PASSWORD=pw\n"), 0o600)
	os.MkdirAll(filepath.Join(root, "etc/nook"), 0o755)
	os.WriteFile(filepath.Join(root, "etc/nook/config"), []byte("BASE_DOMAIN=new.example\nSSH_HOST=2.2.2.2\n"), 0o600)

	var made, started []string
	old := []any{restoreRoot, restoreAddUser, restoreFinish}
	restoreRoot = root
	restoreAddUser = func(app string, uid, gid int) (int, int, error) {
		made = append(made, app)
		if app == "files" {
			return 31050, 31050, nil // its uid was taken here
		}
		return uid, gid, nil
	}
	restoreFinish = func(h *Host, apps []string) error { started = apps; return nil }
	t.Cleanup(func() {
		restoreRoot, restoreAddUser, restoreFinish = old[0].(string), old[1].(func(string, int, int) (int, int, error)), old[2].(func(*Host, []string) error)
	})

	var out strings.Builder
	h := New(&out)
	if err := h.RestoreServer(ServerRestore{}); err == nil && len(Apps()) > 0 {
		t.Fatal("restored onto a server with apps without --force")
	}
	if err := h.RestoreServer(ServerRestore{Force: true}); err != nil {
		t.Fatalf("restore: %v\n%s", err, out.String())
	}
	calls, _ := os.ReadFile(log)
	if !strings.Contains(string(calls), "restore bb22bb22 --target") {
		t.Errorf("didn't restore the latest server snapshot:\n%s", calls)
	}
	if !strings.Contains(string(calls), "restore latest --host old-srv --tag nook,app:wren") {
		t.Errorf("app restore call:\n%s", calls)
	}
	cfg, _ := os.ReadFile(filepath.Join(root, "etc/nook/config"))
	if !strings.Contains(string(cfg), "BASE_DOMAIN=old.example") || !strings.Contains(string(cfg), "SSH_HOST=2.2.2.2") {
		t.Errorf("config: the old server's settings with this server's address, got:\n%s", cfg)
	}
	if id, _ := os.ReadFile(filepath.Join(root, "etc/nook/server-id")); strings.TrimSpace(string(id)) != "old-srv" {
		t.Errorf("server id %q, want old-srv", id)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "srv/nook/wren/data/db")); string(b) != "db\n" {
		t.Error("wren's data isn't back")
	}
	st, _ := os.Lstat(filepath.Join(root, "srv/nook/files/data/db"))
	if st == nil || st.Sys() == nil {
		t.Fatal("files' data isn't back")
	}
	if !slices.Equal(made, []string{"files", "wren"}) || !slices.Equal(started, []string{"files", "wren"}) {
		t.Errorf("users made %v, apps started %v (../x must be refused)", made, started)
	}
	if _, err := os.Stat(filepath.Join(root, "etc/systemd/system/nook-wren.slice")); err != nil {
		t.Error("the app's slice (its limits) isn't back")
	}
	if m, _ := filepath.Glob(filepath.Join(root, "etc/nook.before-restore-*")); len(m) != 1 {
		t.Error("this server's settings weren't set aside")
	}

	// keeping its own id
	os.RemoveAll(filepath.Join(root, "srv/nook"))
	os.WriteFile(serverIDFile, []byte("new-srv\n"), 0o644)
	if err := h.RestoreServer(ServerRestore{Force: true, NewID: true}); err != nil {
		t.Fatal(err)
	}
	if id, _ := os.ReadFile(filepath.Join(root, "etc/nook/server-id")); strings.TrimSpace(string(id)) != "new-srv" {
		t.Errorf("NewID: server id %q, want new-srv", id)
	}
}
