package core

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPairCode(t *testing.T) {
	old := pairFile
	pairFile = filepath.Join(t.TempDir(), "pair.json")
	t.Cleanup(func() { pairFile = old })
	if err := pairCheck("x"); err == nil {
		t.Fatal("no code made, but one was accepted")
	}
	p := pairState{Hash: pairHash("right"), Expires: time.Now().Add(time.Hour).Unix()}
	writePair(p)
	if pairCheck("right") != nil || pairCheck("wrong") == nil {
		t.Fatal("pairCheck")
	}
	p.Expires = time.Now().Add(-time.Minute).Unix()
	writePair(p)
	if pairCheck("right") == nil {
		t.Fatal("an expired code was accepted")
	}
	st, _ := os.Stat(pairFile)
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("pair.json is %v", st.Mode())
	}
	b, _ := os.ReadFile(pairFile)
	if strings.Contains(string(b), "right") {
		t.Fatal("the secret itself is stored")
	}
}

func TestMoveFrom(t *testing.T) {
	fakeRestic(t)
	secret := strings.Repeat("s", 43)
	var steps []string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+secret || r.Method != "POST" {
			w.WriteHeader(403)
			json.NewEncoder(w).Encode(map[string]string{"error": "that pairing code isn't this server's"})
			return
		}
		step := strings.TrimPrefix(r.URL.Path, "/api/v1/pair/")
		steps = append(steps, step)
		switch step {
		case "hello":
			json.NewEncoder(w).Encode(PairHello{ServerID: "old-1", Repo: "s3:https://r2.example/b/nook", KeyID: "K", Secret: "S", Password: "pw", Apps: 3})
		case "stop":
			json.NewEncoder(w).Encode(map[string]any{"serverId": "old-1"})
		}
	}))
	defer srv.Close()
	oldClient, oldBase, oldApps, oldRestore := pairClient, pairBase, moveApps, moveRestore
	pairClient = srv.Client()
	pairBase = func(string) string { return srv.URL }
	moveApps = func() []string { return nil }
	var restored ServerRestore
	moveRestore = func(h *Host, r ServerRestore) error { restored = r; return nil }
	t.Cleanup(func() { pairClient, pairBase, moveApps, moveRestore = oldClient, oldBase, oldApps, oldRestore })

	h := New(&strings.Builder{})
	if err := h.MoveFrom("not a code"); err == nil {
		t.Fatal("accepted a malformed code")
	}
	if err := h.MoveFrom("nookpair_admin.example.com/" + strings.Repeat("x", 43)); err == nil || !strings.Contains(err.Error(), "isn't this server's") {
		t.Fatalf("wrong secret: %v", err)
	}
	steps = nil
	if err := h.MoveFrom("nookpair_admin.example.com/" + secret); err != nil {
		t.Fatal(err)
	}
	if strings.Join(steps, ",") != "hello,stop" || restored.From != "old-1" || restored.NewID {
		t.Fatalf("steps %v, restore %+v", steps, restored)
	}
	env, _ := os.ReadFile(backupEnv)
	if !strings.Contains(string(env), "RESTIC_PASSWORD=pw") || !strings.Contains(string(env), "BACKUP_REPO=s3:https://r2.example/b/nook") {
		t.Fatalf("backup.env: %s", env)
	}
	moveApps = func() []string { return []string{"blog"} }
	if err := h.MoveFrom("nookpair_admin.example.com/" + secret); err == nil {
		t.Fatal("moved onto a server that has apps")
	}
}
