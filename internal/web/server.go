// Package web is the dashboard's HTTP side. It runs as the unprivileged nook-web user: it handles
// logins, sessions, invites and the UI, and asks the root helper for everything else.
package web

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"math"
	"net"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/sparkstonepdx/nook/internal/names"
	"github.com/sparkstonepdx/nook/internal/rpc"
)

type Server struct {
	Store  *Store
	Socket string
	UI     fs.FS

	mu     sync.Mutex
	fails  map[string]*failState
	recent []time.Time     // every failed attempt in the last globalWindow
	good   map[string]bool // client keys that have logged in since start; not paused by the global limit
}

type failState struct {
	n     int
	last  time.Time // latest failure
	until time.Time // no tries before this
}

const (
	globalWindow = 15 * time.Minute
	globalMax    = 50 // failures in globalWindow (from any number of addresses) before new addresses are paused
)

var security = map[string]string{
	"X-Frame-Options": "DENY", "Referrer-Policy": "same-origin", "X-Content-Type-Options": "nosniff",
	"Content-Security-Policy": "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'",
}

type httpError struct {
	status int
	msg    string
}

func (e httpError) Error() string       { return e.msg }
func fail(status int, msg string) error { return httpError{status, msg} }

type ctx struct {
	w      http.ResponseWriter
	r      *http.Request
	me     string
	ip     string
	body   map[string]any
	params map[string]string
}

func (c *ctx) str(k string) string {
	if v, ok := c.body[k].(string); ok {
		return v
	}
	return ""
}

type route struct {
	method string
	re     *regexp.Regexp
	keys   []string
	open   bool
	fn     func(*ctx) (any, error)
}

func (s *Server) routes() []route {
	var rs []route
	add := func(method, pattern string, open bool, fn func(*ctx) (any, error)) {
		var keys []string
		re := regexp.MustCompile(`:(\w+)`).ReplaceAllStringFunc(pattern, func(m string) string { keys = append(keys, m[1:]); return `([^/]+)` })
		rs = append(rs, route{method, regexp.MustCompile("^" + re + "$"), keys, open, fn})
	}
	// --- auth (open) ---
	add("GET", "/api/session", true, func(c *ctx) (any, error) {
		if c.me == "" {
			return map[string]any{"user": nil}, nil
		}
		return map[string]any{"user": c.me}, nil
	})
	add("POST", "/api/login", true, func(c *ctx) (any, error) {
		user := c.str("user")
		if err := s.throttled(c.ip); err != nil {
			return nil, err
		}
		if !s.Store.CheckPassword(user, c.str("password")) {
			s.failed(c.ip)
			return nil, fail(401, "Wrong username or password.")
		}
		s.succeeded(c.ip)
		c.w.Header().Add("Set-Cookie", s.Store.SessionCookie(user, s.Store.Users()[user].Gen))
		return map[string]any{"user": user}, nil
	})
	add("POST", "/api/logout", true, func(c *ctx) (any, error) {
		c.w.Header().Add("Set-Cookie", ClearCookie)
		return map[string]any{}, nil
	})
	add("GET", "/api/invite", true, func(c *ctx) (any, error) {
		return map[string]any{"valid": s.Store.InviteValid(c.r.URL.Query().Get("t"))}, nil
	})
	add("POST", "/api/invite", true, func(c *ctx) (any, error) {
		user, pw := c.str("user"), c.str("password")
		if err := s.throttled(c.ip); err != nil {
			return nil, err
		}
		if !s.Store.InviteValid(c.str("token")) {
			s.failed(c.ip)
			return nil, fail(410, "This invite was already used or has expired.")
		}
		switch {
		case !names.DashboardUser(user):
			return nil, fail(400, "Username: 2-32 of a-z 0-9 . _ -")
		case s.Store.Users()[user].Hash != "":
			return nil, fail(400, "That username is taken.")
		case len(pw) < 12:
			return nil, fail(400, "Password needs 12+ characters.")
		case pw != c.str("password2"):
			return nil, fail(400, "Passwords don't match.")
		}
		inv, ok := s.Store.UseInvite(c.str("token"))
		if !ok {
			return nil, fail(410, "This invite was already used or has expired.")
		}
		u := s.Store.SetPassword(user, pw, inv.By)
		c.w.Header().Add("Set-Cookie", s.Store.SessionCookie(user, u.Gen))
		return map[string]any{"user": user}, nil
	})
	// --- users (handled here, not by root) ---
	add("GET", "/api/users", false, func(c *ctx) (any, error) {
		var list []map[string]any
		for name, u := range s.Store.Users() {
			list = append(list, map[string]any{"name": name, "created": u.Created, "invitedBy": u.InvitedBy, "me": name == c.me})
		}
		if list == nil {
			list = []map[string]any{}
		}
		return map[string]any{"users": list, "inviteHours": inviteHours}, nil
	})
	add("POST", "/api/users/invite", false, func(c *ctx) (any, error) {
		return map[string]any{"link": "https://" + c.r.Host + "/invite?t=" + url.QueryEscape(s.Store.MakeInvite(c.me))}, nil
	})
	add("POST", "/api/users/:user/remove", false, func(c *ctx) (any, error) {
		if c.params["user"] == c.me {
			return nil, fail(400, "You can't remove yourself.")
		}
		if !s.Store.Remove(c.params["user"]) {
			return nil, fail(404, "No such user.")
		}
		return map[string]any{}, nil
	})
	add("POST", "/api/password", false, func(c *ctx) (any, error) {
		pw := c.str("password")
		if len(pw) < 12 || pw != c.str("password2") {
			return nil, fail(400, "Passwords must match and be 12+ characters.")
		}
		u := s.Store.SetPassword(c.me, pw, "")
		c.w.Header().Add("Set-Cookie", s.Store.SessionCookie(c.me, u.Gen))
		return map[string]any{}, nil
	})
	// --- everything else goes to the root helper ---
	helper := func(op string, params func(c *ctx) map[string]string, cmd bool) func(*ctx) (any, error) {
		return func(c *ctx) (any, error) {
			req := rpc.Request{Op: op, By: c.me}
			if params != nil {
				req.Params = params(c)
			}
			if op == "envApply" || op == "bind" {
				req.Set, req.Unset = map[string]string{}, []string{}
				if m, ok := c.body["set"].(map[string]any); ok {
					for k, v := range m {
						if sv, ok := v.(string); ok {
							req.Set[k] = sv
						}
					}
				}
				if a, ok := c.body["unset"].([]any); ok {
					for _, v := range a {
						if sv, ok := v.(string); ok {
							req.Unset = append(req.Unset, sv)
						}
					}
				}
			}
			resp, err := rpc.Call(s.Socket, req, 10*time.Minute)
			if err != nil {
				return nil, fail(502, err.Error())
			}
			if resp.Error != "" {
				st := resp.Status
				if st == 0 {
					st = 400
				}
				return nil, fail(st, resp.Error)
			}
			if cmd {
				return map[string]any{"ok": resp.OK, "code": resp.Code, "cmd": resp.Cmd, "out": resp.Out}, nil
			}
			return resp.Data, nil
		}
	}
	p := func(keys ...string) func(c *ctx) map[string]string {
		return func(c *ctx) map[string]string {
			m := map[string]string{}
			for _, k := range keys {
				if v, ok := c.params[k]; ok {
					m[k] = v
				} else if v := c.r.URL.Query().Get(k); v != "" {
					m[k] = v
				} else if v, ok := c.body[k]; ok {
					switch t := v.(type) {
					case string:
						m[k] = t
					case bool:
						m[k] = map[bool]string{true: "true", false: "false"}[t]
					case float64:
						m[k] = strings.TrimSuffix(strings.TrimRight(strings.TrimRight(fmtFloat(t), "0"), "."), ".")
					}
				}
			}
			return m
		}
	}
	add("GET", "/api/overview", false, helper("overview", nil, false))
	add("POST", "/api/apps", false, helper("createApp", p("name", "memory", "cpu", "tasks", "repo"), true))
	add("POST", "/api/apps/:app/delete", false, helper("deleteApp", p("app", "confirm"), true))
	add("POST", "/api/apps/:app/sync", false, helper("syncApp", p("app"), true))
	add("POST", "/api/sync-all", false, helper("syncAll", nil, true))
	add("POST", "/api/caddy/reload", false, helper("reloadCaddy", nil, true))
	add("POST", "/api/apps/:app/sites/:site/:op", false, helper("siteOp", p("app", "site", "op"), true))
	add("GET", "/api/apps/:app/sites/:site/logs", false, helper("siteLogs", p("app", "site"), false))
	add("GET", "/api/apps/:app/sync-log", false, helper("syncLog", p("app"), false))
	add("GET", "/api/apps/:app/instructions", false, helper("instructions", p("app", "site"), false))
	add("POST", "/api/apps/:app/repo", false, helper("setRepo", p("app", "repo"), false))
	add("GET", "/api/services", false, helper("services", nil, false))
	add("POST", "/api/services/bind", false, helper("bind", p("app", "interface", "provider"), true))
	add("POST", "/api/services/unbind", false, helper("unbind", p("app", "interface"), true))
	add("GET", "/api/apps/:app/tokens", false, helper("tokenList", p("app"), false))
	add("POST", "/api/apps/:app/tokens", false, helper("tokenCreate", p("app", "label"), false))
	add("POST", "/api/apps/:app/tokens/:id/revoke", false, helper("tokenRevoke", p("app", "id"), false))
	add("GET", "/api/apps/:app/env", false, helper("envGet", p("app", "scope"), false))
	add("POST", "/api/apps/:app/env", false, helper("envApply", p("app", "scope"), true))
	add("GET", "/api/domains", false, helper("domains", nil, false))
	add("POST", "/api/grants", false, helper("grant", p("op", "app", "kind", "name"), true))
	add("POST", "/api/default-domain", false, helper("setDefaultDomain", p("domain"), true))
	add("POST", "/api/dashboard-host", false, helper("setDashboardHost", p("host"), true))
	add("POST", "/api/ip-access", false, helper("setIPAccess", p("on"), true))
	add("GET", "/api/update", false, helper("updateStatus", nil, false))
	add("POST", "/api/update", false, helper("updateNow", nil, true))
	return rs
}

func fmtFloat(f float64) string {
	if f == math.Trunc(f) {
		return strings.TrimSuffix(strings.TrimSuffix(jsonNum(f), ".0"), ".")
	}
	return jsonNum(f)
}

func jsonNum(f float64) string { b, _ := json.Marshal(f); return string(b) }

// --- login throttle ---
// Each client (IPv4 address or IPv6 /64) gets 5 free tries, then waits 1s, 2s, 4s ... up to a minute between
// tries. The wait is enforced before the password is checked, so parallel guesses don't get around it. Across
// all clients, more than globalMax failures in globalWindow pauses logins from clients that haven't logged in
// successfully since the dashboard started (open sessions keep working).

func clientKey(ip string) string {
	if a := net.ParseIP(ip); a != nil && a.To4() == nil {
		return a.Mask(net.CIDRMask(64, 128)).String() + "/64"
	}
	return ip
}

func (s *Server) throttled(ip string) error {
	k, now := clientKey(ip), time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if f := s.fails[k]; f != nil && now.Before(f.until) {
		return fail(429, fmt.Sprintf("Too many tries. Wait %d seconds.", int(f.until.Sub(now).Seconds())+1))
	}
	s.prune(now)
	if len(s.recent) >= globalMax && !s.good[k] {
		return fail(429, "Logins are paused for a few minutes after many failed attempts. Try again later.")
	}
	return nil
}

func (s *Server) failed(ip string) {
	k, now := clientKey(ip), time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fails == nil {
		s.fails = map[string]*failState{}
	}
	f := s.fails[k]
	if f == nil {
		f = &failState{}
		s.fails[k] = f
	}
	f.n++
	f.last = now
	if f.n > 5 {
		f.until = now.Add(time.Duration(math.Min(60, math.Pow(2, math.Min(float64(f.n-6), 6)))) * time.Second)
	}
	s.recent = append(s.recent, now)
}

func (s *Server) succeeded(ip string) {
	k := clientKey(ip)
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.fails, k)
	if s.good == nil {
		s.good = map[string]bool{}
	}
	if len(s.good) < 1000 {
		s.good[k] = true
	}
}

// prune drops failures older than globalWindow and clients that have been quiet that long. Caller holds s.mu.
func (s *Server) prune(now time.Time) {
	i := 0
	for i < len(s.recent) && now.Sub(s.recent[i]) > globalWindow {
		i++
	}
	s.recent = s.recent[i:]
	for k, f := range s.fails {
		if now.Sub(f.last) > globalWindow && now.After(f.until) {
			delete(s.fails, k)
		}
	}
}

func clientIP(r *http.Request) string {
	// Only Caddy can connect (the socket is caddy-group only), and Caddy replaces any X-Forwarded-For a client sends.
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		return strings.TrimSpace(parts[len(parts)-1])
	}
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	return host
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	for k, v := range security {
		w.Header().Set(k, v)
	}
	defer func() {
		if e := recover(); e != nil {
			log.Printf("panic: %v", e)
			writeJSON(w, 500, map[string]string{"error": "internal error"})
		}
	}()
	if strings.HasPrefix(r.URL.Path, "/api/v1/") {
		s.serveDeployAPI(w, r)
		return
	}
	if !strings.HasPrefix(r.URL.Path, "/api/") {
		if r.Method != "GET" && r.Method != "HEAD" {
			writeJSON(w, 405, map[string]string{"error": "method not allowed"})
			return
		}
		s.serveUI(w, r)
		return
	}
	c := &ctx{w: w, r: r, ip: clientIP(r), body: map[string]any{}}
	if r.Method == "POST" {
		// only JSON posted from this site: a cross-site form can't send application/json without a CORS preflight
		origin, err := url.Parse(r.Header.Get("Origin"))
		if err != nil || origin.Host == "" || origin.Host != r.Host {
			writeJSON(w, 403, map[string]string{"error": "bad origin"})
			return
		}
		if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
			writeJSON(w, 415, map[string]string{"error": "expected application/json"})
			return
		}
		b, err := io.ReadAll(io.LimitReader(r.Body, 20001))
		if err != nil || len(b) > 20000 {
			writeJSON(w, 413, map[string]string{"error": "too big"})
			return
		}
		if len(b) > 0 && json.Unmarshal(b, &c.body) != nil {
			writeJSON(w, 400, map[string]string{"error": "bad json"})
			return
		}
	}
	c.me = s.Store.SessionUser(r.Header.Get("Cookie"))
	for _, rt := range s.routes() {
		if rt.method != r.Method {
			continue
		}
		m := rt.re.FindStringSubmatch(r.URL.Path)
		if m == nil {
			continue
		}
		if !rt.open && c.me == "" {
			writeJSON(w, 401, map[string]string{"error": "Logged out."})
			return
		}
		c.params = map[string]string{}
		for i, k := range rt.keys {
			v, _ := url.PathUnescape(m[i+1])
			c.params[k] = v
		}
		res, err := rt.fn(c)
		if err != nil {
			if he, ok := err.(httpError); ok {
				writeJSON(w, he.status, map[string]string{"error": he.msg})
			} else {
				writeJSON(w, 500, map[string]string{"error": err.Error()})
			}
			return
		}
		if raw, ok := res.(json.RawMessage); ok {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Cache-Control", "no-store")
			w.WriteHeader(200)
			w.Write(raw)
			return
		}
		writeJSON(w, 200, res)
		return
	}
	writeJSON(w, 404, map[string]string{"error": "not found"})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func (s *Server) serveUI(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	data, err := fs.ReadFile(s.UI, p)
	if p == "" || err != nil {
		p = "index.html" // SPA fallback
		data, err = fs.ReadFile(s.UI, p)
		if err != nil {
			http.Error(w, "dashboard UI missing", 500)
			return
		}
	}
	types := map[string]string{".html": "text/html; charset=utf-8", ".js": "text/javascript", ".css": "text/css", ".svg": "image/svg+xml", ".png": "image/png", ".ico": "image/x-icon", ".woff2": "font/woff2", ".json": "application/json"}
	w.Header().Set("Content-Type", types[path.Ext(p)])
	if strings.HasPrefix(p, "assets/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}
	w.Write(data)
}
