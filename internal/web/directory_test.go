package web

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

var png1 = append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 64)...)

func TestReadCard(t *testing.T) {
	iconDir = t.TempDir()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.Write([]byte(`<!doctype html><title>Old title</title>
<meta name="description" content="From the page">
<link rel="manifest" href="/app.webmanifest"><link rel="icon" href="/favicon.png" sizes="32x32">`))
		case "/app.webmanifest":
			w.Write([]byte(`{"name":"Kinlog  &amp; more","short_name":"Kinlog","description":"Track it together","theme_color":"#3d6b4f",
"background_color":"red;}body{display:none","icons":[
 {"src":"/mask.png","sizes":"512x512","purpose":"maskable"},
 {"src":"/i96.png","sizes":"96x96"},{"src":"/i192.png","sizes":"192x192","purpose":"any maskable"},
 {"src":"https://elsewhere.example/i.png","sizes":"192x192"}],
"screenshots":[{"src":"/s1.png","label":"Home","form_factor":"narrow"},{"src":"//evil.example/x.png"}]}`))
		case "/i192.png":
			w.Write(png1)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	siteAddr = strings.TrimPrefix(srv.URL, "https://")

	c := readCard("kinlog.example.com")
	if !c.Installable || c.Name != "Kinlog & more" || c.ShortName != "Kinlog" || c.Description != "Track it together" {
		t.Fatalf("card: %+v", c)
	}
	if c.Theme != "#3d6b4f" || c.Background != "" {
		t.Errorf("colors: theme %q, background %q (a color that breaks out of CSS must be dropped)", c.Theme, c.Background)
	}
	if !strings.HasSuffix(c.Icon, ".png") {
		t.Fatalf("icon: %q (the 192px any-purpose one, not maskable-only or another host's)", c.Icon)
	}
	if b, _ := os.ReadFile(iconDir + "/" + c.Icon); string(b) != string(png1) {
		t.Fatal("icon not saved")
	}
	if len(c.Screenshots) != 1 || c.Screenshots[0].Src != "https://kinlog.example.com/s1.png" || c.Screenshots[0].FormFactor != "narrow" {
		t.Fatalf("screenshots (only this host's): %+v", c.Screenshots)
	}
}

func TestReadCardUnreachable(t *testing.T) {
	siteAddr = "127.0.0.1:1"
	if c := readCard("down.example.com"); !c.unreachable || c.Name != "down" {
		t.Fatalf("%+v", c)
	}
}

func TestBestIcon(t *testing.T) {
	if got := best([]iconRef{{"a", 48}, {"b", 512}, {"c", 192}, {"d", 256}}); got != "c" {
		t.Errorf("closest to 192 from above: %s", got)
	}
	if got := best([]iconRef{{"a", 48}, {"b", 96}}); got != "b" {
		t.Errorf("all small: the biggest, %s", got)
	}
}

func TestServeIconRejectsNames(t *testing.T) {
	iconDir = t.TempDir()
	for _, name := range []string{"../etc/passwd", "x.html", "0123456789abcdef0123456789abcdef.exe"} {
		w := httptest.NewRecorder()
		serveIcon(w, httptest.NewRequest("GET", "/", nil), name)
		if w.Code != 404 {
			t.Errorf("%s: %d", name, w.Code)
		}
	}
}
