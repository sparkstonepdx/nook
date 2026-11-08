// Package core is everything nook-admin does as root: creating and syncing apps, grants, env, Caddy.
// Rule: root never opens a path inside an app's folder by name. Directory reads go through a file
// descriptor opened with O_NOFOLLOW and the *at() calls; anything that must read an app's files
// runs as the app user (the `nook` binary's hidden pack / read-env commands).
package core

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/sparkstonepdx/nook/internal/config"
	"github.com/sparkstonepdx/nook/internal/names"
)

// Host carries settings and where progress goes.
type Host struct {
	S   config.Settings
	Out io.Writer
}

func New(out io.Writer) *Host { return &Host{S: config.Load(), Out: out} }

func (h *Host) say(format string, a ...any) { fmt.Fprintf(h.Out, format+"\n", a...) }

// Run executes a command and returns combined output.
func Run(name string, args ...string) (string, error) {
	var b bytes.Buffer
	cmd := exec.Command(name, args...)
	cmd.Stdout, cmd.Stderr = &b, &b
	err := cmd.Run()
	return b.String(), err
}

// RunInput is Run with stdin.
func RunInput(input string, name string, args ...string) (string, error) {
	var b bytes.Buffer
	cmd := exec.Command(name, args...)
	cmd.Stdin = strings.NewReader(input)
	cmd.Stdout, cmd.Stderr = &b, &b
	err := cmd.Run()
	return b.String(), err
}

func systemctl(args ...string) error { _, err := Run("systemctl", args...); return err }

// UnitProps reads `systemctl show` properties.
func UnitProps(unit string, props ...string) map[string]string {
	out, _ := Run("systemctl", "show", unit, "-p", strings.Join(props, ","))
	m := map[string]string{}
	for _, l := range strings.Split(out, "\n") {
		if i := strings.IndexByte(l, '='); i > 0 {
			m[l[:i]] = l[i+1:]
		}
	}
	return m
}

// AppUser is the Linux account behind an app.
type AppUser struct {
	Name, Home string
	UID, GID   int
}

func LookupApp(app string) (*AppUser, error) {
	if !names.App(app) {
		return nil, fmt.Errorf("invalid app name %q", app)
	}
	u, err := user.Lookup("nook-" + app)
	if err != nil {
		return nil, fmt.Errorf("no app named %s", app)
	}
	uid, _ := strconv.Atoi(u.Uid)
	gid, _ := strconv.Atoi(u.Gid)
	return &AppUser{Name: app, Home: filepath.Join(config.AppsDir, app), UID: uid, GID: gid}, nil
}

// AsApp runs the user-level `nook` binary as the app user (never root), returning stdout capped at max bytes.
func (u *AppUser) AsApp(max int64, args ...string) ([]byte, error) {
	cmd := exec.Command(config.AppBin, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: uint32(u.UID), Gid: uint32(u.GID), Groups: []uint32{}}}
	cmd.Dir = "/"
	cmd.Env = []string{"HOME=" + u.Home, "USER=nook-" + u.Name, "PATH=/usr/local/bin:/usr/bin:/bin"}
	var errb bytes.Buffer
	cmd.Stderr = &errb
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	data, rerr := io.ReadAll(io.LimitReader(out, max+1))
	io.Copy(io.Discard, out)
	werr := cmd.Wait()
	if rerr != nil {
		return nil, rerr
	}
	if int64(len(data)) > max {
		return nil, fmt.Errorf("output over %d bytes", max)
	}
	if werr != nil {
		return data, fmt.Errorf("%v: %s", werr, strings.TrimSpace(errb.String()))
	}
	return data, nil
}

// Lock serializes everything that edits app state (the same lock the old scripts used).
func Lock() (func(), error) {
	os.MkdirAll(filepath.Dir(config.LockFile), 0o755)
	f, err := os.OpenFile(config.LockFile, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return func() { syscall.Flock(int(f.Fd()), syscall.LOCK_UN); f.Close() }, nil
}

// Apps lists app names (folders in /srv/nook with a matching user).
func Apps() []string {
	entries, _ := os.ReadDir(config.AppsDir)
	var out []string
	for _, e := range entries {
		if names.App(e.Name()) {
			if _, err := user.Lookup("nook-" + e.Name()); err == nil {
				out = append(out, e.Name())
			}
		}
	}
	sort.Strings(out)
	return out
}

// ---- ports registry: "nook site port" lines ----

type portEntry struct {
	App, Site string
	Port      int
}

func readPorts() []portEntry {
	b, _ := os.ReadFile(config.PortsFile)
	var out []portEntry
	for _, l := range strings.Split(string(b), "\n") {
		f := strings.Fields(l)
		if len(f) == 3 {
			if p, err := strconv.Atoi(f[2]); err == nil {
				out = append(out, portEntry{f[0], f[1], p})
			}
		}
	}
	return out
}

func writePorts(ps []portEntry) error {
	var b strings.Builder
	for _, p := range ps {
		fmt.Fprintf(&b, "%s %s %d\n", p.App, p.Site, p.Port)
	}
	return config.WriteAtomic(config.PortsFile, []byte(b.String()), 0o600)
}

func PortOf(app, site string) int {
	for _, p := range readPorts() {
		if p.App == app && p.Site == site {
			return p.Port
		}
	}
	return 0
}

// RunningSites lists the sites of app that have a port (i.e. run as a service).
func RunningSites(app string) []string {
	var out []string
	for _, p := range readPorts() {
		if p.App == app {
			out = append(out, p.Site)
		}
	}
	return out
}

func (h *Host) allocPort(app, site string) (int, error) {
	if p := PortOf(app, site); p > 0 {
		return p, nil
	}
	used := map[int]bool{}
	ps := readPorts()
	for _, p := range ps {
		used[p.Port] = true
	}
	listening, _ := Run("ss", "-Htln")
	span := h.S.PortMax - h.S.PortMin + 1
	start := int(time.Now().UnixNano() % int64(span))
	for i := 0; i < span; i++ {
		p := h.S.PortMin + (start+i)%span
		if used[p] || strings.Contains(listening, ":"+strconv.Itoa(p)+" ") {
			continue
		}
		ps = append(ps, portEntry{app, site, p})
		return p, writePorts(ps)
	}
	return 0, fmt.Errorf("no free ports in %d-%d", h.S.PortMin, h.S.PortMax)
}

func dropPort(app, site string) {
	var keep []portEntry
	for _, p := range readPorts() {
		if !(p.App == app && p.Site == site) {
			keep = append(keep, p)
		}
	}
	writePorts(keep)
}

func UnitName(app, site string) string { return "nook-site-" + app + "--" + site + ".service" }

// Tail returns the last lines of a root-owned log file.
func Tail(path string, lines int) string {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return "(no log yet)"
	}
	defer f.Close()
	st, _ := f.Stat()
	size := st.Size()
	const max = 256 << 10
	off := int64(0)
	if size > max {
		off = size - max
	}
	buf := make([]byte, size-off)
	f.ReadAt(buf, off)
	parts := strings.Split(string(buf), "\n")
	if len(parts) > lines {
		parts = parts[len(parts)-lines:]
	}
	return strings.Join(parts, "\n")
}
