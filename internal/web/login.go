package web

// Nook login for apps (an app's login switch): Caddy asks nook-web about every request to a site of that app.
//
//	GET /api/v1/login-check?app=A&host=H&dash=D   (forward_auth; Caddy writes the query, not the client)
//	    204 + X-Nook-User   a logged-in dashboard user allowed on the app (site cookie, below)
//	    204 + X-Nook-App    a deploy token, as with {"auth": "nook-token"}
//	    302 to https://D/sso  a browser opening a page: log in at the dashboard
//	    401 / 403           anything else
//	GET https://D/sso?...   on the dashboard: with a session that may use the app, a one-time ticket back to
//	                        the site; without one, the login page first
//	GET https://H/.nook/callback?t=TICKET&path=P   trades the ticket for the site's own cookie
//	GET https://H/.nook/logout                     drops the site's cookie and the dashboard session
//
// The dashboard's session cookie is SameSite=Strict on its own host, so each site gets its own cookie
// (__Host-nook-login, bound to that host by its signature). Both carry the user's generation, so a password
// change, a removal or a lost permission ends every one of them at the next request.

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/sparkstonepdx/nook/internal/names"
	"github.com/sparkstonepdx/nook/internal/rpc"
)

const (
	siteCookie = "__Host-nook-login"
	ticketTTL  = 60 * time.Second
)

// cardKey lets nook-web's own reads of a site (for its directory card) past the login check.
func (s *Store) cardKey() string { return s.mac("card") }

func (s *Store) ssoSig(app, host, path string) string {
	return s.mac("sso:" + app + "|" + host + "|" + path)
}

// ---- one-time tickets: dashboard -> site ----

func (s *Store) makeTicket(host, user string, gen int) string {
	nonce := make([]byte, 12)
	rand.Read(nonce)
	payload := base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf("%d|%s|%s|%d|%s", time.Now().Add(ticketTTL).Unix(), host, user, gen, hex.EncodeToString(nonce))))
	return payload + "." + s.mac("ticket:"+payload)
}

// useTicket checks a ticket for host and spends it.
func (srv *Server) useTicket(tok, host string) (string, int, bool) {
	s := srv.Store
	i := strings.LastIndexByte(tok, '.')
	if i <= 0 || subtle.ConstantTimeCompare([]byte(tok[i+1:]), []byte(s.mac("ticket:"+tok[:i]))) != 1 {
		return "", 0, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(tok[:i])
	if err != nil {
		return "", 0, false
	}
	f := strings.Split(string(raw), "|")
	if len(f) != 5 || f[1] != host {
		return "", 0, false
	}
	exp, _ := strconv.ParseInt(f[0], 10, 64)
	gen, err := strconv.Atoi(f[3])
	now := time.Now().Unix()
	if err != nil || exp < now {
		return "", 0, false
	}
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if srv.tickets == nil {
		srv.tickets = map[string]int64{}
	}
	for k, e := range srv.tickets {
		if e < now {
			delete(srv.tickets, k)
		}
	}
	if _, used := srv.tickets[f[4]]; used {
		return "", 0, false
	}
	srv.tickets[f[4]] = exp
	return f[2], gen, true
}

// ---- the site's own cookie ----

func (s *Store) siteCookie(host, user string, gen int) string {
	payload := fmt.Sprintf("%s.%d.%d", user, gen, time.Now().Add(sessionDays*24*time.Hour).UnixMilli())
	return fmt.Sprintf("%s=%s.%s; Path=/; HttpOnly; Secure; SameSite=Lax; Max-Age=%d", siteCookie, payload, s.mac("site:"+host+"|"+payload), sessionDays*86400)
}

const clearSiteCookie = siteCookie + "=; Path=/; HttpOnly; Secure; SameSite=Lax; Max-Age=0"

// siteUser is the user a request's site cookie names, if it's good for host and still current.
func (s *Store) siteUser(r *http.Request, host string) (string, User) {
	c, err := r.Cookie(siteCookie)
	if err != nil {
		return "", User{}
	}
	f := strings.Split(c.Value, ".")
	if len(f) != 4 {
		return "", User{}
	}
	payload := f[0] + "." + f[1] + "." + f[2]
	if subtle.ConstantTimeCompare([]byte(f[3]), []byte(s.mac("site:"+host+"|"+payload))) != 1 {
		return "", User{}
	}
	exp, err := strconv.ParseInt(f[2], 10, 64)
	if err != nil || exp < time.Now().UnixMilli() {
		return "", User{}
	}
	u, ok := s.Users()[f[0]]
	if !ok || strconv.Itoa(u.Gen) != f[1] {
		return "", User{}
	}
	return f[0], u
}

func hostOnly(h string) string {
	if host, _, err := net.SplitHostPort(h); err == nil {
		return strings.ToLower(host)
	}
	return strings.ToLower(h)
}

// localPath keeps a return path on the same site: one leading slash, no scheme or host.
func localPath(p string) string {
	if !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") || strings.HasPrefix(p, "/\\") || len(p) > 4096 || strings.ContainsAny(p, "\r\n") {
		return "/"
	}
	return p
}

// ---- handlers ----

func (srv *Server) loginCheck(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	q := r.URL.Query()
	app, host, dash := q.Get("app"), q.Get("host"), q.Get("dash")
	if !names.App(app) || !names.Host(host) {
		apiError(w, 400, "bad login check")
		return
	}
	if k := r.Header.Get("X-Nook-Card"); k != "" && subtle.ConstantTimeCompare([]byte(k), []byte(srv.Store.cardKey())) == 1 {
		w.WriteHeader(204)
		return
	}
	if tok, ok := requestToken(r, true); ok {
		srv.tokenCheck(w, r, tok)
		return
	}
	if user, u := srv.Store.siteUser(r, host); user != "" {
		if !u.CanUse(app) {
			page(w, 403, "No access", fmt.Sprintf("Your nook login, %s, can't open %s. Ask an admin to add the app to your account.", user, host), "/.nook/logout", "Log in as someone else")
			return
		}
		w.Header().Set("X-Nook-User", user)
		w.WriteHeader(204)
		return
	}
	method, uri := r.Header.Get("X-Forwarded-Method"), localPath(r.Header.Get("X-Forwarded-Uri"))
	browser := r.Header.Get("Sec-Fetch-Mode") != "" || r.Header.Get("Cookie") != ""
	if (method == "GET" || method == "HEAD") && strings.Contains(r.Header.Get("Accept"), "text/html") && names.Host(dash) {
		v := url.Values{"app": {app}, "host": {host}, "path": {uri}, "sig": {srv.Store.ssoSig(app, host, uri)}}
		http.Redirect(w, r, "https://"+dash+"/sso?"+v.Encode(), http.StatusFound)
		return
	}
	if !browser {
		// a script or CLI: it can answer with a deploy token as the password (Docker only sends one after this)
		w.Header().Set("WWW-Authenticate", `Basic realm="nook"`)
	}
	apiError(w, 401, "log in at https://%s/ first, or send a deploy token", host)
}

// tokenCheck answers forward_auth for a deploy token, as /api/v1/token-check does.
func (srv *Server) tokenCheck(w http.ResponseWriter, r *http.Request, tok string) {
	ip := clientIP(r)
	if err := srv.throttled(ip); err != nil {
		he := err.(httpError)
		apiError(w, he.status, "%s", he.msg)
		return
	}
	who, err := rpc.Call(srv.Socket, rpc.Request{Op: "tokenApp", By: "api", Secret: tok}, 10*time.Second)
	if err != nil {
		apiError(w, 502, "%v", err)
		return
	}
	if who.Error != "" {
		if who.Status == 401 {
			srv.failed(ip)
			w.Header().Set("WWW-Authenticate", `Basic realm="nook"`)
		}
		apiError(w, orDefault(who.Status, 500), "%s", who.Error)
		return
	}
	var info struct {
		App string `json:"app"`
	}
	json.Unmarshal(who.Data, &info)
	w.Header().Set("X-Nook-App", info.App)
	w.WriteHeader(204)
}

// sso runs on the dashboard: a signed request from a site's login check, answered with a ticket back to it.
func (srv *Server) sso(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	q := r.URL.Query()
	app, host, path := q.Get("app"), q.Get("host"), q.Get("path")
	if !names.App(app) || !names.Host(host) || subtle.ConstantTimeCompare([]byte(q.Get("sig")), []byte(srv.Store.ssoSig(app, host, path))) != 1 {
		page(w, 400, "Bad login link", "This link didn't come from a nook site. Open the site again.", "/", "Dashboard")
		return
	}
	back := url.Values{"app": {app}, "host": {host}, "path": {path}, "sig": {q.Get("sig")}}
	me := srv.Store.SessionUser(r.Header.Get("Cookie"))
	if me == "" {
		if q.Get("again") == "" {
			// Arriving from another site, the browser holds back the dashboard's SameSite=Strict cookie. One
			// hop from this page is same-site, so it's sent if there is one.
			again := back
			again.Set("again", "1")
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprintf(w, `<!doctype html><meta charset="utf-8"><meta http-equiv="refresh" content="0;url=/sso?%s"><title>nook</title><p>Logging in…</p>`, html.EscapeString(again.Encode()))
			return
		}
		http.Redirect(w, r, "/login?next="+url.QueryEscape("/sso?"+back.Encode()), http.StatusFound)
		return
	}
	u := srv.Store.Users()[me]
	if !u.CanUse(app) {
		page(w, 403, "No access", fmt.Sprintf("Your nook login, %s, can't open %s. Ask an admin to add the app to your account.", me, host), "/", "Your apps")
		return
	}
	v := url.Values{"t": {srv.Store.makeTicket(host, me, u.Gen)}, "path": {localPath(path)}}
	http.Redirect(w, r, "https://"+host+"/.nook/callback?"+v.Encode(), http.StatusFound)
}

// nookPath serves /.nook/* on a site behind the login.
func (srv *Server) nookPath(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	host := hostOnly(r.Host)
	switch r.URL.Path {
	case "/.nook/callback":
		user, gen, ok := srv.useTicket(r.URL.Query().Get("t"), host)
		if !ok {
			page(w, 400, "Login expired", "That login link was used or is too old.", "/", "Try again")
			return
		}
		w.Header().Add("Set-Cookie", srv.Store.siteCookie(host, user, gen))
		http.Redirect(w, r, localPath(r.URL.Query().Get("path")), http.StatusFound)
	case "/.nook/logout":
		w.Header().Add("Set-Cookie", clearSiteCookie)
		if dash := r.Header.Get("X-Nook-Dash"); names.Host(dash) {
			http.Redirect(w, r, "https://"+dash+"/logout", http.StatusFound)
			return
		}
		page(w, 200, "Logged out", "You're logged out of "+host+".", "/", "Log in again")
	default:
		page(w, 404, "Not found", "", "/", "Back")
	}
}

// logout (GET on the dashboard, where a site's log out lands) ends the dashboard session.
func (srv *Server) logout(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Add("Set-Cookie", ClearCookie)
	page(w, 200, "Logged out", "You're logged out of nook.", "/login", "Log in")
}

// page is a small self-contained HTML answer, for browsers outside the dashboard app.
func page(w http.ResponseWriter, status int, title, msg, href, link string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	fmt.Fprintf(w, `<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>%[1]s</title>`+
		`<style>body{font:16px/1.5 system-ui,sans-serif;max-width:32rem;margin:15vh auto;padding:0 1.5rem;color-scheme:light dark}a{color:inherit}</style>`+
		`<h1>%[1]s</h1><p>%[2]s</p><p><a href="%[3]s">%[4]s</a></p></html>`, html.EscapeString(title), html.EscapeString(msg), html.EscapeString(href), html.EscapeString(link))
}
