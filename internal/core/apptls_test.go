package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAppTLS(t *testing.T) {
	dir := t.TempDir()
	certsFile, caddyCertsDir = filepath.Join(dir, "certs.json"), filepath.Join(dir, "certs")
	key := func(name string) {
		os.WriteFile(filepath.Join(caddyCertsDir, "acme-v02.api.letsencrypt.org-directory", name, name+".key"), []byte("k"), 0o600)
	}
	if got := tlsLines("mail.example.com", nil); got != "X-Nook-TLS=mail.example.com -\n" {
		t.Fatalf("no certificate yet: %q", got)
	}
	selfSigned(t, caddyCertsDir, "mail.example.com")
	if crt, _ := tlsFiles("mail.example.com"); crt != "" {
		t.Fatal("a certificate without its key isn't handed over")
	}
	key("mail.example.com")
	lines := tlsLines("mail.example.com", nil)
	crt := unitTLS("[Service]\n" + lines)["mail.example.com"]
	if !strings.HasSuffix(crt, "/mail.example.com/mail.example.com.crt") || !strings.Contains(lines, "LoadCredential=tls.key:"+strings.TrimSuffix(crt, ".crt")+".key\n") {
		t.Fatalf("lines: %q", lines)
	}
	// a port host is named for itself, beside the site's own
	selfSigned(t, caddyCertsDir, "imap.example.org")
	key("imap.example.org")
	lines = tlsLines("mail.example.com", []string{"imap.example.org", "mqtt.example.org"})
	got := unitTLS(lines)
	if len(got) != 3 || got["mqtt.example.org"] != "" || !strings.Contains(lines, "LoadCredential=imap.example.org.crt:") || !strings.Contains(lines, "LoadCredential=tls.crt:") {
		t.Fatalf("port hosts: %q", lines)
	}
	// once the domain's wildcard is issued, the site moves onto it
	os.WriteFile(certsFile, []byte(`{"wildcards":[{"domain":"example.com","provider":"cloudflare","env":"NOOK_DNS_1"}]}`), 0o600)
	selfSigned(t, caddyCertsDir, "wildcard_.example.com")
	key("wildcard_.example.com")
	if crt, _ := tlsFiles("mail.example.com"); !strings.HasSuffix(crt, "wildcard_.example.com.crt") {
		t.Fatalf("wildcard: %s", crt)
	}
	if crt, _ := tlsFiles("example.com"); crt != "" {
		t.Fatal("the bare domain isn't under its wildcard")
	}

	// the watcher: a renewed certificate restarts the site, a changed one syncs its app
	unitsDir = filepath.Join(dir, "units")
	t.Cleanup(func() { unitsDir = "/etc/systemd/system" })
	os.MkdirAll(unitsDir, 0o755)
	wild, _ := tlsFiles("mail.example.com")
	os.WriteFile(filepath.Join(unitsDir, UnitName("mail", "mail")), []byte("[Service]\nX-Nook-TLS=mail.example.com "+wild+"\nLoadCredential=tls.crt:"+wild+"\n"), 0o644)
	os.WriteFile(filepath.Join(unitsDir, UnitName("web", "www")), []byte("[Service]\nExecStart=/x\n"), 0o644)
	var restarted []string
	oldR, oldS := restartUnit, unitStarted
	t.Cleanup(func() { restartUnit, unitStarted = oldR, oldS })
	restartUnit = func(u string) { restarted = append(restarted, u) }
	started := time.Now()
	unitStarted = func(string) (time.Time, bool) { return started, true }
	os.Chtimes(wild, started.Add(-time.Hour), started.Add(-time.Hour))
	if did := refreshAppTLS(New(&strings.Builder{})); len(did) != 0 || len(restarted) != 0 {
		t.Fatalf("nothing renewed, still: %v %v", did, restarted)
	}
	os.Chtimes(wild, started.Add(time.Minute), started.Add(time.Minute))
	if did := refreshAppTLS(New(&strings.Builder{})); len(restarted) != 1 || restarted[0] != UnitName("mail", "mail") || len(did) != 1 {
		t.Fatalf("renewed: %v %v", did, restarted)
	}
}
