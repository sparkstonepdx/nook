package core

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// linkTest puts this server's link files, key and id in a temp folder, with a dashboard address.
func linkTest(t *testing.T) LinkSelf {
	t.Helper()
	dir := t.TempDir()
	oldDir, oldFile, oldID, oldSelf, oldBase, oldClient := linkDir, linksFile, serverIDFile, linkSelf, linkBase, linkClient
	linkDir, linksFile, serverIDFile = filepath.Join(dir, "link"), filepath.Join(dir, "links.json"), filepath.Join(dir, "server-id")
	os.WriteFile(serverIDFile, []byte("alpha-1a2b3c\n"), 0o644)
	linkSelf = func() (LinkSelf, error) {
		k, err := linkKey()
		if err != nil {
			return LinkSelf{}, err
		}
		return LinkSelf{ID: serverID(), Name: "admin.alpha.example", URL: "https://admin.alpha.example", Pub: base64.StdEncoding.EncodeToString(k.Public().(ed25519.PublicKey))}, nil
	}
	t.Cleanup(func() {
		linkDir, linksFile, serverIDFile, linkSelf, linkBase, linkClient = oldDir, oldFile, oldID, oldSelf, oldBase, oldClient
	})
	me, err := linkSelf()
	if err != nil {
		t.Fatal(err)
	}
	return me
}

func TestLinkCodeHello(t *testing.T) {
	me := linkTest(t)
	h := New(io.Discard)
	code, err := h.LinkCode(LinkSame, "")
	if err != nil {
		t.Fatal(err)
	}
	m := linkRE.FindStringSubmatch(code)
	if m == nil || m[1] != "admin.alpha.example" {
		t.Fatalf("code %q", code)
	}
	b, _ := os.ReadFile(codesPath())
	if strings.Contains(string(b), m[2]) {
		t.Fatal("the code's secret is stored")
	}
	other := LinkSelf{ID: "beta-4d5e6f", Name: "admin.beta.example", URL: "https://admin.beta.example", Pub: me.Pub}
	if _, err := h.LinkHello(m[2], LinkSelf{ID: me.ID, Name: "x.example", URL: "https://x.example", Pub: me.Pub}); err == nil {
		t.Fatal("linked with itself")
	}
	if _, err := h.LinkHello(m[2], LinkSelf{ID: "beta", Name: "admin.beta.example", URL: "https://evil.example", Pub: me.Pub}); err == nil {
		t.Fatal("a URL that isn't its address was taken")
	}
	got, err := h.LinkHello(m[2], other)
	if err != nil || got.ID != me.ID || got.Kind != LinkSame {
		t.Fatalf("%+v %v", got, err)
	}
	if l, ok := FindLink("beta-4d5e6f"); !ok || l.Kind != LinkSame || l.Name != "admin.beta.example" {
		t.Fatalf("link %+v", l)
	}
	if _, err := h.LinkHello(m[2], other); err == nil {
		t.Fatal("a code worked twice")
	}
	if st, _ := os.Stat(filepath.Join(linkDir, "key")); st.Mode().Perm() != 0o600 {
		t.Fatalf("key is %v", st.Mode())
	}
}

// A server linked with itself: what it signs, it checks.
func TestLinkSignCheck(t *testing.T) {
	me := linkTest(t)
	saveLink(Link{ID: me.ID, Name: "self.example", URL: "https://self.example", Pub: me.Pub, Kind: LinkSame})
	var last LinkSigned
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		last = LinkSigned{From: r.Header.Get("Nook-Link"), Time: r.Header.Get("Nook-Time"), Signature: r.Header.Get("Nook-Signature"), Method: r.Method, Path: r.URL.Path, Body: body}
		resp := LinkServe(last)
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()
	linkBase = func(string) string { return srv.URL }
	linkClient = srv.Client()

	req, _ := json.Marshal(map[string]any{"op": "pairStep", "params": map[string]string{"step": "hello"}})
	out, err := LinkSend(me.ID, "POST", "/api/v1/link/rpc", req)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "can't be run from another server") {
		t.Fatalf("pairStep from a link: %s", out)
	}
	// the same request again is refused
	if _, err := LinkCheck(last); err == nil || !strings.Contains(err.Error(), "already taken") {
		t.Fatalf("replay: %v", err)
	}
	// a changed body, path or time doesn't check out
	for _, bad := range []func(s *LinkSigned){
		func(s *LinkSigned) { s.Body = []byte(`{"op":"deleteApp"}`) },
		func(s *LinkSigned) { s.Path = "/api/v1/link/users" },
		func(s *LinkSigned) { s.Time = "1" },
		func(s *LinkSigned) { s.From = "someone-else" },
	} {
		s := last
		bad(&s)
		if _, err := LinkCheck(s); err == nil {
			t.Fatalf("a changed request checked out: %+v", s)
		}
	}
	l, _ := FindLink(me.ID)
	if time.Since(time.Unix(l.Seen, 0)) > time.Minute {
		t.Fatal("seen wasn't kept")
	}
}

func TestLinkAllowed(t *testing.T) {
	same := Link{Kind: LinkSame}
	for _, op := range []string{"overview", "storeInstall", "envApply", "appMoveIn"} {
		if err := LinkAllowed(same, op); err != nil {
			t.Errorf("%s: %v", op, err)
		}
	}
	for _, op := range []string{"linkRemove", "linkSend", "pairStep", "deploy", "serverSetup", "nope"} {
		if LinkAllowed(same, op) == nil {
			t.Errorf("%s was allowed", op)
		}
	}
}

func TestLinkRemoveTells(t *testing.T) {
	me := linkTest(t)
	saveLink(Link{ID: "beta-4d5e6f", Name: "admin.beta.example", URL: "https://admin.beta.example", Pub: me.Pub, Kind: LinkSame})
	told := false
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		told = r.URL.Path == "/api/v1/link/bye" && r.Header.Get("Nook-Signature") != ""
		w.Write([]byte("{}"))
	}))
	defer srv.Close()
	linkBase = func(string) string { return srv.URL }
	linkClient = srv.Client()
	if err := New(io.Discard).LinkRemove("beta-4d5e6f", true); err != nil {
		t.Fatal(err)
	}
	if !told {
		t.Fatal("the other server wasn't told")
	}
	if _, ok := FindLink("beta-4d5e6f"); ok {
		t.Fatal("still linked")
	}
}

func TestAppFileAllowed(t *testing.T) {
	for _, p := range []string{"/etc/systemd/system/nook-web.slice", "/etc/nook/env/web.env", "/etc/nook/env/web/site.env", "/etc/nook/state/web.login"} {
		if !appFileAllowed("web", p) {
			t.Errorf("%s refused", p)
		}
	}
	for _, p := range []string{"/etc/systemd/system/nook-other.slice", "/etc/nook/env/web/../other.env", "/etc/nook/env/web/a/b", "/etc/passwd", "/etc/nook/env/web"} {
		if appFileAllowed("web", p) {
			t.Errorf("%s allowed", p)
		}
	}
}

func TestManagedPolicy(t *testing.T) {
	op := Link{Name: "agency.example", Kind: LinkManager, Role: RoleOperator}
	admin := Link{Name: "agency.example", Kind: LinkManager, Role: RoleAdmin}
	client := Link{Name: "client.example", Kind: LinkClient, Role: RoleAdmin}
	for _, o := range []string{"storeInstall", "envApply", "systemUpdate", "siteLogs", "dnsSet", "tokenCreate", "backupRun"} {
		if err := LinkAllowed(op, o); err != nil {
			t.Errorf("operator %s: %v", o, err)
		}
	}
	for _, o := range []string{"deleteApp", "backupSetup", "backupRestore", "systemRestart", "setPorts", "catalogAdd", "setDashboardHost", "appMoveOut"} {
		if LinkAllowed(op, o) == nil {
			t.Errorf("operator was allowed %s", o)
		}
	}
	for _, o := range []string{"deleteApp", "systemRestart", "setPorts"} {
		if err := LinkAllowed(admin, o); err != nil {
			t.Errorf("admin %s: %v", o, err)
		}
	}
	for _, o := range []string{"backupSetup", "appMoveOut", "linkRemove", "deploy"} {
		if LinkAllowed(admin, o) == nil {
			t.Errorf("admin was allowed %s", o)
		}
	}
	if LinkAllowed(client, "overview") == nil {
		t.Error("a managed server ran something on its manager")
	}
}

func TestManagedLinking(t *testing.T) {
	me := linkTest(t)
	old := auditFile
	af := filepath.Join(t.TempDir(), "audit.jsonl")
	auditFile = func() string { return af }
	t.Cleanup(func() { auditFile = old })
	h := New(io.Discard)
	if _, err := h.LinkCode(LinkManaged, "root"); err == nil {
		t.Fatal("a made-up role was taken")
	}
	code, err := h.LinkCode(LinkManaged, RoleOperator)
	if err != nil {
		t.Fatal(err)
	}
	m := linkRE.FindStringSubmatch(code)
	got, err := h.LinkHello(m[2], LinkSelf{ID: "agency-1", Name: "admin.agency.example", URL: "https://admin.agency.example", Pub: me.Pub})
	if err != nil || got.Kind != LinkManaged || got.Role != RoleOperator {
		t.Fatalf("%+v %v", got, err)
	}
	l, _ := FindLink("agency-1")
	if l.Kind != LinkManager || l.Role != RoleOperator {
		t.Fatalf("the managing server's link here: %+v", l)
	}
	if a := LinkAudit(10, true); len(a) != 1 || a[0].Op != "linked" {
		t.Fatalf("audit %+v", a)
	}

	// requests from it are audited, refused ones too
	saveLink(Link{ID: me.ID, Name: "admin.agency.example", Pub: me.Pub, Kind: LinkManager, Role: RoleOperator})
	sign := func(req string) LinkSigned {
		k, _ := linkKey()
		ts := strconvItoa(time.Now().Unix())
		body := []byte(req)
		return LinkSigned{From: me.ID, Time: ts, Method: "POST", Path: "/api/v1/link/rpc", Body: body,
			Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(k, linkMessage(me.ID, me.ID, "POST", "/api/v1/link/rpc", ts, body)))}
	}
	r := LinkServe(sign(`{"op":"deleteApp","by":"sam","params":{"app":"shop","confirm":"shop"}}`))
	if r.Status != 403 {
		t.Fatalf("an operator deleted an app: %+v", r)
	}
	a := LinkAudit(10, true)
	if a[0].Op != "deleteApp" || a[0].Allowed || a[0].By != "sam" || strings.Contains(a[0].Detail, "confirm") {
		t.Fatalf("audit %+v", a[0])
	}
}

func strconvItoa(n int64) string { return strconv.FormatInt(n, 10) }

func TestJoinManaged(t *testing.T) {
	me := linkTest(t)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(LinkSelf{ID: "shop-9f", Name: "admin.shop.example", URL: "https://admin.shop.example", Pub: me.Pub, Kind: LinkManaged, Role: RoleAdmin})
	}))
	defer srv.Close()
	linkBase = func(string) string { return srv.URL }
	linkClient = srv.Client()
	l, err := New(io.Discard).LinkJoin("nooklink_admin.shop.example/"+strings.Repeat("a", 43), "Second Chance Records")
	if err != nil || l.Kind != LinkClient || l.Role != RoleAdmin || l.Entity != "Second Chance Records" {
		t.Fatalf("%+v %v", l, err)
	}
}
