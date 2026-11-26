package core

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
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
	if id := subductionIdentity(home, "subduction.example.com"); id.Installed {
		t.Fatal("installed with no key")
	}
	seed := make([]byte, 32)
	for i := range seed {
		seed[i] = byte(i)
	}
	pub := writeKey(t, home, seed)

	id := subductionIdentity(home, "subduction.example.com")
	if id.URL != "wss://subduction.example.com" || id.PeerIDHex != hex.EncodeToString(pub) || id.Card == "" || id.Problem != "" {
		t.Fatalf("no saved card: made from the key: %+v", id)
	}
	if again := subductionIdentity(home, "subduction.example.com"); again.Card != id.Card {
		t.Fatal("the made card changes between reads")
	}
	if iss, err := cardIssuer([]byte(id.Card)); err != nil || !ed25519.PublicKey(iss).Equal(ed25519.PublicKey(pub)) {
		t.Fatalf("made card's issuer: %v", err)
	}

	os.WriteFile(filepath.Join(home, "data", "subduction-card.json"), []byte(cardFor(pub)), 0o600)
	id = subductionIdentity(home, "subduction.example.com")
	if id.Card == "" || id.Problem != "" {
		t.Fatalf("card: %+v", id)
	}

	other := ed25519.NewKeyFromSeed(make([]byte, 32)).Public().(ed25519.PublicKey)
	os.WriteFile(filepath.Join(home, "data", "subduction-card.json"), []byte(cardFor(other)), 0o600)
	if id = subductionIdentity(home, "subduction.example.com"); id.Card != "" || !strings.Contains(id.Problem, "different key") {
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
	id := subductionIdentity("testdata/subduction", "subduction.example.com")
	if id.Card == "" || id.Problem != "" {
		t.Fatalf("%+v", id)
	}
}

// The card Go makes is signed the way Keyhive checks it: Ed25519 over the share key's 32 bytes.
func TestMakeCardSignature(t *testing.T) {
	seed := make([]byte, 32)
	seed[0] = 7
	c, err := makeCard(seed)
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		Add struct {
			Payload struct {
				ShareKey []int `json:"share_key"`
			} `json:"payload"`
			Issuer, Signature []int
		}
	}
	if err := json.Unmarshal([]byte(c), &v); err != nil {
		t.Fatal(err)
	}
	b := func(x []int) []byte {
		o := make([]byte, len(x))
		for i, n := range x {
			o[i] = byte(n)
		}
		return o
	}
	if !ed25519.Verify(b(v.Add.Issuer), b(v.Add.Payload.ShareKey), b(v.Add.Signature)) || len(v.Add.Payload.ShareKey) != 32 {
		t.Fatalf("signature doesn't verify: %s", c)
	}
}

func TestSubductionSiteOf(t *testing.T) {
	for _, c := range []struct {
		app       string
		rec       Installed
		fromStore bool
		sites     []string
		want      string
	}{
		{"subduction", Installed{}, false, []string{"subduction"}, "subduction"},                                    // the hand setup
		{"sync", Installed{ID: "subduction", Catalog: "official", Site: "relay"}, true, []string{"relay"}, "relay"}, // the catalog's, any name and site
		{"subduction-2", Installed{ID: "subduction", Site: "subduction"}, true, []string{"subduction"}, "subduction"},
		{"subduction", Installed{ID: "subduction", Catalog: "someone", Site: "x"}, true, []string{"x"}, ""}, // a community catalog's: not trusted as the server
		{"pocketbase", Installed{ID: "pocketbase", Site: "pb"}, true, []string{"pb"}, ""},
		{"web", Installed{}, false, []string{"subduction"}, ""}, // a hand-made app that isn't the hand setup
	} {
		if got := subductionSiteOf(c.app, c.rec, c.fromStore, c.sites); got != c.want {
			t.Errorf("%s %+v: %q, want %q", c.app, c.rec, got, c.want)
		}
	}
}
