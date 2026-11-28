package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSetupRealSSH logs in to a real sshd: NOOK_SSHD_TEST=host:port:user:password (an sshd with password logins).
func TestSetupRealSSH(t *testing.T) {
	spec := strings.SplitN(os.Getenv("NOOK_SSHD_TEST"), ":", 4)
	if len(spec) != 4 {
		t.Skip("set NOOK_SSHD_TEST=host:port:user:password")
	}
	port := 0
	for _, c := range spec[1] {
		port = port*10 + int(c-'0')
	}
	tg := SetupTarget{Host: spec[0], Port: port, User: spec[2]}
	keys, err := ScanHostKeys(tg)
	if err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	kh := filepath.Join(work, "known_hosts")
	os.WriteFile(kh, []byte(keys[0].line+"\n"), 0o600)
	old := sshDir
	sshDir = filepath.Join(t.TempDir(), "ssh")
	t.Cleanup(func() { sshDir = old })
	pub, err := ownSSHKey()
	if err != nil {
		t.Fatal(err)
	}
	bad, _ := firstLogin(work, SetupLogin{Password: "wrong"})
	if out, err := sshRun(tg, kh, bad, "true", nil); err == nil || !strings.Contains(sshError(out, err), "refused") {
		t.Fatalf("a wrong password: %q %v", out, err)
	}
	a, _ := firstLogin(work, SetupLogin{Password: spec[3]})
	q := shellQuote(pub)
	if out, err := sshRun(tg, kh, a, "umask 077; mkdir -p ~/.ssh && touch ~/.ssh/authorized_keys && (grep -qxF "+q+" ~/.ssh/authorized_keys || printf '%s\\n' "+q+" >> ~/.ssh/authorized_keys)", nil); err != nil {
		t.Fatalf("password login: %q %v", out, err)
	}
	key := sshAuth{key: filepath.Join(sshDir, "id_ed25519")}
	if out, err := sshRun(tg, kh, key, "echo it-works", nil); err != nil || !strings.Contains(out, "it-works") {
		t.Fatalf("key login: %q %v", out, err)
	}
	del := "f=~/.ssh/authorized_keys; grep -vxF " + q + " \"$f\" > \"$f.nook\"; cat \"$f.nook\" > \"$f\"; rm -f \"$f.nook\""
	if out, err := sshRun(tg, kh, key, del, nil); err != nil {
		t.Fatalf("removing the key: %q %v", out, err)
	}
	if out, err := sshRun(tg, kh, key, "true", nil); err == nil {
		t.Fatalf("the key still logs in after removal: %q", out)
	}
	// a changed host key is refused
	os.WriteFile(kh, []byte("["+tg.Host+"]:"+spec[1]+" ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl\n"), 0o600)
	if out, err := sshRun(tg, kh, a, "true", nil); err == nil || !strings.Contains(sshError(out, err), "doesn't match") {
		t.Fatalf("a changed host key: %q %v", out, err)
	}
}
