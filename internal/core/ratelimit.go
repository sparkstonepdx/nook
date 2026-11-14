package core

// Let's Encrypt issues at most 50 certificates per registered domain a week. When a domain reaches that, Caddy logs
// "HTTP 429 ... rateLimited ... retry after 2026-11-14 08:21:17 UTC". nook keeps the latest of those times per
// configured wildcard domain in /var/lib/nook/cert-limits.json (the journal rotates; a limit can last days), so that:
//
//   - the Domains page says when the next try can work;
//   - hosts under that domain that don't have their own certificate yet stop asking for one, so the wildcard is
//     first in line when the limit lifts. Hosts that already have one keep it.
//
// A record goes once the domain's wildcard and every nested wildcard under it are issued.

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/sparkstonepdx/nook/internal/config"
)

var (
	certLimitsFile = filepath.Join(config.LibDir, "cert-limits.json")
	retryAfterRE   = regexp.MustCompile(`retry after (\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}) UTC`)
)

// limitsFromLog finds, for each domain, the latest retry-after logged for it or any name under it.
func limitsFromLog(text string, domains []string) map[string]time.Time {
	out := map[string]time.Time{}
	for _, l := range strings.Split(text, "\n") {
		if !strings.Contains(l, "rateLimited") && !strings.Contains(l, "429") {
			continue
		}
		m := retryAfterRE.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		at, err := time.Parse("2006-01-02 15:04:05", m[1])
		if err != nil {
			continue
		}
		for _, d := range domains {
			if mentions(l, d) && at.After(out[d]) {
				out[d] = at
			}
		}
	}
	return out
}

// mentions says whether line names domain or a name under it (not notexample.com, not example.com.evil).
func mentions(line, domain string) bool {
	re := regexp.MustCompile(`(^|[^A-Za-z0-9-])` + regexp.QuoteMeta(domain) + `([^A-Za-z0-9.-]|$)`)
	return re.MatchString(line)
}

func readCertLimits() map[string]time.Time {
	m := map[string]time.Time{}
	if b, err := os.ReadFile(certLimitsFile); err == nil {
		json.Unmarshal(b, &m)
	}
	return m
}

// rateLimitLog is a week of Caddy's rate-limit errors (a var for tests).
var rateLimitLog = func() string {
	out, err := exec.Command("journalctl", "-u", "caddy", "--since", "-7d", "--grep", "rateLimited", "-o", "cat", "--no-pager").Output()
	if err != nil { // a journalctl without --grep
		out, _ = exec.Command("journalctl", "-u", "caddy", "--since", "-6h", "-o", "cat", "--no-pager").Output()
	}
	return string(out)
}

var limitsCache struct {
	sync.Mutex
	at time.Time
	m  map[string]time.Time
}

// certLimits is every configured wildcard domain that has hit the limit, with when Let's Encrypt allows the next
// try. Reads the journal at most once a minute.
func certLimits() map[string]time.Time {
	limitsCache.Lock()
	defer limitsCache.Unlock()
	if time.Since(limitsCache.at) < time.Minute && limitsCache.m != nil {
		return limitsCache.m
	}
	c := loadCerts()
	var domains []string
	for _, w := range c.Wildcards {
		domains = append(domains, w.Domain)
	}
	stored := readCertLimits()
	before, _ := json.Marshal(stored)
	for d, at := range limitsFromLog(rateLimitLog(), domains) {
		if at.After(stored[d]) {
			stored[d] = at
		}
	}
	all := allWildcards(c)
	for d := range stored {
		if _, ok := c.find(d); !ok || allIssuedUnder(d, all) {
			delete(stored, d)
		}
	}
	if after, _ := json.Marshal(stored); string(after) != string(before) {
		config.WriteAtomic(certLimitsFile, after, 0o644)
	}
	limitsCache.at, limitsCache.m = time.Now(), stored
	return stored
}

func allIssuedUnder(domain string, all []wildcard) bool {
	for _, w := range all {
		if w.Domain == domain || strings.HasSuffix(w.Domain, "."+domain) {
			if ok, _ := wildcardIssued(w.Domain); !ok {
				return false
			}
		}
	}
	return true
}
