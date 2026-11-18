package core

// Providers that work on bound static sites (#98): a provider's "publish" hook sees the site's files on every
// publish, before it goes live, and can hand back changed copies; "posts" sends the site's POST requests to it.
// A form catcher uses both: it rewrites Netlify-style forms at publish and takes their submissions.
//
// The hook never gets a path to write to. Nook sends the matching files as a tar on its stdin and reads a tar of
// the files it changed from its stdout; only files it was sent can come back, and only as plain files. What it
// prints on stderr goes into the deploy log. A failing hook fails the publish and the previous copy stays live.

import (
	"archive/tar"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/sparkstonepdx/nook/internal/names"
	"github.com/sparkstonepdx/nook/internal/tarx"
)

// publisher is one provider a static site is bound to that works on it.
type publisher struct {
	Binding Binding
	Spec    ProvideSpec
	App     string // provider's app and site
	Site    string
	Port    int
}

// sitePublishers is the providers bound to app for the interfaces a static site needs that publish or take posts,
// in interface order.
func sitePublishers(app string, needs []string) []publisher {
	want := map[string]bool{}
	for _, s := range needs {
		if n, ok := parseNeed(s); ok {
			want[n.Name] = true
		}
	}
	var out []publisher
	for _, b := range readBindings() {
		if b.App != app || !want[b.Interface] {
			continue
		}
		papp, psite := splitAppProvider(b.Provider)
		spec, ok := readAppServices(papp).Provides[psite][b.Interface]
		if !ok || spec.Publish == "" && !spec.Posts {
			continue
		}
		out = append(out, publisher{Binding: b, Spec: spec, App: papp, Site: psite, Port: PortOf(papp, psite)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Binding.Interface < out[j].Binding.Interface })
	return out
}

// publishID changes when what the hooks would do changes, so a site is published again after a bind or a
// provider update even if its own files didn't change.
func publishID(pubs []publisher) string {
	var parts []string
	for _, p := range pubs {
		if p.Spec.Publish != "" {
			parts = append(parts, p.Binding.Interface+"="+p.Binding.Provider+"@"+p.Spec.Version+":"+p.Spec.Publish+":"+strings.Join(p.Spec.PublishFiles, ","))
		}
	}
	return strings.Join(parts, ";")
}

// postTarget is the port a site's POSTs go to (0: none) and a note when more than one provider wants them.
func postTarget(pubs []publisher) (int, string) {
	var takers []string
	port := 0
	for _, p := range pubs {
		if p.Spec.Posts && p.Port > 0 {
			if port == 0 {
				port = p.Port
			}
			takers = append(takers, p.Binding.Provider)
		}
	}
	if len(takers) > 1 {
		return port, fmt.Sprintf("POSTs go to %s; %s also wants them", takers[0], strings.Join(takers[1:], ", "))
	}
	return port, ""
}

func publishPatterns(spec ProvideSpec) []string {
	if len(spec.PublishFiles) > 0 {
		return spec.PublishFiles
	}
	return []string{"*.html", "*.htm"}
}

// runPublishHooks runs each publisher's hook over the staged copy in dir (root-owned, not live yet).
func (h *Host) runPublishHooks(app, site, dir string, pubs []publisher, max int64) error {
	for _, p := range pubs {
		if p.Spec.Publish == "" {
			continue
		}
		if err := h.runPublishHook(app, site, dir, p, max); err != nil {
			return fmt.Errorf("%s (%s) couldn't publish it: %v", p.Binding.Provider, p.Binding.Interface, err)
		}
	}
	return nil
}

func (h *Host) runPublishHook(app, site, dir string, p publisher, max int64) error {
	sent, err := matchingFiles(dir, publishPatterns(p.Spec))
	if err != nil || len(sent) == 0 {
		return err
	}
	in, err := packFiles(dir, sent)
	if err != nil {
		return err
	}
	back := dir + ".hook"
	os.RemoveAll(back)
	defer os.RemoveAll(back)
	logs, err := publishHookRun(p, map[string]string{"SITE": site, "SITE_HOST": names.HostOf(site, h.S.BaseDomain), "NOOK_CONSUMER": app, "NOOK_INTERFACE": p.Binding.Interface},
		bytes.NewReader(in), func(r io.Reader) error { return tarx.Extract(r, back, max) })
	for _, l := range strings.Split(strings.TrimSpace(logs), "\n") {
		if l != "" {
			h.say("%s: %s: %s", app, site, l)
		}
	}
	if err != nil {
		return err
	}
	allowed := map[string]bool{}
	for _, f := range sent {
		allowed[f] = true
	}
	var changed []string
	err = filepath.WalkDir(back, func(full string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(back, full)
		rel = filepath.ToSlash(rel)
		if !allowed[rel] {
			return fmt.Errorf("it handed back %s, which it wasn't sent", rel)
		}
		changed = append(changed, rel)
		return nil
	})
	if err != nil {
		return err
	}
	for _, rel := range changed {
		if err := os.Rename(filepath.Join(back, filepath.FromSlash(rel)), filepath.Join(dir, filepath.FromSlash(rel))); err != nil {
			return err
		}
	}
	return nil
}

// matchingFiles lists dir's plain files whose names match a pattern, as slash paths relative to dir.
func matchingFiles(dir string, patterns []string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(dir, func(full string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.Type().IsRegular() {
			return nil
		}
		for _, pat := range patterns {
			if ok, _ := path.Match(pat, d.Name()); ok {
				rel, _ := filepath.Rel(dir, full)
				out = append(out, filepath.ToSlash(rel))
				break
			}
		}
		return nil
	})
	return out, err
}

func packFiles(dir string, files []string) ([]byte, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	dirs := map[string]bool{}
	for _, rel := range files {
		for d := path.Dir(rel); d != "."; d = path.Dir(d) {
			dirs[d] = true
		}
	}
	var ds []string
	for d := range dirs {
		ds = append(ds, d)
	}
	sort.Strings(ds) // parents before children
	for _, d := range ds {
		if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeDir, Name: d + "/", Mode: 0o755}); err != nil {
			return nil, err
		}
	}
	for _, rel := range files {
		b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil {
			return nil, err
		}
		if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: rel, Mode: 0o644, Size: int64(len(b))}); err != nil {
			return nil, err
		}
		tw.Write(b)
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// publishHookRun runs the hook as the provider's user in its site folder, with the site's tar on stdin; read
// takes its stdout. It returns what the hook said on stderr. A var so tests can stand in.
var publishHookRun = func(p publisher, extra map[string]string, in io.Reader, read func(io.Reader) error) (string, error) {
	u, err := LookupApp(p.App)
	if err != nil {
		return "", err
	}
	if !hookRE.MatchString(p.Spec.Publish) || strings.Contains(p.Spec.Publish, "..") {
		return "", fmt.Errorf("a hook is a script in the site folder, like ./publish.sh, not %q", p.Spec.Publish)
	}
	dir := filepath.Join(u.Home, "sites", p.Site)
	cmd := exec.Command(filepath.Join(dir, p.Spec.Publish))
	cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: uint32(u.UID), Gid: uint32(u.GID), Groups: []uint32{}}}
	cmd.Dir = dir
	extra["PORT"] = strconv.Itoa(p.Port)
	cmd.Env = hookEnv(u, p.Site, extra)
	return pipeHook(cmd, in, read, 2*time.Minute)
}

// pipeHook runs cmd with in on its stdin and read on its stdout, and returns its stderr.
func pipeHook(cmd *exec.Cmd, in io.Reader, read func(io.Reader) error, timeout time.Duration) (string, error) {
	stderr := &limited{b: &bytes.Buffer{}, max: 8 << 10}
	cmd.Stdin, cmd.Stderr = in, stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	if err := cmd.Start(); err != nil {
		return "", err
	}
	timer := time.AfterFunc(timeout, func() { cmd.Process.Kill() })
	defer timer.Stop()
	rerr := read(out)
	io.Copy(io.Discard, out) // let it finish writing if read stopped early
	werr := cmd.Wait()
	if werr != nil {
		return stderr.b.String(), fmt.Errorf("%v", werr)
	}
	if rerr != nil && !errors.Is(rerr, io.EOF) {
		return stderr.b.String(), fmt.Errorf("its output: %v", rerr)
	}
	return stderr.b.String(), nil
}
