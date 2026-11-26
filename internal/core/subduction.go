package core

// A Subduction app's identity (the catalog's Subduction), for apps that sync through it with Keyhive: its
// websocket URL, its peer id (the public half of its key, which nook works out here) and its Keyhive contact card
// (made here from the key; a card saved beside it as subduction-card.json is used instead when it's for that key).
// Apps building against the server need the card and peer id as build variables; the dashboard shows them ready
// to copy.

import (
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sparkstonepdx/nook/internal/config"
	"github.com/sparkstonepdx/nook/internal/names"
)

// SubductionIdentity is what an app needs to sync through the server with Keyhive.
type SubductionIdentity struct {
	// Installed is false when there's no Subduction app on this server.
	Installed bool   `json:"installed"`
	URL       string `json:"url,omitempty"`
	// PeerID is the base64 public key, the form automerge-repo-keyhive takes; PeerIDHex is how the server logs it.
	PeerID    string `json:"peerId,omitempty"`
	PeerIDHex string `json:"peerIdHex,omitempty"`
	// Card is the contact card JSON.
	Card string `json:"card,omitempty"`
	// Problem says what's wrong, when something is.
	Problem string `json:"problem,omitempty"`
}

// ReadSeed reads a Subduction key file: 32 bytes, raw or as 64 hex digits (subduction_cli takes both).
func ReadSeed(b []byte) ([]byte, error) {
	if s := strings.TrimSpace(string(b)); len(s) == 64 {
		if seed, err := hex.DecodeString(s); err == nil {
			return seed, nil
		}
	}
	if len(b) == ed25519.SeedSize {
		return b, nil
	}
	return nil, fmt.Errorf("the key file must hold 32 bytes, raw or as 64 hex digits")
}

// makeCard makes the server's Keyhive contact card from its key, the way @keyhive/keyhive does: an Add op whose
// payload is an X25519 share key, signed by the server's Ed25519 key over the share key's 32 bytes (bincode of the
// op). The share key is derived from the seed, so the card is the same every time it's made. Only the public
// halves are in it.
func makeCard(seed []byte) (string, error) {
	h := sha256.Sum256(append([]byte("nook subduction share key\x00"), seed...))
	share, err := ecdh.X25519().NewPrivateKey(h[:])
	if err != nil {
		return "", err
	}
	pub := share.PublicKey().Bytes()
	priv := ed25519.NewKeyFromSeed(seed)
	ints := func(b []byte) []int {
		out := make([]int, len(b))
		for i, x := range b {
			out[i] = int(x)
		}
		return out
	}
	type payload struct {
		ShareKey []int `json:"share_key"`
	}
	type add struct {
		Payload   payload `json:"payload"`
		Issuer    []int   `json:"issuer"`
		Signature []int   `json:"signature"`
	}
	b, err := json.Marshal(map[string]add{"Add": {payload{ints(pub)}, ints(priv.Public().(ed25519.PublicKey)), ints(ed25519.Sign(priv, pub))}})
	return string(b), err
}

// cardIssuer is the public key a contact card says it's from.
func cardIssuer(card []byte) ([]byte, error) {
	var c struct {
		Add struct {
			Raw []int `json:"issuer"`
		} `json:"Add"`
	}
	if err := json.Unmarshal(card, &c); err != nil {
		return nil, err
	}
	out := make([]byte, len(c.Add.Raw))
	for i, v := range c.Add.Raw {
		if v < 0 || v > 255 {
			return nil, fmt.Errorf("issuer isn't bytes")
		}
		out[i] = byte(v)
	}
	if len(out) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("issuer is %d bytes, want 32", len(out))
	}
	return out, nil
}

// subductionIdentity reads the identity from the app's data folder: key at data/subduction.key, card at
// data/subduction-card.json.
func subductionIdentity(home, host string) SubductionIdentity {
	keyFile := filepath.Join(home, "data", "subduction.key")
	b, err := os.ReadFile(keyFile)
	if err != nil {
		if os.IsNotExist(err) {
			return SubductionIdentity{}
		}
		return SubductionIdentity{Installed: true, Problem: "Couldn't read the key: " + err.Error()}
	}
	id := SubductionIdentity{Installed: true, URL: "wss://" + host}
	seed, err := ReadSeed(b)
	if err != nil {
		id.Problem = err.Error()
		return id
	}
	pub := ed25519.NewKeyFromSeed(seed).Public().(ed25519.PublicKey)
	id.PeerID, id.PeerIDHex = base64.StdEncoding.EncodeToString(pub), hex.EncodeToString(pub)

	// a saved card (the one apps already have, made by Node before nook made them) is kept while it's for this key
	if card, err := os.ReadFile(filepath.Join(home, "data", "subduction-card.json")); err == nil {
		if issuer, err := cardIssuer(card); err == nil && ed25519.PublicKey(issuer).Equal(pub) {
			id.Card = strings.TrimSpace(string(card))
			return id
		}
	}
	if c, err := makeCard(seed); err != nil {
		id.Problem = "Couldn't make the contact card: " + err.Error()
	} else {
		id.Card = c
	}
	return id
}

// subductionSite is app's Subduction site: the one it was installed from the official catalog on (any name). ""
// when app isn't a Subduction.
func subductionSite(app string) string {
	rec, fromStore := readInstalled()[app]
	return subductionSiteOf(rec, fromStore)
}

func subductionSiteOf(rec Installed, fromStore bool) string {
	if fromStore && rec.ID == "subduction" && (rec.Catalog == "" || rec.Catalog == OfficialCatalog) {
		return rec.Site
	}
	return ""
}

// SubductionFor is app's Subduction identity, or one with Installed false when app isn't a Subduction.
func SubductionFor(app string) SubductionIdentity {
	site := subductionSite(app)
	if site == "" {
		return SubductionIdentity{}
	}
	return subductionIdentity(filepath.Join(config.AppsDir, app), names.HostOf(site, config.Load().BaseDomain))
}

// Subduction is this server's Subduction identity, for `nook-admin subduction` and the dashboard when it doesn't
// name an app: the first Subduction app, whatever its name and site.
func Subduction() SubductionIdentity {
	for _, a := range Apps() {
		if id := SubductionFor(a); id.Installed {
			return id
		}
	}
	return SubductionIdentity{}
}
