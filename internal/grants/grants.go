// Package grants decides which hostnames an app may serve, and where its own public ports are. Grants are
// "app kind name [ports]" lines:
//
//	domain client.com   client.com and every *.client.com
//	sub    client.com   *.client.com only
//	host   blog.client.com  exactly that hostname
//	port-domain client.com 25,465,587,993   those ports, for client.com: the app may list the DNS records the
//	                    domain needs for them (MX, TXT, SRV), but serves no website there
//	port-host mqtt.client.com 8883   those ports, at that hostname: its A record, and Caddy's certificate for it
//
// Port grants are what nook knows about an app's ports; nothing about them is specific to mail or anything else.
package grants

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/sparkstonepdx/nook/internal/names"
)

type Grant struct{ App, Kind, Name, Ports string }

func (g Grant) String() string {
	if g.Ports != "" {
		return g.App + " " + g.Kind + " " + g.Name + " " + g.Ports
	}
	return g.App + " " + g.Kind + " " + g.Name
}

// IsPorts says the grant is for public ports (port-domain, port-host), not a website.
func (g Grant) IsPorts() bool { return g.Kind == "port-domain" || g.Kind == "port-host" }

// Label is how the dashboard and CLI show a grant.
func (g Grant) Label() string {
	switch g.Kind {
	case "domain":
		return g.Name + " + *." + g.Name
	case "sub":
		return "*." + g.Name
	case "port-domain", "port-host":
		return g.Name + " ports " + strings.ReplaceAll(g.Ports, ",", ", ")
	}
	return g.Name
}

func Valid(g Grant) error {
	if !names.App(g.App) {
		return fmt.Errorf("invalid app name %q", g.App)
	}
	switch g.Kind {
	case "domain", "sub", "host":
		if g.Ports != "" {
			return fmt.Errorf("a %s grant has no ports", g.Kind)
		}
	case "port-domain", "port-host":
		if g.Ports == "" {
			return fmt.Errorf("a %s grant needs its ports, like 25,993", g.Kind)
		}
		for _, p := range strings.Split(g.Ports, ",") {
			if p == "" || strings.Trim(p, "0123456789") != "" || len(p) > 5 {
				return fmt.Errorf("ports are numbers separated by commas, like 25,993, not %q", g.Ports)
			}
		}
	default:
		return fmt.Errorf("kind must be domain, sub, host, port-domain or port-host")
	}
	if !names.Host(g.Name) {
		return fmt.Errorf("not a valid domain: %q", g.Name)
	}
	return nil
}

type List []Grant

func Parse(text string) List {
	var out List
	for _, line := range strings.Split(text, "\n") {
		f := strings.Fields(line)
		switch len(f) {
		case 3:
			out = append(out, Grant{f[0], f[1], f[2], ""})
		case 4:
			out = append(out, Grant{f[0], f[1], f[2], f[3]})
		}
	}
	return out
}

func Load(path string) (List, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	return Parse(string(b)), err
}

func (l List) Text() string {
	var b strings.Builder
	for _, g := range l {
		b.WriteString(g.String() + "\n")
	}
	return b.String()
}

func (l List) For(app string) List {
	var out List
	for _, g := range l {
		if g.App == app {
			out = append(out, g)
		}
	}
	return out
}

func (l List) Has(g Grant) bool {
	for _, x := range l {
		if x == g {
			return true
		}
	}
	return false
}

func (l List) Without(g Grant) List {
	var out List
	for _, x := range l {
		if x != g {
			out = append(out, x)
		}
	}
	return out
}

// Allowed reports whether app may serve host.
func (l List) Allowed(app, host string) bool {
	for _, g := range l.For(app) {
		sfx := "." + g.Name
		switch g.Kind {
		case "host":
			if host == g.Name {
				return true
			}
		case "domain":
			if host == g.Name || strings.HasSuffix(host, sfx) {
				return true
			}
		case "sub":
			if strings.HasSuffix(host, sfx) && len(host) > len(sfx) {
				return true
			}
		}
	}
	return false
}

// IsApex reports whether host is a root domain the app owns outright (a domain or host grant naming it).
func (l List) IsApex(app, host string) bool {
	for _, g := range l.For(app) {
		if g.Name == host && (g.Kind == "domain" || g.Kind == "host") {
			return true
		}
	}
	return false
}

// Apps returns the app names that appear in the list.
func (l List) Apps() []string {
	seen := map[string]bool{}
	var out []string
	for _, g := range l {
		if !seen[g.App] {
			seen[g.App] = true
			out = append(out, g.App)
		}
	}
	sort.Strings(out)
	return out
}

// PortDomains is the domains app has port grants on, in the order they were granted.
func (l List) PortDomains(app string) []string { return l.portNames(app, "port-domain") }

// PortHosts is the hostnames app has port grants at.
func (l List) PortHosts(app string) []string { return l.portNames(app, "port-host") }

func (l List) portNames(app, kind string) []string {
	var out []string
	for _, g := range l.For(app) {
		if g.Kind == kind {
			out = append(out, g.Name)
		}
	}
	return out
}

// Find is app's grant of kind for name, if there is one (to replace its ports).
func (l List) Find(app, kind, name string) (Grant, bool) {
	for _, g := range l {
		if g.App == app && g.Kind == kind && g.Name == name {
			return g, true
		}
	}
	return Grant{}, false
}
