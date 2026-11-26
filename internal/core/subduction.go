package core

// The Subduction app's identity (extras/subduction-app.sh), for apps that sync through it with Keyhive: its
// websocket URL, its peer id (the public half of its key, which nook works out here) and its Keyhive contact card
// (made from the key by extras/subduction-card.mjs, saved beside it as subduction-card.json). Apps building
// against the server need the card and peer id as build variables; the dashboard shows them ready to copy.

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
	"slices"
	"strings"

	"github.com/sparkstonepdx/nook/internal/config"
	"github.com/sparkstonepdx/nook/internal/names"
)

// SubductionApp and SubductionSite are where extras/subduction-app.sh puts it.
const (
	SubductionApp  = "subduction"
	SubductionSite = "subduction"
)

// SubductionIdentity is what an app needs to sync through the server with Keyhive.
type SubductionIdentity struct {
	// Installed is false when there's no Subduction app on this server.
	Installed bool   `json:"installed"`
	URL       string `json:"url,omitempty"`
	// PeerID is the base64 public key, the form automerge-repo-keyhive takes; PeerIDHex is how the server logs it.
	PeerID    string `json:"peerId,omitempty"`
	PeerIDHex string `json:"peerIdHex,omitempty"`
	// Card is the contact card JSON, empty until extras/subduction-card.mjs has made it.
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
// halves are in it. Replaces extras/subduction-card.mjs, which needed Node on the server.
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
func subductionIdentity(home, baseDomain string) SubductionIdentity {
	keyFile := filepath.Join(home, "data", "subduction.key")
	b, err := os.ReadFile(keyFile)
	if err != nil {
		if os.IsNotExist(err) {
			return SubductionIdentity{}
		}
		return SubductionIdentity{Installed: true, Problem: "Couldn't read the key: " + err.Error()}
	}
	id := SubductionIdentity{Installed: true, URL: "wss://" + names.HostOf(SubductionSite, baseDomain)}
	seed, err := ReadSeed(b)
	if err != nil {
		id.Problem = err.Error()
		return id
	}
	pub := ed25519.NewKeyFromSeed(seed).Public().(ed25519.PublicKey)
	id.PeerID, id.PeerIDHex = base64.StdEncoding.EncodeToString(pub), hex.EncodeToString(pub)

	card, err := os.ReadFile(filepath.Join(home, "data", "subduction-card.json"))
	switch {
	case os.IsNotExist(err): // none saved (the catalog app, or a hand setup without Node): made here from the key
		if c, err := makeCard(seed); err != nil {
			id.Problem = "Couldn't make the contact card: " + err.Error()
		} else {
			id.Card = c
		}
	case err != nil:
		id.Problem = "Couldn't read the contact card: " + err.Error()
	default:
		issuer, err := cardIssuer(card)
		if err != nil {
			id.Problem = "The contact card isn't one nook can read: " + err.Error()
		} else if !ed25519.PublicKey(issuer).Equal(pub) {
			id.Problem = "The contact card is for a different key. Re-run extras/subduction-app.sh to make a new one."
		} else {
			id.Card = strings.TrimSpace(string(card))
		}
	}
	return id
}

// Subduction is this server's Subduction identity, for the dashboard and `nook-admin subduction`: the app serving
// the subduction site (the catalog's, once the hand setup has moved onto it; subductionmove.go), else app subduction.
func Subduction() SubductionIdentity {
	app := SubductionApp
	for _, a := range Apps() {
		if slices.Contains(SiteNames(a), SubductionSite) {
			app = a
			break
		}
	}
	return subductionIdentity(filepath.Join(config.AppsDir, app), config.Load().BaseDomain)
}
