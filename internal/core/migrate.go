package core

// Moving an app's service to another provider with its data (#42 step 4), such as from postgres-16 to
// postgres-17. Never in place: the new major runs beside the old one, and each consumer moves on its own.
//
//  1. The consumer's sites stop, so nothing writes while it's copied.
//  2. The new provider sets the consumer up (its provision hook): a login and an empty database.
//  3. The old provider's export hook (./export.sh APP, as the old provider's user) streams the consumer's data into
//     the new one's import hook (./import.sh APP, as the new provider's user). nook only pipes the bytes between
//     them; neither user can read the other's files.
//  4. The binding moves to the new provider and the consumer starts with its new env. The old provider lets it go
//     (deprovision), which keeps its copy of the data until you remove that instance.
//
// If any step fails, the new provider lets the consumer go again and it starts on the old binding, unchanged.

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/sparkstonepdx/nook/internal/config"
)

// migrateTimeout bounds the copy; a var for tests.
var migrateTimeout = 2 * time.Hour

// MigrateTarget is a provider the consumer's binding could move to, for the dashboard.
type MigrateTarget struct {
	ID      string `json:"id"`
	Version string `json:"version,omitempty"`
}

// migrateTargets lists the providers b could move to with its data: same interface, ready, accepted by the
// consumer's needs, with an import hook, when b's provider has an export hook.
func migrateTargets(b Binding, provs []Provider) []MigrateTarget {
	var from *Provider
	for i := range provs {
		if provs[i].ID == b.Provider && provs[i].Interface == b.Interface {
			from = &provs[i]
		}
	}
	if from == nil || from.spec.Export == "" {
		return nil
	}
	wants := consumerNeeds(b.App, b.Interface)
	var out []MigrateTarget
	for _, p := range provs {
		if p.Interface == b.Interface && p.ID != b.Provider && p.Ready && p.spec.Import != "" && p.app != b.App && acceptsAll(wants, p.Version) {
			out = append(out, MigrateTarget{ID: p.ID, Version: p.Version})
		}
	}
	return out
}

// Migrate moves app's binding for iface to provider to, carrying its data. to may be a provider id
// ("app:postgres-17/postgres") or just the providing app's name when it has one site providing iface.
func (h *Host) Migrate(app, iface, to string) error {
	if _, err := LookupApp(app); err != nil {
		return err
	}
	var old *Binding
	for _, b := range readBindings() {
		if b.App == app && b.Interface == iface {
			bb := b
			old = &bb
		}
	}
	if old == nil {
		return fmt.Errorf("%s has no %s binding to move", app, iface)
	}
	provs := Providers()
	if !strings.HasPrefix(to, "app:") {
		var ids []string
		for _, p := range provs {
			if p.Interface == iface && p.app == to {
				ids = append(ids, p.ID)
			}
		}
		if len(ids) != 1 {
			return fmt.Errorf("%s provides %s from %d sites; name one, like app:%s/SITE", to, iface, len(ids), to)
		}
		to = ids[0]
	}
	if to == old.Provider {
		return fmt.Errorf("%s already gets %s from %s", app, iface, to)
	}
	src, ok := findProvider(old.Provider, iface)
	if !ok {
		return fmt.Errorf("%s isn't providing %s any more, so there's nothing to copy from", old.Provider, iface)
	}
	dst, ok := findProvider(to, iface)
	if !ok {
		return fmt.Errorf("%s doesn't provide %s", to, iface)
	}
	ok = false
	for _, t := range migrateTargets(*old, provs) {
		ok = ok || t.ID == to
	}
	switch {
	case src.spec.Export == "":
		return fmt.Errorf("%s can't hand over its data (it has no export hook); bind %s to %s instead to start empty", src.ID, app, to)
	case dst.spec.Import == "":
		return fmt.Errorf("%s can't take data in (it has no import hook)", to)
	case !dst.Ready:
		return fmt.Errorf("%s isn't running", to)
	case !ok:
		return fmt.Errorf("%s needs %s; %s provides %s", app, needsText(iface, consumerNeeds(app, iface)), to, dst.Version)
	}

	running := RunningSites(app)
	h.say("stopping %s while its %s data is copied", app, iface)
	for _, s := range running {
		systemctl("stop", UnitName(app, s))
	}
	startAgain := func() {
		for _, s := range running {
			systemctl("start", UnitName(app, s))
		}
	}

	nb := Binding{App: app, Interface: iface, Provider: to, As: old.As, Created: time.Now().Unix()}
	raw := make([]byte, 24)
	rand.Read(raw)
	nb.Token = base64.RawURLEncoding.EncodeToString(raw)
	if dst.spec.Provision != "" {
		h.say("%s is setting up %s for %s", to, iface, app)
		vals, err := provision(dst, nb)
		if err != nil {
			startAgain()
			return fmt.Errorf("%s couldn't set up %s for %s: %v (it's back on %s)", to, iface, app, err, old.Provider)
		}
		nb.Values = vals
	}
	h.say("copying %s's %s data from %s to %s", app, iface, old.Provider, to)
	start := time.Now()
	n, err := runMigrateHooks(src, dst, *old, nb)
	if err != nil {
		h.deprovision(nb)
		startAgain()
		return fmt.Errorf("copying failed: %v (%s is back on %s, unchanged)", err, app, old.Provider)
	}
	h.say("copied %s in %s", sizeText(float64(n)), time.Since(start).Round(time.Second))

	unlock, err := Lock()
	if err != nil {
		h.deprovision(nb)
		startAgain()
		return err
	}
	var keep []Binding
	for _, b := range readBindings() {
		if !(b.App == app && b.Interface == iface) {
			keep = append(keep, b)
		}
	}
	err = writeBindings(append(keep, nb))
	unlock()
	if err != nil {
		h.deprovision(nb)
		startAgain()
		return err
	}
	h.say("moved %s's %s from %s to %s", app, iface, old.Provider, to)
	h.deprovision(*old)
	writeProviderEnv(src.app, src.site)
	if err := writeConsumerEnv(app); err != nil {
		return err
	}
	if dst.port > 0 {
		writeProviderEnv(dst.app, dst.site)
	}
	applyFirewall()
	startAgain()
	if dst.port > 0 {
		h.restartSites(dst.app, []string{dst.site})
	}
	h.say("%s still has its copy; remove %s once nothing else uses it", old.Provider, src.app)
	return h.resyncStatic(app, dst.spec)
}

// runMigrateHooks pipes src's export hook into dst's import hook and returns how many bytes went across.
var runMigrateHooks = func(src, dst Provider, old, nb Binding) (int64, error) {
	exp, err := migrateCmd(src, src.spec.Export, old)
	if err != nil {
		return 0, err
	}
	imp, err := migrateCmd(dst, dst.spec.Import, nb)
	if err != nil {
		return 0, err
	}
	return pipeHooks(exp, imp, migrateTimeout)
}

// migrateCmd is a provider hook as its own user in its site folder, with the consumer as its argument.
func migrateCmd(p Provider, run string, b Binding) (*exec.Cmd, error) {
	if !hookRE.MatchString(run) || strings.Contains(run, "..") {
		return nil, fmt.Errorf("a hook is a script in the site folder, like ./export.sh, not %q", run)
	}
	u, err := LookupApp(p.app)
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(u.Home, "sites", p.site)
	cmd := exec.Command(filepath.Join(dir, run), b.App)
	cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: uint32(u.UID), Gid: uint32(u.GID), Groups: []uint32{}}, Setpgid: true}
	cmd.Dir = dir
	extra := map[string]string{"NOOK_CONSUMER": b.App, "NOOK_CONSUMER_USER": "nook-" + b.App, "NOOK_INTERFACE": b.Interface}
	if p.port > 0 {
		extra["PORT"] = strconv.Itoa(p.port)
	}
	cmd.Env = hookEnv(u, p.site, extra)
	return cmd, nil
}

// pipeHooks runs exp | imp: both must exit 0. Their stderr (the last 8 KB each) is in the error. Each runs in its
// own process group, so a timeout or a failed side stops everything either one started (pg_dump under a script).
func pipeHooks(exp, imp *exec.Cmd, timeout time.Duration) (int64, error) {
	for _, c := range []*exec.Cmd{exp, imp} {
		if c.SysProcAttr == nil {
			c.SysProcAttr = &syscall.SysProcAttr{}
		}
		c.SysProcAttr.Setpgid = true
	}
	kill := func(c *exec.Cmd) {
		if c.Process != nil {
			syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
		}
	}
	r, w, err := os.Pipe()
	if err != nil {
		return 0, err
	}
	expErr, impErr := &limited{b: &bytes.Buffer{}, max: 8 << 10}, &limited{b: &bytes.Buffer{}, max: 8 << 10}
	out, err := exp.StdoutPipe()
	if err != nil {
		r.Close()
		w.Close()
		return 0, err
	}
	exp.Stderr, imp.Stderr, imp.Stdout, imp.Stdin = expErr, impErr, impErr, r
	if err := imp.Start(); err != nil {
		r.Close()
		w.Close()
		return 0, err
	}
	r.Close()
	if err := exp.Start(); err != nil {
		w.Close()
		kill(imp)
		imp.Wait()
		return 0, err
	}
	var timedOut atomic.Bool
	timer := time.AfterFunc(timeout, func() { timedOut.Store(true); kill(exp); kill(imp) })
	defer timer.Stop()
	impDone := make(chan error, 1)
	go func() { impDone <- imp.Wait() }()
	n, cerr := io.Copy(w, out)
	w.Close()
	if cerr != nil { // the import side went away: stop the export too
		kill(exp)
		io.Copy(io.Discard, out)
	}
	eerr := exp.Wait()
	ierr := <-impDone
	var msgs []string
	if timedOut.Load() {
		msgs = append(msgs, fmt.Sprintf("still running after %s", timeout))
	}
	if eerr != nil && cerr == nil {
		msgs = append(msgs, fmt.Sprintf("export: %v: %s", eerr, strings.TrimSpace(expErr.b.String())))
	}
	if ierr != nil {
		msgs = append(msgs, fmt.Sprintf("import: %v: %s", ierr, strings.TrimSpace(impErr.b.String())))
	}
	if len(msgs) == 0 && cerr != nil {
		msgs = append(msgs, "copying: "+cerr.Error())
	}
	if len(msgs) > 0 {
		return n, errors.New(strings.Join(msgs, "; "))
	}
	return n, nil
}

// ---- for the dashboard: a migration runs on after the request that started it ----

// Migration is one move in progress or finished in the last 10 minutes, in /var/lib/nook/migrations/APP.json.
type Migration struct {
	App       string    `json:"app"`
	Interface string    `json:"interface"`
	From      string    `json:"from"`
	To        string    `json:"to"`
	Started   time.Time `json:"started"`
	Done      bool      `json:"done,omitempty"`
	Error     string    `json:"error,omitempty"`
	Out       string    `json:"out,omitempty"`
}

var migrationsDir = func() string { return filepath.Join(config.LibDir, "migrations") }

func saveMigration(m Migration) {
	os.MkdirAll(migrationsDir(), 0o755)
	b, _ := json.Marshal(m)
	config.WriteAtomic(filepath.Join(migrationsDir(), m.App+".json"), b, 0o644)
}

// Migrations is every move running now or finished in the last 10 minutes. One the helper didn't finish (it
// restarted) reads as failed after 3 hours.
func Migrations() []Migration {
	out := []Migration{}
	es, _ := os.ReadDir(migrationsDir())
	for _, e := range es {
		p := filepath.Join(migrationsDir(), e.Name())
		var m Migration
		b, err := os.ReadFile(p)
		if err != nil || json.Unmarshal(b, &m) != nil {
			continue
		}
		st, _ := os.Stat(p)
		switch {
		case m.Done && time.Since(st.ModTime()) > jobKeep:
			os.Remove(p)
			continue
		case !m.Done && time.Since(m.Started) > migrateTimeout+time.Hour:
			m.Done, m.Error = true, "stopped without finishing"
		}
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Started.Before(out[j].Started) })
	return out
}

// StartMigrate checks the move can start and runs Migrate on after returning.
func StartMigrate(app, iface, to string) error {
	for _, m := range Migrations() {
		if m.App == app && !m.Done {
			return fmt.Errorf("%s is already moving its %s", app, m.Interface)
		}
	}
	from := ""
	for _, b := range readBindings() {
		if b.App == app && b.Interface == iface {
			from = b.Provider
		}
	}
	if from == "" {
		return fmt.Errorf("%s has no %s binding to move", app, iface)
	}
	m := Migration{App: app, Interface: iface, From: from, To: to, Started: time.Now().UTC()}
	saveMigration(m)
	go func() {
		var out strings.Builder
		err := New(&out).Migrate(app, iface, to)
		m.Done, m.Out = true, out.String()
		if err != nil {
			m.Error = err.Error()
		}
		saveMigration(m)
	}()
	return nil
}
