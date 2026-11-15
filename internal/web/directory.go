package web

// Cards for the directory and the dashboard's launcher, read from each site's own web manifest: name, short name,
// description, theme and background colors, an icon, screenshots. nook-web fetches them from Caddy on this machine
// (127.0.0.1:443, by name), keeps each for an hour, and keeps icons (the dashboard only shows images from its own
// origin) in /var/lib/nook-web/directory-icons.

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sparkstonepdx/nook/internal/config"
	"github.com/sparkstonepdx/nook/internal/rpc"
)

type shot struct {
	Src        string `json:"src"` // absolute, on the site's own host
	Label      string `json:"label,omitempty"`
	FormFactor string `json:"formFactor,omitempty"` // wide or narrow
}

type card struct {
	Host        string `json:"host"`
	URL         string `json:"url"`
	App         string `json:"app"`
	Listed      bool   `json:"listed"`
	Preview     bool   `json:"preview"`
	Name        string `json:"name"`
	ShortName   string `json:"shortName,omitempty"`
	Description string `json:"description,omitempty"`
	Theme       string `json:"theme,omitempty"`
	Background  string `json:"background,omitempty"`
	Icon        string `json:"icon,omitempty"` // a file name under directory-icons: <sha256>.<ext>
	Screenshots []shot `json:"screenshots,omitempty"`
	Image       string `json:"image,omitempty"` // og:image or twitter:image, absolute, on the site's own host
	Installable bool   `json:"installable"`     // it has a web manifest
	unreachable bool   // the page couldn't be read: kept for a minute, not an hour
}

var iconDir = filepath.Join(config.WebStateDir, "directory-icons")

type cardCache struct {
	mu   sync.Mutex
	at   map[string]time.Time
	card map[string]card
}

var cards = cardCache{at: map[string]time.Time{}, card: map[string]card{}}

// siteClient talks to this machine's Caddy for any of its hosts. Certificates aren't checked: the connection never
// leaves the machine, and a site still waiting for its certificate should still get a card.
var siteClient = &http.Client{
	Timeout: 8 * time.Second,
	Transport: &http.Transport{
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, network, siteAddr)
		},
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	},
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 3 || req.URL.Host != via[0].URL.Host {
			return http.ErrUseLastResponse
		}
		return nil
	},
}

var siteAddr = "127.0.0.1:443"

// cardKey gets nook-web's reads past the login check of sites behind nook's login (see login.go).
var cardKey atomic.Value

func get(u string, max int64) ([]byte, string, error) {
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return nil, "", err
	}
	if k, _ := cardKey.Load().(string); k != "" {
		req.Header.Set("X-Nook-Card", k)
	}
	resp, err := siteClient.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, "", statusErr(resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, max))
	return b, resp.Request.URL.String(), err
}

// statusErr is a site that answered, just not with its page (a 401 from a token-only API, a 404 at /).
type statusErr string

func (e statusErr) Error() string { return string(e) }

var (
	metaRE   = regexp.MustCompile(`(?is)<meta\b[^>]*>`)
	linkRE   = regexp.MustCompile(`(?is)<link\b[^>]*>`)
	attrRE   = regexp.MustCompile(`(?is)\b(rel|href|sizes|property|name|content)\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s>]+))`)
	titleRE  = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	descRE   = regexp.MustCompile(`(?is)<meta\b[^>]*name\s*=\s*["']?description["']?[^>]*content\s*=\s*"([^"]*)"`)
	themeRE  = regexp.MustCompile(`(?is)<meta\b[^>]*name\s*=\s*["']?theme-color["']?[^>]*content\s*=\s*"([^"]*)"`)
	colorRE  = regexp.MustCompile(`^(#[0-9a-fA-F]{3,8}|[a-zA-Z]{3,20}|(rgb|rgba|hsl|hsla|oklch|oklab|lab|lch)\([0-9a-zA-Z.,%/ +-]{1,60}\))$`)
	sizesRE  = regexp.MustCompile(`(\d+)x(\d+)`)
	spacesRE = regexp.MustCompile(`\s+`)
)

func attrs(tag string) map[string]string {
	m := map[string]string{}
	for _, a := range attrRE.FindAllStringSubmatch(tag, -1) {
		m[strings.ToLower(a[1])] = html.UnescapeString(a[2] + a[3] + a[4])
	}
	return m
}

func clean(s string, max int) string {
	s = strings.TrimSpace(spacesRE.ReplaceAllString(html.UnescapeString(s), " "))
	if r := []rune(s); len(r) > max {
		s = string(r[:max-1]) + "…"
	}
	return s
}

func color(s string) string {
	s = strings.TrimSpace(s)
	if colorRE.MatchString(s) {
		return s
	}
	return ""
}

// sameHost resolves ref against base and keeps it only when it stays on host over https.
func sameHost(base *url.URL, ref, host string) string {
	if strings.TrimSpace(ref) == "" { // resolves to the page itself, which isn't an image or a manifest
		return ""
	}
	u, err := base.Parse(strings.TrimSpace(ref))
	if err != nil || u.Host != host || (u.Scheme != "https" && u.Scheme != "http") {
		return ""
	}
	u.Scheme = "https"
	return u.String()
}

type iconRef struct {
	src  string
	size int // the largest side; 0 unknown, 1024 for "any" (svg)
}

// best is the icon closest to 192px from below, or the smallest above it.
func best(icons []iconRef) string {
	sort.SliceStable(icons, func(i, j int) bool {
		di, dj := icons[i].size-192, icons[j].size-192
		if (di >= 0) != (dj >= 0) {
			return di >= 0
		}
		if di >= 0 {
			return di < dj
		}
		return di > dj
	})
	if len(icons) == 0 {
		return ""
	}
	return icons[0].src
}

func iconSize(sizes string) int {
	if strings.Contains(sizes, "any") {
		return 1024
	}
	n := 0
	for _, m := range sizesRE.FindAllStringSubmatch(sizes, -1) {
		if v, _ := strconv.Atoi(m[1]); v > n {
			n = v
		}
	}
	return n
}

// readCard fetches host's page and manifest.
func readCard(host string) card {
	c := card{Host: host, URL: "https://" + host + "/", Name: strings.Split(host, ".")[0]}
	page, final, err := get(c.URL, 512<<10)
	if err != nil {
		var se statusErr
		if !errors.As(err, &se) {
			c.unreachable = true // nothing answered: try again in a minute
			return c
		}
		// it answered, just not with a page (an API, a token-only site): its favicon may still be there
		c.Icon = firstIcon(fallbackIcons(host))
		return c
	}
	base, _ := url.Parse(final)
	text := string(page)
	if m := titleRE.FindStringSubmatch(text); m != nil {
		c.Name = clean(m[1], 60)
	}
	if m := descRE.FindStringSubmatch(text); m != nil {
		c.Description = clean(m[1], 300)
	}
	if m := themeRE.FindStringSubmatch(text); m != nil {
		c.Theme = color(m[1])
	}
	// Open Graph and Twitter cards: the share image, and a name or description the page doesn't give otherwise
	og := map[string]string{}
	for _, tag := range metaRE.FindAllString(text, -1) {
		a := attrs(tag)
		k := strings.ToLower(cmpOr(a["property"], a["name"]))
		if strings.HasPrefix(k, "og:") || strings.HasPrefix(k, "twitter:") {
			if _, seen := og[k]; !seen {
				og[k] = a["content"]
			}
		}
	}
	for _, k := range []string{"og:image:secure_url", "og:image", "og:image:url", "twitter:image", "twitter:image:src"} {
		if u := sameHost(base, og[k], host); u != "" {
			c.Image = u
			break
		}
	}
	if c.Description == "" {
		c.Description = clean(cmpOr(og["og:description"], og["twitter:description"]), 300)
	}
	if titleRE.FindStringSubmatch(text) == nil {
		if n := clean(cmpOr(og["og:site_name"], og["og:title"], og["twitter:title"]), 60); n != "" {
			c.Name = n
		}
	}
	var icons []iconRef
	manifest := ""
	for _, tag := range linkRE.FindAllString(text, -1) {
		a := attrs(tag)
		rels := strings.Fields(strings.ToLower(a["rel"]))
		for _, rel := range rels {
			switch rel {
			case "manifest":
				manifest = sameHost(base, a["href"], host)
			case "apple-touch-icon":
				if u := sameHost(base, a["href"], host); u != "" {
					icons = append(icons, iconRef{u, max(iconSize(a["sizes"]), 180)})
				}
			case "icon":
				if u := sameHost(base, a["href"], host); u != "" {
					icons = append(icons, iconRef{u, iconSize(a["sizes"])})
				}
			}
		}
	}
	if manifest != "" {
		if b, mfinal, err := get(manifest, 256<<10); err == nil {
			var m struct {
				Name        string `json:"name"`
				ShortName   string `json:"short_name"`
				Description string `json:"description"`
				Theme       string `json:"theme_color"`
				Background  string `json:"background_color"`
				Icons       []struct {
					Src, Sizes, Purpose string
				} `json:"icons"`
				Screenshots []struct {
					Src        string `json:"src"`
					Label      string `json:"label"`
					FormFactor string `json:"form_factor"`
				} `json:"screenshots"`
			}
			if json.Unmarshal(b, &m) == nil {
				c.Installable = true
				mbase, _ := url.Parse(mfinal)
				if n := clean(m.Name, 60); n != "" {
					c.Name = n
				}
				c.ShortName = clean(m.ShortName, 30)
				if d := clean(m.Description, 300); d != "" {
					c.Description = d
				}
				if t := color(m.Theme); t != "" {
					c.Theme = t
				}
				c.Background = color(m.Background)
				var mi []iconRef
				for _, i := range m.Icons {
					p := strings.Fields(strings.ToLower(i.Purpose))
					if len(p) > 0 && !slices.Contains(p, "any") { // a maskable-only icon is cropped for launchers, not for cards
						continue
					}
					if u := sameHost(mbase, i.Src, host); u != "" {
						mi = append(mi, iconRef{u, iconSize(i.Sizes)})
					}
				}
				if len(mi) > 0 {
					icons = mi
				}
				for _, s := range m.Screenshots {
					if u := sameHost(mbase, s.Src, host); u != "" && len(c.Screenshots) < 6 {
						ff := s.FormFactor
						if ff != "wide" && ff != "narrow" {
							ff = ""
						}
						c.Screenshots = append(c.Screenshots, shot{Src: u, Label: clean(s.Label, 120), FormFactor: ff})
					}
				}
			}
		}
	}
	// the page's own icons best first, then the usual paths: a single-page app answers every path with its
	// page, so each candidate is only kept if it's really an image
	c.Icon = firstIcon(append(ranked(icons), fallbackIcons(host)...))
	return c
}

// fallbackIcons are where sites keep icons without saying so, best first.
func fallbackIcons(host string) []string {
	return []string{"https://" + host + "/apple-touch-icon.png", "https://" + host + "/favicon.svg", "https://" + host + "/favicon.ico"}
}

// ranked orders icons as best would pick them.
func ranked(icons []iconRef) []string {
	var out []string
	for len(icons) > 0 {
		b := best(slices.Clone(icons))
		out = append(out, b)
		icons = slices.DeleteFunc(icons, func(i iconRef) bool { return i.src == b })
	}
	return out
}

// firstIcon saves the first of srcs that is an image and returns its file name.
func firstIcon(srcs []string) string {
	seen := map[string]bool{}
	for _, u := range srcs {
		if seen[u] {
			continue
		}
		seen[u] = true
		if name := saveIcon(u); name != "" {
			return name
		}
	}
	return ""
}

func cmpOr(vs ...string) string {
	for _, v := range vs {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}

// saveIcon downloads an icon (up to 512 KB of PNG, JPEG, WebP, GIF, ICO or SVG) and returns its file name.
func saveIcon(u string) string {
	if u == "" {
		return ""
	}
	b, _, err := get(u, 512<<10)
	if err != nil || len(b) == 0 {
		return ""
	}
	ext := ""
	switch t := http.DetectContentType(b); {
	case t == "image/png":
		ext = "png"
	case t == "image/jpeg":
		ext = "jpg"
	case t == "image/webp":
		ext = "webp"
	case t == "image/gif":
		ext = "gif"
	case t == "image/x-icon" || t == "image/vnd.microsoft.icon":
		ext = "ico"
	case !strings.HasPrefix(t, "text/html") && strings.Contains(strings.ToLower(string(b[:min(len(b), 512)])), "<svg"):
		ext = "svg"
	default:
		return ""
	}
	sum := sha256.Sum256(b)
	name := hex.EncodeToString(sum[:16]) + "." + ext
	os.MkdirAll(iconDir, 0o755)
	p := filepath.Join(iconDir, name)
	if _, err := os.Stat(p); err != nil {
		tmp := p + ".tmp"
		if os.WriteFile(tmp, b, 0o644) == nil {
			os.Rename(tmp, p)
		}
	}
	return name
}

// directory is every live site with its card, from the helper's list and the cache (read again after an hour, or
// when fresh is set). Several sites are read at once.
func (s *Server) directory(fresh bool) ([]card, error) {
	cardKey.Store(s.Store.cardKey())
	resp, err := rpc.Call(s.Socket, rpc.Request{Op: "directory", By: "web"}, 10*time.Second)
	if err != nil {
		return nil, err
	}
	if resp.Error != "" {
		return nil, errors.New(resp.Error)
	}
	var entries []struct {
		Host      string `json:"host"`
		App       string `json:"app"`
		Listed    bool   `json:"listed"`
		Preview   bool   `json:"preview"`
		StoreIcon string `json:"storeIcon"`
	}
	json.Unmarshal(resp.Data, &entries)
	out := make([]card, len(entries))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	for i, e := range entries {
		cards.mu.Lock()
		c, ok := cards.card[e.Host]
		keep := time.Hour
		if c.unreachable {
			keep = time.Minute
		}
		stale := !ok || fresh || time.Since(cards.at[e.Host]) > keep
		cards.mu.Unlock()
		if stale {
			wg.Add(1)
			go func(i int, host string) {
				defer wg.Done()
				sem <- struct{}{}
				c := readCard(host)
				<-sem
				cards.mu.Lock()
				cards.card[host], cards.at[host] = c, time.Now()
				cards.mu.Unlock()
				out[i] = c
			}(i, e.Host)
		} else {
			out[i] = c
		}
	}
	wg.Wait()
	for i, e := range entries {
		out[i].App, out[i].Listed, out[i].Preview = e.App, e.Listed, e.Preview
		if out[i].Icon == "" && e.StoreIcon != "" {
			out[i].Icon = s.storeIcon(e.StoreIcon)
		}
	}
	return out, nil
}

// storeIcon is a store app's catalog icon (catalog/sha256) saved with the other icons, for a site that serves
// none of its own. The helper fetches it and checks it against the hash the signed catalog pins.
func (s *Server) storeIcon(ref string) string {
	catalog, sum, ok := strings.Cut(ref, "/")
	if !ok || len(sum) != 64 {
		return ""
	}
	for _, ext := range []string{"png", "jpg"} {
		if _, err := os.Stat(filepath.Join(iconDir, sum[:32]+"."+ext)); err == nil {
			return sum[:32] + "." + ext
		}
	}
	resp, err := rpc.Call(s.Socket, rpc.Request{Op: "storeImage", By: "web", Params: map[string]string{"catalog": catalog, "sha": sum}}, 30*time.Second)
	if err != nil || resp.Error != "" {
		return ""
	}
	var img struct {
		Type string `json:"type"`
		Data []byte `json:"data"`
	}
	if json.Unmarshal(resp.Data, &img) != nil {
		return ""
	}
	ext := map[string]string{"image/png": "png", "image/jpeg": "jpg"}[img.Type]
	if ext == "" {
		return ""
	}
	name := sum[:32] + "." + ext
	os.MkdirAll(iconDir, 0o755)
	if os.WriteFile(filepath.Join(iconDir, name+".tmp"), img.Data, 0o644) == nil {
		os.Rename(filepath.Join(iconDir, name+".tmp"), filepath.Join(iconDir, name))
	}
	return name
}

// publicDirectory serves GET /api/v1/directory (the listed sites, for a Directory app or anyone) and
// GET /api/v1/directory/icon/NAME (a listed site's icon).
func (s *Server) publicDirectory(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if r.Method != "GET" && r.Method != "HEAD" {
		writeJSON(w, 405, map[string]string{"error": "GET only"})
		return
	}
	all, err := s.directory(false)
	if err != nil {
		writeJSON(w, 502, map[string]string{"error": err.Error()})
		return
	}
	listed := []card{}
	for _, c := range all {
		if c.Listed {
			c.App = "" // which app serves it is the server's business
			listed = append(listed, c)
		}
	}
	if name, ok := strings.CutPrefix(r.URL.Path, "/api/v1/directory/icon/"); ok {
		for _, c := range listed {
			if c.Icon != "" && c.Icon == name {
				serveIcon(w, r, name)
				return
			}
		}
		writeJSON(w, 404, map[string]string{"error": "no such icon"})
		return
	}
	if r.URL.Path != "/api/v1/directory" {
		writeJSON(w, 404, map[string]string{"error": "not found"})
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=60")
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"sites": listed})
}

var iconNameRE = regexp.MustCompile(`^[0-9a-f]{32}\.(png|jpg|webp|gif|ico|svg)$`)

func serveIcon(w http.ResponseWriter, r *http.Request, name string) {
	if !iconNameRE.MatchString(name) {
		http.NotFound(w, r)
		return
	}
	// an SVG opened on its own can't run script on this origin
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
	w.Header().Set("Cache-Control", "public, max-age=86400, immutable")
	if strings.HasSuffix(name, ".svg") {
		w.Header().Set("Content-Type", "image/svg+xml")
	}
	http.ServeFile(w, r, filepath.Join(iconDir, name))
}
