package caddyfile

import (
	"strings"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	text := Render("/srv/apps/a/sites", []Site{{Host: "api.x.com", Port: 20001}, {Host: "x.com", Root: "/var/lib/app-host/www/a/x.com"}}, [][2]string{{"www.x.com", "x.com"}})
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
