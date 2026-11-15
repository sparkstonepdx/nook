package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

const (
	dash  = "admin.example.com"
	notes = "notes.example.com"
)

func loginServer(t *testing.T) *Server {
	t.Helper()
	st, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	st.SetPassword("ada", "correct horse battery", "")
	st.SetPassword("mo", "correct horse battery", "ada")
	st.SetAccess("mo", Member, []string{"notes", "notes", "../x"})
	return &Server{Store: st, Socket: "/nonexistent"}
}

func session(s *Server, user string) string {
	c := s.Store.SessionCookie(user, s.Store.Users()[user].Gen)
	return strings.SplitN(c, ";", 2)[0]
}

func do(s *Server, r *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}

// check is what Caddy's forward_auth sends for a request to the app's site.
func check(app, host, cookie string, browser bool) *http.Request {
	r := httptest.NewRequest("GET", "/api/v1/login-check?app="+app+"&host="+host+"&dash="+dash, nil)
	r.Header.Set("X-Forwarded-Method", "GET")
	r.Header.Set("X-Forwarded-Uri", "/notes/42?x=1")
	if browser {
		r.Header.Set("Accept", "text/html,application/xhtml+xml")
		r.Header.Set("Sec-Fetch-Mode", "navigate")
	}
	if cookie != "" {
		r.Header.Set("Cookie", cookie)
	}
	return r
}

// login walks a browser through the whole hand-off for user and returns the site cookie it ends with.
func login(t *testing.T, s *Server, user, app, host string) string {
	t.Helper()
	w := do(s, check(app, host, "", true))
	if w.Code != 302 {
		t.Fatalf("check without a login: %d %s", w.Code, w.Body)
	}
	to, _ := url.Parse(w.Header().Get("Location"))
	if to.Host != dash || to.Path != "/sso" {
		t.Fatalf("sent to %s", to)
	}
	r := httptest.NewRequest("GET", to.RequestURI(), nil)
	r.Host = dash
	r.Header.Set("Cookie", session(s, user))
	w = do(s, r)
	if w.Code != 302 {
		t.Fatalf("sso: %d %s", w.Code, w.Body)
	}
	cb, _ := url.Parse(w.Header().Get("Location"))
	if cb.Host != host || cb.Path != "/.nook/callback" {
		t.Fatalf("sso sent to %s", cb)
	}
	r = httptest.NewRequest("GET", cb.RequestURI(), nil)
	r.Host = host
	w = do(s, r)
	if w.Code != 302 || w.Header().Get("Location") != "/notes/42?x=1" {
		t.Fatalf("callback: %d to %q", w.Code, w.Header().Get("Location"))
	}
	// the ticket works once
	if again := do(s, r); again.Code != 400 {
		t.Errorf("ticket reused: %d", again.Code)
	}
	c := w.Header().Get("Set-Cookie")
	if !strings.HasPrefix(c, siteCookie+"=") || !strings.Contains(c, "SameSite=Lax") || !strings.Contains(c, "Secure") {
		t.Fatalf("cookie: %q", c)
	}
	return strings.SplitN(c, ";", 2)[0]
}

func TestLoginHandOff(t *testing.T) {
	s := loginServer(t)
	c := login(t, s, "ada", "notes", notes)
	w := do(s, check("notes", notes, c, true))
	if w.Code != 204 || w.Header().Get("X-Nook-User") != "ada" {
		t.Fatalf("logged in: %d %q", w.Code, w.Header().Get("X-Nook-User"))
	}
	// both headers, so Caddy 2.6's copy_headers never passes its placeholder's text for the missing one
	if v, ok := w.Header()["X-Nook-App"]; !ok || v[0] != "" {
		t.Errorf("X-Nook-App should be there and empty: %q", v)
	}
	r := check("notes", notes, "", false)
	r.Header.Set("X-Nook-Card", s.Store.cardKey())
	if w := do(s, r); w.Code != 204 || len(w.Header()["X-Nook-User"]) != 1 || len(w.Header()["X-Nook-App"]) != 1 {
		t.Errorf("card read: %d %v", w.Code, w.Header())
	}
	// bound to its host
	if w := do(s, check("blog", "blog.example.com", c, true)); w.Code != 302 {
		t.Errorf("cookie worked on another host: %d", w.Code)
	}
	// a password change ends it
	s.Store.SetPassword("ada", "another long password", "")
	if w := do(s, check("notes", notes, c, true)); w.Code != 302 {
		t.Errorf("cookie outlived a password change: %d", w.Code)
	}
}

func TestLoginMembers(t *testing.T) {
	s := loginServer(t)
	if u := s.Store.Users()["mo"]; len(u.Apps) != 1 || u.Apps[0] != "notes" || u.Admin() {
		t.Fatalf("mo = %+v", u)
	}
	c := login(t, s, "mo", "notes", notes)
	if w := do(s, check("notes", notes, c, true)); w.Code != 204 || w.Header().Get("X-Nook-User") != "mo" {
		t.Fatalf("member on own app: %d", w.Code)
	}
	// not their app: the dashboard refuses the ticket
	w := do(s, check("blog", "blog.example.com", "", true))
	to, _ := url.Parse(w.Header().Get("Location"))
	r := httptest.NewRequest("GET", to.RequestURI(), nil)
	r.Header.Set("Cookie", session(s, "mo"))
	if w := do(s, r); w.Code != 403 {
		t.Errorf("sso for someone else's app: %d", w.Code)
	}
	// losing the app ends access at the next request
	s.Store.SetAccess("mo", Member, nil)
	if w := do(s, check("notes", notes, c, true)); w.Code != 403 {
		t.Errorf("after losing the app: %d", w.Code)
	}
}

func TestLoginRefusals(t *testing.T) {
	s := loginServer(t)
	// a script without a token is asked for one; a page's fetch isn't (no Basic popup)
	if w := do(s, check("notes", notes, "", false)); w.Code != 401 || w.Header().Get("WWW-Authenticate") == "" {
		t.Errorf("script: %d %q", w.Code, w.Header().Get("WWW-Authenticate"))
	}
	r := check("notes", notes, "", false)
	r.Header.Set("Sec-Fetch-Mode", "cors")
	if w := do(s, r); w.Code != 401 || w.Header().Get("WWW-Authenticate") != "" {
		t.Errorf("fetch: %d %q", w.Code, w.Header().Get("WWW-Authenticate"))
	}
	// nook-web's own reads for cards
	r = check("notes", notes, "", false)
	r.Header.Set("X-Nook-Card", s.Store.cardKey())
	if w := do(s, r); w.Code != 204 {
		t.Errorf("card key: %d", w.Code)
	}
	r.Header.Set("X-Nook-Card", "guess")
	if w := do(s, r); w.Code == 204 {
		t.Error("a wrong card key got in")
	}
	// /sso only answers links a login check signed
	r = httptest.NewRequest("GET", "/sso?app=notes&host=evil.example.com&path=/&sig=x", nil)
	r.Header.Set("Cookie", session(s, "ada"))
	if w := do(s, r); w.Code != 400 {
		t.Errorf("unsigned sso: %d", w.Code)
	}
	// a ticket for one host doesn't work on another
	tk := s.Store.makeTicket(notes, "ada", s.Store.Users()["ada"].Gen)
	r = httptest.NewRequest("GET", "/.nook/callback?t="+url.QueryEscape(tk)+"&path=/", nil)
	r.Host = "blog.example.com"
	if w := do(s, r); w.Code != 400 {
		t.Errorf("ticket on another host: %d", w.Code)
	}
}

func TestSSOWithoutSession(t *testing.T) {
	s := loginServer(t)
	w := do(s, check("notes", notes, "", true))
	to, _ := url.Parse(w.Header().Get("Location"))
	// first arrival: one same-site hop, since a cross-site arrival doesn't carry the Strict cookie
	w = do(s, httptest.NewRequest("GET", to.RequestURI(), nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "again=1") {
		t.Fatalf("first hop: %d %s", w.Code, w.Body)
	}
	w = do(s, httptest.NewRequest("GET", to.RequestURI()+"&again=1", nil))
	if w.Code != 302 || !strings.HasPrefix(w.Header().Get("Location"), "/login?next=%2Fsso%3F") {
		t.Fatalf("then the login page: %d %q", w.Code, w.Header().Get("Location"))
	}
}

func TestLocalPath(t *testing.T) {
	for in, want := range map[string]string{"/a?b=1": "/a?b=1", "//evil.com": "/", "/\\evil.com": "/", "https://evil.com": "/", "": "/", "/x\r\nSet-Cookie: a": "/"} {
		if got := localPath(in); got != want {
			t.Errorf("localPath(%q) = %q", in, got)
		}
	}
}

func TestMembersAPI(t *testing.T) {
	s := loginServer(t)
	get := func(user, path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("Cookie", session(s, user))
		return do(s, r)
	}
	if w := get("mo", "/api/overview"); w.Code != 403 {
		t.Errorf("member overview: %d", w.Code)
	}
	if w := get("mo", "/api/users"); w.Code != 403 {
		t.Errorf("member users: %d", w.Code)
	}
	if w := get("mo", "/api/directory"); w.Code == 403 {
		t.Error("members get their launcher")
	}
	var sess struct {
		User  string   `json:"user"`
		Admin bool     `json:"admin"`
		Apps  []string `json:"apps"`
	}
	json.Unmarshal(get("mo", "/api/session").Body.Bytes(), &sess)
	if sess.User != "mo" || sess.Admin || len(sess.Apps) != 1 {
		t.Errorf("member session: %+v", sess)
	}
	json.Unmarshal(get("ada", "/api/session").Body.Bytes(), &sess)
	if !sess.Admin {
		t.Errorf("admin session: %+v", sess)
	}
}

func TestMemberInvite(t *testing.T) {
	s := loginServer(t)
	inv, ok := s.Store.UseInvite(s.Store.MakeRoleInvite("ada", Member, []string{"notes", "blog"}))
	if !ok || inv.Role != Member || strings.Join(inv.Apps, ",") != "blog,notes" {
		t.Fatalf("member invite: %+v %v", inv, ok)
	}
	inv, ok = s.Store.UseInvite(s.Store.MakeInvite("ada"))
	if !ok || inv.Role != "" {
		t.Fatalf("admin invite: %+v %v", inv, ok)
	}
}
