package core

// Runtimes (#42 step 3): binaries other apps run as themselves, like FrankenPHP or PHP. A runtime is a site of
// kind runtime: nook copies its folder root-owned to /var/lib/nook/runtimes/APP/SITE (as it copies a static site
// to be served), adds its links to what apt installed, and never runs or serves it. An app bound to it gets the
// runtime's bin folder first on PATH and in NOOK_RUNTIME_BIN, in its sites, cron jobs and hooks; it's never a
// shared process, so isolation is what every app has.

import (
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"github.com/sparkstonepdx/nook/internal/config"
	"github.com/sparkstonepdx/nook/internal/tarx"
)

// systemPath is what follows a bound app's runtimes on its PATH (systemd's default).
const systemPath = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"

var (
	linkNameRE   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,63}$`)
	linkTargetRE = regexp.MustCompile(`^/usr/(bin|sbin|lib|libexec|local/bin)/[A-Za-z0-9._+/-]{1,200}$`)
)

func checkRuntimeSpec(s ProvideSpec) error {
	if s.Bin != "" && (path.IsAbs(s.Bin) || path.Clean(s.Bin) != s.Bin || s.Bin == ".." || strings.HasPrefix(s.Bin, "../")) {
		return fmt.Errorf("bin is a folder inside the runtime, like bin or ., not %q", s.Bin)
	}
	if len(s.Links) > 20 {
		return fmt.Errorf("at most 20 links")
	}
	for name, target := range s.Links {
		if !linkNameRE.MatchString(name) || !linkTargetRE.MatchString(target) || path.Clean(target) != target {
			return fmt.Errorf("links name a file in the bin folder and what apt installed under /usr, like \"php\": \"/usr/bin/php8.3\", not %q: %q", name, target)
		}
	}
	return nil
}

func runtimeDir(app, site string) string { return filepath.Join(config.RuntimeDir, app, site) }

func runtimePublished(app, site string) bool {
	_, err := os.Stat(runtimeDir(app, site))
	return err == nil
}

// runtimeBin is the folder of a published runtime that goes on PATH.
func runtimeBin(app, site string, spec ProvideSpec) string {
	if !spec.Runtime {
		return ""
	}
	return filepath.Join(runtimeDir(app, site), filepath.FromSlash(cmpOrDot(spec.Bin)))
}

func cmpOrDot(s string) string {
	if s == "" {
		return "."
	}
	return s
}

// runtimeBins is the bin folders of the runtimes app is bound to, by interface name.
func runtimeBins(app string) []string {
	var bs []Binding
	for _, b := range readBindings() {
		if b.App == app {
			bs = append(bs, b)
		}
	}
	sort.Slice(bs, func(i, j int) bool { return bs[i].Interface < bs[j].Interface })
	var out []string
	for _, b := range bs {
		papp, psite := splitAppProvider(b.Provider)
		spec, ok := readAppServices(papp).Provides[psite][b.Interface]
		if ok && spec.Runtime {
			out = append(out, runtimeBin(papp, psite, spec))
		}
	}
	return out
}

// appPath is app's PATH: its runtimes first, then the system's.
func appPath(app string) string {
	return strings.Join(append(runtimeBins(app), systemPath), ":")
}

// runtimeEnv is the unit Environment entries for app's runtimes ("" with none: systemd's own PATH stands).
func runtimeEnv(app string) string {
	bins := runtimeBins(app)
	if len(bins) == 0 {
		return ""
	}
	return " PATH=" + strings.Join(append(bins, systemPath), ":") + " NOOK_RUNTIME_BIN=" + bins[0]
}

// publishRuntime copies a runtime site root-owned to where apps run it from. Returns true when it changed.
func (h *Host) publishRuntime(u *AppUser, site string, st *syscall.Stat_t, m Manifest) (bool, error) {
	app := u.Name
	dst := runtimeDir(app, site)
	idFile := filepath.Join(config.StateDir, app+"--"+site+".runtime")
	id := fmt.Sprintf("%d:%d", st.Ino, st.Mtim.Sec)
	var links []string
	for _, spec := range m.Provides {
		for name, target := range spec.Links {
			links = append(links, name+"="+target)
		}
	}
	sort.Strings(links)
	id += " " + strings.Join(links, ",")
	if old, err := os.ReadFile(idFile); err == nil && string(old) == id {
		if _, err := os.Stat(dst); err == nil {
			return false, nil
		}
	}
	os.MkdirAll(filepath.Join(config.RuntimeDir, app), 0o755)
	tmp := filepath.Join(config.RuntimeDir, app, ".new-"+site)
	os.RemoveAll(tmp)
	err := u.AsAppStream(func(r io.Reader) error { return tarx.Extract(r, tmp, h.S.SiteMaxBytes) },
		"internal-pack", filepath.Join(u.Home, "sites", site), strconv.FormatInt(h.S.SiteMaxBytes, 10))
	if err != nil {
		os.RemoveAll(tmp)
		return false, fmt.Errorf("couldn't copy the runtime (%v); kept the previous copy", err)
	}
	os.Remove(filepath.Join(tmp, "nook.json"))
	for _, spec := range m.Provides {
		bin := filepath.Join(tmp, filepath.FromSlash(cmpOrDot(spec.Bin)))
		for name, target := range spec.Links {
			os.MkdirAll(bin, 0o755)
			l := filepath.Join(bin, name)
			os.Remove(l) // a link replaces a file of the same name
			if err := os.Symlink(target, l); err != nil {
				os.RemoveAll(tmp)
				return false, err
			}
			if _, err := os.Stat(target); err != nil {
				h.say("%s: %s: %s points at %s, which isn't installed", app, site, name, target)
			}
		}
	}
	os.RemoveAll(dst + ".old")
	os.Rename(dst, dst+".old")
	if err := os.Rename(tmp, dst); err != nil {
		return false, err
	}
	os.RemoveAll(dst + ".old")
	return true, os.WriteFile(idFile, []byte(id), 0o600)
}

// dropRuntimes removes app's published runtimes whose site is gone or is no longer a runtime.
func dropRuntimes(app string, keep map[string]bool) {
	entries, err := os.ReadDir(filepath.Join(config.RuntimeDir, app))
	if err != nil {
		return
	}
	for _, e := range entries {
		if !keep[e.Name()] {
			os.RemoveAll(filepath.Join(config.RuntimeDir, app, e.Name()))
			os.Remove(filepath.Join(config.StateDir, app+"--"+e.Name()+".runtime"))
		}
	}
}
