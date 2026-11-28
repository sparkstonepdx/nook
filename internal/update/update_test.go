package update

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewer(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{{"v1.2.0", "v1.1.9", true}, {"v1.2.0", "v1.2.0", false}, {"v1.10.0", "v1.9.0", true}, {"v1.0.0", "v2.0.0", false}, {"v1.0.0", "dev", true}}
	for _, c := range cases {
		if Newer(c.a, c.b) != c.want {
			t.Errorf("%s vs %s", c.a, c.b)
		}
	}
}

func TestVerify(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	old := signingPub
	defer func() { signingPub = old }()
	signingPub = base64.StdEncoding.EncodeToString(pub)
	sums := []byte("abc  nook_v1.0.0_linux_amd64.tar.gz\n")
	sig := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, sums))
	if err := VerifyChecksums(sums, []byte(sig)); err != nil {
		t.Fatal(err)
	}
	if VerifyChecksums(append(sums, 'x'), []byte(sig)) == nil {
		t.Fatal("tampered sums accepted")
	}
	signingPub = ""
	if VerifyChecksums(sums, []byte(sig)) == nil {
		t.Fatal("no key should refuse")
	}
	if h, ok := sha256For(sums, "nook_v1.0.0_linux_amd64.tar.gz"); !ok || h != "abc" {
		t.Fatal("sha256For")
	}
}

func TestGetToStaleToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			w.WriteHeader(403)
			w.Write([]byte(`{"message":"Resource not accessible by personal access token"}`))
			return
		}
		w.Write([]byte("ok"))
	}))
	defer srv.Close()
	var b bytes.Buffer
	if err := getTo(srv.URL, "github_pat_stale", "application/json", &b, 1<<20, nil); err != nil || b.String() != "ok" {
		t.Fatalf("a refused token should fall back to none: %v %q", err, b.String())
	}
}

func TestGetToRateLimit(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.Header().Set("X-RateLimit-Reset", "1795848428")
		w.WriteHeader(403)
		w.Write([]byte(`{"message":"API rate limit exceeded for 203.0.113.7."}`))
	}))
	defer srv.Close()
	err := getTo(srv.URL, "", "application/json", io.Discard, 1<<20, nil)
	if err == nil || !strings.Contains(err.Error(), "rate limit for this server is used up until") || !strings.Contains(err.Error(), "GITHUB_TOKEN") {
		t.Fatalf("rate limit: %v", err)
	}
	calls = 0
	getTo(srv.URL, "tok", "application/json", io.Discard, 1<<20, nil)
	if calls != 1 {
		t.Fatalf("a rate-limited token was retried without it (%d calls): that only spends the address's limit", calls)
	}
}

func TestGetToOtherError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		w.Write([]byte(`{"message":"Repository access blocked"}`))
	}))
	defer srv.Close()
	if err := getTo(srv.URL, "", "application/json", io.Discard, 1<<20, nil); err == nil || !strings.Contains(err.Error(), "Repository access blocked") {
		t.Fatalf("GitHub's message should be in the error: %v", err)
	}
}
