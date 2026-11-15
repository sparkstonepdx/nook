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

var svg1 = []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16"><circle cx="8" cy="8" r="8"/></svg>`)

// A token-only API answers / with a 401: its favicon still makes the tile.
func TestReadCardNoPageStillGetsFavicon(t *testing.T) {
	iconDir = t.TempDir()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/favicon.ico" {
			w.Write(png1) // what a Go server or Caddy often has
			return
		}
		http.Error(w, "send a token", 401)
	}))
	defer srv.Close()
	siteAddr = strings.TrimPrefix(srv.URL, "https://")
	c := readCard("push.example.com")
	if c.unreachable || !strings.HasSuffix(c.Icon, ".png") || c.Name != "push" {
		t.Fatalf("%+v", c)
	}
}

// A single-page app answers every path with its page: an HTML "favicon.ico" (even one with an inline svg) is
// skipped for the real favicon.svg; and its share card fills in the image and description.
func TestReadCardSPAAndOpenGraph(t *testing.T) {
	iconDir = t.TempDir()
	page := `<!doctype html><html><head><title>Wren</title>
<meta property="og:image" content="/og.png"><meta property="og:description" content="Say it, done.">
<meta name="twitter:image" content="https://cdn.elsewhere.example/x.png"></head><body><svg><path/></svg></body></html>`
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/favicon.svg":
			w.Header().Set("Content-Type", "image/svg+xml")
			w.Write(svg1)
		default:
			w.Write([]byte(page))
		}
	}))
	defer srv.Close()
	siteAddr = strings.TrimPrefix(srv.URL, "https://")
	c := readCard("wren.example.com")
	if !strings.HasSuffix(c.Icon, ".svg") {
		t.Fatalf("icon: %q", c.Icon)
	}
	if b, _ := os.ReadFile(iconDir + "/" + c.Icon); string(b) != string(svg1) {
		t.Fatalf("saved the page instead of the icon: %q", b)
	}
	if c.Image != "https://wren.example.com/og.png" || c.Description != "Say it, done." || c.Name != "Wren" {
		t.Fatalf("og: %+v", c)
	}
}

// og:title fills in a page with no <title>; another host's share image is left out.
func TestReadCardOpenGraphName(t *testing.T) {
	iconDir = t.TempDir()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<meta property="og:site_name" content="Rummykube"><meta property="og:image" content="https://cdn.example/x.png">`))
	}))
	defer srv.Close()
	siteAddr = strings.TrimPrefix(srv.URL, "https://")
	if c := readCard("rk.example.com"); c.Name != "Rummykube" || c.Image != "" {
		t.Fatalf("%+v", c)
	}
}

// A site with an app icon in its manifest and a favicon keeps both: the tiles show the favicon.
func TestReadCardFavicon(t *testing.T) {
	iconDir = t.TempDir()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.Write([]byte(`<title>Kinlog</title><link rel="manifest" href="/m.json"><link rel="icon" href="/favicon.svg" type="image/svg+xml">`))
		case "/m.json":
			w.Write([]byte(`{"name":"Kinlog","icons":[{"src":"/i192.png","sizes":"192x192"}]}`))
		case "/i192.png":
			w.Write(png1)
		case "/favicon.svg":
			w.Write(svg1)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	siteAddr = strings.TrimPrefix(srv.URL, "https://")
	c := readCard("kinlog.example.com")
	if !strings.HasSuffix(c.Icon, ".png") || !strings.HasSuffix(c.Favicon, ".svg") {
		t.Fatalf("icon %q, favicon %q", c.Icon, c.Favicon)
	}
}
