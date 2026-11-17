package core

// Versions and provisioning for services (#42 step 1).
//
//	needs:    "postgres", "postgres>=15", "php@8.3" (8.3.x), "frankenphp@1" (1.x), "notify?" (optional: used if bound)
//	provides: "version": "17", "provision": "./provision.sh", "deprovision": "./deprovision.sh"
//
// Provision runs as the provider's user in its site folder with the consumer's name as its argument and in
// NOOK_CONSUMER, NOOK_CONSUMER_USER (its OS user: Postgres peer auth logs it in as that) and NOOK_CONSUMER_UID.
// It prints KEY=value lines for the interface's env names; they go into the consumer's services.env.

import (
	"bufio"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/sparkstonepdx/nook/internal/envfile"
)

var (
	needRE    = regexp.MustCompile(`^([a-z][a-z0-9-]{0,30})(?:(@|>=)(\d{1,4}(?:\.\d{1,4}){0,2}))?(\?)?$`)
	versionRE = regexp.MustCompile(`^\d{1,4}(\.\d{1,4}){0,2}$`)
)

// need is one entry of "needs". Optional ones ("notify?") are used when bound and never asked for: the app works
// without them.
type need struct {
	Name, Op, Version string
	Optional          bool
}

func parseNeed(s string) (need, bool) {
	m := needRE.FindStringSubmatch(s)
	if m == nil {
		return need{}, false
	}
	return need{m[1], m[2], m[3], m[4] == "?"}, true
}

func (n need) String() string { return n.Name + n.Op + n.Version }

// accepts says whether a provider at version v meets n. A constraint needs a stated version.
func (n need) accepts(v string) bool {
	switch n.Op {
	case "":
		return true
	case ">=":
		return v != "" && versionCmp(v, n.Version) >= 0
	case "@": // same release line: 8.3 takes 8.3 and 8.3.12, not 8.30
		return v == n.Version || strings.HasPrefix(v, n.Version+".")
	}
	return false
}

func acceptsAll(ns []need, v string) bool {
	for _, n := range ns {
		if !n.accepts(v) {
			return false
		}
	}
	return true
}

// versionCmp compares dotted numbers; "" is lower than any version, and 8.3 equals 8.3.0.
func versionCmp(a, b string) int {
	switch {
	case a == b:
		return 0
	case a == "":
		return -1
	case b == "":
		return 1
	}
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < max(len(pa), len(pb)); i++ {
		x, y := 0, 0
		if i < len(pa) {
			x, _ = strconv.Atoi(pa[i])
		}
		if i < len(pb) {
			y, _ = strconv.Atoi(pb[i])
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}

// consumerNeeds is every constraint app's sites put on iface (bare names say nothing and are left out).
func consumerNeeds(app, iface string) []need {
	var out []need
	seen := map[string]bool{}
	for _, needs := range readAppServices(app).Needs {
		for _, s := range needs {
			if n, ok := parseNeed(s); ok && n.Name == iface && n.Op != "" && !seen[s] {
				seen[s] = true
				out = append(out, n)
			}
		}
	}
	return out
}

func needsText(iface string, ns []need) string {
	if len(ns) == 0 {
		return iface
	}
	var parts []string
	for _, n := range ns {
		parts = append(parts, n.Op+n.Version)
	}
	return iface + strings.Join(parts, ",")
}

// provision runs p's provision hook for b's consumer and returns the values it printed.
func provision(p Provider, b Binding) (map[string]string, error) {
	out, err := runProviderHook(p.app, p.site, p.spec.Provision, b)
	if err != nil {
		return nil, err
	}
	allowed := map[string]bool{}
	for _, k := range interfaceEnv(p) {
		allowed[k] = true
	}
	vals := map[string]string{}
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok || !allowed[k] {
			return nil, fmt.Errorf("it printed %q: only KEY=value lines for %s", line, strings.Join(interfaceEnv(p), ", "))
		}
		if err := envfile.Check(k, v); err != nil {
			return nil, fmt.Errorf("%s: %v", k, err)
		}
		vals[k] = v
	}
	return vals, nil
}

// deprovision runs the provider's deprovision hook for a binding that's going. A failure is said, not fatal:
// the binding goes either way.
func (h *Host) deprovision(b Binding) {
	papp, psite := splitAppProvider(b.Provider)
	spec, ok := readAppServices(papp).Provides[psite][b.Interface]
	if !ok || spec.Deprovision == "" {
		return
	}
	if _, err := runProviderHook(papp, psite, spec.Deprovision, b); err != nil {
		h.say("%s couldn't let %s go: %v", b.Provider, b.App, err)
	}
}

var runProviderHook = func(app, site, run string, b Binding) (string, error) {
	u, err := LookupApp(app)
	if err != nil {
		return "", err
	}
	extra := map[string]string{"NOOK_CONSUMER": b.App, "NOOK_CONSUMER_USER": "nook-" + b.App, "NOOK_INTERFACE": b.Interface}
	if c, err := LookupApp(b.App); err == nil {
		extra["NOOK_CONSUMER_UID"] = strconv.Itoa(c.UID)
	}
	return runSiteHook(u, site, run, []string{b.App}, extra, 2*time.Minute)
}
