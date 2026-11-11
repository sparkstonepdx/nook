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
	"strings"
	"time"

	"github.com/sparkstonepdx/nook/internal/envfile"
	"github.com/sparkstonepdx/nook/internal/rpc"
)

// The deploy API: plain HTTPS with a per-app bearer token, for any CI.
//
//	POST   /api/v1/sites/SITE[?pr=N]   body: the site as .tar.gz, either raw or as multipart field "site";
//	                                   multipart may add "env" (a .env file) and/or "env.KEY" fields, which
//	                                   replace the site's deploy env (#13)
//	DELETE /api/v1/sites/SITE[?pr=N]   remove it (a preview when its PR closes)
//	GET    /api/v1/whoami              which app the token deploys
//
// Cookies are never read here, so there's nothing for a browser to forge.

var (
	bearerRE  = regexp.MustCompile(`^nook_[A-Za-z0-9_-]{43}$`)
	siteRoute = regexp.MustCompile(`^/api/v1/sites/([^/]+)$`)
	envKeyRE  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

func apiError(w http.ResponseWriter, status int, format string, a ...any) {
	writeJSON(w, status, map[string]string{"error": fmt.Sprintf(format, a...)})
}

func (s *Server) serveDeployAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	ip := clientIP(r)
	tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || !bearerRE.MatchString(strings.TrimSpace(tok)) {
		apiError(w, 401, "send Authorization: Bearer <deploy token> (make one on the dashboard or with nook-admin token create APP)")
		return
	}
	tok = strings.TrimSpace(tok)
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
		}
		apiError(w, orDefault(who.Status, 500), "%s", who.Error)
		return
	}
	var info struct {
		App      string `json:"app"`
		MaxBytes int64  `json:"maxBytes"`
	}
	json.Unmarshal(who.Data, &info)

	if r.URL.Path == "/api/v1/whoami" && r.Method == "GET" {
		w.Header().Set("Content-Type", "application/json")
		w.Write(who.Data)
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
