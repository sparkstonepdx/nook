package core

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// A stand-in service: a copy of this test binary that just sleeps. (Copying sleep itself fails where it's a
// multi-call binary that exits when it doesn't recognise its own name.)
func TestStaleHelperSleep(t *testing.T) {
	if os.Getenv("NOOK_STALE_HELPER") == "1" {
		time.Sleep(30 * time.Second)
	}
}

func TestStaleUnit(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Skip(err)
	}
	data, err := os.ReadFile(self)
	if err != nil {
		t.Skip(err)
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "svc")
	os.WriteFile(bin, data, 0o755)
	cmd := exec.Command(bin, "-test.run=^TestStaleHelperSleep$")
	cmd.Env = append(os.Environ(), "NOOK_STALE_HELPER=1")
	if err := cmd.Start(); err != nil {
		t.Skip(err)
	}
	defer cmd.Process.Kill()
	time.Sleep(200 * time.Millisecond)
	// a systemctl that says this process is the unit's main PID
	stub := filepath.Join(dir, "systemctl")
	os.WriteFile(stub, []byte("#!/bin/sh\necho "+strconv.Itoa(cmd.Process.Pid)+"\n"), 0o755)
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))

	if _, stale := staleUnit("x.service", bin); stale {
		t.Fatal("fresh process reported stale")
	}
	// what an update does: a new file renamed over the old one
	os.WriteFile(bin+".new", data, 0o755)
	os.Rename(bin+".new", bin)
	if pid, stale := staleUnit("x.service", bin); !stale || pid != strconv.Itoa(cmd.Process.Pid) {
		t.Fatalf("replaced binary not noticed: %s %v", pid, stale)
	}
}
