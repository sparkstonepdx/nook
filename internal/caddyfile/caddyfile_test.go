package caddyfile

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sparkstonepdx/nook/internal/redirects"
)

func TestRoundTrip(t *testing.T) {
	text := Render("/srv/nook/a/sites", []Site{{Host: "api.x.com", Port: 20001, Folder: "api.x.com", Version: "11"}, {Host: "x.com", Root: "/var/lib/nook/www/a/x.com", Folder: "x.com", Version: "42"}}, [][2]string{{"www.x.com", "x.com"}}, nil)
	if Version(text, "x.com") != "42" || Version(text, "api.x.com") != "11" || Version(text, "nope") != "" {
		t.Fatalf("Version:\n%s", text)
	}
	live, red := Hosts(text)
	if strings.Join(live, ",") != "api.x.com,x.com" || strings.Join(red, ",") != "www.x.com" {
		t.Fatalf("live=%v red=%v\n%s", live, red, text)
	}
	if !strings.Contains(text, "\t\t@own file /404.html\n\t\thandle @own {\n\t\t\trewrite * /404.html") || !strings.Contains(text, "\t\thandle {\n\t\t\troot * /etc/caddy/nook-404\n") {
		t.Fatalf("static block: own 404.html first, then the server's:\n%s", text)
	}
	out, found := WithoutRedirect(text, "www.x.com")
	if !found || strings.Contains(out, "www.x.com") || !strings.Contains(out, "x.com {") {
		t.Fatalf("WithoutRedirect:\n%s", out)
	}
}

func TestRenderCORS(t *testing.T) {
	text := Render("app", []Site{{Host: "a.test", Root: "/w/a", CORS: true}, {Host: "b.test", Root: "/w/b"}, {Host: "c.test", Port: 9000, CORS: true}}, nil, nil)
	a := text[strings.Index(text, "a.test {"):strings.Index(text, "b.test {")]
	if !strings.Contains(a, "\theader Access-Control-Allow-Origin *\n") {
		t.Fatalf("static site with cors has no header:\n%s", a)
	}
	if n := strings.Count(text, "Access-Control-Allow-Origin"); n != 1 {
		t.Fatalf("want the header only on a.test, got %d:\n%s", n, text)
	}
	if live, _ := Hosts(text); len(live) != 3 {
		t.Fatalf("Hosts after cors: %v", live)
	}
}

func TestRenderRules(t *testing.T) {
	rules, _ := redirects.Parse("/sync /sync.html 200\n/blog/:slug /posts/:slug 302\n/forced /x 301!\n")
	text := Render("app", []Site{{Host: "a.test", Root: "/w/a", Rules: rules}, {Host: "b.test", Root: "/w/b"}}, nil, nil)
	want := "a.test {\n\troot * /w/a\n\thandle /_redirects {\n\t\terror 404\n\t}\n" +
		"\t@nook_r0 {\n\t\tpath_regexp nook_r0 `^/sync/?$`\n\t\tnot file\n\t}\n\thandle @nook_r0 {\n\t\trewrite * /sync.html\n\t\tfile_server {\n\t\t\tprecompressed gzip\n\t\t}\n\t}\n" +
		"\t@nook_r1 {\n\t\tpath_regexp nook_r1 `^/blog/(?P<slug>[^/]+)/?$`\n\t\tnot file\n\t}\n\thandle @nook_r1 {\n\t\tredir /posts/{re.nook_r1.slug} 302\n\t}\n" +
		"\t@nook_r2 {\n\t\tpath_regexp nook_r2 `^/forced/?$`\n\t}\n\thandle @nook_r2 {\n\t\tredir /x 301\n\t}\n" +
		"\thandle {\n\t\ttry_files {path} {path}/ /200.html\n\t\tfile_server {\n\t\t\tprecompressed gzip\n\t\t}\n\t}\n\thandle_errors {"
	if !strings.Contains(text, want) {
		t.Fatalf("got:\n%s", text)
	}
	b := text[strings.Index(text, "b.test {"):]
	if !strings.Contains(b, "\thandle /_redirects {\n\t\terror 404\n\t}\n\ttry_files {path} {path}/ /200.html\n\tfile_server {") {
		t.Fatalf("site without rules:\n%s", b)
	}
	if live, _ := Hosts(text); strings.Join(live, ",") != "a.test,b.test" {
		t.Fatalf("Hosts: %v", live)
	}
}

func TestRenderOwnCert(t *testing.T) {
	own := func(h string) bool { return h == "api.x.com" || h == "www.x.com" }
	text := Render("app", []Site{{Host: "api.x.com", Port: 9000}, {Host: "x.com", Root: "/w/x"}, {Host: "b.x.com", Root: "/w/b"}}, [][2]string{{"www.x.com", "x.com"}}, own)
	for _, want := range []string{"api.x.com {\n\ttls force_automate\n\treverse_proxy", "www.x.com { # redirect\n\ttls force_automate\n\tredir", "b.x.com {\n\troot"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in\n%s", want, text)
		}
	}
	if strings.Count(text, "force_automate") != 2 {
		t.Errorf("force_automate on the wrong hosts:\n%s", text)
	}
	if live, redirs := Hosts(text); len(live) != 3 || len(redirs) != 1 {
		t.Errorf("Hosts read back %v %v", live, redirs)
	}
}

func TestTokenAuth(t *testing.T) {
	out := Render("x", []Site{{Host: "npm.example.com", Port: 20001, TokenAuth: true}}, nil, nil)
	for _, want := range []string{"request_header -X-Nook-App", "forward_auth unix//run/nook-web.sock {", "uri /api/v1/token-check", "copy_headers X-Nook-App", "reverse_proxy 127.0.0.1:20001 {", "header_up -Authorization"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if i, j := strings.Index(out, "request_header"), strings.Index(out, "forward_auth"); i > j {
		t.Errorf("the client's X-Nook-App must go before the check:\n%s", out)
	}
}

func TestTokenAuthOpenPaths(t *testing.T) {
	out := Render("x", []Site{{Host: "push.example.com", Port: 20002, TokenAuth: true, Open: []string{"/v1/health", "/v1/send/*"}}}, nil, nil)
	for _, want := range []string{"@nook_open path /v1/health /v1/send/*", "handle @nook_open {", "\t\t\trequest_header -X-Nook-App\n\t\t\treverse_proxy 127.0.0.1:20002 {\n\t\t\t\theader_up -Authorization\n", "\thandle {\n\t\troute {", "\t\t\tforward_auth unix/"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestLoginRunning(t *testing.T) {
	out := Render("x", []Site{{Host: "notes.example.com", Port: 20003, Login: "notes", Dashboard: "admin.example.com"}}, nil, nil)
	for _, want := range []string{
		"\thandle /.nook/* {\n\t\treverse_proxy unix//run/nook-web.sock {\n\t\t\theader_up X-Nook-Dash admin.example.com\n\t\t}\n\t}\n",
		"uri /api/v1/login-check?app=notes&host=notes.example.com&dash=admin.example.com",
		"copy_headers X-Nook-App X-Nook-User",
		"\t\t\treverse_proxy 127.0.0.1:20003 {\n\t\t\t\theader_up -X-Nook-Card\n\t\t\t}\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "header_up -Authorization") {
		t.Errorf("a login-only app keeps its own Authorization headers:\n%s", out)
	}
	if i, j := strings.Index(out, "request_header -X-Nook-User"), strings.Index(out, "forward_auth"); i > j {
		t.Errorf("the client's X-Nook-User must go before the check:\n%s", out)
	}
	if live, _ := Hosts(out); len(live) != 1 || live[0] != "notes.example.com" {
		t.Errorf("Hosts = %v", live)
	}
}

func TestLoginStaticWithRules(t *testing.T) {
	rules, _ := redirects.Parse("/old /new 301\n/app/* /app.html 200\n")
	out := Render("x", []Site{{Host: "docs.example.com", Root: "/var/lib/nook/www/docs/docs", Login: "docs", Dashboard: "admin.example.com", Rules: rules}}, nil, nil)
	// the check runs before any rule or file, inside one route
	check, firstRule := strings.Index(out, "forward_auth"), strings.Index(out, "@nook_r0")
	if check < 0 || firstRule < 0 || check > firstRule {
		t.Errorf("the login check must come before the rules:\n%s", out)
	}
	if !strings.Contains(out, "\thandle_errors {") {
		t.Errorf("static sites keep their 404 handling:\n%s", out)
	}
}

func TestLoginTokenOpen(t *testing.T) {
	out := Render("x", []Site{{Host: "push.example.com", Port: 20004, TokenAuth: true, Open: []string{"/v1/health"}, Login: "push", Dashboard: "admin.example.com"}}, nil, nil)
	open, check := strings.Index(out, "handle @nook_open"), strings.Index(out, "forward_auth")
	if open < 0 || open > check {
		t.Errorf("open paths skip the check:\n%s", out)
	}
	if strings.Count(out, "header_up -Authorization") != 2 {
		t.Errorf("token sites drop the token on both paths:\n%s", out)
	}
}

// With a real Caddy on PATH (CI doesn't have one), the login blocks must adapt.
func TestLoginAdapts(t *testing.T) {
	bin, err := exec.LookPath("caddy")
	if err != nil {
		t.Skip("no caddy")
	}
	rules, _ := redirects.Parse("/old /new 301\n/app/* /app.html 200\n")
	out := Render("x", []Site{
		{Host: "notes.example.com", Port: 20003, Login: "notes", Dashboard: "admin.example.com"},
		{Host: "docs.example.com", Root: "/tmp", Login: "docs", Dashboard: "admin.example.com", Rules: rules, CORS: true},
		{Host: "plain.example.com", Root: "/tmp", Login: "docs", Dashboard: "admin.example.com"},
		{Host: "push.example.com", Port: 20004, TokenAuth: true, Open: []string{"/v1/health"}, Login: "push", Dashboard: "admin.example.com"},
	}, nil, nil)
	f := filepath.Join(t.TempDir(), "Caddyfile")
	os.WriteFile(f, []byte(out), 0o644)
	if b, err := exec.Command(bin, "adapt", "--config", f, "--adapter", "caddyfile").CombinedOutput(); err != nil {
		t.Fatalf("caddy adapt: %v\n%s\n%s", err, b, out)
	}
}

func TestStreamCloseDelay(t *testing.T) {
	sites := []Site{
		{Host: "a.x.com", Port: 20001},
		{Host: "b.x.com", Port: 20002, TokenAuth: true},
		{Host: "c.x.com", Port: 20003, TokenAuth: true, Open: []string{"/v1/health"}},
		{Host: "d.x.com", Port: 20004, Login: "d", Dashboard: "admin.x.com"},
		{Host: "e.x.com", Port: 20005, Login: "e", Dashboard: "admin.x.com", Open: []string{"/ws"}},
		{Host: "f.x.com", Root: "/w/f"},
	}
	without := Render("x", sites, nil, nil)
	if strings.Contains(without, "stream_close_delay") {
		t.Fatal("no delay unless set (Caddy before 2.7 rejects it)")
	}
	StreamCloseDelay = "24h"
	defer func() { StreamCloseDelay = "" }()
	with := Render("x", sites, nil, nil)
	// one per upstream proxy: a, b, c twice (open and checked), d, e twice
	if n := strings.Count(with, "stream_close_delay 24h"); n != 7 {
		t.Fatalf("%d stream_close_delay lines, want 7:\n%s", n, with)
	}
	if strings.Count(with, "{") != strings.Count(with, "}") {
		t.Fatal("unbalanced braces")
	}
	live, _ := Hosts(with)
	if len(live) != 6 {
		t.Fatalf("hosts read back: %v", live)
	}
}

func TestSameConfig(t *testing.T) {
	a := Render("x", []Site{{Host: "a.x.com", Port: 20001, Folder: "a", Version: "11"}}, nil, nil)
	b := Render("x", []Site{{Host: "a.x.com", Port: 20001, Folder: "a", Version: "12"}}, nil, nil)
	c := Render("x", []Site{{Host: "a.x.com", Port: 20002, Folder: "a", Version: "12"}}, nil, nil)
	if a == b || !SameConfig(a, b) {
		t.Error("a redeploy (new folder inode only) should need no reload")
	}
	if SameConfig(b, c) {
		t.Error("a new port needs a reload")
	}
}
