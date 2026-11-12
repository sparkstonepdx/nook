package core

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/sparkstonepdx/nook/internal/config"
	"github.com/sparkstonepdx/nook/internal/envfile"
	"github.com/sparkstonepdx/nook/internal/names"
	"github.com/sparkstonepdx/nook/internal/rpc"
)

// The deploy API (POST/DELETE /api/v1/sites/SITE on the dashboard's address). The unprivileged dashboard
// checks the bearer token's format, spools the upload to an unlinked file and passes the open file here;
// this side checks the token for real and does everything else as the app user.

// DeployTarget is the site name a deploy lands in: SITE, or its preview when pr is set.
func DeployTarget(site, pr string) (string, error) {
	if !names.Site(site) {
		return "", fmt.Errorf("bad site name %q: a subdomain like blog, or a hostname like client.com", site)
	}
	if pr == "" {
		return site, nil
	}
	n, err := strconv.Atoi(pr)
	if err != nil || n < 1 || n > 999999999 {
		return "", fmt.Errorf("bad pr %q", pr)
	}
	name := names.Preview(site, n)
	if !names.Site(name) {
		return "", fmt.Errorf("preview name %q is too long", name)
	}
	return name, nil
}

type DeployResult struct {
	App    string `json:"app"`
	Site   string `json:"site"`
	URL    string `json:"url,omitempty"`
	Live   bool   `json:"live"`
	Kind   string `json:"kind,omitempty"`
	Output string `json:"output"`
	// set when the site declares a health path: whether it answered healthy after the deploy
	Health  string `json:"health,omitempty"`
	Healthy *bool  `json:"healthy,omitempty"`
}

func deployFail(status int, format string, a ...any) rpc.Response {
	return rpc.Response{Error: fmt.Sprintf(format, a...), Status: status}
}

// DispatchDeploy handles the "deploy" op: req.Secret is the token, file the uploaded .tar.gz.
func DispatchDeploy(req *rpc.Request, file *os.File) rpc.Response {
	app, ok := TokenApp(req.Secret)
	if !ok {
		return deployFail(401, "unknown deploy token")
	}
	if file == nil {
		return deployFail(400, "no upload")
	}
	name, err := DeployTarget(req.Params["site"], req.Params["pr"])
	if err != nil {
		return deployFail(400, "%v", err)
	}
	u, err := LookupApp(app)
	if err != nil {
		return deployFail(400, "%v", err)
	}
	s := config.Load()
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return deployFail(500, "%v", err)
	}
	if out, err := u.asAppIn(file, 64<<10, "internal-unpack", name, strconv.FormatInt(s.SiteMaxBytes, 10)); err != nil {
		return deployFail(400, "%s", firstNonEmpty(strings.TrimSpace(string(out)), err.Error()))
	}
	// env only after the upload unpacked, so a bad upload changes nothing
	if req.Params["env"] == "1" {
		vars := envfile.Parse("")
		for k, v := range req.Set {
			vars.Set(k, v)
		}
		vars = envfile.Filter(vars)
		m := map[string]string{}
		for _, k := range vars.Keys() {
			m[k], _ = vars.Get(k)
		}
		if err := EnvReplace(app, Scope{"deploy", name}, m); err != nil {
			return deployFail(400, "env: %v", err)
		}
	}
	out, err := u.asAppIn(nil, 64<<10, "site", "deploy", filepath.Join(u.Home, "builds", name), name)
	res := DeployResult{App: app, Site: name, URL: "https://" + names.HostOf(name, s.BaseDomain), Output: strings.TrimSpace(string(out))}
	res.Live = err == nil
	for marker, kind := range map[string]string{"(running app)": "web", "(static)": "static", "(cron job)": "cron", "(service)": "service"} {
		if strings.Contains(res.Output, marker) {
			res.Kind = kind
		}
	}
	if res.Kind == "cron" || res.Kind == "service" {
		res.URL = "" // no hostname
	}
	if err == nil && (res.Kind == "web" || res.Kind == "service") {
		if path := SiteHealthPath(app, name); path != "" {
			res.Health = path
			herr := waitHealthy(app, name, path, res.Kind == "web", 60*time.Second)
			ok := herr == nil
			res.Healthy = &ok
			if ok {
				res.Output += "\nhealthy: " + path + " answered"
			} else {
				res.Output += "\nNOT healthy after 60 s: " + herr.Error() + ". Check its logs on the dashboard."
			}
		}
	}
	var ee *exec.ExitError
	if err != nil && !(errors.As(err, &ee) && ee.ExitCode() == 2) {
		return deployFail(500, "%s", strings.TrimSpace(res.Output+" "+err.Error()))
	}
	r := data(res, nil)
	if !res.Live {
		r.Status = 409 // deployed but not served: the app isn't granted that hostname
	}
	return r
}

// undeploy removes a site (normally a preview when its PR closes).
func undeploy(req *rpc.Request) rpc.Response {
	app, ok := TokenApp(req.Secret)
	if !ok {
		return deployFail(401, "unknown deploy token")
	}
	name, err := DeployTarget(req.Params["site"], req.Params["pr"])
	if err != nil {
		return deployFail(400, "%v", err)
	}
	u, err := LookupApp(app)
	if err != nil {
		return deployFail(400, "%v", err)
	}
	out, err := u.asAppIn(nil, 64<<10, "site", "rm", name)
	if err != nil {
		return deployFail(500, "%s", strings.TrimSpace(string(out)+" "+err.Error()))
	}
	return data(map[string]string{"app": app, "site": name, "output": strings.TrimSpace(string(out))}, nil)
}

// tokenApp answers the dashboard's pre-upload check: which app (and upload limit) a token is for.
func tokenApp(req *rpc.Request) rpc.Response {
	app, ok := TokenApp(req.Secret)
	if !ok {
		return deployFail(401, "unknown deploy token")
	}
	s := config.Load()
	return data(map[string]any{"app": app, "maxBytes": s.SiteMaxBytes, "domain": s.BaseDomain}, nil)
}

// asAppIn runs the nook binary as the app user with stdin, returning stdout (and stderr on failure).
func (u *AppUser) asAppIn(stdin io.Reader, max int64, args ...string) ([]byte, error) {
	cmd := exec.Command(config.AppBin, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: uint32(u.UID), Gid: uint32(u.GID), Groups: []uint32{}}}
	cmd.Dir = u.Home
	cmd.Env = []string{"HOME=" + u.Home, "USER=nook-" + u.Name, "PATH=/usr/local/bin:/usr/bin:/bin"}
	cmd.Stdin = stdin
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &limited{&out, max}, &limited{&errb, max}
	err := cmd.Run()
	if err != nil {
		return append(out.Bytes(), errb.Bytes()...), err
	}
	return out.Bytes(), nil
}

type limited struct {
	b   *bytes.Buffer
	max int64
}

func (l *limited) Write(p []byte) (int, error) {
	if room := l.max - int64(l.b.Len()); room > 0 {
		if int64(len(p)) > room {
			l.b.Write(p[:room])
		} else {
			l.b.Write(p)
		}
	}
	return len(p), nil
}

func firstNonEmpty(a ...string) string {
	for _, s := range a {
		if s != "" {
			return s
		}
	}
	return ""
}

// ciEnv handles the env API: values from CI into the app's "ci" scope (no site) or a site's deploy scope.
func ciEnv(req *rpc.Request) rpc.Response {
	app, ok := TokenApp(req.Secret)
	if !ok {
		return deployFail(401, "unknown deploy token")
	}
	scope := Scope{Kind: "ci"}
	if req.Params["site"] != "" {
		name, err := DeployTarget(req.Params["site"], req.Params["pr"])
		if err != nil {
			return deployFail(400, "%v", err)
		}
		scope = Scope{"deploy", name}
	}
	h := New(io.Discard)
	keys, restarted, err := h.CIEnv(app, scope, req.Set, req.Unset, req.Params["replace"] == "1")
	if err != nil {
		return deployFail(400, "%v", err)
	}
	if keys == nil {
		keys = []string{}
	}
	if restarted == nil {
		restarted = []string{}
	}
	return data(map[string]any{"app": app, "scope": scope.String(), "keys": keys, "restarted": restarted}, nil)
}
