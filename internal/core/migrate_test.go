package core

import (
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestMigrateTargets(t *testing.T) {
	pg := func(id, app, v string, exp, imp bool, ready bool) Provider {
		s := ProvideSpec{Version: v}
		if exp {
			s.Export = "./export.sh"
		}
		if imp {
			s.Import = "./import.sh"
		}
		return Provider{ID: id, Interface: "postgres", Version: v, Ready: ready, spec: s, app: app}
	}
	provs := []Provider{
		pg("app:postgres-16/postgres", "postgres-16", "16", true, true, true),
		pg("app:postgres-17/postgres", "postgres-17", "17", true, true, true),
		pg("app:postgres-18/postgres", "postgres-18", "18", true, true, false), // not running
		pg("app:pg-old/postgres", "pg-old", "15", true, false, true),           // can't import
		{ID: "app:mail/mail", Interface: "email", Ready: true, spec: ProvideSpec{Import: "./import.sh"}, app: "mail"},
	}
	got := migrateTargets(Binding{App: "shop", Interface: "postgres", Provider: "app:postgres-16/postgres"}, provs)
	if len(got) != 1 || got[0].ID != "app:postgres-17/postgres" || got[0].Version != "17" {
		t.Fatalf("targets: %+v", got)
	}
	provs[0].spec.Export = ""
	if got := migrateTargets(Binding{App: "shop", Interface: "postgres", Provider: "app:postgres-16/postgres"}, provs); len(got) != 0 {
		t.Fatalf("no export hook, still offered %+v", got)
	}
}

func TestPipeHooks(t *testing.T) {
	sh := func(s string) *exec.Cmd { return exec.Command("sh", "-c", s) }
	n, err := pipeHooks(sh("printf 'row1\\nrow2\\n'"), sh(`[ "$(cat)" = "$(printf 'row1\nrow2')" ]`), time.Minute)
	if err != nil || n != 10 {
		t.Fatalf("pipe: %d %v", n, err)
	}
	if _, err := pipeHooks(sh("echo half; echo 'pg_dump: lost connection' >&2; exit 1"), sh("cat >/dev/null"), time.Minute); err == nil || !strings.Contains(err.Error(), "export") || !strings.Contains(err.Error(), "lost connection") {
		t.Fatalf("export failure: %v", err)
	}
	if _, err := pipeHooks(sh("echo data"), sh("cat >/dev/null; echo 'database not empty' >&2; exit 3"), time.Minute); err == nil || !strings.Contains(err.Error(), "import") || !strings.Contains(err.Error(), "not empty") {
		t.Fatalf("import failure: %v", err)
	}
	// an import that quits early must not leave the export hanging
	if _, err := pipeHooks(sh("yes | head -c 50000000"), sh("exit 1"), 20*time.Second); err == nil {
		t.Fatal("import quit early, no error")
	}
	start := time.Now()
	if _, err := pipeHooks(sh("sleep 30"), sh("cat"), 300*time.Millisecond); err == nil || time.Since(start) > 10*time.Second {
		t.Fatalf("timeout: %v after %s", err, time.Since(start))
	}
}

func TestCheckExportImportHooks(t *testing.T) {
	m := Manifest{Kind: "service", Provides: map[string]ProvideSpec{"postgres": {Env: map[string]string{"DATABASE_URL": ""}, Export: "./export.sh", Import: "./import.sh"}}}
	if err := checkServices(&m); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []ProvideSpec{{Env: map[string]string{"DATABASE_URL": ""}, Export: "pg_dump"}, {Env: map[string]string{"DATABASE_URL": ""}, Import: "./../x.sh"}} {
		m := Manifest{Kind: "service", Provides: map[string]ProvideSpec{"postgres": bad}}
		if checkServices(&m) == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
}
