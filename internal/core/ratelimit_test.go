package core

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// what Caddy logs when Let's Encrypt's weekly limit is reached
const limitLine = `{"level":"error","ts":1794600000.1,"logger":"tls.obtain","msg":"could not get certificate from issuer","identifier":"*.example.com","issuer":"acme-v02.api.letsencrypt.org-directory","error":"HTTP 429 urn:ietf:params:acme:error:rateLimited - too many certificates (50) already issued for \"example.com\" in the last 168h0m0s, retry after 2036-11-14 08:21:17 UTC: see https://letsencrypt.org/docs/rate-limits/#new-certificates-per-registered-domain"}`

func noLimits(t *testing.T, dir string) {
	t.Helper()
	certLimitsFile = filepath.Join(dir, "cert-limits.json")
	rateLimitLog = func() string { return "" }
	limitsCache.m = nil
}

func TestLimitsFromLog(t *testing.T) {
	got := limitsFromLog("noise\n"+limitLine+"\n", []string{"example.com", "notexample.com", "other.com"})
	want := time.Date(2036, 11, 14, 8, 21, 17, 0, time.UTC)
	if !got["example.com"].Equal(want) {
		t.Fatalf("example.com: got %v, want %v", got["example.com"], want)
	}
	if len(got) != 1 {
		t.Fatalf("only example.com is limited, got %v", got)
	}
	if mentions(`for "notexample.com"`, "example.com") || mentions("example.com.evil", "example.com") || !mentions("a.b.example.com", "example.com") {
		t.Fatal("mentions matches the wrong names")
	}
}

func selfSigned(t *testing.T, dir, name string) {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(90 * 24 * time.Hour)}
	der, _ := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	d := filepath.Join(dir, "acme-v02.api.letsencrypt.org-directory", name)
	os.MkdirAll(d, 0o755)
	os.WriteFile(filepath.Join(d, name+".crt"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644)
}

func TestRateLimitHoldsNewHosts(t *testing.T) {
	dir := t.TempDir()
	certsFile, caddyCertsDir = filepath.Join(dir, "certs.json"), filepath.Join(dir, "certs")
	noLimits(t, dir)
	os.WriteFile(certsFile, []byte(`{"wildcards":[{"domain":"example.com","provider":"cloudflare","env":"NOOK_DNS_1"}]}`), 0o600)
	bin := filepath.Join(dir, "bin")
	os.MkdirAll(bin, 0o755)
	os.WriteFile(filepath.Join(bin, "caddy"), []byte("#!/bin/sh\necho v2.10.2\n"), 0o755)
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	caddyVersionCache.mtime = time.Time{}

	selfSigned(t, caddyCertsDir, "old.example.com") // got its own before the limit
	before := wildcardState()
	rateLimitLog = func() string { return limitLine }
	limitsCache.m = nil

	own := OwnCert()
	if !own("old.example.com") {
		t.Error("a host with its own certificate should keep it")
	}
	if own("new.example.com") {
		t.Error("a host without one should wait for the wildcard, not ask for its own")
	}
	if wildcardState() == before {
		t.Error("the watcher wouldn't re-sync when the limit is hit")
	}
	st, _, _, retry := wildcardStatus("example.com", certLimits()["example.com"])
	if st != "limited" || retry != "2036-11-14T08:21:17Z" {
		t.Errorf("status %q retry %q", st, retry)
	}
	// the journal rotates: the stored record still holds
	rateLimitLog = func() string { return "" }
	limitsCache.m = nil
	if _, ok := certLimits()["example.com"]; !ok {
		t.Fatal("the limit was forgotten when the log rotated")
	}
	// the wildcard is issued: the record goes
	selfSigned(t, caddyCertsDir, "wildcard_.example.com")
	limitsCache.m = nil
	if len(certLimits()) != 0 {
		t.Fatal("the record should go once the wildcard is issued")
	}
}
