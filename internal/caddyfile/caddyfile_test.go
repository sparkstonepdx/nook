package caddyfile

import (
	"strings"
	"testing"

	"github.com/sparkstonepdx/nook/internal/redirects"
)

func TestRoundTrip(t *testing.T) {
	text := Render("/srv/nook/a/sites", []Site{{Host: "api.x.com", Port: 20001, Folder: "api.x.com", Version: "11"}, {Host: "x.com", Root: "/var/lib/nook/www/a/x.com", Folder: "x.com", Version: "42"}}, [][2]string{{"www.x.com", "x.com"}})
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
	text := Render("app", []Site{{Host: "a.test", Root: "/w/a", CORS: true}, {Host: "b.test", Root: "/w/b"}, {Host: "c.test", Port: 9000, CORS: true}}, nil)
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
	text := Render("app", []Site{{Host: "a.test", Root: "/w/a", Rules: rules}, {Host: "b.test", Root: "/w/b"}}, nil)
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
