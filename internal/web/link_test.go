package web

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sparkstonepdx/nook/internal/rpc"
)

// fakeHelper answers helper requests on a unix socket with fn.
func fakeHelper(t *testing.T, fn func(req *rpc.Request) rpc.Response) string {
	t.Helper()
	dir, _ := os.MkdirTemp("", "h")
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "s")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				req, err := rpc.ReadRequest(c)
				if err != nil {
					return
				}
				json.NewEncoder(c).Encode(fn(req))
			}()
		}
	}()
	return sock
}

func raw(v any) json.RawMessage { b, _ := json.Marshal(v); return b }

// Two dashboards: alpha's request for beta's overview goes to beta, which runs it through its own helper.
func TestLinkForward(t *testing.T) {
	var betaSaw *rpc.Request
	betaStore, _ := OpenStore(t.TempDir())
	beta := &Server{Store: betaStore}
	betaHTTP := httptest.NewServer(beta)
	defer betaHTTP.Close()
	beta.Socket = fakeHelper(t, func(req *rpc.Request) rpc.Response {
		switch req.Op {
		case "linkServe":
			var inner rpc.Request
			json.Unmarshal([]byte(req.Set["body"]), &inner)
			if req.Params["from"] != "alpha" || req.Params["signature"] != "sig" || req.Params["path"] != "/api/v1/link/rpc" {
				return rpc.Response{OK: true, Data: raw(rpc.Response{Error: "bad signature", Status: 401})}
			}
			inner.By += "@alpha"
			betaSaw = &inner
			return rpc.Response{OK: true, Data: raw(rpc.Response{OK: true, Data: raw(map[string]any{"apps": []string{"kinlog"}})})}
		case "linkCheck":
			return rpc.Response{OK: true, Data: raw(map[string]string{"id": "alpha", "kind": "same"})}
		}
		return rpc.Response{Error: "unexpected " + req.Op, Status: 500}
	})

	alphaStore, _ := OpenStore(t.TempDir())
	alphaStore.SetPassword("adam", "correct horse battery", "")
	alpha := &Server{Store: alphaStore}
	var local []string
	alpha.Socket = fakeHelper(t, func(req *rpc.Request) rpc.Response {
		switch req.Op {
		case "links":
			return rpc.Response{OK: true, Data: raw(map[string]any{"self": map[string]string{"id": "alpha"}, "links": []map[string]string{{"id": "beta", "name": "beta", "kind": "same"}}})}
		case "linkSend": // the real helper signs; here it sends as is
			r, _ := http.NewRequest("POST", betaHTTP.URL+req.Params["path"], strings.NewReader(req.Set["body"]))
			r.Header.Set("Nook-Link", "alpha")
			r.Header.Set("Nook-Signature", "sig")
			resp, err := http.DefaultClient.Do(r)
			if err != nil {
				return rpc.Response{Error: err.Error(), Status: 502}
			}
			b, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != 200 {
				return rpc.Response{Error: string(b), Status: resp.StatusCode}
			}
			return rpc.Response{OK: true, Data: b}
		default:
			local = append(local, req.Op)
			return rpc.Response{OK: true, Data: raw(map[string]any{"apps": []string{"wordbag"}})}
		}
	})

	get := func(server string) string {
		r := httptest.NewRequest("GET", "/api/overview", nil)
		r.Header.Set("Cookie", session(alpha, "adam"))
		if server != "" {
			r.Header.Set("X-Nook-Server", server)
		}
		w := do(alpha, r)
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", server, w.Code, w.Body)
		}
		return w.Body.String()
	}
	if b := get(""); !strings.Contains(b, "wordbag") {
		t.Fatalf("here: %s", b)
	}
	if b := get("alpha"); !strings.Contains(b, "wordbag") {
		t.Fatalf("this server by its id: %s", b)
	}
	if b := get("beta"); !strings.Contains(b, "kinlog") {
		t.Fatalf("beta: %s", b)
	}
	if betaSaw == nil || betaSaw.Op != "overview" || betaSaw.By != "adam@alpha" {
		t.Fatalf("beta ran %+v", betaSaw)
	}
	if len(local) != 2 {
		t.Fatalf("alpha's own helper ran %v", local)
	}

	// logins cross both ways in one exchange
	betaStore.SetPassword("sam", "correct horse battery 2", "")
	alpha.SyncUsers()
	if !alphaStore.CheckPassword("sam", "correct horse battery 2") || !betaStore.CheckPassword("adam", "correct horse battery") {
		t.Fatal("logins didn't cross")
	}

	// a bad signature is refused before anything runs
	r := httptest.NewRequest("POST", "/api/v1/link/rpc", bytes.NewReader(raw(rpc.Request{Op: "overview"})))
	r.Header.Set("Nook-Link", "mallory")
	w := do(beta, r)
	var resp rpc.Response
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Error == "" {
		t.Fatalf("a bad signature ran: %s", w.Body)
	}
}
