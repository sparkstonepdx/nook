package core

// Certificates for an app's own TLS ports (#95 step 3). Mail listens on 465, 587 and 993 itself, not through
// Caddy, but it needs the same certificate Caddy got for its hostname. A site's nook.json "tls": true hands it
// over: the unit loads Caddy's certificate and key as systemd credentials, tls.crt and tls.key in
// $CREDENTIALS_DIRECTORY, readable only by that service. Nothing is copied into the app's folder (or its backups),
// there's one ACME account and no second port-80 listener.
//
// systemd reads the files when the service starts, so when Caddy renews, the helper restarts it (it looks every 2
// minutes; renewal is a month ahead). Until Caddy has a certificate for the host, the unit starts without one and
// the site picks it up as soon as it's issued.

import (
	"crypto/x509"
	"encoding/pem"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// tlsFiles is where Caddy keeps the certificate that covers host: its wildcard's once that's issued, else host's
// own. The newest unexpired one when more than one issuer has it. "" when there's none yet.
func tlsFiles(host string) (crt, key string) {
	names := []string{host}
	if _, dom, ok := underWildcard(host, allWildcards(loadCerts())); ok {
		names = append([]string{"wildcard_." + dom}, names...)
	}
	for _, name := range names {
		files, _ := filepath.Glob(filepath.Join(caddyCertsDir, "*", name, name+".crt"))
		var best string
		var bestUntil time.Time
		for _, f := range files {
			if until, ok := certUntil(f); ok && until.After(bestUntil) {
				if _, err := os.Stat(strings.TrimSuffix(f, ".crt") + ".key"); err == nil {
					best, bestUntil = f, until
				}
			}
		}
		if best != "" {
			return best, strings.TrimSuffix(best, ".crt") + ".key"
		}
	}
	return "", ""
}

func certUntil(f string) (time.Time, bool) {
	b, err := os.ReadFile(f)
	if err != nil {
		return time.Time{}, false
	}
	blk, _ := pem.Decode(b)
	if blk == nil {
		return time.Time{}, false
	}
	c, err := x509.ParseCertificate(blk.Bytes)
	if err != nil || time.Now().After(c.NotAfter) {
		return time.Time{}, false
	}
	return c.NotAfter, true
}

// tlsLines is the unit's lines for a site with "tls": a marker the helper looks for, and the credentials once
// Caddy has a certificate.
func tlsLines(host string) string {
	if host == "" {
		return ""
	}
	out := "X-Nook-TLS=" + host + "\n"
	if crt, key := tlsFiles(host); crt != "" {
		out += "LoadCredential=tls.crt:" + crt + "\nLoadCredential=tls.key:" + key + "\n"
	}
	return out
}

// unitTLS reads back what a unit has: the host it asked for, and the certificate file it loads ("" if none yet).
func unitTLS(text string) (host, crt string) {
	for _, line := range strings.Split(text, "\n") {
		if v, ok := strings.CutPrefix(line, "X-Nook-TLS="); ok {
			host = v
		}
		if v, ok := strings.CutPrefix(line, "LoadCredential=tls.crt:"); ok {
			crt = v
		}
	}
	return host, crt
}

// refreshAppTLS restarts a site whose certificate was renewed since it started, and syncs an app whose site was
// waiting for its first certificate (or moved onto a wildcard). It says what it did.
func refreshAppTLS(h *Host) []string {
	units, _ := filepath.Glob(filepath.Join(unitsDir, "nook-site-*.service"))
	sort.Strings(units)
	var did []string
	resync := map[string]bool{}
	for _, path := range units {
		b, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		host, crt := unitTLS(string(b))
		if host == "" {
			continue
		}
		unit := filepath.Base(path)
		app, _, _ := strings.Cut(strings.TrimSuffix(strings.TrimPrefix(unit, "nook-site-"), ".service"), "--")
		want, _ := tlsFiles(host)
		if want != crt {
			resync[app] = true
			continue
		}
		st, err := os.Stat(crt)
		if err != nil {
			continue
		}
		if started, ok := unitStarted(unit); ok && st.ModTime().After(started) {
			restartUnit(unit)
			did = append(did, "restarted "+unit+": "+host+"'s certificate was renewed")
		}
	}
	apps := make([]string, 0, len(resync))
	for a := range resync {
		apps = append(apps, a)
	}
	sort.Strings(apps)
	for _, a := range apps {
		if err := h.Sync(a); err == nil {
			did = append(did, "synced "+a+": its certificate changed")
		}
	}
	return did
}

// unitsDir is where the site units are; a var for tests.
var unitsDir = "/etc/systemd/system"

// restartUnit is a var for tests.
var restartUnit = func(unit string) { systemctl("restart", unit) }

// unitStarted is when a running unit last started.
var unitStarted = func(unit string) (time.Time, bool) {
	out, err := Run("systemctl", "show", "--timestamp=unix", "-p", "ActiveState,ActiveEnterTimestamp", unit)
	if err != nil || !strings.Contains(out, "ActiveState=active\n") {
		return time.Time{}, false
	}
	for _, line := range strings.Split(out, "\n") {
		if v, ok := strings.CutPrefix(line, "ActiveEnterTimestamp=@"); ok {
			if n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil {
				return time.Unix(n, 0), true
			}
		}
	}
	return time.Time{}, false
}

// WatchAppTLS runs refreshAppTLS every 2 minutes, from the helper.
func WatchAppTLS() {
	for {
		time.Sleep(2 * time.Minute)
		refreshAppTLS(New(io.Discard))
	}
}
