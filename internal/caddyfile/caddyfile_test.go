package caddyfile

import (
	"strings"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	text := Render("/srv/apps/a/sites", []Site{{Host: "api.x.com", Port: 20001, Folder: "api.x.com", Version: "11"}, {Host: "x.com", Root: "/var/lib/app-host/www/a/x.com", Folder: "x.com", Version: "42"}}, [][2]string{{"www.x.com", "x.com"}})
	if Version(text, "x.com") != "42" || Version(text, "api.x.com") != "11" || Version(text, "nope") != "" {
		t.Fatalf("Version:\n%s", text)
	}
	live, red := Hosts(text)
	if strings.Join(live, ",") != "api.x.com,x.com" || strings.Join(red, ",") != "www.x.com" {
		t.Fatalf("live=%v red=%v\n%s", live, red, text)
	}
	if strings.Contains(text, "handle_errors {\n\t\trewrite") == false {
		t.Fatal("static block")
	}
	out, found := WithoutRedirect(text, "www.x.com")
	if !found || strings.Contains(out, "www.x.com") || !strings.Contains(out, "x.com {") {
		t.Fatalf("WithoutRedirect:\n%s", out)
	}
}
