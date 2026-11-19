package core

// DNS records an app's public ports need (#95 step 2). A port-domain grant ("port-domain client.com 25,993") grants
// ports for a domain without serving a website there; a port-host grant ("port-host mqtt.client.com 8883") for a
// hostname, which gets its A record and certificate. The app learns them from NOOK_PORT_DOMAINS and
// NOOK_PORT_HOSTS, and a running site's nook.json "dns": "./dns.sh" prints the records they need, which the
// Domains page lists beside the A records, ready to copy. For a mail server:
//
//	MX   client.com                10 mail.client.com.
//	TXT  client.com                v=spf1 mx -all
//	TXT  stalwart._domainkey.client.com  v=DKIM1; k=rsa; p=MIIB...
//	TXT  _dmarc.client.com         v=DMARC1; p=quarantine
//
// One record a line: type, name, then the value (the rest of the line). Only MX, TXT, CNAME and SRV, and only for
// names in the app's port domains and hosts: an app can't suggest records for a domain it wasn't given. The script runs as
// the app's user, with the site's env, for up to 5 seconds each time the page loads.

import (
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sparkstonepdx/nook/internal/grants"
	"github.com/sparkstonepdx/nook/internal/names"
)

type DNSRecord struct {
	App   string `json:"app"`
	Type  string `json:"type"`
	Name  string `json:"name"`
	Value string `json:"value"`
}

var dnsTypes = map[string]bool{"MX": true, "TXT": true, "CNAME": true, "SRV": true}

// parseDNSRecords reads a dns hook's output, keeping the records for names in domains. It says what it dropped.
func parseDNSRecords(app, out string, domains []string) (recs []DNSRecord, dropped []string) {
	inDomains := func(name string) bool {
		for _, d := range domains {
			if name == d || strings.HasSuffix(name, "."+d) {
				return true
			}
		}
		return false
	}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Fields(line)
		if len(f) < 3 || len(recs) >= 50 {
			dropped = append(dropped, line)
			continue
		}
		typ, name := strings.ToUpper(f[0]), strings.ToLower(strings.TrimSuffix(f[1], "."))
		rest := strings.TrimSpace(line[len(f[0]):])
		value := strings.TrimSpace(rest[len(f[1]):])
		labels := strings.TrimPrefix(name, "_")
		if !dnsTypes[typ] || !inDomains(name) || !names.Host(strings.ReplaceAll(labels, "._", ".")) || len(value) > 2048 || strings.ContainsAny(value, "\x00\r") {
			dropped = append(dropped, line)
			continue
		}
		recs = append(recs, DNSRecord{App: app, Type: typ, Name: name, Value: value})
	}
	return recs, dropped
}

// runDNSHook is a var for tests: the site's dns script's output.
var runDNSHook = func(app, site, run string, domains, hosts []string) (string, error) {
	u, err := LookupApp(app)
	if err != nil {
		return "", err
	}
	extra := map[string]string{"NOOK_PORT_DOMAINS": strings.Join(domains, ","), "NOOK_PORT_HOSTS": strings.Join(hosts, ",")}
	if port := PortOf(app, site); port > 0 {
		extra["PORT"] = strconv.Itoa(port)
	}
	return runSiteHook(u, site, run, nil, extra, 5*time.Second)
}

// dnsHooks is each running site with a dns script, by app; a var for tests.
var dnsHooks = func(app string) map[string]string {
	out := map[string]string{}
	u, err := LookupApp(app)
	if err != nil {
		return out
	}
	for _, s := range RunningSites(app) {
		if m, err := u.readManifest(s); err == nil && m.DNS != "" {
			out[s] = m.DNS
		}
	}
	return out
}

// appDNSRecords asks every app with port grants for the records its domains and hosts need, in parallel.
func appDNSRecords(gl grants.List) []DNSRecord {
	var mu sync.Mutex
	var wg sync.WaitGroup
	var all []DNSRecord
	for _, app := range gl.Apps() {
		domains, hosts := gl.PortDomains(app), gl.PortHosts(app)
		if len(domains)+len(hosts) == 0 {
			continue
		}
		allowed := append(append([]string{}, domains...), hosts...)
		for site, run := range dnsHooks(app) {
			wg.Add(1)
			go func(app, site, run string) {
				defer wg.Done()
				out, err := runDNSHook(app, site, run, domains, hosts)
				if err != nil {
					return
				}
				recs, _ := parseDNSRecords(app, out, allowed)
				mu.Lock()
				all = append(all, recs...)
				mu.Unlock()
			}(app, site, run)
		}
	}
	wg.Wait()
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].Name != all[j].Name {
			return all[i].Name < all[j].Name
		}
		return all[i].Type < all[j].Type
	})
	return all
}
