package core

// Shared services (#12). An app asks for a service by interface name (nook.json "needs"); the admin binds it
// to a provider; the app sees only conventional env vars. Interfaces fix the env names, providers fill the
// values, and a binding may rename them for an app that uses its own names.
//
//	providers: "app:APP/SITE"        an app's service site whose nook.json says "provides": {"alerts": {...}}
//	state:     /etc/nook/bindings.json, /etc/nook/state/APP.services.json (needs/provides seen by sync)
//	env:       /etc/nook/env/APP/services.env (consumer, read-only scope), .../APP/provider/SITE.env (provider)

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/sparkstonepdx/nook/internal/config"
	"github.com/sparkstonepdx/nook/internal/envfile"
	"github.com/sparkstonepdx/nook/internal/names"
)

// Interface is a service contract: the env names a consumer gets.
type Interface struct {
	Name string   `json:"name"`
	Desc string   `json:"desc"`
	Env  []string `json:"env"`
}

// Known interfaces use the names their ecosystems already use. Providers of these must fill exactly these.
var Known = map[string]Interface{
	"postgres":  {"postgres", "a private PostgreSQL database", []string{"DATABASE_URL"}},
	"email":     {"email", "send email as the app's domains", []string{"SMTP_URL"}},
	"alerts":    {"alerts", "POST {title, body, level} to raise an alert", []string{"ALERT_URL", "ALERT_TOKEN"}},
	"analytics": {"analytics", "page-view analytics", []string{"ANALYTICS_URL", "ANALYTICS_TOKEN"}},
	"s3":        {"s3", "S3-compatible object storage", []string{"S3_ENDPOINT", "S3_BUCKET", "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY"}},
}

// ProvideSpec is what a provider app declares for one interface: env templates using {url} {host} {port} {token}.
type ProvideSpec struct {
	Desc string            `json:"desc,omitempty"`
	Env  map[string]string `json:"env"`
}

var (
	ifaceRE    = regexp.MustCompile(`^[a-z][a-z0-9-]{0,30}$`)
	templateRE = regexp.MustCompile(`^[A-Za-z0-9 _./:@?=&%+,{}-]{0,500}$`)
	envNameRE  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)
)

func checkServices(m *Manifest) error {
	if len(m.Needs) > 20 || len(m.Provides) > 20 {
		return fmt.Errorf("nook.json: too many needs/provides")
	}
	for _, n := range m.Needs {
		if !ifaceRE.MatchString(n) {
			return fmt.Errorf("nook.json: bad service name %q in needs", n)
		}
	}
	if len(m.Provides) > 0 && m.Kind != "service" {
		return fmt.Errorf("nook.json: only a service (\"kind\": \"service\") can provide services")
	}
	for name, spec := range m.Provides {
		if !ifaceRE.MatchString(name) {
			return fmt.Errorf("nook.json: bad or reserved interface name %q in provides", name)
		}
		if len(spec.Env) == 0 || len(spec.Env) > 20 {
			return fmt.Errorf("nook.json: provides %q needs 1-20 env entries", name)
		}
		for k, v := range spec.Env {
			if !envNameRE.MatchString(k) || names.ReservedEnv[k] || !templateRE.MatchString(v) {
				return fmt.Errorf("nook.json: provides %q: bad env %s", name, k)
			}
		}
		if known, ok := Known[name]; ok {
			if len(spec.Env) != len(known.Env) {
				return fmt.Errorf("nook.json: %q must provide exactly %s", name, strings.Join(known.Env, ", "))
			}
			for _, k := range known.Env {
				if _, ok := spec.Env[k]; !ok {
					return fmt.Errorf("nook.json: %q must provide exactly %s", name, strings.Join(known.Env, ", "))
				}
			}
		}
		if len(spec.Desc) > 200 {
			spec.Desc = spec.Desc[:200]
			m.Provides[name] = spec
		}
	}
	return nil
}

// ---- what sync saw ----

type appServices struct {
	Needs    map[string][]string               `json:"needs"`    // site -> interfaces
	Provides map[string]map[string]ProvideSpec `json:"provides"` // site -> interface -> spec
}

func servicesStatePath(app string) string {
	return filepath.Join(config.StateDir, app+".services.json")
}

func readAppServices(app string) appServices {
	var a appServices
	if b, err := os.ReadFile(servicesStatePath(app)); err == nil {
		json.Unmarshal(b, &a)
	}
	if a.Needs == nil {
		a.Needs = map[string][]string{}
	}
	if a.Provides == nil {
		a.Provides = map[string]map[string]ProvideSpec{}
	}
	return a
}

// recordServices stores what sync found; returns true when a provider's offer changed.
func recordServices(app string, a appServices) bool {
	old := readAppServices(app)
	b, _ := json.Marshal(a)
	ob, _ := json.Marshal(old)
	if string(b) == string(ob) {
		return false
	}
	config.WriteAtomic(servicesStatePath(app), b, 0o600)
	pb, _ := json.Marshal(a.Provides)
	opb, _ := json.Marshal(old.Provides)
	return string(pb) != string(opb)
}

// ---- bindings ----

type Binding struct {
	App       string            `json:"app"`       // consumer
	Interface string            `json:"interface"` // e.g. postgres, alerts
	Provider  string            `json:"provider"`  // "app:APP/SITE"
	As        map[string]string `json:"as,omitempty"`
	Token     string            `json:"token,omitempty"`
	Created   int64             `json:"created"`
}

func readBindings() []Binding {
	var bs []Binding
	if b, err := os.ReadFile(config.BindingsFile); err == nil {
		json.Unmarshal(b, &bs)
	}
	return bs
}

func writeBindings(bs []Binding) error {
	sort.Slice(bs, func(i, j int) bool {
		if bs[i].App != bs[j].App {
			return bs[i].App < bs[j].App
		}
		return bs[i].Interface < bs[j].Interface
	})
	b, _ := json.MarshalIndent(bs, "", "  ")
	return config.WriteAtomic(config.BindingsFile, b, 0o600)
}

// Provider is one place an interface can come from.
type Provider struct {
	ID        string            `json:"id"`
	Interface string            `json:"interface"`
	Desc      string            `json:"desc"`
	Ready     bool              `json:"ready"`
	Note      string            `json:"note,omitempty"`
	Env       map[string]string `json:"-"`
	app, site string
	port      int
}

// Providers lists every provider available now.
func Providers() []Provider {
	var out []Provider
	for _, app := range Apps() {
		for site, offers := range readAppServices(app).Provides {
			port := PortOf(app, site)
			for iface, spec := range offers {
				desc := spec.Desc
				if desc == "" {
					if k, ok := Known[iface]; ok {
						desc = k.Desc
					}
				}
				p := Provider{ID: "app:" + app + "/" + site, Interface: iface, Desc: desc, Ready: port > 0, Env: spec.Env, app: app, site: site, port: port}
				if port == 0 {
					p.Note = "not running"
				}
				out = append(out, p)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Interface+out[i].ID < out[j].Interface+out[j].ID })
	return out
}

func findProvider(id, iface string) (Provider, bool) {
	for _, p := range Providers() {
		if p.ID == id && p.Interface == iface {
			return p, true
		}
	}
	return Provider{}, false
}

// InterfaceEnv is the env names a binding of iface from provider p gives the consumer (before renames).
func interfaceEnv(p Provider) []string {
	if k, ok := Known[p.Interface]; ok {
		return k.Env
	}
	var ks []string
	for k := range p.Env {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

// Bind connects app to provider for iface. as renames env vars ("DATABASE_URL" -> "PG_URL").
func (h *Host) Bind(app, iface, providerID string, as map[string]string) error {
	if _, err := LookupApp(app); err != nil {
		return err
	}
	if !ifaceRE.MatchString(iface) {
		return fmt.Errorf("bad service name %q", iface)
	}
	if providerID == "" {
		providerID = h.defaultProvider(iface)
		if providerID == "" {
			return fmt.Errorf("nothing provides %q yet", iface)
		}
	}
	p, ok := findProvider(providerID, iface)
	if !ok {
		return fmt.Errorf("%s doesn't provide %q", providerID, iface)
	}
	if p.app == app {
		return fmt.Errorf("an app can't bind to its own service; it can reach it already")
	}
	names := map[string]bool{}
	for _, k := range interfaceEnv(p) {
		names[k] = true
	}
	for from, to := range as {
		if !names[from] {
			return fmt.Errorf("%q isn't one of %s's env names", from, iface)
		}
		if !envNameRE.MatchString(to) || envfile.Check(to, "") != nil {
			return fmt.Errorf("can't rename to %q", to)
		}
	}
	unlock, err := Lock()
	if err != nil {
		return err
	}
	bs := readBindings()
	var keep []Binding
	for _, b := range bs {
		if !(b.App == app && b.Interface == iface) {
			keep = append(keep, b)
		}
	}
	b := Binding{App: app, Interface: iface, Provider: providerID, As: as, Created: time.Now().Unix()}
	raw := make([]byte, 24)
	rand.Read(raw)
	b.Token = base64.RawURLEncoding.EncodeToString(raw)
	if err := writeBindings(append(keep, b)); err != nil {
		unlock()
		return err
	}
	unlock()
	h.say("bound %s to %s from %s", app, iface, providerID)
	return h.applyBindings(app, p.app, p.site)
}

// Unbind removes app's binding for iface. Data (a database) stays; only the connection goes.
func (h *Host) Unbind(app, iface string) error {
	unlock, err := Lock()
	if err != nil {
		return err
	}
	var keep []Binding
	var gone *Binding
	for _, b := range readBindings() {
		if b.App == app && b.Interface == iface {
			bb := b
			gone = &bb
			continue
		}
		keep = append(keep, b)
	}
	if gone == nil {
		unlock()
		return fmt.Errorf("%s has no %s binding", app, iface)
	}
	err = writeBindings(keep)
	unlock()
	if err != nil {
		return err
	}
	h.say("unbound %s from %s (%s)", app, iface, gone.Provider)
	papp, psite := splitAppProvider(gone.Provider)
	return h.applyBindings(app, papp, psite)
}

func splitAppProvider(id string) (string, string) {
	rest, ok := strings.CutPrefix(id, "app:")
	if !ok {
		return "", ""
	}
	a, s, _ := strings.Cut(rest, "/")
	return a, s
}

func (h *Host) defaultProvider(iface string) string {
	var ready []string
	for _, p := range Providers() {
		if p.Interface == iface && p.Ready {
			ready = append(ready, p.ID)
		}
	}
	if len(ready) == 1 {
		return ready[0]
	}
	return ""
}

// applyBindings rewrites the consumer's services.env and the provider's client list, refreshes the
// firewall, and restarts what loads them.
func (h *Host) applyBindings(consumer, provApp, provSite string) error {
	if err := writeConsumerEnv(consumer); err != nil {
		return err
	}
	if provApp != "" {
		writeProviderEnv(provApp, provSite)
	}
	applyFirewall()
	h.restartSites(consumer, RunningSites(consumer))
	if provApp != "" && PortOf(provApp, provSite) > 0 {
		h.restartSites(provApp, []string{provSite})
	}
	return nil
}

func servicesEnvPath(app string) string { return filepath.Join(config.EnvDir, app, "services.env") }
func providerEnvPath(app, site string) string {
	return filepath.Join(config.EnvDir, app, "provider", site+".env")
}

func writeConsumerEnv(app string) error {
	v := envfile.Parse("")
	for _, b := range readBindings() {
		if b.App != app {
			continue
		}
		for k, val := range bindingValues(b) {
			if to, ok := b.As[k]; ok {
				k = to
			}
			v.Set(k, val)
		}
	}
	path := servicesEnvPath(app)
	if v.Len() == 0 {
		os.Remove(path)
		return nil
	}
	os.MkdirAll(filepath.Dir(path), 0o700)
	return config.WriteAtomic(path, []byte(envfile.Format(v, "managed by nook from service bindings; change them on the dashboard's Services page")), 0o600)
}

// bindingValues fills in a binding's env (before renames).
func bindingValues(b Binding) map[string]string {
	out := map[string]string{}
	papp, psite := splitAppProvider(b.Provider)
	spec, ok := readAppServices(papp).Provides[psite][b.Interface]
	if !ok {
		return out
	}
	port := strconv.Itoa(PortOf(papp, psite))
	r := strings.NewReplacer("{url}", "http://127.0.0.1:"+port, "{host}", "127.0.0.1", "{port}", port, "{token}", b.Token)
	for k, t := range spec.Env {
		out[k] = r.Replace(t)
	}
	return out
}

// writeProviderEnv gives a provider site NOOK_CLIENTS: {"app": "token", ...} for each interface it serves.
func writeProviderEnv(app, site string) {
	clients := map[string]map[string]string{} // interface -> app -> token
	for _, b := range readBindings() {
		if b.Provider == "app:"+app+"/"+site {
			if clients[b.Interface] == nil {
				clients[b.Interface] = map[string]string{}
			}
			clients[b.Interface][b.App] = b.Token
		}
	}
	path := providerEnvPath(app, site)
	if len(clients) == 0 {
		os.Remove(path)
		return
	}
	j, _ := json.Marshal(clients)
	v := envfile.Parse("")
	v.Set("NOOK_CLIENTS", string(j))
	os.MkdirAll(filepath.Dir(path), 0o700)
	config.WriteAtomic(path, []byte(envfile.Format(v, "managed by nook: the apps bound to this service and their tokens, by interface")), 0o600)
}

// bindingFirewall is the extra "provider port . consumer uid" pairs bindings allow.
func bindingFirewall() []string {
	var out []string
	for _, b := range readBindings() {
		papp, psite := splitAppProvider(b.Provider)
		if papp == "" {
			continue
		}
		if _, offered := readAppServices(papp).Provides[psite][b.Interface]; !offered {
			continue // the provider stopped offering it: no way in
		}
		port := PortOf(papp, psite)
		u, err := LookupApp(b.App)
		if port == 0 || err != nil {
			continue
		}
		out = append(out, fmt.Sprintf("%d . %d", port, u.UID))
	}
	return out
}

// dropAppBindings removes every binding where app is the consumer, and every binding to its services.
func (h *Host) dropAppBindings(app string) {
	var keep []Binding
	affected := map[string]bool{}
	for _, b := range readBindings() {
		papp, _ := splitAppProvider(b.Provider)
		if b.App == app || papp == app {
			if b.App != app {
				affected[b.App] = true
			}
			continue
		}
		keep = append(keep, b)
	}
	writeBindings(keep)
	os.Remove(servicesStatePath(app))
	for a := range affected {
		writeConsumerEnv(a)
	}
}

// ---- for the dashboard ----

type ServicesView struct {
	Apps       []string     `json:"apps"`
	Interfaces []Interface  `json:"interfaces"`
	Providers  []Provider   `json:"providers"`
	Bindings   []BindingRow `json:"bindings"`
	Requests   []Request    `json:"requests"`
}

type BindingRow struct {
	Binding
	Env   []string `json:"env"` // names the app sees
	Ready bool     `json:"ready"`
}

type Request struct {
	App       string   `json:"app"`
	Interface string   `json:"interface"`
	Sites     []string `json:"sites"`
}

func ServicesOverview() ServicesView {
	v := ServicesView{Apps: Apps(), Providers: Providers(), Bindings: []BindingRow{}, Requests: []Request{}}
	if v.Apps == nil {
		v.Apps = []string{}
	}
	seen := map[string]bool{}
	for _, k := range Known {
		v.Interfaces = append(v.Interfaces, k)
		seen[k.Name] = true
	}
	for _, p := range v.Providers {
		if !seen[p.Interface] {
			v.Interfaces = append(v.Interfaces, Interface{Name: p.Interface, Desc: p.Desc, Env: interfaceEnv(p)})
			seen[p.Interface] = true
		}
	}
	sort.Slice(v.Interfaces, func(i, j int) bool { return v.Interfaces[i].Name < v.Interfaces[j].Name })
	bound := map[string]bool{}
	for _, b := range readBindings() {
		bound[b.App+"/"+b.Interface] = true
		row := BindingRow{Binding: b}
		row.Token = ""
		for k := range bindingValues(b) {
			if to, ok := b.As[k]; ok {
				k = to
			}
			row.Env = append(row.Env, k)
		}
		sort.Strings(row.Env)
		if p, ok := findProvider(b.Provider, b.Interface); ok {
			row.Ready = p.Ready
		}
		v.Bindings = append(v.Bindings, row)
	}
	for _, app := range Apps() {
		want := map[string][]string{}
		for site, needs := range readAppServices(app).Needs {
			for _, n := range needs {
				want[n] = append(want[n], site)
			}
		}
		for iface, sites := range want {
			if !bound[app+"/"+iface] {
				sort.Strings(sites)
				v.Requests = append(v.Requests, Request{App: app, Interface: iface, Sites: sites})
			}
		}
	}
	sort.Slice(v.Requests, func(i, j int) bool {
		return v.Requests[i].App+v.Requests[i].Interface < v.Requests[j].App+v.Requests[j].Interface
	})
	return v
}
