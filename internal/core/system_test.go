package core

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const simulated = `Reading package lists...
Calculating upgrade...
The following packages will be upgraded:
  libssl3 openssl postgresql-17 tzdata
Inst libssl3 [3.0.11-1~deb12u2] (3.0.15-1~deb12u1 Debian-Security:12/stable-security [amd64])
Inst openssl [3.0.11-1~deb12u2] (3.0.15-1~deb12u1 Debian-Security:12/stable-security [amd64])
Inst postgresql-17 [17.1-1.pgdg120+1] (17.2-1.pgdg120+1 apt.postgresql.org:bookworm-pgdg [amd64])
Inst tzdata [2024a-0+deb12u1] (2024b-0+deb12u1 Debian:12.8/stable [all])
Conf libssl3 (3.0.15-1~deb12u1 Debian-Security:12/stable-security [amd64])
`

func TestParseUpgrades(t *testing.T) {
	ps := parseUpgrades(simulated)
	if len(ps) != 4 {
		t.Fatalf("%+v", ps)
	}
	if ps[0].Name != "libssl3" || ps[0].From != "3.0.11-1~deb12u2" || ps[0].To != "3.0.15-1~deb12u1" || !ps[0].Security {
		t.Fatalf("%+v", ps[0])
	}
	if ps[3].Security || ps[2].Security {
		t.Fatal("only the security archive's packages are security fixes")
	}
}

func TestNewestKernel(t *testing.T) {
	dir := t.TempDir()
	for _, k := range []string{"6.1.0-27-amd64", "6.1.0-28-amd64", "6.12.0-1-amd64", "6.1.0-9-amd64"} {
		os.WriteFile(filepath.Join(dir, "vmlinuz-"+k), nil, 0o644)
	}
	if got := newestKernel(dir); got != "6.12.0-1-amd64" {
		t.Fatal(got)
	}
	if !debVersionLess("6.1.0-9-amd64", "6.1.0-27-amd64") {
		t.Fatal("numbers compare as numbers")
	}
}

func TestMapsDeletedLib(t *testing.T) {
	if !mapsDeletedLib("7f00-7f10 r-xp 0 08:01 123 /usr/lib/x86_64-linux-gnu/libssl.so.3 (deleted)\n") {
		t.Fatal("a replaced library")
	}
	if mapsDeletedLib("7f00-7f10 rw-s 0 00:01 5 /memfd:x (deleted)\n7f00 r-xp 0 08:01 1 /usr/lib/libc.so.6\n") {
		t.Fatal("a deleted memfd isn't a library")
	}
}

func TestSystemUpdateLeavesStoreAppsAlone(t *testing.T) {
	dir := t.TempDir()
	oldFile, oldAuto, oldRun := systemFile, systemAuto, systemRun
	systemFile, systemAuto = filepath.Join(dir, "system.json"), filepath.Join(dir, "auto")
	defer func() { systemFile, systemAuto, systemRun = oldFile, oldAuto, oldRun }()
	var calls []string
	systemRun = func(env []string, name string, args ...string) (string, error) {
		calls = append(calls, name+" "+strings.Join(args, " "))
		if name == "apt-get" && len(args) > 0 && args[0] == "-s" {
			return simulated, nil
		}
		return "", nil
	}
	h := &Host{Out: io.Discard}
	s, err := h.SystemCheck(false)
	if err != nil {
		t.Fatal(err)
	}
	s.Packages[2].App = "postgres-17" // as storeAptPackages would mark it
	writeSystemStatus(s)
	if s.Security != 2 {
		t.Fatalf("security: %d", s.Security)
	}
	calls = nil
	if _, err := h.SystemUpdate(true); err != nil {
		t.Fatal(err)
	}
	install := ""
	for _, c := range calls {
		if strings.HasPrefix(c, "apt-get install") {
			install = c
		}
	}
	if !strings.HasSuffix(install, "--only-upgrade -o Dpkg::Options::=--force-confold -o Dpkg::Options::=--force-confdef libssl3 openssl") {
		t.Fatalf("security only, and never a store app's package: %q", install)
	}
	calls = nil
	writeSystemStatus(s)
	h.SystemUpdate(false)
	for _, c := range calls {
		if strings.HasPrefix(c, "apt-get install") && strings.Contains(c, "postgresql-17") {
			t.Fatal("a store app's package follows its app's update policy")
		}
	}
	// automatic security updates install from the daily check, and only when on
	calls = nil
	h.SystemCheck(true)
	for _, c := range calls {
		if strings.HasPrefix(c, "apt-get install") {
			t.Fatal("installed with automatic updates off")
		}
	}
	SetSystemAuto(true)
	calls = nil
	h.SystemCheck(true)
	found := false
	for _, c := range calls {
		found = found || strings.HasPrefix(c, "apt-get install")
	}
	if !found {
		t.Fatal("automatic security updates on: the daily check installs them")
	}
}
