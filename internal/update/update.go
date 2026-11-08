// Package update checks GitHub releases for new nook binaries, verifies their ed25519 signature,
// installs them, and notifies when one is available.
package update

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/sparkstonepdx/nook/internal/config"
)

//go:embed signing.pub
var signingPub string

// Version is set by main (from -ldflags at build time).
var Version = "dev"

const stateFile = "/var/lib/nook/update.json"
const motdFile = "/var/lib/nook/update-available"

type Status struct {
	Current   string `json:"current"`
	Latest    string `json:"latest"`
	Available bool   `json:"available"`
	URL       string `json:"url"`
	Notes     string `json:"notes"`
	CheckedAt string `json:"checkedAt"`
	Notified  string `json:"notified,omitempty"`
	Error     string `json:"error,omitempty"`
}

type release struct {
	Tag     string `json:"tag_name"`
	HTMLURL string `json:"html_url"`
	Body    string `json:"body"`
	Draft   bool   `json:"draft"`
	Pre     bool   `json:"prerelease"`
	Assets  []struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
		URL  string `json:"url"`
	} `json:"assets"`
}

// apiBase can be pointed elsewhere for testing (UPDATE_API in update.env); defaults to GitHub.
func apiBase() string {
	if v := config.Read(config.UpdateEnv)["UPDATE_API"]; v != "" {
		return strings.TrimRight(v, "/")
	}
	return "https://api.github.com"
}

func settings() (repo, token, notify string) {
	m := config.Read(config.UpdateEnv)
	repo = m["UPDATE_REPO"]
	if repo == "" {
		repo = "sparkstonepdx/nook"
	}
	return repo, m["GITHUB_TOKEN"], m["UPDATE_NOTIFY_URL"]
}

var client = &http.Client{Timeout: 5 * time.Minute}

func get(url, token, accept string) ([]byte, error) {
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Accept", accept)
	req.Header.Set("User-Agent", "nook-updater")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token) // Go drops it on the redirect to the asset host
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		return nil, errors.New("not found (private repo? put a read-only GITHUB_TOKEN in " + config.UpdateEnv + ")")
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("GitHub returned %s", resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 200<<20))
}

func latest() (*release, string, error) {
	repo, token, _ := settings()
	b, err := get(apiBase()+"/repos/"+repo+"/releases/latest", token, "application/vnd.github+json")
	if err != nil {
		return nil, token, err
	}
	var r release
	return &r, token, json.Unmarshal(b, &r)
}

// Newer compares vMAJOR.MINOR.PATCH tags.
func Newer(latest, current string) bool {
	p := func(v string) [3]int {
		var out [3]int
		v = strings.TrimPrefix(strings.SplitN(v, "-", 2)[0], "v")
		for i, s := range strings.SplitN(v, ".", 3) {
			if i < 3 {
				out[i], _ = strconv.Atoi(s)
			}
		}
		return out
	}
	if current == "dev" || current == "" {
		return true
	}
	a, b := p(latest), p(current)
	for i := 0; i < 3; i++ {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	return false
}

func readState() Status {
	var s Status
	b, _ := os.ReadFile(stateFile)
	json.Unmarshal(b, &s)
	return s
}

func writeState(s Status) {
	os.MkdirAll(filepath.Dir(stateFile), 0o755)
	b, _ := json.MarshalIndent(s, "", "  ")
	config.WriteAtomic(stateFile, b, 0o644)
	if s.Available {
		config.WriteAtomic(motdFile, []byte(fmt.Sprintf("\nnook-admin %s is available (running %s). Install it with: nook-admin update\n%s\n\n", s.Latest, s.Current, s.URL)), 0o644)
	} else {
		os.Remove(motdFile)
	}
}

// CurrentStatus is the last check's result (for the dashboard), with the running version filled in.
func CurrentStatus() (any, error) {
	s := readState()
	s.Current = Version
	s.Available = s.Latest != "" && Newer(s.Latest, Version)
	return s, nil
}

// Check asks GitHub for the latest release, records it, and notifies once per new version.
func Check(out io.Writer, notify bool) (Status, error) {
	s := readState()
	s.Current, s.CheckedAt, s.Error = Version, time.Now().UTC().Format(time.RFC3339), ""
	r, _, err := latest()
	if err != nil {
		s.Error = err.Error()
		writeState(s)
		return s, err
	}
	s.Latest, s.URL, s.Notes = r.Tag, r.HTMLURL, firstLines(r.Body, 12)
	s.Available = Newer(r.Tag, Version)
	if s.Available {
		fmt.Fprintf(out, "nook %s is available (running %s): %s\n", r.Tag, Version, r.HTMLURL)
		if notify && s.Notified != r.Tag {
			if err := sendNotification(s); err != nil {
				fmt.Fprintf(out, "notification failed: %v\n", err)
			} else if _, _, url := settings(); url != "" {
				s.Notified = r.Tag
			}
		}
	} else {
		fmt.Fprintf(out, "nook %s is the latest\n", Version)
	}
	writeState(s)
	return s, nil
}

func sendNotification(s Status) error {
	_, _, url := settings()
	if url == "" {
		return nil
	}
	msg := fmt.Sprintf("nook %s is available (running %s). Update from the dashboard or run `nook-admin update`. %s", s.Latest, s.Current, s.URL)
	var body []byte
	ctype := "text/plain"
	switch {
	case strings.Contains(url, "discord.com/api/webhooks"):
		body, _ = json.Marshal(map[string]string{"content": msg})
		ctype = "application/json"
	case strings.Contains(url, "hooks.slack.com"):
		body, _ = json.Marshal(map[string]string{"text": msg})
		ctype = "application/json"
	default: // ntfy and anything that takes a plain body
		body = []byte(msg)
	}
	req, _ := http.NewRequest("POST", url, bytes.NewReader(body))
	req.Header.Set("Content-Type", ctype)
	req.Header.Set("Title", "nook-admin update")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned %s", resp.Status)
	}
	return nil
}

func firstLines(s string, n int) string {
	l := strings.Split(strings.TrimSpace(s), "\n")
	if len(l) > n {
		l = l[:n]
	}
	return strings.Join(l, "\n")
}

// VerifyChecksums checks sig (base64 ed25519) over sums with the built-in public key.
func VerifyChecksums(sums, sig []byte) error {
	pubText := strings.TrimSpace(signingPub)
	if pubText == "" {
		return errors.New("this build has no release signing key, so it can't verify updates")
	}
	pub, err := base64.StdEncoding.DecodeString(pubText)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return errors.New("built-in signing key is malformed")
	}
	s, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sig)))
	if err != nil || !ed25519.Verify(pub, sums, s) {
		return errors.New("release signature doesn't match: refusing to install")
	}
	return nil
}

func sha256For(sums []byte, name string) (string, bool) {
	sc := bufio.NewScanner(bytes.NewReader(sums))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == name {
			return f[0], true
		}
	}
	return "", false
}

// Apply downloads, verifies and installs the latest release, then restarts the dashboard services.
func Apply(out io.Writer, force bool) error {
	r, token, err := latest()
	if err != nil {
		return err
	}
	if !force && !Newer(r.Tag, Version) {
		fmt.Fprintf(out, "already on the latest (%s)\n", Version)
		return nil
	}
	want := fmt.Sprintf("nook_%s_linux_%s.tar.gz", r.Tag, runtime.GOARCH)
	assets := map[string]string{}
	for _, a := range r.Assets {
		assets[a.Name] = a.URL
	}
	for _, n := range []string{want, "checksums.txt", "checksums.txt.sig"} {
		if assets[n] == "" {
			return fmt.Errorf("release %s has no %s", r.Tag, n)
		}
	}
	dl := func(n string) ([]byte, error) { return get(assets[n], token, "application/octet-stream") }
	sums, err := dl("checksums.txt")
	if err != nil {
		return err
	}
	sig, err := dl("checksums.txt.sig")
	if err != nil {
		return err
	}
	if err := VerifyChecksums(sums, sig); err != nil {
		return err
	}
	tgz, err := dl(want)
	if err != nil {
		return err
	}
	expect, ok := sha256For(sums, want)
	got := sha256.Sum256(tgz)
	if !ok || hex.EncodeToString(got[:]) != expect {
		return errors.New("download doesn't match its signed checksum: refusing to install")
	}
	fmt.Fprintf(out, "verified %s (signature and checksum)\n", want)
	bins, err := untar(tgz, "nook-admin", "nook")
	if err != nil {
		return err
	}
	tmp, err := os.MkdirTemp("/usr/local/sbin", ".nook-update-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	newHost := filepath.Join(tmp, "nook-admin")
	os.WriteFile(newHost, bins["nook-admin"], 0o700)
	if v, err := exec.Command(newHost, "version").Output(); err != nil || strings.TrimSpace(string(v)) != r.Tag {
		return fmt.Errorf("the new binary doesn't run or reports the wrong version (%q)", strings.TrimSpace(string(v)))
	}
	if err := replace(config.HostBin, bins["nook-admin"], 0o750); err != nil {
		return err
	}
	if err := replace(config.AppBin, bins["nook"], 0o755); err != nil {
		return err
	}
	fmt.Fprintf(out, "installed %s\n", r.Tag)
	// let the new version bring units and config up to date, then restart the dashboard
	if o, err := exec.Command(config.HostBin, "install", "--upgrade").CombinedOutput(); err != nil {
		fmt.Fprintf(out, "post-update install step failed: %v\n%s\n", err, o)
	}
	writeState(Status{Current: r.Tag, Latest: r.Tag, URL: r.HTMLURL, CheckedAt: time.Now().UTC().Format(time.RFC3339)})
	exec.Command("systemctl", "--no-block", "restart", "nook-helper.service", "nook-web.service").Run()
	fmt.Fprintln(out, "restarting the dashboard; running sites are not affected")
	return nil
}

func replace(path string, data []byte, mode os.FileMode) error {
	tmp := path + ".new"
	os.Remove(tmp)
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	os.Chmod(tmp, mode)
	if st, err := os.Stat(path); err == nil { // keep the group (root:nook-web for nook)
		if sys, ok := st.Sys().(interface{ Gid() uint32 }); ok {
			os.Chown(tmp, 0, int(sys.Gid()))
		} else if fi, ok := st.Sys().(*syscallStat); ok {
			os.Chown(tmp, 0, int(fi.Gid))
		}
	}
	return os.Rename(tmp, path)
}

func untar(tgz []byte, want ...string) (map[string][]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(tgz))
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(zr)
	out := map[string][]byte{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		for _, w := range want {
			if filepath.Base(h.Name) == w && h.Typeflag == tar.TypeReg {
				b, err := io.ReadAll(io.LimitReader(tr, 100<<20))
				if err != nil {
					return nil, err
				}
				out[w] = b
			}
		}
	}
	for _, w := range want {
		if out[w] == nil {
			return nil, fmt.Errorf("release archive has no %s", w)
		}
	}
	return out, nil
}

type syscallStat = syscall.Stat_t
