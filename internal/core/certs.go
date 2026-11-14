package core

// Wildcard certificates. Caddy gets one certificate per hostname by default, and every app and PR preview on a
// domain uses one, which runs into Let's Encrypt's 50-per-registered-domain-per-week limit. A wildcard
// (*.example.com, by DNS challenge) covers them all with one certificate.
//
//	/etc/nook/certs.json                               which domains, which DNS provider, which env var (no secrets)
//	/etc/nook/dns.env                                  root-only: NOOK_DNS_<n>=<API token>
//	/etc/systemd/system/caddy.service.d/nook-dns.conf  EnvironmentFile=/etc/nook/dns.env (an env change needs a restart)
//	/etc/caddy/nook-tls.caddy                          *.example.com { tls { dns cloudflare {env.NOOK_DNS_1} } respond 404 }
//	/usr/bin/caddy                                     replaced by a build with the DNS plugin from caddyserver.com, package held
//
// with "auto_https prefer_wildcard" in the Caddyfile's global options (Caddy 2.10+), so each app's per-host block
// uses the wildcard instead of getting its own certificate. Every `caddy validate` nook runs loads dns.env, since
// the DNS module rejects an empty token.

import (
	"bufio"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/sparkstonepdx/nook/internal/config"
	"github.com/sparkstonepdx/nook/internal/grants"
)

var (
	certsFile      = "/etc/nook/certs.json"
	dnsEnvFile     = "/etc/nook/dns.env"
	caddyDNSDropIn = "/etc/systemd/system/caddy.service.d/nook-dns.conf"
	tlsCaddy       = "/etc/caddy/nook-tls.caddy"
	caddyCertsDir  = "/var/lib/caddy/.local/share/caddy/certificates"
	cloudflareAPI  = "https://api.cloudflare.com/client/v4"
)

// DNSProvider is a DNS host Caddy can answer the challenge through. Add others here.
type DNSProvider struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Module   string   `json:"module"`   // in `caddy list-modules`
	Package  string   `json:"package"`  // its Go package, for a Caddy build from caddyserver.com
	Hint     string   `json:"hint"`     // one line about the token
	TokenURL string   `json:"tokenUrl"` // where tokens are made
	Steps    []string `json:"steps"`    // how to make one; {zone} is the domain's zone
	verify   func(token, domain string) error
}

var dnsProviders = []DNSProvider{
	{ID: "cloudflare", Name: "Cloudflare", Module: "dns.providers.cloudflare", Package: "github.com/caddy-dns/cloudflare",
		Hint:     `A token from the "Edit zone DNS" template, scoped to this domain's zone.`,
		TokenURL: "https://dash.cloudflare.com/profile/api-tokens",
		Steps: []string{
			"Open Cloudflare's API Tokens page (My Profile → API Tokens) and press Create Token.",
			"Next to Edit zone DNS, press Use template. It already has the one permission needed: Zone · DNS · Edit.",
			"Under Zone Resources, keep Include · Specific zone and pick {zone}.",
			"Press Continue to summary, then Create Token.",
			"Copy the token: Cloudflare shows it only once. Paste it below.",
		},
		verify: verifyCloudflare},
}

func dnsProvider(id string) (DNSProvider, bool) {
	for _, p := range dnsProviders {
		if p.ID == id {
			return p, true
		}
	}
	return DNSProvider{}, false
}

type wildcard struct {
	Domain   string `json:"domain"`
	Provider string `json:"provider"`
	Env      string `json:"env"`
}

type certsConfig struct {
	Wildcards []wildcard `json:"wildcards"`
}

func loadCerts() certsConfig {
	var c certsConfig
	if b, err := os.ReadFile(certsFile); err == nil {
		json.Unmarshal(b, &c)
	}
	return c
}

func (c certsConfig) find(domain string) (wildcard, bool) {
	for _, w := range c.Wildcards {
		if w.Domain == domain {
			return w, true
		}
	}
	return wildcard{}, false
}

// ---- the token file ----

func readDNSEnv() map[string]string {
	m := map[string]string{}
	f, err := os.Open(dnsEnvFile)
	if err != nil {
		return m
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	for s.Scan() {
		if k, v, ok := strings.Cut(strings.TrimSpace(s.Text()), "="); ok && strings.HasPrefix(k, "NOOK_DNS_") {
			m[k] = v
		}
	}
	return m
}

func writeDNSEnv(m map[string]string) error {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString("# written by nook: DNS API tokens for wildcard certificates (Domains page). Root only.\n")
	for _, k := range keys {
		fmt.Fprintf(&b, "%s=%s\n", k, m[k])
	}
	return config.WriteAtomic(dnsEnvFile, []byte(b.String()), 0o600)
}

// caddyEnv is the environment Caddy runs with: ours plus the DNS tokens, so `caddy validate` sees what Caddy will.
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

var dnsTokenRE = regexp.MustCompile(`^[A-Za-z0-9_.-]{20,200}$`)

func renderTLS(c certsConfig) string {
	var b strings.Builder
	b.WriteString("# generated by nook (Domains → Certificates): do not edit\n")
	ws := append([]wildcard(nil), c.Wildcards...)
	sort.Slice(ws, func(i, j int) bool { return ws[i].Domain < ws[j].Domain })
	for _, w := range ws {
		fmt.Fprintf(&b, "*.%s {\n\ttls {\n\t\tdns %s {env.%s}\n\t}\n\trespond 404\n}\n", w.Domain, w.Provider, w.Env)
	}
	return b.String()
}

const preferWildcard = "auto_https prefer_wildcard"

// withPreferWildcard adds or removes "auto_https prefer_wildcard" in the Caddyfile's global options block.
func withPreferWildcard(text string, on bool) string {
	lines := strings.Split(text, "\n")
	var out []string
	for _, l := range lines {
		if strings.TrimSpace(l) == preferWildcard {
			continue
		}
		out = append(out, l)
	}
	if !on {
		return strings.Join(out, "\n")
	}
	for i, l := range out {
		t := strings.TrimSpace(l)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		if t == "{" { // the global options block
			return strings.Join(append(out[:i+1], append([]string{"\t" + preferWildcard}, out[i+1:]...)...), "\n")
		}
		break
	}
	return "{\n\t" + preferWildcard + "\n}\n\n" + strings.Join(out, "\n")
}

// ---- what Caddy has ----

type CaddyInfo struct {
	Version        string          `json:"version"`
	PreferWildcard bool            `json:"preferWildcard"` // 2.10+: per-host blocks reuse a wildcard
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

// ---- the view ----

type CertDomain struct {
	Domain   string   `json:"domain"`
	For      []string `json:"for"` // "default domain", or the apps with a grant on it
	Wildcard bool     `json:"wildcard"`
	Provider string   `json:"provider,omitempty"`
	Status   string   `json:"status,omitempty"` // issued, waiting, error
	Detail   string   `json:"detail,omitempty"`
	Expires  string   `json:"expires,omitempty"`
}

type CertsView struct {
	Caddy     CaddyInfo     `json:"caddy"`
	Domains   []CertDomain  `json:"domains"`
	Providers []DNSProvider `json:"providers"`
}

// certDomains is every domain nook serves subdomains of: the default domain and every domain and sub grant.
func certDomains() map[string][]string {
	out := map[string][]string{}
	if d := config.Load().BaseDomain; d != "" {
		out[d] = append(out[d], "default domain")
	}
	gl, _ := grants.Load(config.GrantsFile)
	for _, g := range gl {
		if g.Kind == "domain" || g.Kind == "sub" {
			if !contains(out[g.Name], g.App) {
				out[g.Name] = append(out[g.Name], g.App)
			}
		}
	}
	return out
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

func Certs() CertsView {
	v := CertsView{Caddy: caddyInfo(), Providers: dnsProviders}
	c := loadCerts()
	doms := certDomains()
	for _, w := range c.Wildcards { // a wildcard whose grant went away still shows, so it can be removed
		if _, ok := doms[w.Domain]; !ok {
			doms[w.Domain] = nil
		}
	}
	keys := make([]string, 0, len(doms))
	for d := range doms {
		keys = append(keys, d)
	}
	sort.Strings(keys)
	for _, d := range keys {
		cd := CertDomain{Domain: d, For: doms[d]}
		if w, ok := c.find(d); ok {
			cd.Wildcard, cd.Provider = true, w.Provider
			cd.Status, cd.Detail, cd.Expires = wildcardStatus(d)
		}
		v.Domains = append(v.Domains, cd)
	}
	return v
}

// wildcardStatus reads Caddy's own store: a certificate on disk is issued; otherwise the last error Caddy logged
// for it, or waiting.
func wildcardStatus(domain string) (status, detail, expires string) {
	name := "wildcard_." + domain
	files, _ := filepath.Glob(filepath.Join(caddyCertsDir, "*", name, name+".crt"))
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		if blk, _ := pem.Decode(b); blk != nil {
			if cert, err := x509.ParseCertificate(blk.Bytes); err == nil && time.Now().Before(cert.NotAfter) {
				return "issued", "", cert.NotAfter.UTC().Format(time.RFC3339)
			}
		}
	}
	out, _ := exec.Command("journalctl", "-u", "caddy", "--since", "-6h", "-o", "cat", "--no-pager").Output()
	last := ""
	for _, l := range strings.Split(string(out), "\n") {
		if strings.Contains(l, "*."+domain) && strings.Contains(strings.ToLower(l), "error") {
			last = l
		}
	}
	if last != "" {
		var e struct {
			Msg   string `json:"msg"`
			Error string `json:"error"`
		}
		if json.Unmarshal([]byte(last), &e) == nil && (e.Error != "" || e.Msg != "") {
			last = strings.TrimSpace(e.Msg + ": " + e.Error)
		}
		if len(last) > 300 {
			last = last[:300] + "…"
		}
		return "error", last, ""
	}
	return "waiting", "Caddy asks for it after a restart; it can take a minute or two.", ""
}

// ---- changes ----

// SetWildcard turns on (or updates the token for) domain's wildcard certificate.
func (h *Host) SetWildcard(domain, provider, token string) error {
	domain = strings.ToLower(strings.TrimSpace(domain))
	token = strings.TrimSpace(token)
	if _, ok := certDomains()[domain]; !ok {
		return fmt.Errorf("%s isn't the default domain or a domain an app is allowed", domain)
	}
	p, ok := dnsProvider(provider)
	if !ok {
		return fmt.Errorf("unknown DNS provider %q", provider)
	}
	info := caddyInfo()
	if !info.Modules[p.ID] {
		return fmt.Errorf("Caddy doesn't have the %s plugin yet: add it first", p.Name)
	}
	if !dnsTokenRE.MatchString(token) {
		return fmt.Errorf("that doesn't look like an API token")
	}
	h.say("checking the token with %s", p.Name)
	if err := p.verify(token, domain); err != nil {
		return err
	}
	c := loadCerts()
	tokens := readDNSEnv()
	w, found := c.find(domain)
	if !found {
		w = wildcard{Domain: domain, Env: nextEnv(c)}
		c.Wildcards = append(c.Wildcards, w)
	}
	w.Provider = p.ID
	for i := range c.Wildcards {
		if c.Wildcards[i].Domain == domain {
			c.Wildcards[i] = w
		}
	}
	tokens[w.Env] = token
	if err := h.applyCerts(c, tokens); err != nil {
		return err
	}
	h.say("*.%s: Caddy is asking %s for the certificate now", domain, p.Name)
	return nil
}

// RemoveWildcard goes back to one certificate per hostname for domain.
func (h *Host) RemoveWildcard(domain string) error {
	c := loadCerts()
	w, ok := c.find(domain)
	if !ok {
		return fmt.Errorf("%s has no wildcard certificate", domain)
	}
	var keep []wildcard
	for _, x := range c.Wildcards {
		if x.Domain != domain {
			keep = append(keep, x)
		}
	}
	c.Wildcards = keep
	tokens := readDNSEnv()
	delete(tokens, w.Env)
	if err := h.applyCerts(c, tokens); err != nil {
		return err
	}
	h.say("*.%s: back to one certificate per hostname", domain)
	return nil
}

func nextEnv(c certsConfig) string {
	used := map[string]bool{}
	for _, w := range c.Wildcards {
		used[w.Env] = true
	}
	for n := 1; ; n++ {
		if k := fmt.Sprintf("NOOK_DNS_%d", n); !used[k] {
			return k
		}
	}
}

// applyCerts writes everything, checks Caddy accepts it with the new tokens, and restarts Caddy (its environment
// changed). If the check fails, every file goes back to how it was.
func (h *Host) applyCerts(c certsConfig, tokens map[string]string) error {
	files := []string{certsFile, dnsEnvFile, caddyDNSDropIn, tlsCaddy, config.Caddyfile}
	saved := map[string][]byte{}
	for _, f := range files {
		if b, err := os.ReadFile(f); err == nil {
			saved[f] = b
		}
	}
	restore := func() {
		for _, f := range files {
			if b, ok := saved[f]; ok {
				config.WriteAtomic(f, b, modeOf(f))
			} else {
				os.Remove(f)
			}
		}
	}
	on := len(c.Wildcards) > 0
	b, _ := json.MarshalIndent(c, "", "  ")
	err := config.WriteAtomic(certsFile, b, 0o600)
	if err == nil {
		err = writeDNSEnv(tokens)
	}
	if err == nil && on {
		os.MkdirAll(filepath.Dir(caddyDNSDropIn), 0o755)
		err = config.WriteAtomic(caddyDNSDropIn, []byte("# written by nook: DNS tokens for wildcard certificates\n[Service]\nEnvironmentFile="+dnsEnvFile+"\n"), 0o644)
	} else if err == nil {
		os.Remove(caddyDNSDropIn)
	}
	if err == nil {
		err = config.WriteAtomic(tlsCaddy, []byte(renderTLS(c)), 0o644)
	}
	if err == nil {
		cf, _ := os.ReadFile(config.Caddyfile)
		err = config.WriteAtomic(config.Caddyfile, []byte(withPreferWildcard(string(cf), on && caddyInfo().PreferWildcard)), 0o644)
		ensureImport(tlsCaddy)
	}
	if err != nil {
		restore()
		return err
	}
	if out, err := caddyValidate(); err != nil {
		restore()
		return fmt.Errorf("Caddy didn't accept it; nothing changed\n%s", lastLines(out, 6))
	}
	systemctl("daemon-reload")
	h.say("restarting Caddy (its environment changed; sites are back within seconds)")
	if err := systemctl("restart", "caddy"); err != nil {
		return fmt.Errorf("restarting Caddy: %v", err)
	}
	return nil
}

func modeOf(f string) os.FileMode {
	if f == certsFile || f == dnsEnvFile {
		return 0o600
	}
	return 0o644
}

// Caddy with plugins comes from Caddy's build server (what `caddy add-package` uses), not from apt: Debian's caddy
// has no add-package or upgrade, and its version is often older than prefer_wildcard needs. The binary is
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
	after := caddyInfo()
	// a version that now supports prefer_wildcard turns it on
	if c := loadCerts(); len(c.Wildcards) > 0 {
		cf, _ := os.ReadFile(config.Caddyfile)
		config.WriteAtomic(config.Caddyfile, []byte(withPreferWildcard(string(cf), after.PreferWildcard)), 0o644)
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

var certHTTP = &http.Client{Timeout: 20 * time.Second}

func cfGet(token, path string) (map[string]any, error) {
	req, _ := http.NewRequest("GET", cloudflareAPI+path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := certHTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("couldn't reach Cloudflare: %v", err)
	}
	defer resp.Body.Close()
	var body map[string]any
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return nil, fmt.Errorf("Cloudflare answered %s", resp.Status)
	}
	return body, nil
}

// verifyCloudflare checks the token is live and can see the zone the domain is in.
func verifyCloudflare(token, domain string) error {
	if b, err := cfGet(token, "/user/tokens/verify"); err != nil {
		return err
	} else if b["success"] != true {
		// account-owned tokens verify elsewhere; the zone check below still decides
		if _, isAccount := strings.CutPrefix(token, "cfat_"); !isAccount {
			return fmt.Errorf("Cloudflare doesn't accept that token (%s)", cfErrors(b))
		}
	}
	labels := strings.Split(domain, ".")
	for i := 0; i+2 <= len(labels); i++ {
		zone := strings.Join(labels[i:], ".")
		b, err := cfGet(token, "/zones?name="+url.QueryEscape(zone))
		if err != nil {
			return err
		}
		if res, ok := b["result"].([]any); ok && len(res) > 0 {
			return nil
		}
	}
	return fmt.Errorf("the token works but can't see the zone for %s. Make it from the \"Edit zone DNS\" template, scoped to that zone", domain)
}

func cfErrors(b map[string]any) string {
	var msgs []string
	if errs, ok := b["errors"].([]any); ok {
		for _, e := range errs {
			if m, ok := e.(map[string]any); ok {
				msgs = append(msgs, fmt.Sprint(m["message"]))
			}
		}
	}
	if len(msgs) == 0 {
		return "no reason given"
	}
	return strings.Join(msgs, "; ")
}
