package core

// Caddy itself: its version, validating its config with the DNS tokens loaded, and replacing it with a build
// from caddyserver.com that has the DNS plugins.

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/sparkstonepdx/nook/internal/config"
)

func caddyEnv(tokens map[string]string) []string {
	env := os.Environ()
	if os.Getenv("HOME") == "" {
		env = append(env, "HOME=/root")
	}
	for k, v := range tokens {
		env = append(env, k+"="+v)
	}
	return env
}

// caddyValidate checks the whole Caddyfile the way Caddy will load it, tokens included.
func caddyValidate() (string, error) {
	cmd := exec.Command("caddy", "validate", "--config", config.Caddyfile, "--adapter", "caddyfile")
	cmd.Env = caddyEnv(readDNSEnv())
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// ---- generated config ----

type CaddyInfo struct {
	Version        string          `json:"version"`
	PreferWildcard bool            `json:"preferWildcard"` // 2.10+: uses a managed wildcard for its subdomains by itself
	Held           bool            `json:"held"`           // apt-mark hold, so an apt upgrade can't drop plugins
	Modules        map[string]bool `json:"modules"`        // provider id -> its DNS module is built in
}

var caddyVersionRE = regexp.MustCompile(`v?(\d+)\.(\d+)\.(\d+)`)

func caddyAtLeast(version string, major, minor int) bool {
	m := caddyVersionRE.FindStringSubmatch(version)
	if m == nil {
		return false
	}
	a, _ := strconv.Atoi(m[1])
	b, _ := strconv.Atoi(m[2])
	return a > major || a == major && b >= minor
}

func caddyInfo() CaddyInfo {
	info := CaddyInfo{Modules: map[string]bool{}}
	if out, err := exec.Command("caddy", "version").Output(); err == nil {
		info.Version = strings.Fields(strings.TrimSpace(string(out)) + " ")[0]
	}
	info.PreferWildcard = caddyAtLeast(info.Version, 2, 10)
	mods, _ := exec.Command("caddy", "list-modules").Output()
	for _, p := range dnsProviders {
		info.Modules[p.ID] = regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(p.Module) + `\s*$`).Match(mods)
	}
	held, _ := exec.Command("apt-mark", "showhold").Output()
	info.Held = regexp.MustCompile(`(?m)^caddy\s*$`).Match(held)
	return info
}

// ---- own certificates until the wildcard is issued ----

// Caddy 2.10+ uses a managed wildcard for every subdomain it covers and stops getting them certificates of their
// own, so until the wildcard is issued (a rate limit can hold it for days) those hosts would serve nothing. Hosts
// under a wildcard that isn't issued yet keep their own certificate (`tls force_automate`), and the helper switches
// them over once it is. The dashboard address always keeps its own, so a wildcard can't lock anyone out.

var caddyVersionCache struct {
	mtime   time.Time
	version string
}

// caddyForceAutomate: whether this Caddy prefers wildcards (and so understands force_automate), 2.10+.
func caddyForceAutomate() bool {
	bin, err := exec.LookPath("caddy")
	if err != nil {
		return false
	}
	st, err := os.Stat(bin)
	if err != nil {
		return false
	}
	if !st.ModTime().Equal(caddyVersionCache.mtime) {
		out, _ := exec.Command(bin, "version").Output()
		caddyVersionCache.mtime, caddyVersionCache.version = st.ModTime(), strings.TrimSpace(string(out))
	}
	return caddyAtLeast(caddyVersionCache.version, 2, 10)
}

// wildcardIssued: Caddy has a current certificate for *.domain in its store.

// Caddy with plugins comes from Caddy's build server (what `caddy add-package` uses), not from apt: Debian's caddy
// has no add-package or upgrade, and its version is often older than 2.10, which uses a wildcard for subdomains. The binary is
// replaced in place (the distro's unit and config stay), keeps its port permission, and the package is held so
// an apt upgrade can't put a plugin-less Caddy back.
var caddyDownload = "https://caddyserver.com/api/download"

// caddyPlugins is every DNS plugin the running Caddy has, plus extra: a rebuild must keep them all.
func caddyPlugins(extra string) []string {
	info := caddyInfo()
	var pkgs []string
	for _, p := range dnsProviders {
		if info.Modules[p.ID] || p.ID == extra {
			pkgs = append(pkgs, p.Package)
		}
	}
	return pkgs
}

// AddDNSPlugin gets a Caddy with provider's DNS module (and every plugin it already has), and holds the package.
func (h *Host) AddDNSPlugin(provider string) error {
	p, ok := dnsProvider(provider)
	if !ok {
		return fmt.Errorf("unknown DNS provider %q", provider)
	}
	if caddyInfo().Modules[p.ID] {
		h.say("Caddy already has the %s plugin", p.Name)
		return h.holdCaddy()
	}
	if err := h.installCaddyBuild(caddyPlugins(p.ID), p.Module); err != nil {
		return err
	}
	return h.holdCaddy()
}

// UpgradeCaddy gets the current Caddy with the same plugins.
func (h *Host) UpgradeCaddy() error {
	before := caddyInfo()
	var want string
	for _, p := range dnsProviders {
		if before.Modules[p.ID] {
			want = p.Module
		}
	}
	if err := h.installCaddyBuild(caddyPlugins(""), want); err != nil {
		return err
	}
	cf, _ := os.ReadFile(config.Caddyfile)
	if clean := withoutPreferWildcard(string(cf)); clean != string(cf) {
		config.WriteAtomic(config.Caddyfile, []byte(clean), 0o644)
	}
	return h.restartCaddyIfValid()
}

func (h *Host) holdCaddy() error {
	if out, err := Run("apt-mark", "hold", "caddy"); err != nil {
		h.say("couldn't hold the caddy package: %s", strings.TrimSpace(out))
	} else {
		h.say("held the caddy package: apt won't replace it. Update Caddy from the Domains page")
	}
	return h.restartCaddyIfValid()
}

// installCaddyBuild downloads Caddy with pkgs for this machine, checks it runs and has module, and swaps it in for
// the caddy on PATH. The old binary is kept beside it as caddy.nook-old.
func (h *Host) installCaddyBuild(pkgs []string, module string) error {
	bin, err := exec.LookPath("caddy")
	if err != nil {
		return fmt.Errorf("caddy isn't installed")
	}
	if real, err := filepath.EvalSymlinks(bin); err == nil {
		bin = real
	}
	q := url.Values{"os": {"linux"}, "arch": {runtime.GOARCH}}
	for _, p := range pkgs {
		q.Add("p", p)
	}
	h.say("downloading Caddy with %s from caddyserver.com (about 40 MB; takes a minute)", strings.Join(pkgs, ", "))
	resp, err := caddyHTTP.Get(caddyDownload + "?" + q.Encode())
	if err != nil {
		return fmt.Errorf("couldn't reach caddyserver.com: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2000))
		return fmt.Errorf("caddyserver.com couldn't build it (%s): %s", resp.Status, strings.TrimSpace(string(b)))
	}
	tmp, err := os.CreateTemp(filepath.Dir(bin), ".caddy-nook-")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := io.Copy(tmp, io.LimitReader(resp.Body, 300<<20)); err != nil {
		tmp.Close()
		return fmt.Errorf("download failed: %v", err)
	}
	tmp.Close()
	os.Chmod(tmp.Name(), 0o755)
	ver, err := exec.Command(tmp.Name(), "version").Output()
	if err != nil {
		return fmt.Errorf("the downloaded Caddy doesn't run: %v", err)
	}
	if module != "" {
		mods, _ := exec.Command(tmp.Name(), "list-modules").Output()
		if !regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(module) + `\s*$`).Match(mods) {
			return fmt.Errorf("the downloaded Caddy doesn't list %s", module)
		}
	}
	// keep the old binary's capabilities (binding ports 80 and 443 as the caddy user)
	if caps, err := exec.Command("getcap", bin).Output(); err == nil {
		if f := strings.Fields(string(caps)); len(f) >= 2 {
			if out, err := Run("setcap", strings.Join(f[1:], " "), tmp.Name()); err != nil {
				return fmt.Errorf("couldn't give the new Caddy the old one's capabilities: %s", strings.TrimSpace(out))
			}
		}
	}
	// check the config with the new binary before it's in place
	cmd := exec.Command(tmp.Name(), "validate", "--config", config.Caddyfile, "--adapter", "caddyfile")
	cmd.Env = caddyEnv(readDNSEnv())
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("the new Caddy doesn't accept the current config; kept the old one\n%s", lastLines(string(out), 6))
	}
	copyFileMode(bin, bin+".nook-old")
	if err := os.Rename(tmp.Name(), bin); err != nil {
		return err
	}
	h.say("installed Caddy %s (the previous one is kept as %s.nook-old)", strings.Fields(string(ver) + " ")[0], bin)
	return nil
}

var caddyHTTP = &http.Client{Timeout: 10 * time.Minute}

func copyFileMode(src, dst string) {
	b, err := os.ReadFile(src)
	if err != nil {
		return
	}
	st, _ := os.Stat(src)
	os.WriteFile(dst, b, st.Mode().Perm())
}

func (h *Host) restartCaddyIfValid() error {
	if out, err := caddyValidate(); err != nil {
		return fmt.Errorf("Caddy's config doesn't validate, so it wasn't restarted:\n%s", lastLines(out, 6))
	}
	return systemctl("restart", "caddy")
}

// ---- token checks ----
