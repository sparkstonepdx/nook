package core

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fakeServer stands in for the new server: a real host key for the scan, and answers to the commands setup sends.
type fakeServer struct {
	line, fingerprint string
	os, uid, sudo     string
	cmds              []string
	auths             []sshAuth
}

func newFakeServer(t *testing.T) *fakeServer {
	t.Helper()
	dir := t.TempDir()
	if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", filepath.Join(dir, "host")).CombinedOutput(); err != nil {
		t.Skipf("ssh-keygen: %s", out)
	}
	pub, _ := os.ReadFile(filepath.Join(dir, "host.pub"))
	w := strings.Fields(string(pub))
	f := &fakeServer{line: "203.0.113.7 " + w[0] + " " + w[1], os: "debian 12", uid: "0"}
	out, _ := RunInput(f.line+"\n", "ssh-keygen", "-lf", "-")
	f.fingerprint = strings.Fields(out)[1]

	oldScan, oldRun, oldDir, oldFile, oldScript := sshKeyscan, sshRun, sshDir, setupFile, setupScript
	sshDir = filepath.Join(t.TempDir(), "ssh")
	state := filepath.Join(t.TempDir(), "server-setup.json")
	setupFile = func() string { return state }
	setupScript = "echo installing nook"
	sshKeyscan = func(host string, port int) (string, error) { return "# " + host + " SSH-2.0\n" + f.line + "\n", nil }
	sshRun = func(_ SetupTarget, kh string, a sshAuth, cmd string, live io.Writer) (string, error) {
		f.cmds = append(f.cmds, cmd)
		f.auths = append(f.auths, a)
		if b, _ := os.ReadFile(kh); !strings.Contains(string(b), f.line) {
			t.Errorf("ran %q without the checked host key in known_hosts", cmd)
		}
		var out string
		switch {
		case strings.Contains(cmd, "os-release"):
			out = "os=" + f.os + "\nuid=" + f.uid + "\n" + f.sudo
		case strings.Contains(cmd, "nook-admin invite"):
			out = "https://203.0.113.7:8443/invite?t=abc\n"
		case strings.Contains(cmd, "echo installing"):
			out = "==> packages\n==> nook-admin\n"
		}
		if live != nil {
			io.WriteString(live, out)
		}
		return out, nil
	}
	t.Cleanup(func() {
		sshKeyscan, sshRun, sshDir, setupFile, setupScript = oldScan, oldRun, oldDir, oldFile, oldScript
	})
	return f
}

func (f *fakeServer) ran(s string) bool {
	for _, c := range f.cmds {
		if strings.Contains(c, s) {
			return true
		}
	}
	return false
}

func TestSetupTargetCheck(t *testing.T) {
	good, err := SetupTarget{Host: " [2001:db8::1] "}.check()
	if err != nil || good.Host != "2001:db8::1" || good.Port != 22 || good.User != "root" {
		t.Fatalf("%+v %v", good, err)
	}
	for _, bad := range []SetupTarget{{Host: "a b"}, {Host: "-oProxyCommand=x"}, {Host: "x.example", Port: 70000}, {Host: "x.example", User: "-l"}} {
		if _, err := bad.check(); err == nil {
			t.Errorf("%+v was accepted", bad)
		}
	}
}

func TestScanHostKeys(t *testing.T) {
	f := newFakeServer(t)
	keys, err := ScanHostKeys(SetupTarget{Host: "203.0.113.7"})
	if err != nil || len(keys) != 1 || keys[0].Fingerprint != f.fingerprint || keys[0].Type != "ED25519" {
		t.Fatalf("%+v %v", keys, err)
	}
	sshKeyscan = func(string, int) (string, error) { return "", nil }
	if _, err := ScanHostKeys(SetupTarget{Host: "203.0.113.7"}); err == nil || !strings.Contains(err.Error(), "couldn't reach") {
		t.Fatalf("no keys: %v", err)
	}
}

func TestSetupInstall(t *testing.T) {
	f := newFakeServer(t)
	j := &SetupJob{SetupTarget: SetupTarget{Host: "203.0.113.7", Port: 22, User: "root"}, Fingerprint: f.fingerprint}
	if err := New(io.Discard).runSetup(j, SetupLogin{Password: "hunter2hunter2"}); err != nil {
		t.Fatal(err)
	}
	if j.Invite != "https://203.0.113.7:8443/invite?t=abc" {
		t.Fatalf("invite %q", j.Invite)
	}
	if len(f.auths) < 3 || f.auths[0].password != "hunter2hunter2" || f.auths[1].key == "" || f.auths[1].password != "" {
		t.Fatalf("the password is for the first login only: %+v", f.auths)
	}
	pub, _ := os.ReadFile(filepath.Join(sshDir, "id_ed25519.pub"))
	if !strings.Contains(f.cmds[0], strings.TrimSpace(string(pub))) || !f.ran("echo installing nook") {
		t.Fatalf("commands: %q", f.cmds)
	}
	if last := f.cmds[len(f.cmds)-1]; !strings.Contains(last, "grep -vxF") {
		t.Fatalf("the key wasn't taken back out at the end: %q", last)
	}
	if !strings.Contains(strings.Join(j.Log, "\n"), "==> nook-admin") {
		t.Fatalf("install output missing from the log: %q", j.Log)
	}
	b, _ := os.ReadFile(setupFile())
	if strings.Contains(string(b), "hunter2") {
		t.Fatal("the password was written down")
	}
}

func TestSetupRefusals(t *testing.T) {
	f := newFakeServer(t)
	target := SetupTarget{Host: "203.0.113.7", Port: 22, User: "root"}

	j := &SetupJob{SetupTarget: target, Fingerprint: "SHA256:" + strings.Repeat("A", 43)}
	if err := New(io.Discard).runSetup(j, SetupLogin{Password: "x"}); err == nil || !strings.Contains(err.Error(), "isn't the one you checked") {
		t.Fatalf("a different host key: %v", err)
	}
	if len(f.cmds) != 0 {
		t.Fatalf("logged in to a server with the wrong host key: %q", f.cmds)
	}

	f.os = "ubuntu 24.04"
	j = &SetupJob{SetupTarget: target, Fingerprint: f.fingerprint}
	if err := New(io.Discard).runSetup(j, SetupLogin{Password: "x"}); err == nil || !strings.Contains(err.Error(), "needs Debian") {
		t.Fatalf("not Debian: %v", err)
	}
	if f.ran("echo installing") || !strings.Contains(f.cmds[len(f.cmds)-1], "grep -vxF") {
		t.Fatalf("installed anyway, or left the key: %q", f.cmds)
	}

	f.os, f.uid, f.cmds = "debian 13", "1000", nil
	j = &SetupJob{SetupTarget: SetupTarget{Host: "203.0.113.7", Port: 22, User: "admin"}, Fingerprint: f.fingerprint}
	if err := New(io.Discard).runSetup(j, SetupLogin{Password: "x"}); err == nil || !strings.Contains(err.Error(), "passwordless sudo") {
		t.Fatalf("no sudo: %v", err)
	}
	f.sudo, f.cmds = "sudo=yes", nil
	if err := New(io.Discard).runSetup(j, SetupLogin{Password: "x"}); err != nil {
		t.Fatal(err)
	}
	if !f.ran("sudo -n sh -c 'echo installing nook'") || !f.ran("sudo -n nook-admin invite") {
		t.Fatalf("sudo: %q", f.cmds)
	}
}

func TestSetupKeyLogin(t *testing.T) {
	newFakeServer(t)
	work := t.TempDir()
	if _, err := firstLogin(work, SetupLogin{Key: "not a key"}); err == nil {
		t.Fatal("junk accepted as a key")
	}
	dir := t.TempDir()
	exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "secret", "-f", filepath.Join(dir, "locked")).Run()
	locked, _ := os.ReadFile(filepath.Join(dir, "locked"))
	if _, err := firstLogin(work, SetupLogin{Key: string(locked)}); err == nil || !strings.Contains(err.Error(), "passphrase") {
		t.Fatalf("a key with a passphrase: %v", err)
	}
	exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", filepath.Join(dir, "open")).Run()
	open, _ := os.ReadFile(filepath.Join(dir, "open"))
	a, err := firstLogin(work, SetupLogin{Key: strings.ReplaceAll(string(open), "\n", "\r\n")})
	if err != nil || a.key == "" {
		t.Fatalf("%+v %v", a, err)
	}
	if st, _ := os.Stat(a.key); st.Mode().Perm() != 0o600 {
		t.Fatalf("key file is %v", st.Mode())
	}
}
