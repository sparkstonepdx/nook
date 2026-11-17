package core

// System updates: the Debian packages under nook, from the dashboard. nook checks daily (with its own update
// check) and on demand, lists what apt would upgrade and which of those are security fixes, and says when the
// server needs a restart (a newer kernel than the running one, or a package asked for it). Updating upgrades
// everything except the packages of store apps from apt (Postgres): those follow their app's own update policy,
// which restarts and checks the app. With automatic security updates on, the daily check installs security fixes
// by itself. Restarting the server is its own button.

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/sparkstonepdx/nook/internal/config"
	"github.com/sparkstonepdx/nook/internal/update"
)

type SystemPackage struct {
	Name     string `json:"name"`
	From     string `json:"from"`
	To       string `json:"to"`
	Security bool   `json:"security"`
	App      string `json:"app,omitempty"` // a store app's package: updated with that app, not here
}

type SystemStatus struct {
	OS            string          `json:"os"`
	Kernel        string          `json:"kernel"`
	Packages      []SystemPackage `json:"packages"`
	Security      int             `json:"security"`
	Restart       bool            `json:"restart"`
	RestartWhy    string          `json:"restartWhy,omitempty"`
	Auto          bool            `json:"auto"` // install security updates by themselves
	CheckedAt     string          `json:"checkedAt,omitempty"`
	UpdatedAt     string          `json:"updatedAt,omitempty"`
	Error         string          `json:"error,omitempty"`
	UpSince       string          `json:"upSince,omitempty"`
	HeldPackages  []string        `json:"held,omitempty"` // apt-mark holds (nook's own Caddy build)
	Stale         []string        `json:"stale"`          // running units still on libraries an update replaced
	LastUpdateOut string          `json:"-"`
}

var (
	systemFile = filepath.Join(config.LibDir, "system.json")
	systemAuto = filepath.Join(config.StateDir, "system-auto")
	// "Inst libssl3 [3.0.11-1] (3.0.13-1~deb12u1 Debian-Security:12/stable-security [amd64])"
	instRE = regexp.MustCompile(`^Inst (\S+) (?:\[([^\]]*)\] )?\((\S+) ([^\[]*)`)
	// systemRun runs apt and friends (a var for tests)
	systemRun = func(env []string, name string, args ...string) (string, error) { return aptRun(env, name, args...) }
)

// ReadSystemStatus is the last check, with the live restart state and the auto switch.
func ReadSystemStatus() SystemStatus {
	var s SystemStatus
	if b, err := os.ReadFile(systemFile); err == nil {
		json.Unmarshal(b, &s)
	}
	if s.Packages == nil {
		s.Packages = []SystemPackage{}
	}
	s.Auto = fileExists(systemAuto)
	s.Restart, s.RestartWhy = restartNeeded()
	s.Stale = staleLibUnits()
	if s.Stale == nil {
		s.Stale = []string{}
	}
	s.OS = config.Read("/etc/os-release")["PRETTY_NAME"]
	s.Kernel = strings.TrimSpace(readFirst("/proc/sys/kernel/osrelease"))
	if up := uptime(); up > 0 {
		s.UpSince = time.Now().Add(-up).UTC().Format(time.RFC3339)
	}
	return s
}

func readFirst(p string) string {
	b, _ := os.ReadFile(p)
	return string(b)
}

func uptime() time.Duration {
	var secs float64
	if _, err := fmt.Sscanf(readFirst("/proc/uptime"), "%f", &secs); err != nil {
		return 0
	}
	return time.Duration(secs * float64(time.Second))
}

// restartNeeded: a package asked for it (/run/reboot-required), or a newer kernel is installed than the one running.
func restartNeeded() (bool, string) {
	if b, err := os.ReadFile("/run/reboot-required.pkgs"); err == nil {
		return true, "updated " + strings.Join(strings.Fields(string(b)), ", ")
	}
	if fileExists("/run/reboot-required") {
		return true, "an update asked for it"
	}
	running := strings.TrimSpace(readFirst("/proc/sys/kernel/osrelease"))
	if newest := newestKernel("/boot"); running != "" && newest != "" && newest != running {
		return true, "kernel " + newest + " is installed; " + running + " is running"
	}
	return false, ""
}

// newestKernel is the highest-versioned /boot/vmlinuz-* ("" if there are none).
func newestKernel(dir string) string {
	m, _ := filepath.Glob(filepath.Join(dir, "vmlinuz-*"))
	var vs []string
	for _, f := range m {
		vs = append(vs, strings.TrimPrefix(filepath.Base(f), "vmlinuz-"))
	}
	if len(vs) == 0 {
		return ""
	}
	sort.Slice(vs, func(i, j int) bool { return debVersionLess(vs[i], vs[j]) })
	return vs[len(vs)-1]
}

// debVersionLess compares kernel release strings by their numbers in order (6.1.0-27 < 6.1.0-28 < 6.12.0-1).
func debVersionLess(a, b string) bool {
	na, nb := numParts(a), numParts(b)
	for i := 0; i < len(na) && i < len(nb); i++ {
		if na[i] != nb[i] {
			return na[i] < nb[i]
		}
	}
	if len(na) != len(nb) {
		return len(na) < len(nb)
	}
	return a < b
}

func numParts(s string) []int {
	var out []int
	n, in := 0, false
	for _, r := range s {
		if r >= '0' && r <= '9' {
			n, in = n*10+int(r-'0'), true
			continue
		}
		if in {
			out = append(out, n)
			n, in = 0, false
		}
	}
	if in {
		out = append(out, n)
	}
	return out
}

// parseUpgrades reads `apt-get -s upgrade` output.
func parseUpgrades(out string) []SystemPackage {
	var ps []SystemPackage
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		m := instRE.FindStringSubmatch(sc.Text())
		if m == nil {
			continue
		}
		origin := strings.TrimSpace(m[4])
		ps = append(ps, SystemPackage{Name: m[1], From: m[2], To: m[3], Security: strings.Contains(strings.ToLower(origin), "security")})
	}
	return ps
}

// storeAptPackages maps each package a store app installed from apt to that app.
func storeAptPackages() map[string]string {
	out := map[string]string{}
	for app, rec := range readInstalled() {
		a, err := findCatalogApp(rec.Catalog, rec.ID)
		if err != nil || a.Apt == nil {
			continue
		}
		if rec.Major != "" {
			a = a.withVersion(rec.Major)
		}
		for _, p := range a.Apt.Packages {
			out[p] = app
		}
	}
	return out
}

// SystemCheck refreshes apt's lists and records what an update would install. With install, the daily run then
// installs the security fixes when automatic security updates are on.
func (h *Host) SystemCheck(install bool) (SystemStatus, error) {
	s := ReadSystemStatus()
	s.CheckedAt = time.Now().UTC().Format(time.RFC3339)
	s.Error = ""
	if out, err := systemRun(nil, "apt-get", "update", "-q"); err != nil {
		s.Error = "apt-get update: " + lastLines(out, 3)
		writeSystemStatus(s)
		return s, errors.New(s.Error)
	}
	out, err := systemRun(nil, "apt-get", "-s", "-q", "--with-new-pkgs", "upgrade")
	if err != nil {
		s.Error = "apt-get -s upgrade: " + lastLines(out, 3)
		writeSystemStatus(s)
		return s, errors.New(s.Error)
	}
	owned := storeAptPackages()
	s.Packages, s.Security = parseUpgrades(out), 0
	for i := range s.Packages {
		s.Packages[i].App = owned[s.Packages[i].Name]
		if s.Packages[i].Security && s.Packages[i].App == "" {
			s.Security++
		}
	}
	if held, err := systemRun(nil, "apt-mark", "showhold"); err == nil {
		s.HeldPackages = strings.Fields(held)
	}
	writeSystemStatus(s)
	h.say("%d system updates, %d of them security fixes", countOwn(s.Packages, false), s.Security)
	if install && s.Auto && s.Security > 0 {
		h.say("installing the security fixes (automatic security updates are on)")
		return h.SystemUpdate(true)
	}
	return s, nil
}

func countOwn(ps []SystemPackage, securityOnly bool) int {
	n := 0
	for _, p := range ps {
		if p.App == "" && (!securityOnly || p.Security) {
			n++
		}
	}
	return n
}

func writeSystemStatus(s SystemStatus) {
	b, _ := json.MarshalIndent(s, "", "  ")
	os.MkdirAll(filepath.Dir(systemFile), 0o755)
	config.WriteAtomic(systemFile, b, 0o644)
}

// SystemUpdate installs the waiting updates (or only the security fixes), leaving store apps' packages to them.
func (h *Host) SystemUpdate(securityOnly bool) (SystemStatus, error) {
	s := ReadSystemStatus()
	var names []string
	for _, p := range s.Packages {
		if p.App == "" && (!securityOnly || p.Security) {
			names = append(names, p.Name)
		}
	}
	if len(names) == 0 {
		h.say("nothing to update")
		return s, nil
	}
	h.say("updating %d packages: %s", len(names), strings.Join(names, " "))
	env := []string{"NEEDRESTART_MODE=a", "APT_LISTCHANGES_FRONTEND=none"}
	args := append([]string{"install", "-y", "-q", "--only-upgrade", "-o", "Dpkg::Options::=--force-confold", "-o", "Dpkg::Options::=--force-confdef"}, names...)
	out, err := systemRun(env, "apt-get", args...)
	h.say("%s", lastLines(out, 12))
	if err != nil {
		return s, fmt.Errorf("apt-get install: %v", err)
	}
	s.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	writeSystemStatus(s)
	// what's left (store apps' packages, anything held back) and whether a restart is now needed
	s2, err := h.SystemCheck(false)
	if err != nil {
		return s2, err
	}
	s2.UpdatedAt = s.UpdatedAt
	writeSystemStatus(s2)
	// running programs keep the old libraries until they restart: restart the ones nook runs (not the helper,
	// which is running this; it picks them up at the next restart or nook update)
	for _, u := range staleLibUnits() {
		if u == "nook-helper.service" {
			continue
		}
		h.say("restarting %s onto the updated libraries", u)
		systemctl("restart", u)
	}
	if r, why := restartNeeded(); r {
		h.say("the server needs a restart: %s", why)
	}
	return ReadSystemStatus(), nil
}

// staleLibUnits is the running units nook looks after (Caddy, nook's own, every app's) whose main process still maps
// a library an update replaced.
func staleLibUnits() []string {
	units := []string{"caddy.service", "nook-web.service", "nook-helper.service"}
	for _, app := range Apps() {
		for _, site := range RunningSites(app) {
			units = append(units, UnitName(app, site))
		}
	}
	var out []string
	for _, u := range units {
		pid := UnitProps(u, "MainPID")["MainPID"]
		if pid == "" || pid == "0" {
			continue
		}
		if mapsDeletedLib(readFirst("/proc/" + pid + "/maps")) {
			out = append(out, u)
		}
	}
	return out
}

// mapsDeletedLib says whether a /proc/PID/maps lists a shared library that was replaced on disk.
func mapsDeletedLib(maps string) bool {
	for _, l := range strings.Split(maps, "\n") {
		if strings.HasSuffix(l, " (deleted)") && strings.Contains(l, ".so") {
			return true
		}
	}
	return false
}

// SetSystemAuto turns automatic security updates on or off.
func SetSystemAuto(on bool) error {
	if on {
		os.MkdirAll(config.StateDir, 0o700)
		return config.WriteAtomic(systemAuto, []byte("on\n"), 0o600)
	}
	if err := os.Remove(systemAuto); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// SystemRestart restarts the server in a few seconds, so the request that asked gets its answer first.
func SystemRestart() error {
	_, err := Run("systemd-run", "--on-active=5", "--unit=nook-restart-server", "/bin/systemctl", "reboot")
	return err
}

// NotifySystem tells the admin about security fixes waiting, through nook's update alerts.
func NotifySystem(s SystemStatus) {
	if s.Security > 0 && !s.Auto {
		update.Notify("Security updates waiting", fmt.Sprintf("%d security fixes for the server's packages. Install them from the dashboard's Server section.", s.Security))
	}
}
