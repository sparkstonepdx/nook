package rpc

import (
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func serveOnce(t *testing.T, sock string, got chan<- *Request, gotFile chan<- string) {
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		defer ln.Close()
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		req, f, err := ReadRequestFile(c.(*net.UnixConn))
		if err != nil {
			t.Errorf("read: %v", err)
			got <- nil
			gotFile <- ""
			return
		}
		body := ""
		if f != nil {
			b, _ := io.ReadAll(f)
			body = string(b)
			f.Close()
		}
		got <- req
		gotFile <- body
		c.Write([]byte(`{"ok":true}`))
	}()
}

func TestCallAndCallWithFile(t *testing.T) {
	dir := t.TempDir()
	got, gotFile := make(chan *Request, 1), make(chan string, 1)

	sock := filepath.Join(dir, "a.sock")
	serveOnce(t, sock, got, gotFile)
	if _, err := Call(sock, Request{Op: "overview", Secret: "s"}, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	if r := <-got; r == nil || r.Op != "overview" || r.Secret != "s" {
		t.Fatalf("plain call: %+v", r)
	}
	if f := <-gotFile; f != "" {
		t.Fatalf("unexpected file %q", f)
	}

	sock2 := filepath.Join(dir, "b.sock")
	serveOnce(t, sock2, got, gotFile)
	f, _ := os.CreateTemp(dir, "up")
	f.WriteString("tarball bytes")
	f.Seek(0, 0)
	if _, err := CallWithFile(sock2, Request{Op: "deploy", Params: map[string]string{"site": "x"}}, f, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	if r := <-got; r == nil || r.Op != "deploy" || r.Params["site"] != "x" {
		t.Fatalf("file call: %+v", r)
	}
	if b := <-gotFile; b != "tarball bytes" {
		t.Fatalf("file body %q", b)
	}
}
