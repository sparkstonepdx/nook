// Package grants decides which hostnames an app may serve. Grants are "app kind name" lines:
//
//	domain client.com   client.com and every *.client.com
//	sub    client.com   *.client.com only
//	host   blog.client.com  exactly that hostname
package grants

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/sparkstonepdx/app-host/internal/names"
)

type Grant struct{ App, Kind, Name string }

func (g Grant) String() string { return g.App + " " + g.Kind + " " + g.Name }

// Label is how the dashboard and CLI show a grant.
func (g Grant) Label() string {
	switch g.Kind {
	case "domain":
		return g.Name + " + *." + g.Name
	case "sub":
		return "*." + g.Name
	}
	return g.Name
}

func Valid(g Grant) error {
	if !names.App(g.App) {
		return fmt.Errorf("invalid app name %q", g.App)
	}
	if g.Kind != "domain" && g.Kind != "sub" && g.Kind != "host" {
		return fmt.Errorf("kind must be domain, sub or host")
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
		if len(f) == 3 {
			out = append(out, Grant{f[0], f[1], f[2]})
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
