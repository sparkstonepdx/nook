package core

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCopyAppData(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("runs the copy as a user: needs root")
	}
	from, to := &AppUser{Home: t.TempDir()}, &AppUser{Home: t.TempDir()}
	os.MkdirAll(filepath.Join(from.Home, "data", "subduction", "trees"), 0o700)
	os.MkdirAll(filepath.Join(to.Home, "data"), 0o700)
	os.WriteFile(filepath.Join(from.Home, "data", "subduction.key"), []byte("ab"), 0o600)
	os.WriteFile(filepath.Join(from.Home, "data", "subduction", "trees", "t1"), []byte("doc"), 0o600)
	if err := copyAppData(from, to); err != nil {
		t.Fatal(err)
	}
	for f, want := range map[string]string{"subduction.key": "ab", "subduction/trees/t1": "doc"} {
		b, err := os.ReadFile(filepath.Join(to.Home, "data", f))
		if err != nil || string(b) != want {
			t.Errorf("%s: %q %v", f, b, err)
		}
	}
	if st, _ := os.Stat(filepath.Join(to.Home, "data", "subduction.key")); st.Mode().Perm() != 0o600 {
		t.Errorf("key mode %v", st.Mode().Perm())
	}
}

func TestSubductionMoveStale(t *testing.T) {
	dir := t.TempDir()
	old := subductionMovePath
	subductionMovePath = func() string { return filepath.Join(dir, "move.json") }
	defer func() { subductionMovePath = old }()
	saveSubductionMove(SubductionMove{State: "moving", Step: "Stopping the old server"})
	if m := readSubductionMove(); m.State != "moving" {
		t.Fatalf("fresh: %+v", m)
	}
	os.WriteFile(subductionMovePath(), []byte(`{"state":"moving","updated":"`+time.Now().Add(-time.Hour).Format(time.RFC3339)+`"}`), 0o644)
	if m := readSubductionMove(); m.State != "failed" || m.Error == "" {
		t.Fatalf("stale: %+v", m)
	}
}
