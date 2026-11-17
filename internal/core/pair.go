package core

// Moving to a new server (#68 part 3). The old server makes a one-time pairing code; the new one, freshly
// installed, takes it and does the rest:
//
//	1. hello  the old server hands over its backup settings (same bucket, same password) and its id
//	2. stop   the old server stops its apps (so nothing changes after the last snapshot) and backs up once more
//	3. the new server restores that backup (restore-server, taking over the old id) and starts everything
//	4. you point DNS at the new address; the old server stays as it is, apps stopped, until you retire it
//	   (`nook-admin move resume` on it starts its apps again if you call the move off)
//
// The code is the old dashboard's address and a secret, good for 24 hours. The new server talks to the old one
// over HTTPS at /api/v1/pair/STEP with the secret as its bearer token; the old server keeps only its hash.
// Downtime is the final backup plus the restore, then DNS.
//
// The same pairing, kept, is where adopting fits later: two servers both serving, an app moved between them by
// restoring its snapshot from the other server's id (the per-app restore already takes --from).

import (
	"cmp"
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
	"strings"
	"time"

	"github.com/sparkstonepdx/nook/internal/config"
)

var (
	pairFile = "/etc/nook/pair.json"
	pairRE   = regexp.MustCompile(`^nookpair_([A-Za-z0-9.-]+(?::[0-9]{1,5})?)/([A-Za-z0-9_-]{43})$`)
)

const pairTTL = 24 * time.Hour

type pairState struct {
	Hash    string `json:"hash"`
	Expires int64  `json:"expires"`
	Stopped int64  `json:"stopped,omitempty"` // when a move stopped this server's apps
}

func readPair() pairState {
	var p pairState
	if b, err := os.ReadFile(pairFile); err == nil {
		json.Unmarshal(b, &p)
	}
	return p
}

func writePair(p pairState) error {
	b, _ := json.MarshalIndent(p, "", "  ")
	return config.WriteAtomic(pairFile, b, 0o600)
}

func pairHash(secret string) string {
	h := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(h[:])
}

// PairCode makes this server's one-time pairing code (replacing any earlier one).
func (h *Host) PairCode() (string, error) {
	d := config.LoadDashboard()
	if d.Host == "" {
		return "", errors.New("set the dashboard's address first (Domains page): the new server reaches this one there")
	}
	b := make([]byte, 32)
	rand.Read(b)
	secret := base64.RawURLEncoding.EncodeToString(b)
	p := readPair()
	p.Hash, p.Expires = pairHash(secret), time.Now().Add(pairTTL).Unix()
	if err := writePair(p); err != nil {
		return "", err
	}
	return "nookpair_" + d.Host + "/" + secret, nil
}

// pairCheck says whether secret is this server's current pairing code.
func pairCheck(secret string) error {
	p := readPair()
	if p.Hash == "" || time.Now().Unix() > p.Expires {
		return errors.New("no pairing code is waiting: make one on the old server with nook-admin move code")
	}
	if subtle.ConstantTimeCompare([]byte(pairHash(secret)), []byte(p.Hash)) != 1 {
		return errors.New("that pairing code isn't this server's")
	}
	return nil
}

// PairHello is what the old server tells the new one: its id and its backup settings, secrets included.
type PairHello struct {
	ServerID string `json:"serverId"`
	Repo     string `json:"repo"`
	KeyID    string `json:"keyId"`
	Secret   string `json:"secret"`
	Password string `json:"password"`
	Apps     int    `json:"apps"`
}

// PairStep runs one step of a move on the old server, for a caller holding the pairing code.
func (h *Host) PairStep(secret, step string) (any, error) {
	if err := pairCheck(secret); err != nil {
		return nil, err
	}
	c := LoadBackupConfig()
	switch step {
	case "hello":
		if !c.Configured() {
			return nil, errors.New("this server has no backups set up: set them up first (Backups page), since moving goes through them")
		}
		return PairHello{ServerID: c.ServerID, Repo: c.Repo, KeyID: c.KeyID, Secret: c.secret, Password: c.password, Apps: len(Apps())}, nil
	case "stop":
		h.say("moving: stopping every app, then a last backup")
		h.stopAllApps()
		p := readPair()
		p.Stopped = time.Now().Unix()
		writePair(p)
		if err := h.BackupRun("moving to another server"); err != nil {
			h.startAllApps() // nothing moved: carry on serving
			return nil, fmt.Errorf("the last backup failed, so the apps are running again: %v", err)
		}
		return map[string]any{"serverId": c.ServerID, "stopped": p.Stopped}, nil
	}
	return nil, fmt.Errorf("unknown step %q", step)
}

// stopAllApps stops every app's sites, cron timers and deploy watchers, leaving them as they are on disk.
func (h *Host) stopAllApps() {
	for _, a := range Apps() {
		systemctl("stop", "nook-sync@"+a+".path")
		for _, s := range RunningSites(a) {
			systemctl("stop", UnitName(a, s))
		}
		if timers, _ := filepath.Glob("/etc/systemd/system/" + CronUnit(a, "*") + ".timer"); timers != nil {
			for _, t := range timers {
				systemctl("stop", strings.TrimPrefix(t, "/etc/systemd/system/"))
			}
		}
	}
}

// startAllApps undoes stopAllApps.
func (h *Host) startAllApps() {
	for _, a := range Apps() {
		systemctl("start", "nook-sync@"+a+".path")
		for _, s := range RunningSites(a) {
			systemctl("start", UnitName(a, s))
		}
		if timers, _ := filepath.Glob("/etc/systemd/system/" + CronUnit(a, "*") + ".timer"); timers != nil {
			for _, t := range timers {
				systemctl("start", strings.TrimPrefix(t, "/etc/systemd/system/"))
			}
		}
	}
}

// MoveResume starts this server's apps again after a move it was the old side of is called off.
func (h *Host) MoveResume() error {
	h.startAllApps()
	p := readPair()
	p.Stopped, p.Hash = 0, "" // the code is spent either way
	writePair(p)
	h.say("this server's apps are running again; make a new code to try the move again")
	return nil
}

// ---- the new server's side ----

// pairClient talks to the old server (a var for tests).
var pairClient = &http.Client{Timeout: 2 * time.Hour}

// pairBase is how the new server reaches the old one: https and its dashboard address (a var for tests).
var pairBase = func(host string) string { return "https://" + host }

func pairCall(host, secret, step string, out any) error {
	req, _ := http.NewRequest("POST", pairBase(host)+"/api/v1/pair/"+step, nil)
	req.Header.Set("Authorization", "Bearer "+secret)
	resp, err := pairClient.Do(req)
	if err != nil {
		return fmt.Errorf("couldn't reach the old server at %s: %v", host, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != 200 {
		var e struct {
			Error string `json:"error"`
		}
		json.Unmarshal(b, &e)
		return fmt.Errorf("the old server (%s) said: %s", step, cmp.Or(e.Error, resp.Status))
	}
	return json.Unmarshal(b, out)
}

// moveApps is this server's apps (a var for tests).
var moveApps = Apps

// moveRestore is RestoreServer (a var for tests).
var moveRestore = func(h *Host, r ServerRestore) error { return h.RestoreServer(r) }

// MoveFrom moves the server behind code onto this one, which should be a fresh install.
func (h *Host) MoveFrom(code string) error {
	m := pairRE.FindStringSubmatch(strings.TrimSpace(code))
	if m == nil {
		return errors.New("a pairing code looks like nookpair_admin.example.com/…: make one on the old server with nook-admin move code")
	}
	host, secret := m[1], m[2]
	if apps := moveApps(); len(apps) > 0 {
		return fmt.Errorf("this server already has apps (%s): move onto a fresh install", strings.Join(apps, ", "))
	}
	var hello PairHello
	h.say("asking %s for its backup settings", host)
	if err := pairCall(host, secret, "hello", &hello); err != nil {
		return err
	}
	h.say("%s (%d apps) backs up to %s", hello.ServerID, hello.Apps, hello.Repo)
	if _, err := h.SetBackup(BackupSetup{Repo: hello.Repo, KeyID: hello.KeyID, Secret: hello.Secret, Password: hello.Password}); err != nil {
		return fmt.Errorf("connecting to its backups: %v", err)
	}
	h.say("stopping its apps and taking a last backup (its sites are down from here until DNS points here)")
	var stopped map[string]any
	if err := pairCall(host, secret, "stop", &stopped); err != nil {
		return err
	}
	if err := moveRestore(h, ServerRestore{From: hello.ServerID}); err != nil {
		return fmt.Errorf("%v (the old server's apps are stopped: run nook-admin move resume there to bring them back)", err)
	}
	ip := config.Load().SSHHost
	h.say("moved. Point DNS for every domain the old server served (the Domains page lists them) at %s; the old server keeps its apps stopped until you retire it.", cmp.Or(ip, "this server's address"))
	return nil
}
