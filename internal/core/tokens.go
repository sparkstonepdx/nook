package core

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/sparkstonepdx/nook/internal/config"
	"github.com/sparkstonepdx/nook/internal/token"
)

// Deploy tokens: "nook:" + the dashboard's address + "/" + 43 base64url characters (32 random bytes); see
// internal/token/api.go. /etc/nook/tokens (root-only) keeps only their SHA-256, one per line:
// "hash app created_unix label".

var tokenRE = token.APIRE

var labelRE = regexp.MustCompile(`^[A-Za-z0-9 ._@/:-]{0,40}$`)

type TokenInfo struct {
	ID      string `json:"id"` // first 12 hex characters of the hash: enough to name it, useless to use it
	App     string `json:"app"`
	Label   string `json:"label"`
	Created int64  `json:"created"`
}

type tokenLine struct {
	hash string
	TokenInfo
}

func readTokens() []tokenLine {
	b, _ := os.ReadFile(config.TokensFile)
	var out []tokenLine
	for _, l := range strings.Split(string(b), "\n") {
		f := strings.SplitN(l, " ", 4)
		if len(f) < 3 || len(f[0]) != 64 {
			continue
		}
		created, _ := strconv.ParseInt(f[2], 10, 64)
		label := ""
		if len(f) == 4 {
			label = f[3]
		}
		out = append(out, tokenLine{f[0], TokenInfo{ID: f[0][:12], App: f[1], Label: label, Created: created}})
	}
	return out
}

func writeTokens(ts []tokenLine) error {
	var b strings.Builder
	for _, t := range ts {
		fmt.Fprintf(&b, "%s %s %d %s\n", t.hash, t.App, t.Created, t.Label)
	}
	return config.WriteAtomic(config.TokensFile, []byte(b.String()), 0o600)
}

func hashToken(tok string) string {
	h := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(h[:])
}

// TokenCreate makes a deploy token for app. The token itself is only ever returned here.
func TokenCreate(app, label string) (string, TokenInfo, error) {
	if _, err := LookupApp(app); err != nil {
		return "", TokenInfo{}, err
	}
	label = strings.TrimSpace(label)
	if !labelRE.MatchString(label) {
		return "", TokenInfo{}, fmt.Errorf("label: up to 40 of letters, digits, space . _ @ / : -")
	}
	tok, err := newToken()
	if err != nil {
		return "", TokenInfo{}, err
	}
	unlock, err := Lock()
	if err != nil {
		return "", TokenInfo{}, err
	}
	defer unlock()
	info, err := addTokenLocked(app, label, tok)
	return tok, info, err
}

func addTokenLocked(app, label, tok string) (TokenInfo, error) {
	t := tokenLine{hashToken(tok), TokenInfo{App: app, Label: label, Created: time.Now().Unix()}}
	t.ID = t.hash[:12]
	return t.TokenInfo, writeTokens(append(readTokens(), t))
}

func newToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	// the address deploys go to, as DeployBase gives it: the dashboard's hostname, or IP:port before it has one
	addr := strings.TrimSuffix(strings.TrimPrefix(DeployBase(config.Load()), "https://"), "/api/v1/sites/")
	secret := base64.RawURLEncoding.EncodeToString(raw)
	if _, ok := token.APIServer("nook:" + addr + "/" + secret); ok {
		return "nook:" + addr + "/" + secret, nil
	}
	return "nook_" + secret, nil // no usable address: the older form, used with the action's url
}

// TokenList lists app's tokens (all apps when app is "").
func TokenList(app string) []TokenInfo {
	out := []TokenInfo{}
	for _, t := range readTokens() {
		if app == "" || t.App == app {
			out = append(out, t.TokenInfo)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created < out[j].Created })
	return out
}

// TokenRevoke removes app's token with the given ID.
func TokenRevoke(app, id string) error {
	unlock, err := Lock()
	if err != nil {
		return err
	}
	defer unlock()
	var keep []tokenLine
	found := false
	for _, t := range readTokens() {
		if t.App == app && t.ID == id {
			found = true
			continue
		}
		keep = append(keep, t)
	}
	if !found {
		return fmt.Errorf("no token %s for app %s", id, app)
	}
	return writeTokens(keep)
}

// tokensDropApp removes every token of a deleted app.
func tokensDropApp(app string) {
	var keep []tokenLine
	for _, t := range readTokens() {
		if t.App != app {
			keep = append(keep, t)
		}
	}
	writeTokens(keep)
}

// TokenApp returns the app a deploy token belongs to.
func TokenApp(tok string) (string, bool) {
	if !tokenRE.MatchString(tok) {
		return "", false
	}
	h := hashToken(tok)
	for _, t := range readTokens() {
		if t.hash == h {
			if _, err := LookupApp(t.App); err == nil {
				return t.App, true
			}
		}
	}
	return "", false
}

// LooksLikeToken is a cheap format check, for refusing junk before any work.
func LooksLikeToken(tok string) bool { return tokenRE.MatchString(tok) }
