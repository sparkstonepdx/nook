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
// Caddy 2.10+ prefers a managed wildcard over a certificate per subdomain by itself, so each app's per-host block
// uses it. (The "auto_https prefer_wildcard" option that used to ask for this was removed from Caddy; current Caddy
// rejects it, so nook takes it out of the Caddyfile if it's there.) Every `caddy validate` nook runs loads dns.env, since
// the DNS module rejects an empty token.

import (
	"bufio"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/sparkstonepdx/nook/internal/caddyfile"
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

type wildcard struct {
	Domain   string `json:"domain"`
	Provider string `json:"provider"`
	Env      string `json:"env"`
	Parent   string `json:"parent,omitempty"` // set on nested wildcards (derived, never stored): the one they come from
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
var dnsTokenRE = regexp.MustCompile(`^[A-Za-z0-9_.-]{20,200}$`)

// A wildcard covers one level, so a host two or more levels under a domain with a wildcard (a.b.example.com) gets
// a wildcard for its parent (*.b.example.com), through the same DNS provider and token. These are derived from the
// hostnames nook publishes on every sync and never stored, so one goes away with its last host.

// publishedHosts is every hostname in nook's generated app config.
func publishedHosts() []string {
	var hosts []string
	files, _ := filepath.Glob(filepath.Join(config.CaddyAppsDir, "*.caddy"))
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		live, redirs := caddyfile.Hosts(string(b))
		hosts = append(append(hosts, live...), redirs...)
	}
	return hosts
}

// underWildcard is the configured wildcard host falls under (the longest matching domain) and the wildcard domain
// that covers host: the configured one for a host one level under it, its parent for a deeper one.
func underWildcard(host string, ws []wildcard) (wildcard, string, bool) {
	var best wildcard
	found := false
	for _, w := range ws {
		if strings.HasSuffix(host, "."+w.Domain) && (!found || len(w.Domain) > len(best.Domain)) {
			best, found = w, true
		}
	}
	if !found {
		return wildcard{}, "", false
	}
	sub := strings.TrimSuffix(host, "."+best.Domain)
	if !strings.Contains(sub, ".") {
		return best, best.Domain, true
	}
	return best, host[strings.Index(host, ".")+1:], true
}

// nestedWildcards is the wildcard each deeper host needs, one per distinct parent.
func nestedWildcards(ws []wildcard, hosts []string) []wildcard {
	seen := map[string]bool{}
	for _, w := range ws {
		seen[w.Domain] = true
	}
	var out []wildcard
	for _, h := range hosts {
		w, target, ok := underWildcard(h, ws)
		if !ok || seen[target] {
			continue
		}
		seen[target] = true
		out = append(out, wildcard{Domain: target, Provider: w.Provider, Env: w.Env, Parent: w.Domain})
	}
	return out
}

// allWildcards is the configured wildcards plus the nested ones the published hosts need.
func allWildcards(c certsConfig) []wildcard {
	return append(append([]wildcard(nil), c.Wildcards...), nestedWildcards(c.Wildcards, publishedHosts())...)
}

func renderTLS(all []wildcard) string {
	var b strings.Builder
	b.WriteString("# generated by nook (Domains → Certificates): do not edit\n")
	ws := append([]wildcard(nil), all...)
	sort.Slice(ws, func(i, j int) bool { return ws[i].Domain < ws[j].Domain })
	for _, w := range ws {
		fmt.Fprintf(&b, "*.%s {\n\ttls {\n\t\tdns %s {env.%s}\n\t}\n\trespond 404\n}\n", w.Domain, w.Provider, w.Env)
	}
	return b.String()
}

// withoutPreferWildcard drops "auto_https prefer_wildcard" from the Caddyfile: current Caddy refuses the whole
// config over it (it prefers wildcards without being asked), and an earlier nook may have written it.
func withoutPreferWildcard(text string) string {
	var out []string
	for _, l := range strings.Split(text, "\n") {
		if strings.TrimSpace(l) == "auto_https prefer_wildcard" {
			continue
		}
		out = append(out, l)
	}
	return strings.Join(out, "\n")
}

// ---- what Caddy has ----

func wildcardIssued(domain string) (bool, time.Time) { return certIssued("wildcard_." + domain) }

// certIssued looks for an unexpired certificate Caddy stored under name (a hostname, or wildcard_.DOMAIN).
func certIssued(name string) (bool, time.Time) {
	files, _ := filepath.Glob(filepath.Join(caddyCertsDir, "*", name, name+".crt"))
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		if blk, _ := pem.Decode(b); blk != nil {
			if cert, err := x509.ParseCertificate(blk.Bytes); err == nil && time.Now().Before(cert.NotAfter) {
				return true, cert.NotAfter
			}
		}
	}
	return false, time.Time{}
}

// OwnCert says which hosts keep their own certificate: those whose wildcard (the configured one, or the nested one
// for a deeper host) isn't issued yet. Nil (none) on Caddy before 2.10, which doesn't prefer wildcards.
func OwnCert() func(host string) bool {
	if !caddyForceAutomate() {
		return nil
	}
	ws := loadCerts().Wildcards
	if len(ws) == 0 {
		return nil
	}
	issued := map[string]bool{}
	limits := certLimits()
	return func(host string) bool {
		w, target, ok := underWildcard(host, ws)
		if !ok {
			return false
		}
		done, seen := issued[target]
		if !seen {
			done, _ = wildcardIssued(target)
			issued[target] = done
		}
		if done {
			return false
		}
		if _, limited := limits[w.Domain]; limited {
			// Out of certificates for the week: the wildcard goes first. A host that has its own keeps it.
			has, _ := certIssued(host)
			return has
		}
		return true
	}
}

// refreshNested rewrites the wildcard config when the published hosts need a different set of nested wildcards,
// and reloads Caddy (their tokens are already in its environment). Called after each app's sync.
func refreshNested() {
	c := loadCerts()
	if len(c.Wildcards) == 0 {
		return
	}
	text := renderTLS(allWildcards(c))
	old, err := os.ReadFile(tlsCaddy)
	if err == nil && string(old) == text {
		return
	}
	if err := config.WriteAtomic(tlsCaddy, []byte(text), 0o644); err != nil {
		return
	}
	if _, err := caddyValidate(); err != nil {
		config.WriteAtomic(tlsCaddy, old, 0o644)
		return
	}
	systemctl("reload", "caddy")
}

// wildcardState is what the generated config depends on: each wildcard and whether it's issued.
func wildcardState() string {
	var parts []string
	for _, w := range allWildcards(loadCerts()) {
		ok, _ := wildcardIssued(w.Domain)
		parts = append(parts, fmt.Sprintf("%s=%v", w.Domain, ok))
	}
	for d := range certLimits() {
		parts = append(parts, "limited:"+d)
	}
	sort.Strings(parts)
	return strings.Join(parts, " ")
}

// WatchWildcards regenerates the Caddy config whenever a wildcard is issued (or one is added or removed), so hosts
// move onto it as soon as it exists. Run by the helper; the first look also updates config written by an older nook.
func WatchWildcards() {
	last := ""
	for {
		if st := wildcardState(); st != last {
			if last != "" || st != "" {
				h := New(io.Discard)
				h.SyncAll()
				h.AdminCaddy()
			}
			last = st
		}
		time.Sleep(2 * time.Minute)
	}
}

// ---- the view ----

type CertDomain struct {
	Domain   string   `json:"domain"`
	For      []string `json:"for"` // "default domain", or the apps with a grant on it
	Wildcard bool     `json:"wildcard"`
	Provider string   `json:"provider,omitempty"`
	Status   string   `json:"status,omitempty"` // issued, waiting, limited, error
	Detail   string   `json:"detail,omitempty"`
	Expires  string   `json:"expires,omitempty"`
	Retry    string   `json:"retry,omitempty"` // limited: when Let's Encrypt allows the next try (RFC 3339)
	Nested   []Nested `json:"nested,omitempty"` // wildcards for hosts deeper than one level
}

type Nested struct {
	Domain  string `json:"domain"`
	Status  string `json:"status"`
	Detail  string `json:"detail,omitempty"`
	Expires string `json:"expires,omitempty"`
	Retry   string `json:"retry,omitempty"`
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
			if !slices.Contains(out[g.Name], g.App) {
				out[g.Name] = append(out[g.Name], g.App)
			}
		}
	}
	return out
}

func Certs() CertsView {
	v := CertsView{Caddy: caddyInfo(), Providers: dnsProviders}
	c := loadCerts()
	nested := nestedWildcards(c.Wildcards, publishedHosts())
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
			limits := certLimits()
			cd.Status, cd.Detail, cd.Expires, cd.Retry = wildcardStatus(d, limits[d])
			for _, n := range nested {
				if n.Parent == d {
					st, det, exp, retry := wildcardStatus(n.Domain, limits[d])
					cd.Nested = append(cd.Nested, Nested{Domain: n.Domain, Status: st, Detail: det, Expires: exp, Retry: retry})
				}
			}
		}
		v.Domains = append(v.Domains, cd)
	}
	return v
}

// wildcardStatus reads Caddy's own store: a certificate on disk is issued; otherwise the last error Caddy logged
// for it, or waiting.
func wildcardStatus(domain string, limit time.Time) (status, detail, expires, retry string) {
	if ok, until := wildcardIssued(domain); ok {
		return "issued", "", until.UTC().Format(time.RFC3339), ""
	}
	if limit.After(time.Now()) {
		return "limited", "Let's Encrypt's weekly limit for this domain is used up. Hosts that don't have a certificate yet wait for the wildcard, so it gets the first one.", "", limit.UTC().Format(time.RFC3339)
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
		if caddyForceAutomate() {
			last += " Until it's issued, each hostname keeps its own certificate; Caddy keeps trying."
		}
		if len(last) > 300 {
			last = last[:300] + "…"
		}
		return "error", last, "", ""
	}
	if !limit.IsZero() {
		return "waiting", "The weekly limit has lifted, and Caddy will ask again on its next try. Hosts that don't have a certificate yet keep waiting for the wildcard.", "", ""
	}
	return "waiting", "Caddy is asking for it. Until it's issued, each hostname keeps its own certificate.", "", ""
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
		err = config.WriteAtomic(tlsCaddy, []byte(renderTLS(allWildcards(c))), 0o644)
	}
	if err == nil {
		cf, _ := os.ReadFile(config.Caddyfile)
		err = config.WriteAtomic(config.Caddyfile, []byte(withoutPreferWildcard(string(cf))), 0o644)
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
	// hosts under a wildcard that isn't issued yet keep their own certificate (or drop that once it is)
	quiet := New(io.Discard)
	quiet.SyncAll()
	quiet.AdminCaddy()
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
