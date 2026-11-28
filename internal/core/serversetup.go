package core

// Setting up another server from the dashboard (#111, step A). You give a fresh Debian VPS's address and an SSH
// login (a password or a private key); nook installs itself there and, if you asked, moves this server onto it.
//
//	1. scan     ssh-keyscan reads the server's host keys; the dashboard shows their fingerprints to check
//	            against the VPS provider's console. Setup only goes on with the fingerprint you confirmed.
//	2. connect  the login you gave is used once, to put this server's own key (/etc/nook/ssh/id_ed25519) in
//	            the server's authorized_keys. The password or key is never written anywhere.
//	3. check    Debian, root or passwordless sudo
//	4. install  setup-server.sh from GitHub, as in the README
//	5. move     (when asked) this server makes a pairing code and the new one runs `nook-admin move from`
//	            with it: the same move as nook-admin move, driven from here
//	6. finish   this server's key comes out of the new server's authorized_keys again
//
// One setup runs at a time; its progress is /var/lib/nook/server-setup.json, which the dashboard polls.

import (
	"bufio"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sparkstonepdx/nook/internal/config"
)

const (
	SetupScan    = "scan"
	SetupConnect = "connect"
	SetupCheck   = "check"
	SetupInstall = "install"
	SetupMove    = "move"
	SetupFinish  = "finish"
)

// setupScript is what the new server runs to install nook (a var for tests).
var setupScript = "curl -fsSL https://raw.githubusercontent.com/sparkstonepdx/nook/main/setup-server.sh | sh"

var (
	setupFile   = func() string { return filepath.Join(config.LibDir, "server-setup.json") }
	sshDir      = "/etc/nook/ssh"
	setupHostRE = regexp.MustCompile(`^(?:[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?\.)*[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`)
	setupUserRE = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)
	fingerRE    = regexp.MustCompile(`^SHA256:[A-Za-z0-9+/]{43}$`)
)

// SetupTarget is the server being set up.
type SetupTarget struct {
	Host string `json:"host"`
	Port int    `json:"port"`
	User string `json:"user"`
}

func (t SetupTarget) check() (SetupTarget, error) {
	t.Host = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(t.Host), "["), "]"))
	t.Port = cmp.Or(t.Port, 22)
	t.User = cmp.Or(strings.TrimSpace(t.User), "root")
	if net.ParseIP(t.Host) == nil && !setupHostRE.MatchString(t.Host) {
		return t, fmt.Errorf("%q isn't a server address: give its IP or hostname", t.Host)
	}
	if t.Port < 1 || t.Port > 65535 {
		return t, errors.New("the SSH port is a number from 1 to 65535")
	}
	if !setupUserRE.MatchString(t.User) {
		return t, fmt.Errorf("%q isn't a Linux user name", t.User)
	}
	return t, nil
}

// HostKey is one of the server's SSH host keys.
type HostKey struct {
	Type        string `json:"type"`        // ED25519, ECDSA, RSA
	Fingerprint string `json:"fingerprint"` // SHA256:…
	line        string // the known_hosts line
}

// SetupJob is a setup's progress.
type SetupJob struct {
	SetupTarget
	Move        bool      `json:"move"`
	Fingerprint string    `json:"fingerprint"`
	Step        string    `json:"step"`
	Log         []string  `json:"log"`
	Started     time.Time `json:"started"`
	Updated     time.Time `json:"updated"`
	Done        bool      `json:"done,omitempty"`
	Error       string    `json:"error,omitempty"`
	Invite      string    `json:"invite,omitempty"` // the new server's first admin login, when it wasn't a move
}

const setupLogLines = 400

var (
	setupMu    sync.Mutex // one setup at a time
	setupJobMu sync.Mutex // the running job's fields
)

// ServerSetupStatus is the last setup, or nil. One that stopped writing 15 minutes ago died with its process.
func ServerSetupStatus() *SetupJob {
	b, err := os.ReadFile(setupFile())
	if err != nil {
		return nil
	}
	var j SetupJob
	if json.Unmarshal(b, &j) != nil {
		return nil
	}
	if !j.Done && time.Since(j.Updated) > 15*time.Minute {
		j.Done, j.Error = true, "the setup stopped (nook restarted while it ran): start it again"
	}
	return &j
}

// ScanHostKeys reads the server's host keys and their fingerprints, to check before setting it up.
func ScanHostKeys(t SetupTarget) ([]HostKey, error) {
	t, err := t.check()
	if err != nil {
		return nil, err
	}
	if err := needSSH(); err != nil {
		return nil, err
	}
	out, err := sshKeyscan(t.Host, t.Port)
	if err != nil {
		return nil, fmt.Errorf("couldn't read %s's SSH host keys on port %d (is it up, and is that the SSH port?): %v", t.Host, t.Port, strings.TrimSpace(out))
	}
	var lines []string
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "#") {
			lines = append(lines, l)
		}
	}
	if len(lines) == 0 {
		return nil, fmt.Errorf("couldn't reach SSH on %s port %d: is the server up, and is that its SSH port?", t.Host, t.Port)
	}
	var keys []HostKey
	for _, l := range lines {
		f, err := RunInput(l+"\n", "ssh-keygen", "-lf", "-")
		if err != nil {
			continue
		}
		// "256 SHA256:… host (ED25519)"
		w := strings.Fields(f)
		if len(w) < 4 || !fingerRE.MatchString(w[1]) {
			continue
		}
		keys = append(keys, HostKey{Type: strings.Trim(w[len(w)-1], "()"), Fingerprint: w[1], line: l})
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("%s answered on port %d but gave no SSH host keys", t.Host, t.Port)
	}
	return keys, nil
}

// SetupLogin is how to log in the first time: a password or a private key (without a passphrase).
type SetupLogin struct {
	Password string
	Key      string
}

// SetupServer checks what it can, then sets the server up in the background (follow it in ServerSetupStatus).
func (h *Host) SetupServer(t SetupTarget, fingerprint string, login SetupLogin, move bool) error {
	t, err := t.check()
	if err != nil {
		return err
	}
	if !fingerRE.MatchString(strings.TrimSpace(fingerprint)) {
		return errors.New("check the server's fingerprint first")
	}
	if login.Password == "" && strings.TrimSpace(login.Key) == "" {
		return errors.New("give the server's root password or a private key that logs in")
	}
	if move {
		if !LoadBackupConfig().Configured() {
			return errors.New("set up backups first (Backups page): moving goes through them")
		}
		if config.LoadDashboard().Host == "" {
			return errors.New("set the dashboard's address first (Domains page): the new server reaches this one there")
		}
	}
	if !setupMu.TryLock() {
		return errors.New("a server is already being set up")
	}
	if j := ServerSetupStatus(); j != nil && !j.Done {
		setupMu.Unlock()
		return errors.New("a server is already being set up")
	}
	now := time.Now().UTC()
	j := &SetupJob{SetupTarget: t, Move: move, Fingerprint: strings.TrimSpace(fingerprint), Step: SetupScan, Started: now, Updated: now, Log: []string{}}
	saveSetup(j)
	go func() {
		defer setupMu.Unlock()
		// a restore can go quiet for a long while: keep the job fresh so it doesn't read as dead
		alive := time.NewTicker(time.Minute)
		stop := make(chan struct{})
		go func() {
			for {
				select {
				case <-alive.C:
					saveSetup(j)
				case <-stop:
					return
				}
			}
		}()
		err := h.runSetup(j, login)
		alive.Stop()
		close(stop)
		if err != nil {
			j.line("stopped: " + err.Error())
		}
		setupJobMu.Lock()
		j.Done = true
		if err != nil {
			j.Error = err.Error()
		}
		setupJobMu.Unlock()
		saveSetup(j)
	}()
	return nil
}

func saveSetup(j *SetupJob) {
	setupJobMu.Lock()
	j.Updated = time.Now().UTC()
	b, _ := json.Marshal(j)
	setupJobMu.Unlock()
	os.MkdirAll(filepath.Dir(setupFile()), 0o755)
	config.WriteAtomic(setupFile(), b, 0o600)
}

func (j *SetupJob) step(s string) {
	setupJobMu.Lock()
	j.Step = s
	setupJobMu.Unlock()
	saveSetup(j)
}

func (j *SetupJob) line(s string) {
	setupJobMu.Lock()
	defer setupJobMu.Unlock()
	j.Log = append(j.Log, s)
	if len(j.Log) > setupLogLines {
		j.Log = j.Log[len(j.Log)-setupLogLines:]
	}
}

// lines is a writer that adds what's written to the job's log a line at a time.
type lines struct {
	j    *SetupJob
	part string
	last time.Time
}

func (w *lines) Write(b []byte) (int, error) {
	s := w.part + strings.ReplaceAll(string(b), "\r", "\n")
	parts := strings.Split(s, "\n")
	w.part = parts[len(parts)-1]
	for _, l := range parts[:len(parts)-1] {
		if strings.TrimSpace(l) != "" {
			w.j.line(l)
		}
	}
	if time.Since(w.last) > 500*time.Millisecond {
		w.last = time.Now()
		saveSetup(w.j)
	}
	return len(b), nil
}

func (w *lines) flush() {
	if strings.TrimSpace(w.part) != "" {
		w.j.line(w.part)
	}
	w.part = ""
	saveSetup(w.j)
}

func (h *Host) runSetup(j *SetupJob, login SetupLogin) error {
	t := j.SetupTarget
	work, err := os.MkdirTemp("", "nook-setup-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)

	// 1. the host key you checked, and only that server's keys, in a known_hosts of its own
	j.line(fmt.Sprintf("reading %s's host keys", t.Host))
	keys, err := ScanHostKeys(t)
	if err != nil {
		return err
	}
	var known []string
	for _, k := range keys {
		if k.Fingerprint == j.Fingerprint {
			known = append(known, k.line)
		}
	}
	if len(known) == 0 {
		return fmt.Errorf("%s's host key isn't the one you checked (%s) any more: check it again before going on", t.Host, j.Fingerprint)
	}
	kh := filepath.Join(work, "known_hosts")
	if err := os.WriteFile(kh, []byte(strings.Join(known, "\n")+"\n"), 0o600); err != nil {
		return err
	}

	// 2. this server's key in, with the login you gave
	j.step(SetupConnect)
	pub, err := ownSSHKey()
	if err != nil {
		return err
	}
	j.line(fmt.Sprintf("logging in as %s with the %s you gave, once, to add this server's key", t.User, map[bool]string{true: "password", false: "key"}[login.Password != ""]))
	first, err := firstLogin(work, login)
	if err != nil {
		return err
	}
	q := shellQuote(pub)
	add := "umask 077; mkdir -p ~/.ssh && touch ~/.ssh/authorized_keys && (grep -qxF " + q + " ~/.ssh/authorized_keys || printf '%s\\n' " + q + " >> ~/.ssh/authorized_keys)"
	if out, err := sshRun(t, kh, first, add, nil); err != nil {
		return fmt.Errorf("couldn't log in as %s: %s", t.User, sshError(out, err))
	}
	key := sshAuth{key: filepath.Join(sshDir, "id_ed25519")}
	defer func() {
		// 6. and out again, whatever happened, unless it's this server's key that can't log in
		j.step(SetupFinish)
		del := "f=~/.ssh/authorized_keys; grep -vxF " + q + " \"$f\" > \"$f.nook\"; cat \"$f.nook\" > \"$f\"; rm -f \"$f.nook\""
		if out, err := sshRun(t, kh, key, del, nil); err != nil {
			j.line("couldn't take this server's key back out of " + t.User + "'s authorized_keys there (" + sshError(out, err) + "): remove the line ending in " + strings.Fields(pub)[len(strings.Fields(pub))-1])
		} else {
			j.line("took this server's key back out of the new server")
		}
		saveSetup(j)
	}()

	// 3. what it is
	j.step(SetupCheck)
	out, err := sshRun(t, kh, key, `. /etc/os-release 2>/dev/null; echo "os=$ID $VERSION_ID"; echo "uid=$(id -u)"; sudo -n true 2>/dev/null && echo sudo=yes; command -v nook-admin >/dev/null && echo nook=yes; true`, nil)
	if err != nil {
		return fmt.Errorf("logged in once, but not with this server's key: %s", sshError(out, err))
	}
	facts := map[string]string{}
	for _, l := range strings.Split(out, "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(l), "="); ok {
			facts[k] = v
		}
	}
	if !strings.HasPrefix(facts["os"], "debian ") {
		return fmt.Errorf("%s runs %s: nook needs Debian (12 or newer)", t.Host, cmp.Or(strings.TrimSpace(facts["os"]), "an unknown system"))
	}
	sudo := ""
	if facts["uid"] != "0" {
		if facts["sudo"] != "yes" {
			return fmt.Errorf("%s isn't root there and can't sudo without a password: log in as root, or give %s passwordless sudo", t.User, t.User)
		}
		sudo = "sudo -n "
	}
	j.line(fmt.Sprintf("%s, %s", facts["os"], map[bool]string{true: "logged in as root", false: "with sudo"}[sudo == ""]))

	// 4. nook
	j.step(SetupInstall)
	if facts["nook"] == "yes" {
		j.line("nook is already there: running the setup again (it's safe to re-run)")
	}
	w := &lines{j: j}
	out, err = sshRun(t, kh, key, sudo+"sh -c "+shellQuote(setupScript), w)
	w.flush()
	if err != nil {
		return fmt.Errorf("the install failed: %s", sshError(out, err))
	}

	// 5. the move, or the first login
	if j.Move {
		j.step(SetupMove)
		code, err := h.PairCode()
		if err != nil {
			return err
		}
		w := &lines{j: j}
		out, err := sshRun(t, kh, key, sudo+"nook-admin move from "+shellQuote(code), w)
		w.flush()
		if err != nil {
			return fmt.Errorf("the move failed: %s", sshError(out, err))
		}
		return nil
	}
	out, err = sshRun(t, kh, key, sudo+"nook-admin invite", nil)
	if err != nil {
		return fmt.Errorf("nook is installed, but making its first login failed: %s", sshError(out, err))
	}
	for _, f := range strings.Fields(out) {
		if strings.HasPrefix(f, "https://") && strings.Contains(f, "/invite?t=") {
			setupJobMu.Lock()
			j.Invite = f
			setupJobMu.Unlock()
		}
	}
	if j.Invite == "" {
		return fmt.Errorf("nook is installed, but its first login link didn't come back: run nook-admin invite there")
	}
	j.line("nook is installed: open the link to make its admin login")
	return nil
}

// ---- ssh ----

type sshAuth struct {
	key      string // a private key file
	password string
	askpass  string // the script that hands ssh the password
}

// needSSH installs the OpenSSH client if the server doesn't have it.
func needSSH() error {
	if _, err := exec.LookPath("ssh"); err == nil {
		if _, err := exec.LookPath("ssh-keyscan"); err == nil {
			return nil
		}
	}
	if out, err := Run("apt-get", "install", "-y", "-qq", "openssh-client"); err != nil {
		return fmt.Errorf("installing the SSH client: %s", strings.TrimSpace(out))
	}
	return nil
}

// sshKeyscan is ssh-keyscan (a var for tests).
var sshKeyscan = func(host string, port int) (string, error) {
	return Run("ssh-keyscan", "-T", "10", "-p", strconv.Itoa(port), "-t", "ed25519,ecdsa,rsa", host)
}

// ownSSHKey makes this server's SSH key if it has none and returns its public half.
func ownSSHKey() (string, error) {
	priv := filepath.Join(sshDir, "id_ed25519")
	if _, err := os.Stat(priv); err != nil {
		os.MkdirAll(sshDir, 0o700)
		id := cmp.Or(LoadBackupConfig().ServerID, config.Load().SSHHost, "server")
		if out, err := Run("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", "nook@"+id, "-f", priv); err != nil {
			return "", fmt.Errorf("making this server's SSH key: %s", strings.TrimSpace(out))
		}
	}
	b, err := os.ReadFile(priv + ".pub")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

// firstLogin readies the login you gave: an askpass script for a password, or the key in a 0600 file.
func firstLogin(work string, l SetupLogin) (sshAuth, error) {
	if l.Password != "" {
		p := filepath.Join(work, "askpass")
		if err := os.WriteFile(p, []byte("#!/bin/sh\nprintf '%s\\n' \"$NOOK_SSH_PASSWORD\"\n"), 0o700); err != nil {
			return sshAuth{}, err
		}
		return sshAuth{password: l.Password, askpass: p}, nil
	}
	k := strings.TrimSpace(strings.ReplaceAll(l.Key, "\r\n", "\n")) + "\n"
	p := filepath.Join(work, "key")
	if err := os.WriteFile(p, []byte(k), 0o600); err != nil {
		return sshAuth{}, err
	}
	if out, err := RunInput("", "ssh-keygen", "-y", "-P", "", "-f", p); err != nil {
		if strings.Contains(out, "passphrase") || strings.Contains(out, "incorrect") {
			return sshAuth{}, errors.New("that key has a passphrase: give one without, or the password")
		}
		return sshAuth{}, errors.New("that isn't a private key ssh can read (paste the whole file, from -----BEGIN to -----END)")
	}
	return sshAuth{key: p}, nil
}

// sshRun runs cmd on the server and returns its output; with live set, the output also goes there as it comes.
// A var for tests.
var sshRun = func(t SetupTarget, knownHosts string, a sshAuth, cmd string, live io.Writer) (string, error) {
	args := []string{"-p", strconv.Itoa(t.Port),
		"-o", "UserKnownHostsFile=" + knownHosts, "-o", "GlobalKnownHostsFile=/dev/null", "-o", "StrictHostKeyChecking=yes",
		"-o", "ConnectTimeout=15", "-o", "ServerAliveInterval=30", "-o", "ServerAliveCountMax=6", "-o", "LogLevel=ERROR",
		"-o", "ControlMaster=no", "-F", "/dev/null"}
	env := append(os.Environ(), "LC_ALL=C")
	if a.password != "" {
		args = append(args, "-o", "PubkeyAuthentication=no", "-o", "PreferredAuthentications=password,keyboard-interactive", "-o", "NumberOfPasswordPrompts=1")
		env = append(env, "SSH_ASKPASS="+a.askpass, "SSH_ASKPASS_REQUIRE=force", "DISPLAY=none", "NOOK_SSH_PASSWORD="+a.password)
	} else {
		args = append(args, "-o", "BatchMode=yes", "-o", "IdentitiesOnly=yes", "-o", "PasswordAuthentication=no", "-i", a.key)
	}
	args = append(args, t.User+"@"+t.Host, cmd)
	c := exec.Command("ssh", args...)
	c.Env = env
	c.Stdin = nil
	var buf strings.Builder
	var out io.Writer = &buf
	if live != nil {
		out = io.MultiWriter(&buf, live)
	}
	c.Stdout, c.Stderr = out, out
	err := c.Run()
	return buf.String(), err
}

// sshError is the useful end of a failed ssh's output.
func sshError(out string, err error) string {
	sc := bufio.NewScanner(strings.NewReader(out))
	var last []string
	for sc.Scan() {
		if l := strings.TrimSpace(sc.Text()); l != "" {
			last = append(last, l)
		}
	}
	if len(last) > 3 {
		last = last[len(last)-3:]
	}
	msg := strings.Join(last, " / ")
	switch {
	case strings.Contains(msg, "Permission denied"):
		return "the server refused that login (wrong password or key, or that user can't log in over SSH)"
	case strings.Contains(msg, "Host key verification failed"), strings.Contains(msg, "REMOTE HOST IDENTIFICATION HAS CHANGED"):
		return "the server's host key doesn't match the one you checked"
	}
	return cmp.Or(msg, err.Error())
}

// shellQuote quotes s for sh.
func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
