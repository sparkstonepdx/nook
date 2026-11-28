package core

// Linked servers (#111, step B). Two nook servers that trust each other: each has an Ed25519 key, and each keeps
// the other's public key. A dashboard then acts on a linked server by sending it signed requests, which it runs
// through its own helper as if a local admin had clicked.
//
//	1. code   the server to be linked makes a one-time code: its dashboard's address and a secret, 24 hours
//	2. join   the other server sends its id, address and public key to /api/v1/link/hello with the secret as a
//	          bearer token, and gets back the same about the first. Both now hold a Link to the other.
//	3. use    signed requests to /api/v1/link/*: who sent it, to whom, the method, path, time and the body's
//	          sha256, signed with the sender's key. The receiver checks the signature against the link, the
//	          time against its clock (5 minutes either way) and that it hasn't seen the signature before.
//	4. end    either side removes the link, and tells the other (best effort): a link one side has dropped
//	          is refused there either way.
//
// The private key never leaves the helper (root): the dashboard asks it to send, and to check what arrives.

import (
	"bytes"
	"cmp"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sparkstonepdx/nook/internal/config"
	"github.com/sparkstonepdx/nook/internal/rpc"
)

const (
	LinkSame = "same" // both servers are yours: everything, and users kept in step
	linkTTL  = 24 * time.Hour
	linkSkew = 5 * time.Minute
)

var (
	linkDir   = "/etc/nook/link" // key, codes (a var for tests)
	linksFile = "/etc/nook/links.json"
	linkRE    = regexp.MustCompile(`^nooklink_([A-Za-z0-9.-]+(?::[0-9]{1,5})?)/([A-Za-z0-9_-]{43})$`)
	linkIDRE  = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
	linkHost  = regexp.MustCompile(`^[A-Za-z0-9.-]+(?::[0-9]{1,5})?$`)
	linkMu    sync.Mutex
)

// Link is a server this one is linked to.
type Link struct {
	ID    string `json:"id"`   // its server id
	Name  string `json:"name"` // its dashboard's address
	URL   string `json:"url"`  // https://NAME
	Pub   string `json:"pub"`  // its Ed25519 public key, base64
	Kind  string `json:"kind"` // LinkSame
	Since int64  `json:"since"`
	Seen  int64  `json:"seen,omitempty"` // the last request it sent that checked out
}

// LinkSelf is what this server tells another about itself.
type LinkSelf struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	URL  string `json:"url"`
	Pub  string `json:"pub"`
	Kind string `json:"kind,omitempty"` // in a hello's answer: what the link is
}

func readLinks() []Link {
	var l []Link
	if b, err := os.ReadFile(linksFile); err == nil {
		json.Unmarshal(b, &l)
	}
	return l
}

func writeLinks(l []Link) error {
	if l == nil {
		l = []Link{}
	}
	b, _ := json.MarshalIndent(l, "", "  ")
	return config.WriteAtomic(linksFile, b, 0o600)
}

// Links is every server this one is linked to.
func Links() []Link { linkMu.Lock(); defer linkMu.Unlock(); return readLinks() }

// FindLink is the link to server id.
func FindLink(id string) (Link, bool) {
	for _, l := range Links() {
		if l.ID == id {
			return l, true
		}
	}
	return Link{}, false
}

func saveLink(n Link) error {
	linkMu.Lock()
	defer linkMu.Unlock()
	l := slices.DeleteFunc(readLinks(), func(x Link) bool { return x.ID == n.ID })
	return writeLinks(append(l, n))
}

// linkKey is this server's signing key, made on first use.
func linkKey() (ed25519.PrivateKey, error) {
	p := filepath.Join(linkDir, "key")
	if b, err := os.ReadFile(p); err == nil {
		seed, err := hex.DecodeString(strings.TrimSpace(string(b)))
		if err == nil && len(seed) == ed25519.SeedSize {
			return ed25519.NewKeyFromSeed(seed), nil
		}
		return nil, fmt.Errorf("%s isn't a key: remove it to make a new one (every link will need making again)", p)
	}
	seed := make([]byte, ed25519.SeedSize)
	rand.Read(seed)
	os.MkdirAll(linkDir, 0o700)
	if err := config.WriteAtomic(p, []byte(hex.EncodeToString(seed)+"\n"), 0o600); err != nil {
		return nil, err
	}
	return ed25519.NewKeyFromSeed(seed), nil
}

// linkSelf is this server: its id, address and public key. It needs the dashboard's address.
var linkSelf = func() (LinkSelf, error) {
	d := config.LoadDashboard()
	if d.Host == "" {
		return LinkSelf{}, errors.New("set the dashboard's address first (Domains page): linked servers reach each other there")
	}
	k, err := linkKey()
	if err != nil {
		return LinkSelf{}, err
	}
	return LinkSelf{ID: serverID(), Name: d.Host, URL: "https://" + d.Host, Pub: base64.StdEncoding.EncodeToString(k.Public().(ed25519.PublicKey))}, nil
}

func (s LinkSelf) check() error {
	pub, err := base64.StdEncoding.DecodeString(s.Pub)
	switch {
	case !linkIDRE.MatchString(s.ID):
		return fmt.Errorf("%q isn't a server id", s.ID)
	case !linkHost.MatchString(s.Name) || s.URL != "https://"+s.Name:
		return fmt.Errorf("%q isn't a dashboard address", s.Name)
	case err != nil || len(pub) != ed25519.PublicKeySize:
		return errors.New("that isn't a public key")
	}
	return nil
}

// ---- codes ----

type linkCode struct {
	Hash    string `json:"hash"`
	Expires int64  `json:"expires"`
	Kind    string `json:"kind"`
}

func codesPath() string { return filepath.Join(linkDir, "codes.json") }

func readCodes() []linkCode {
	var c []linkCode
	if b, err := os.ReadFile(codesPath()); err == nil {
		json.Unmarshal(b, &c)
	}
	now := time.Now().Unix()
	return slices.DeleteFunc(c, func(x linkCode) bool { return x.Expires < now })
}

func writeCodes(c []linkCode) error {
	os.MkdirAll(linkDir, 0o700)
	b, _ := json.Marshal(c)
	return config.WriteAtomic(codesPath(), b, 0o600)
}

// LinkCode makes a one-time code another server takes to link with this one.
func (h *Host) LinkCode(kind string) (string, error) {
	if kind != LinkSame {
		return "", fmt.Errorf("unknown kind of link %q", kind)
	}
	me, err := linkSelf()
	if err != nil {
		return "", err
	}
	b := make([]byte, 32)
	rand.Read(b)
	secret := base64.RawURLEncoding.EncodeToString(b)
	linkMu.Lock()
	defer linkMu.Unlock()
	if err := writeCodes(append(readCodes(), linkCode{Hash: pairHash(secret), Expires: time.Now().Add(linkTTL).Unix(), Kind: kind})); err != nil {
		return "", err
	}
	return "nooklink_" + me.Name + "/" + secret, nil
}

// takeCode spends secret's code and returns it.
func takeCode(secret string) (linkCode, error) {
	linkMu.Lock()
	defer linkMu.Unlock()
	codes := readCodes()
	h := pairHash(secret)
	for i, c := range codes {
		if subtle.ConstantTimeCompare([]byte(c.Hash), []byte(h)) == 1 {
			writeCodes(slices.Delete(codes, i, i+1))
			return c, nil
		}
	}
	return linkCode{}, errors.New("that link code isn't this server's, or it's used or more than 24 hours old: make a new one")
}

// LinkHello is the code maker's side of joining: it records the other server and says who it is.
func (h *Host) LinkHello(secret string, other LinkSelf) (LinkSelf, error) {
	if err := other.check(); err != nil {
		return LinkSelf{}, err
	}
	me, err := linkSelf()
	if err != nil {
		return LinkSelf{}, err
	}
	if other.ID == me.ID {
		return LinkSelf{}, errors.New("that's this server's own code")
	}
	c, err := takeCode(secret)
	if err != nil {
		return LinkSelf{}, err
	}
	if err := saveLink(Link{ID: other.ID, Name: other.Name, URL: other.URL, Pub: other.Pub, Kind: c.Kind, Since: time.Now().Unix()}); err != nil {
		return LinkSelf{}, err
	}
	me.Kind = c.Kind
	return me, nil
}

// linkClient talks to other servers (a var for tests).
var linkClient = &http.Client{Timeout: 15 * time.Minute}

// LinkJoin takes another server's code and links this server with it.
func (h *Host) LinkJoin(code string) (Link, error) {
	m := linkRE.FindStringSubmatch(strings.TrimSpace(code))
	if m == nil {
		return Link{}, errors.New("a link code looks like nooklink_admin.example.com/…: make one on the other server's Server page")
	}
	me, err := linkSelf()
	if err != nil {
		return Link{}, err
	}
	if m[1] == me.Name {
		return Link{}, errors.New("that's this server's own code: take it to the other server")
	}
	body, _ := json.Marshal(me)
	req, _ := http.NewRequest("POST", linkBase(m[1])+"/api/v1/link/hello", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+m[2])
	req.Header.Set("Content-Type", "application/json")
	var other LinkSelf
	if err := linkDo(req, m[1], &other); err != nil {
		return Link{}, err
	}
	if err := other.check(); err != nil {
		return Link{}, fmt.Errorf("%s answered oddly: %v", m[1], err)
	}
	if other.Name != m[1] {
		return Link{}, fmt.Errorf("%s says it's %s", m[1], other.Name)
	}
	if other.Kind != LinkSame {
		return Link{}, fmt.Errorf("%s offered a kind of link this server doesn't know (%q): update nook here", m[1], other.Kind)
	}
	l := Link{ID: other.ID, Name: other.Name, URL: other.URL, Pub: other.Pub, Kind: other.Kind, Since: time.Now().Unix()}
	if err := saveLink(l); err != nil {
		return Link{}, err
	}
	h.say("linked with %s (%s)", l.Name, l.ID)
	return l, nil
}

// linkBase is a server's https address from its dashboard address (a var for tests).
var linkBase = func(host string) string { return "https://" + host }

func linkDo(req *http.Request, name string, out any) error {
	resp, err := linkClient.Do(req)
	if err != nil {
		return fmt.Errorf("couldn't reach %s: %v", name, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode != 200 {
		var e struct {
			Error string `json:"error"`
		}
		json.Unmarshal(b, &e)
		return linkError{status: resp.StatusCode, msg: fmt.Sprintf("%s said: %s", name, cmp.Or(e.Error, resp.Status))}
	}
	if out == nil {
		return nil
	}
	if raw, ok := out.(*json.RawMessage); ok {
		*raw = append((*raw)[:0], b...)
		return nil
	}
	return json.Unmarshal(b, out)
}

type linkError struct {
	status int
	msg    string
}

func (e linkError) Error() string { return e.msg }

// ---- signing and checking ----

func linkMessage(from, to, method, path, ts string, body []byte) []byte {
	sum := sha256.Sum256(body)
	return []byte(strings.Join([]string{"nook-link/1", from, to, method, path, ts, hex.EncodeToString(sum[:])}, "\n"))
}

// LinkSend sends a signed request to a linked server and returns its answer's body.
func LinkSend(id, method, path string, body []byte) (json.RawMessage, error) {
	l, ok := FindLink(id)
	if !ok {
		return nil, fmt.Errorf("this server isn't linked with %s", id)
	}
	k, err := linkKey()
	if err != nil {
		return nil, err
	}
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	sig := ed25519.Sign(k, linkMessage(serverID(), l.ID, method, path, ts, body))
	req, _ := http.NewRequest(method, linkBase(l.Name)+path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Nook-Link", serverID())
	req.Header.Set("Nook-Time", ts)
	req.Header.Set("Nook-Signature", base64.StdEncoding.EncodeToString(sig))
	var out json.RawMessage
	if err := linkDo(req, l.Name, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// LinkSigned is what a signed request carries, as the dashboard hands it to the helper to check.
type LinkSigned struct {
	From, Time, Signature, Method, Path string
	Body                                []byte
}

var (
	seenMu   sync.Mutex
	seenSigs = map[string]int64{} // signatures already taken, until they'd be too old anyway
)

// LinkCheck says whether a signed request really comes from a linked server, and returns that link.
func LinkCheck(s LinkSigned) (Link, error) {
	l, ok := FindLink(s.From)
	if !ok {
		return Link{}, errors.New("this server isn't linked with yours (any more)")
	}
	ts, err := strconv.ParseInt(s.Time, 10, 64)
	if err != nil || time.Since(time.Unix(ts, 0)).Abs() > linkSkew {
		return Link{}, errors.New("the request's time is more than 5 minutes off this server's clock")
	}
	pub, _ := base64.StdEncoding.DecodeString(l.Pub)
	sig, err := base64.StdEncoding.DecodeString(s.Signature)
	if err != nil || len(pub) != ed25519.PublicKeySize || !ed25519.Verify(pub, linkMessage(s.From, serverID(), s.Method, s.Path, s.Time, s.Body), sig) {
		return Link{}, errors.New("the request's signature doesn't check out")
	}
	seenMu.Lock()
	now := time.Now().Unix()
	for k, exp := range seenSigs {
		if exp < now {
			delete(seenSigs, k)
		}
	}
	if _, dup := seenSigs[s.Signature]; dup {
		seenMu.Unlock()
		return Link{}, errors.New("that request was already taken")
	}
	seenSigs[s.Signature] = now + int64(2*linkSkew/time.Second)
	seenMu.Unlock()
	if time.Now().Unix()-l.Seen > 60 { // not a write on every request
		l.Seen = time.Now().Unix()
		saveLink(l)
	}
	return l, nil
}

// linkRefused are ops another server may never run here: the links themselves, moves and setups, deploys.
var linkRefused = map[string]bool{"pairStep": true, "deploy": true, "undeploy": true, "tokenApp": true, "ciEnv": true,
	"serverScan": true, "serverSetup": true, "serverSetupStatus": true}

// LinkAllowed says whether a linked server may run op here.
func LinkAllowed(l Link, op string) error {
	if linkRefused[op] || strings.HasPrefix(op, "link") {
		return fmt.Errorf("%s can't be run from another server", op)
	}
	if !rpc.Ops[op] {
		return fmt.Errorf("unknown operation %q", op)
	}
	if l.Kind != LinkSame {
		return fmt.Errorf("a %s link can't run %s", l.Kind, op)
	}
	return nil
}

// LinkServe runs a linked server's helper request here, once its signature has checked out.
func LinkServe(s LinkSigned) rpc.Response {
	l, err := LinkCheck(s)
	if err != nil {
		return rpc.Response{Error: err.Error(), Status: 401}
	}
	var req rpc.Request
	if err := json.Unmarshal(s.Body, &req); err != nil {
		return rpc.Response{Error: "bad request", Status: 400}
	}
	if err := LinkAllowed(l, req.Op); err != nil {
		return rpc.Response{Error: err.Error(), Status: 403}
	}
	req.By = cmp.Or(req.By, "someone") + "@" + l.Name
	req.Secret = ""
	return Dispatch(&req)
}

// LinkRemove ends the link with id here, and tells it so (best effort).
func (h *Host) LinkRemove(id string, tell bool) error {
	l, ok := FindLink(id)
	if !ok {
		return fmt.Errorf("this server isn't linked with %s", id)
	}
	if tell {
		if _, err := LinkSend(id, "POST", "/api/v1/link/bye", []byte("{}")); err != nil {
			h.say("couldn't tell %s (%v): it keeps its side until it's removed there too, but this server refuses it", l.Name, err)
		}
	}
	linkMu.Lock()
	defer linkMu.Unlock()
	h.say("unlinked %s", l.Name)
	return writeLinks(slices.DeleteFunc(readLinks(), func(x Link) bool { return x.ID == id }))
}

// LinkInfo is this server and its links, for the dashboard.
type LinkInfo struct {
	Self  *LinkSelf `json:"self,omitempty"`
	Error string    `json:"error,omitempty"` // why this server can't link yet
	Links []Link    `json:"links"`
}

func GetLinks() LinkInfo {
	i := LinkInfo{Links: Links()}
	if i.Links == nil {
		i.Links = []Link{}
	}
	if me, err := linkSelf(); err != nil {
		i.Error = err.Error()
	} else {
		i.Self = &me
	}
	return i
}
