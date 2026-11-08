package update

import (
	"crypto/ed25519"
	"encoding/base64"
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
