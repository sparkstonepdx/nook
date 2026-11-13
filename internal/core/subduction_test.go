package core

import (
	"crypto/ed25519"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeKey(t *testing.T, home string, seed []byte) ed25519.PublicKey {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(home, "data"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "data", "subduction.key"), []byte(hex.EncodeToString(seed)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return ed25519.NewKeyFromSeed(seed).Public().(ed25519.PublicKey)
}

func cardFor(pub []byte) string {
	nums := make([]string, len(pub))
	for i, b := range pub {
		nums[i] = fmt.Sprint(b)
	}
	return `{"Add":{"payload":{"share_key":[1,2]},"issuer":[` + strings.Join(nums, ",") + `],"signature":[0]}}`
}

func TestSubductionIdentity(t *testing.T) {
	home := t.TempDir()
	if id := subductionIdentity(home, "example.com"); id.Installed {
		t.Fatal("installed with no key")
	}
	seed := make([]byte, 32)
	for i := range seed {
		seed[i] = byte(i)
	}
	pub := writeKey(t, home, seed)

	id := subductionIdentity(home, "example.com")
	if id.URL != "wss://subduction.example.com" || id.PeerIDHex != hex.EncodeToString(pub) || id.Card != "" || !strings.Contains(id.Problem, "No contact card") {
		t.Fatalf("no card: %+v", id)
	}

	os.WriteFile(filepath.Join(home, "data", "subduction-card.json"), []byte(cardFor(pub)), 0o600)
	id = subductionIdentity(home, "example.com")
	if id.Card == "" || id.Problem != "" {
		t.Fatalf("card: %+v", id)
	}

	other := ed25519.NewKeyFromSeed(make([]byte, 32)).Public().(ed25519.PublicKey)
	os.WriteFile(filepath.Join(home, "data", "subduction-card.json"), []byte(cardFor(other)), 0o600)
	if id = subductionIdentity(home, "example.com"); id.Card != "" || !strings.Contains(id.Problem, "different key") {
		t.Fatalf("someone else's card: %+v", id)
	}
}

func TestReadSeed(t *testing.T) {
	raw := make([]byte, 32)
	if s, err := ReadSeed(raw); err != nil || len(s) != 32 {
		t.Fatal("raw seed", err)
	}
	if _, err := ReadSeed([]byte("nope")); err == nil {
		t.Fatal("took a bad key")
	}
}

// A card made by extras/subduction-card.mjs with @keyhive/keyhive 0.4.0-alpha.1, for a throwaway key.
func TestSubductionRealCard(t *testing.T) {
	id := subductionIdentity("testdata/subduction", "example.com")
	if id.Card == "" || id.Problem != "" {
		t.Fatalf("%+v", id)
	}
}
