package web

import (
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/sparkstonepdx/nook/internal/envfile"
	"github.com/sparkstonepdx/nook/internal/rpc"
	"github.com/sparkstonepdx/nook/internal/token"
)

// The deploy API: plain HTTPS with a per-app bearer token, for any CI.
//
//	POST   /api/v1/sites/SITE[?pr=N]   body: the site as .tar.gz, either raw or as multipart field "site";
//	                                   multipart may add "env" (a .env file) and/or "env.KEY" fields, which
//	                                   replace the site's deploy env (#13)
//	DELETE /api/v1/sites/SITE[?pr=N]   remove it (a preview when its PR closes)
//	POST   /api/v1/env[?site=SITE[&pr=N]] set env without deploying (see envAPI)
//	GET    /api/v1/whoami              which app the token deploys
//
// Cookies are never read here, so there's nothing for a browser to forge.

var (
	bearerRE  = token.APIRE
	siteRoute = regexp.MustCompile(`^/api/v1/sites/([^/]+)$`)
	envKeyRE  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

func apiError(w http.ResponseWriter, status int, format string, a ...any) {
	writeJSON(w, status, map[string]string{"error": fmt.Sprintf(format, a...)})
}

// requestToken reads a deploy token from Authorization: Bearer, or (basic set, for the token check) from the
// password of Basic auth, which is how `docker login` sends it.
func requestToken(r *http.Request, basic bool) (string, bool) {
	tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok && basic {
		_, tok, ok = r.BasicAuth()
	}
	tok = strings.TrimSpace(tok)
	return tok, ok && bearerRE.MatchString(tok)
}

func (s *Server) serveDeployAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	ip := clientIP(r)
	check := r.URL.Path == "/api/v1/token-check"
	tok, ok := requestToken(r, check)
	if !ok {
		if check {
			// Docker sends credentials only after a 401 that asks for them.
			w.Header().Set("WWW-Authenticate", `Basic realm="nook"`)
			apiError(w, 401, "this site needs a deploy token from its nook server: send Authorization: Bearer <token>, or log in with the token as the password")
			return
		}
		apiError(w, 401, "send Authorization: Bearer <deploy token> (make one on the dashboard or with nook-admin token create APP)")
		return
	}
	if err := s.throttled(ip); err != nil {
		he := err.(httpError)
		apiError(w, he.status, "%s", he.msg)
		return
	}
	who, err := rpc.Call(s.Socket, rpc.Request{Op: "tokenApp", By: "api", Secret: tok}, 10*time.Second)
	if err != nil {
		apiError(w, 502, "%v", err)
		return
	}
	if who.Error != "" {
		if who.Status == 401 {
			s.failed(ip)
			if check {
				w.Header().Set("WWW-Authenticate", `Basic realm="nook"`)
			}
		}
		apiError(w, orDefault(who.Status, 500), "%s", who.Error)
		return
	}
	var info struct {
		App      string `json:"app"`
		MaxBytes int64  `json:"maxBytes"`
	}
	json.Unmarshal(who.Data, &info)

	if check {
		// Caddy's forward_auth for a site with {"auth": "nook-token"}: it copies X-Nook-App to the app and drops the token.
		w.Header().Set("X-Nook-App", info.App)
		w.WriteHeader(204)
		return
	}
	if r.URL.Path == "/api/v1/whoami" && r.Method == "GET" {
		w.Header().Set("Content-Type", "application/json")
		w.Write(who.Data)
		return
	}
	if r.URL.Path == "/api/v1/env" {
		if r.Method != "POST" {
			apiError(w, 405, "POST env to set it")
			return
		}
		s.envAPI(w, r, tok, "token:"+info.App)
		return
	}
	m := siteRoute.FindStringSubmatch(r.URL.Path)
	if m == nil {
		apiError(w, 404, "not found")
		return
	}
	params := map[string]string{"site": m[1], "pr": r.URL.Query().Get("pr")}
	by := "token:" + info.App
	switch r.Method {
	case "DELETE":
		resp, err := rpc.Call(s.Socket, rpc.Request{Op: "undeploy", By: by, Params: params, Secret: tok}, time.Minute)
		writeRPC(w, resp, err)
	case "POST":
		s.deployUpload(w, r, tok, by, params, info.MaxBytes)
	default:
		apiError(w, 405, "use POST to deploy or DELETE to remove")
	}
}

func (s *Server) deployUpload(w http.ResponseWriter, r *http.Request, tok, by string, params map[string]string, max int64) {
	rc := http.NewResponseController(w)
	rc.SetReadDeadline(time.Now().Add(30 * time.Minute))
	rc.SetWriteDeadline(time.Now().Add(35 * time.Minute))
	if max <= 0 {
		max = 1 << 30
	}
	body := http.MaxBytesReader(w, r.Body, max+(1<<20))

	dir := filepath.Join(s.Store.Dir, "uploads")
	os.MkdirAll(dir, 0o700)
	f, err := os.CreateTemp(dir, "deploy-")
	if err != nil {
		apiError(w, 500, "%v", err)
		return
	}
	os.Remove(f.Name()) // only the open file remains; nothing to clean up if we die
	defer f.Close()

	env := map[string]string{}
	envSent := false
	got := int64(0)
	mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if strings.HasPrefix(mt, "multipart/") {
		mr, err := r.MultipartReader()
		if err != nil {
			apiError(w, 400, "%v", err)
			return
		}
		for {
			part, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				apiError(w, 400, "upload: %v", err)
				return
			}
			name := part.FormName()
			switch {
			case name == "site":
				n, err := io.Copy(f, part)
				got += n
				if err != nil {
					apiError(w, 413, "upload: %v", err)
					return
				}
			case name == "env":
				b, err := io.ReadAll(io.LimitReader(part, 64<<10+1))
				if err != nil || len(b) > 64<<10 {
					apiError(w, 413, "env file over 64 KB")
					return
				}
				v := envfile.Parse(string(b))
				for _, k := range v.Keys() {
					env[k], _ = v.Get(k)
				}
				envSent = true
			case strings.HasPrefix(name, "env."):
				k := strings.TrimPrefix(name, "env.")
				if !envKeyRE.MatchString(k) {
					apiError(w, 400, "bad env name %q", k)
					return
				}
				b, err := io.ReadAll(io.LimitReader(part, 64<<10+1))
				if err != nil || len(b) > 64<<10 {
					apiError(w, 413, "env value over 64 KB")
					return
				}
				env[k] = string(b)
				envSent = true
			default:
				io.Copy(io.Discard, io.LimitReader(part, 1<<20))
			}
			part.Close()
		}
	} else {
		n, err := io.Copy(f, body)
		got = n
		if err != nil {
			apiError(w, 413, "upload: %v", err)
			return
		}
	}
	if got == 0 {
		apiError(w, 400, `no site uploaded: send the folder as a .tar.gz, raw or as -F site=@site.tgz`)
		return
	}
	if len(env) > 500 {
		apiError(w, 400, "too many env values")
		return
	}
	if envSent {
		params["env"] = "1"
	}
	resp, err := rpc.CallWithFile(s.Socket, rpc.Request{Op: "deploy", By: by, Params: params, Set: env, Secret: tok}, f, 15*time.Minute)
	writeRPC(w, resp, err)
}

func writeRPC(w http.ResponseWriter, resp *rpc.Response, err error) {
	if err != nil {
		apiError(w, 502, "%v", err)
		return
	}
	if resp.Error != "" {
		apiError(w, orDefault(resp.Status, 500), "%s", resp.Error)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(orDefault(resp.Status, 200))
	w.Write(resp.Data)
}

func orDefault(n, d int) int {
	if n == 0 {
		return d
	}
	return n
}

// envAPI: POST /api/v1/env sets values from CI without deploying. Write-only: nothing ever returns a value.
//
//	no site:          the app's "sent by CI" scope (every site; your dashboard values still win)
//	?site=S[&pr=N]:   that site's deploy scope
//	body:             a .env file, JSON {"KEY": "value"} (GitHub's toJSON(secrets) works as is), or
//	                  multipart env=@file / env.KEY=value
//	?merge=1          add to what's there (default: replace the scope with exactly these)
//	?unset=A,B        remove these
//	?prefix=PROD_     only keys starting with PROD_, with the prefix removed
//
// GITHUB_TOKEN, NOOK_TOKEN and nook's own names (PORT, HOST, ...) are dropped, as are names that aren't
// valid env names; the response lists what was skipped.
func (s *Server) envAPI(w http.ResponseWriter, r *http.Request, tok, by string) {
	q := r.URL.Query()
	vals := map[string]string{}
	body := http.MaxBytesReader(w, r.Body, 1<<20)
	mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	switch {
	case strings.HasPrefix(mt, "multipart/"):
		mr, err := r.MultipartReader()
		if err != nil {
			apiError(w, 400, "%v", err)
			return
		}
		for {
			part, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				apiError(w, 400, "%v", err)
				return
			}
			b, err := io.ReadAll(io.LimitReader(part, 64<<10+1))
			if err != nil || len(b) > 64<<10 {
				apiError(w, 413, "part %q over 64 KB", part.FormName())
				return
			}
			switch name := part.FormName(); {
			case name == "env":
				v := envfile.Parse(string(b))
				for _, k := range v.Keys() {
					vals[k], _ = v.Get(k)
				}
			case strings.HasPrefix(name, "env."):
				vals[strings.TrimPrefix(name, "env.")] = string(b)
			}
		}
	case mt == "application/json":
		var raw map[string]any
		if err := json.NewDecoder(body).Decode(&raw); err != nil {
			apiError(w, 400, "body: %v", err)
			return
		}
		for k, v := range raw {
			switch t := v.(type) {
			case string:
				vals[k] = t
			case float64, bool:
				b, _ := json.Marshal(t)
				vals[k] = string(b)
			}
		}
	default:
		b, err := io.ReadAll(body)
		if err != nil {
			apiError(w, 413, "body over 1 MB")
			return
		}
		v := envfile.Parse(string(b))
		for _, k := range v.Keys() {
			vals[k], _ = v.Get(k)
		}
	}

	prefix := q.Get("prefix")
	set := map[string]string{}
	skipped := []string{}
	drop := map[string]bool{"GITHUB_TOKEN": true, "NOOK_TOKEN": true}
	for k, v := range vals {
		name := k
		if prefix != "" {
			if !strings.HasPrefix(k, prefix) {
				continue
			}
			name = strings.TrimPrefix(k, prefix)
		}
		if drop[strings.ToUpper(k)] || !envKeyRE.MatchString(name) || len(v) > 64<<10 || envfile.Check(name, v) != nil {
			skipped = append(skipped, k)
			continue
		}
		set[name] = v
	}
	if len(set) > 500 {
		apiError(w, 400, "too many values (500 at most)")
		return
	}
	var unset []string
	for _, k := range strings.Split(q.Get("unset"), ",") {
		if k = strings.TrimSpace(k); k != "" {
			if !envKeyRE.MatchString(k) {
				apiError(w, 400, "bad name %q in unset", k)
				return
			}
			unset = append(unset, k)
		}
	}
	params := map[string]string{"site": q.Get("site"), "pr": q.Get("pr")}
	if q.Get("merge") != "1" && len(unset) == 0 {
		params["replace"] = "1"
	}
	if q.Get("merge") != "1" && len(unset) > 0 && len(set) > 0 {
		apiError(w, 400, "unset only makes sense with merge=1 (a replace already drops everything not sent)")
		return
	}
	resp, err := rpc.Call(s.Socket, rpc.Request{Op: "ciEnv", By: by, Params: params, Set: set, Unset: unset, Secret: tok}, 2*time.Minute)
	if err != nil {
		apiError(w, 502, "%v", err)
		return
	}
	if resp.Error != "" {
		apiError(w, orDefault(resp.Status, 500), "%s", resp.Error)
		return
	}
	var out map[string]any
	json.Unmarshal(resp.Data, &out)
	sort.Strings(skipped)
	out["skipped"] = skipped
	writeJSON(w, 200, out)
}
