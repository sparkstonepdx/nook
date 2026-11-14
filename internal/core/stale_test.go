package core

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
)

func TestStaleUnit(t *testing.T) {
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("no sleep")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "svc")
	data, _ := os.ReadFile(sleep)
	os.WriteFile(bin, data, 0o755)
	cmd := exec.Command(bin, "30")
	if err := cmd.Start(); err != nil {
		t.Skip(err)
	}
	defer cmd.Process.Kill()
	// a systemctl that says this process is the unit's main PID
	stub := filepath.Join(dir, "systemctl")
	os.WriteFile(stub, []byte("#!/bin/sh\necho "+strconv.Itoa(cmd.Process.Pid)+"\n"), 0o755)
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))

	if _, stale := staleUnit("x.service"); stale {
		t.Fatal("fresh process reported stale")
	}
	// what an update does: a new file renamed over the old one
	os.WriteFile(bin+".new", data, 0o755)
	os.Rename(bin+".new", bin)
	if pid, stale := staleUnit("x.service"); !stale || pid != strconv.Itoa(cmd.Process.Pid) {
		t.Fatalf("replaced binary not noticed: %s %v", pid, stale)
	}
}
