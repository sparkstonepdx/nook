package core

// Public ports (#95): a running site can listen on ports of the server's own address, not through Caddy, the
// way a mail server takes 25, 465, 587 and 993. The site asks in nook.json ("ports": [25, 993]); an admin grants
// them to the app (nook-admin ports APP grant 25,993, or installing a store app that lists them). Only a port
// both asked for and granted is opened to that one unit: SocketBindAllow, and for a port under 1024 the one
// capability that binds it (CAP_NET_BIND_SERVICE). Everything else about the unit is sandboxed as usual.
// A port is granted to one app at a time; nook's own ports and the app port range are never granted.
//
// Ports are granted for a domain or a hostname (port-domain and port-host grants, in the grants file, from the
// Domains page), which also says where they're used: the DNS records a domain needs (dns.go) and the certificate a
// hostname needs (apptls.go). A grant with no place (nook-admin ports APP grant, a store install) still works.

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/sparkstonepdx/nook/internal/config"
	"github.com/sparkstonepdx/nook/internal/grants"
)

// reservedPorts are what the server itself listens on (SSH, Caddy, Caddy's admin API, DNS).
var reservedPorts = map[int]string{22: "SSH", 53: "DNS", 80: "Caddy", 443: "Caddy", 2019: "Caddy's admin API"}

func portsPath(app string) string { return filepath.Join(config.StateDir, app+".ports") }

var grantedPorts = GrantedPorts // tests stand in

// GrantedPorts is the public ports granted to app, with or without a place, in order.
func GrantedPorts(app string) []int {
	have := map[int]bool{}
	for _, p := range unplacedPorts(app) {
		have[p] = true
	}
	gl, _ := grants.Load(config.GrantsFile)
	for _, g := range gl.For(app) {
		if g.IsPorts() {
			ps, _ := ParsePorts(g.Ports)
			for _, p := range ps {
				have[p] = true
			}
		}
	}
	out := make([]int, 0, len(have))
	for p := range have {
		out = append(out, p)
	}
	sort.Ints(out)
	return out
}

// unplacedPorts is the ports granted to app with no domain or hostname (nook-admin ports, store installs).
func unplacedPorts(app string) []int {
	b, err := os.ReadFile(portsPath(app))
	if err != nil {
		return nil
	}
	var out []int
	for _, f := range strings.Fields(string(b)) {
		if n, err := strconv.Atoi(f); err == nil {
			out = append(out, n)
		}
	}
	sort.Ints(out)
	return out
}

// checkPortGrant is a port-domain or port-host grant's ports, checked and in order ("25,465,587,993").
func checkPortGrant(app, ports string) (string, error) {
	ps, err := ParsePorts(ports)
	if err != nil || len(ps) == 0 {
		return "", fmt.Errorf("ports are numbers like 25,993, not %q", ports)
	}
	s := config.Load()
	seen := map[int]bool{}
	var out []int
	for _, p := range ps {
		if err := checkPublicPort(p, s.PortMin, s.PortMax); err != nil {
			return "", err
		}
		if err := portFree(app, p); err != nil {
			return "", err
		}
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	sort.Ints(out)
	var strs []string
	for _, p := range out {
		strs = append(strs, strconv.Itoa(p))
	}
	return strings.Join(strs, ","), nil
}

// portFree says whether p isn't another app's already.
func portFree(app string, p int) error {
	for _, other := range Apps() {
		if other == app {
			continue
		}
		for _, q := range GrantedPorts(other) {
			if q == p {
				return fmt.Errorf("port %d is granted to %s already", p, other)
			}
		}
	}
	return nil
}

func checkPublicPort(p, min, max int) error {
	switch {
	case p < 1 || p > 65535:
		return fmt.Errorf("%d isn't a port", p)
	case reservedPorts[p] != "":
		return fmt.Errorf("port %d is %s's", p, reservedPorts[p])
	case p >= min && p <= max:
		return fmt.Errorf("port %d is in nook's app port range (%d-%d)", p, min, max)
	}
	return nil
}

// ParsePorts reads "25,465 587".
func ParsePorts(s string) ([]int, error) {
	var out []int
	for _, f := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' }) {
		n, err := strconv.Atoi(f)
		if err != nil {
			return nil, fmt.Errorf("%q isn't a port number", f)
		}
		out = append(out, n)
	}
	return out, nil
}

// GrantPorts adds ports to app's grant (or with revoke, takes them away) and republishes the app.
func (h *Host) GrantPorts(app string, ports []int, revoke bool) error {
	if _, err := LookupApp(app); err != nil {
		return err
	}
	have := map[int]bool{}
	for _, p := range unplacedPorts(app) {
		have[p] = true
	}
	s := config.Load()
	for _, p := range ports {
		if revoke {
			delete(have, p)
			continue
		}
		if err := checkPublicPort(p, s.PortMin, s.PortMax); err != nil {
			return err
		}
		if err := portFree(app, p); err != nil {
			return err
		}
		have[p] = true
	}
	if err := writeGrantedPorts(app, have); err != nil {
		return err
	}
	if g := GrantedPorts(app); len(g) > 0 {
		h.say("%s may listen publicly on %s", app, portList(g))
	} else {
		h.say("%s has no public ports", app)
	}
	return h.Sync(app)
}

func writeGrantedPorts(app string, ports map[int]bool) error {
	if len(ports) == 0 {
		if err := os.Remove(portsPath(app)); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	var ps []string
	for p := range ports {
		ps = append(ps, strconv.Itoa(p))
	}
	sort.Strings(ps)
	os.MkdirAll(config.StateDir, 0o700)
	return config.WriteAtomic(portsPath(app), []byte(strings.Join(ps, "\n")+"\n"), 0o600)
}

func portList(ports []int) string {
	var s []string
	for _, p := range ports {
		s = append(s, strconv.Itoa(p))
	}
	return strings.Join(s, ", ")
}

// openPorts is the ports a site may listen on: asked for in its nook.json and granted to the app, and not taken
// by another of the app's sites this sync (taken). It says what's asked for and not granted.
func (h *Host) openPorts(app, site string, asked []int, taken map[int]string, missingAll map[int]bool) []int {
	granted := map[int]bool{}
	for _, p := range grantedPorts(app) {
		granted[p] = true
	}
	var out, missing []int
	for _, p := range asked {
		switch {
		case !granted[p]:
			missing = append(missing, p)
		case taken[p] != "" && taken[p] != site:
			h.say("%s: %s: port %d is %s's already", app, site, p, taken[p])
		default:
			taken[p] = site
			out = append(out, p)
		}
	}
	for _, p := range missing {
		missingAll[p] = true
	}
	if len(missing) > 0 {
		h.say("%s: %s asks to listen publicly on %s; an admin can allow it: nook-admin ports %s grant %s",
			app, site, portList(missing), app, strings.ReplaceAll(portList(missing), " ", ""))
	}
	sort.Ints(out)
	return out
}

// withPublicPorts adds the ports' rules to a unit's sandbox lines.
func withPublicPorts(sandbox string, ports []int) string {
	if len(ports) == 0 {
		return sandbox
	}
	bind, caps := publicPortRules(ports)
	sandbox = strings.Replace(sandbox, "SocketBindDeny=any\n", bind+"SocketBindDeny=any\n", 1)
	if caps != "" {
		sandbox = strings.Replace(sandbox, "CapabilityBoundingSet=\n", caps, 1)
	}
	return sandbox
}

func askedPath(app string) string { return filepath.Join(config.StateDir, app+".ports-asked") }

// askedPorts is the public ports app's sites asked for at the last sync and weren't granted.
func askedPorts(app string) []int {
	b, _ := os.ReadFile(askedPath(app))
	ps, _ := ParsePorts(string(b))
	return ps
}

func recordAsked(app string, missing map[int]bool) {
	if len(missing) == 0 {
		os.Remove(askedPath(app))
		return
	}
	var ps []int
	for p := range missing {
		ps = append(ps, p)
	}
	sort.Ints(ps)
	config.WriteAtomic(askedPath(app), []byte(portList(ps)+"\n"), 0o600)
}

func orEmpty(ps []int) []int {
	if ps == nil {
		return []int{}
	}
	return ps
}

// publicPortRules is the unit lines that let it listen on ports, with the capability only if one needs it.
func publicPortRules(ports []int) (bind, caps string) {
	low := false
	for _, p := range ports {
		bind += fmt.Sprintf("SocketBindAllow=tcp:%d\n", p)
		low = low || p < 1024
	}
	if low {
		caps = "AmbientCapabilities=CAP_NET_BIND_SERVICE\nCapabilityBoundingSet=CAP_NET_BIND_SERVICE\n"
	}
	return bind, caps
}
