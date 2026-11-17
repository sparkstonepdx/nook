package web

import (
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/sparkstonepdx/nook/internal/rpc"
)

var pairSecretRE = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

// pairAPI is the old server's side of a move (core/pair.go): POST /api/v1/pair/{hello,stop} with the pairing code's
// secret as the bearer token. Throttled like the deploy API; the helper checks the secret against its hash.
func (s *Server) pairAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	step := strings.TrimPrefix(r.URL.Path, "/api/v1/pair/")
	if r.Method != "POST" || (step != "hello" && step != "stop") {
		apiError(w, 404, "not found")
		return
	}
	ip := clientIP(r)
	if err := s.throttled(ip); err != nil {
		he := err.(httpError)
		apiError(w, he.status, "%s", he.msg)
		return
	}
	secret, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || !pairSecretRE.MatchString(secret) {
		s.failed(ip)
		apiError(w, 401, "send the pairing code's secret as Authorization: Bearer")
		return
	}
	resp, err := rpc.Call(s.Socket, rpc.Request{Op: "pairStep", By: "pair", Params: map[string]string{"step": step}, Secret: secret}, 2*time.Hour)
	if err != nil {
		apiError(w, 502, "%v", err)
		return
	}
	if resp.Error != "" {
		s.failed(ip)
		apiError(w, resp.Status, "%s", resp.Error)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(resp.Data)
}
