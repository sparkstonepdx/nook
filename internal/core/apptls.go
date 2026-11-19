package core

// Certificates for an app's own TLS ports (#95 step 3). An app listening on public ports itself (a mail server's
// 465 and 993) needs the certificate Caddy gets for the hostname they're at. Two ways in:
//
//   - a web site's nook.json "tls": true: its own hostname, as tls.crt and tls.key
//   - a port-host grant (port-host mail.client.com 993): that hostname, as mail.client.com.crt and .key, for every
//     site of the app that listens on public ports; Caddy keeps a certificate for it even with no site there
//
// The unit loads them as systemd credentials in $CREDENTIALS_DIRECTORY, readable only by that service. Nothing is
// copied into the app's folder (or its backups), there's one ACME account and no second port-80 listener.
//
// systemd reads the files when the service starts, so when Caddy renews, the helper restarts it (it looks every 2
// minutes; renewal is a month ahead). Until Caddy has a certificate for the host, the unit starts without one and
// the site picks it up as soon as it's issued.

import (
	"cmp"
	"crypto/x509"
	"encoding/pem"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/sparkstonepdx/nook/internal/grants"
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

// portHostsFor is the port-host grants a site gets certificates for: all of its app's, when it listens on public
// ports itself.
func portHostsFor(gl grants.List, app string, m Manifest) []string {
	if len(m.Ports) == 0 {
		return nil
	}
	return gl.PortHosts(app)
}

// tlsLines is the unit's lines for the certificates a site gets: own (with "tls", as tls.crt) and each port host's
// (as HOST.crt). A marker per host records the file it loaded ("-" until Caddy has one), for the watcher.
func tlsLines(own string, portHosts []string) string {
	var b strings.Builder
	add := func(host, name string) {
		crt, key := tlsFiles(host)
		b.WriteString("X-Nook-TLS=" + host + " " + cmp.Or(crt, "-") + "\n")
		if crt != "" {
			b.WriteString("LoadCredential=" + name + ".crt:" + crt + "\nLoadCredential=" + name + ".key:" + key + "\n")
		}
	}
	if own != "" {
		add(own, "tls")
	}
	for _, h := range portHosts {
		if h != own {
			add(h, h)
		}
	}
	return b.String()
}

// unitTLS reads back the markers: each host and the certificate file it loaded ("" if none yet).
func unitTLS(text string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		if v, ok := strings.CutPrefix(line, "X-Nook-TLS="); ok {
			host, crt, _ := strings.Cut(v, " ")
			if crt == "-" {
				crt = ""
			}
			out[host] = crt
		}
	}
	return out
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
		loaded := unitTLS(string(b))
		if len(loaded) == 0 {
			continue
		}
		unit := filepath.Base(path)
		app, _, _ := strings.Cut(strings.TrimSuffix(strings.TrimPrefix(unit, "nook-site-"), ".service"), "--")
		var renewed []string
		hostsOf := make([]string, 0, len(loaded))
		for host := range loaded {
			hostsOf = append(hostsOf, host)
		}
		sort.Strings(hostsOf)
		for _, host := range hostsOf {
			crt := loaded[host]
			if want, _ := tlsFiles(host); want != crt {
				resync[app] = true
				continue
			}
			if st, err := os.Stat(crt); err == nil {
				if started, ok := unitStarted(unit); ok && st.ModTime().After(started) {
					renewed = append(renewed, host)
				}
			}
		}
		if len(renewed) > 0 && !resync[app] {
			restartUnit(unit)
			did = append(did, "restarted "+unit+": renewed "+strings.Join(renewed, ", "))
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
