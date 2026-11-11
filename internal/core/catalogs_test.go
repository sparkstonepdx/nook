package core

import (
	"crypto/ed25519"
	"encoding/base64"
	"testing"

	"github.com/sparkstonepdx/nook/internal/update"
)

func TestSourceFor(t *testing.T) {
	for in, want := range map[string]string{
		"alice/nook-apps":                        "https://raw.githubusercontent.com/alice/nook-apps/dist/",
		"https://github.com/alice/nook-apps":     "https://raw.githubusercontent.com/alice/nook-apps/dist/",
		"https://github.com/alice/nook-apps.git": "https://raw.githubusercontent.com/alice/nook-apps/dist/",
		"https://apps.example.com/catalog/":      "https://apps.example.com/catalog/",
		"https://apps.example.com/c/index.json":  "https://apps.example.com/c/",
	} {
		s, err := sourceFor(in)
		if err != nil || s.URL != want {
			t.Errorf("%s: got %q %v, want %q", in, s.URL, err, want)
		}
	}
	for _, bad := range []string{"", "alice", "http://apps.example.com/", "ftp://x/y", "a/b/c"} {
		if _, err := sourceFor(bad); err == nil {
			t.Errorf("%q: accepted", bad)
		}
	}
	if s, _ := sourceFor("Alice/Nook-Apps"); s.ID != "alice-nook-apps" {
		t.Errorf("id %q", s.ID)
	}
}

func TestVerifyWith(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	other, _, _ := ed25519.GenerateKey(nil)
	data := []byte(`{"apps":[]}`)
	sig := []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(priv, data)))
	if err := update.VerifyWith(base64.StdEncoding.EncodeToString(pub), data, sig); err != nil {
		t.Fatal(err)
	}
	if update.VerifyWith(base64.StdEncoding.EncodeToString(other), data, sig) == nil {
		t.Fatal("accepted another key")
	}
	if update.VerifyWith(base64.StdEncoding.EncodeToString(pub), []byte(`{"apps":[1]}`), sig) == nil {
		t.Fatal("accepted changed data")
	}
}
