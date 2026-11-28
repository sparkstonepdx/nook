package web

// Linked servers, the dashboard's side (core/link.go has the rest). Other servers reach this one at
// /api/v1/link/{hello,rpc,users,bye}; the helper checks every signature, since only it holds the keys. This
// dashboard acts on a linked server by sending its helper requests there: a request to /api/... with the
// X-Nook-Server header set to a linked server's id goes to that server instead of this one's helper.

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/sparkstonepdx/nook/internal/rpc"
)

// LinkInfo is the part of core.LinkInfo the dashboard uses here.
type linkInfo struct {
	Self *struct {
		ID string `json:"id"`
	} `json:"self"`
	Links []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Kind string `json:"kind"`
	} `json:"links"`
}

func (s *Server) links() linkInfo {
	var i linkInfo
	resp, err := rpc.Call(s.Socket, rpc.Request{Op: "links", By: "nook"}, 30*time.Second)
	if err == nil && resp.Error == "" {
		json.Unmarshal(resp.Data, &i)
	}
	return i
}

// linkSend has the helper sign body and send it to server's path; the answer's body comes back.
func (s *Server) linkSend(server, path string, body []byte, timeout time.Duration) (json.RawMessage, error) {
	resp, err := rpc.Call(s.Socket, rpc.Request{Op: "linkSend", By: "nook", Params: map[string]string{"server": server, "path": path}, Set: map[string]string{"body": string(body)}}, timeout)
	if err != nil {
		return nil, fail(502, err.Error())
	}
	if resp.Error != "" {
		return nil, fail(max(resp.Status, 400), resp.Error)
	}
	return resp.Data, nil
}

// forward sends a helper request to a linked server's helper, for the dashboard user me.
func (s *Server) forward(server, me string, req rpc.Request) (*rpc.Response, error) {
	req.By = me
	b, _ := json.Marshal(req)
	out, err := s.linkSend(server, "/api/v1/link/rpc", b, 10*time.Minute)
	if err != nil {
		return nil, err
	}
	var r rpc.Response
	if err := json.Unmarshal(out, &r); err != nil {
		return nil, fail(502, "the other server's answer didn't make sense")
	}
	return &r, nil
}

// linkAPI is what linked servers call here.
func (s *Server) linkAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	what := strings.TrimPrefix(r.URL.Path, "/api/v1/link/")
	if r.Method != "POST" || (what != "hello" && what != "rpc" && what != "users" && what != "bye") {
		apiError(w, 404, "not found")
		return
	}
	ip := clientIP(r)
	if err := s.throttled(ip); err != nil {
		he := err.(httpError)
		apiError(w, he.status, "%s", he.msg)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20+1))
	if err != nil || len(body) > 4<<20 {
		apiError(w, 413, "too big")
		return
	}
	call := func(op string, req rpc.Request) (*rpc.Response, bool) {
		req.Op, req.By = op, "link"
		resp, err := rpc.Call(s.Socket, req, 10*time.Minute)
		if err != nil {
			apiError(w, 502, "%v", err)
			return nil, false
		}
		if resp.Error != "" {
			if resp.Status == 401 {
				s.failed(ip)
			}
			apiError(w, max(resp.Status, 400), "%s", resp.Error)
			return nil, false
		}
		return resp, true
	}
	if what == "hello" {
		secret, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || !pairSecretRE.MatchString(secret) {
			s.failed(ip)
			apiError(w, 401, "send the link code's secret as Authorization: Bearer")
			return
		}
		if resp, ok := call("linkHello", rpc.Request{Secret: secret, Set: map[string]string{"body": string(body)}}); ok {
			w.Header().Set("Content-Type", "application/json")
			w.Write(resp.Data)
		}
		return
	}
	signed := rpc.Request{Params: map[string]string{
		"from": r.Header.Get("Nook-Link"), "time": r.Header.Get("Nook-Time"), "signature": r.Header.Get("Nook-Signature"),
		"method": r.Method, "path": r.URL.Path,
	}, Set: map[string]string{"body": string(body)}}
	switch what {
	case "rpc":
		if resp, ok := call("linkServe", signed); ok {
			w.Header().Set("Content-Type", "application/json")
			w.Write(resp.Data) // the inner request's own rpc.Response, errors included
		}
	case "users":
		resp, ok := call("linkCheck", signed)
		if !ok {
			return
		}
		var l struct {
			Kind string `json:"kind"`
		}
		json.Unmarshal(resp.Data, &l)
		if l.Kind != "same" {
			apiError(w, 403, "logins are only shared between your own servers")
			return
		}
		var in UsersExport
		if err := json.Unmarshal(body, &in); err != nil {
			apiError(w, 400, "bad request")
			return
		}
		if s.Store.Merge(in) {
			s.Store.changed() // and on to this server's other links
		}
		writeJSON(w, 200, s.Store.Export())
	case "bye":
		resp, ok := call("linkCheck", signed)
		if !ok {
			return
		}
		var l struct {
			ID string `json:"id"`
		}
		json.Unmarshal(resp.Data, &l)
		if _, ok := call("linkRemove", rpc.Request{Params: map[string]string{"id": l.ID, "tell": "false"}}); ok {
			writeJSON(w, 200, map[string]any{})
		}
	}
}

// SyncUsers swaps logins with every linked server of your own: each takes the other's newer changes.
func (s *Server) SyncUsers() {
	for _, l := range s.links().Links {
		if l.Kind != "same" {
			continue
		}
		b, _ := json.Marshal(s.Store.Export())
		out, err := s.linkSend(l.ID, "/api/v1/link/users", b, time.Minute)
		if err != nil {
			log.Printf("logins with %s: %v", l.Name, err)
			continue
		}
		var in UsersExport
		if json.Unmarshal(out, &in) == nil {
			s.Store.Merge(in)
		}
	}
}

// RunUserSync keeps logins in step: after every change here, and every 10 minutes for ones missed.
func (s *Server) RunUserSync() {
	kick := make(chan struct{}, 1)
	s.Store.OnChange = func() {
		select {
		case kick <- struct{}{}:
		default:
		}
	}
	t := time.NewTicker(10 * time.Minute)
	for {
		s.SyncUsers()
		select {
		case <-kick:
			time.Sleep(time.Second) // a member invite is two changes
		case <-t.C:
		}
	}
}

// call runs a helper request on server: this one ("" or its own id) or a linked one.
func (s *Server) call(server, me string, req rpc.Request) (*rpc.Response, error) {
	if self := s.links().Self; server == "" || (self != nil && server == self.ID) {
		req.By = me
		resp, err := rpc.Call(s.Socket, req, 10*time.Minute)
		if err != nil {
			return nil, fail(502, err.Error())
		}
		return resp, nil
	}
	return s.forward(server, me, req)
}

// moveApp moves app from one server to another, both this one or linked ones (core/appmove.go).
func (s *Server) moveApp(c *ctx) (any, error) {
	app, from, to := c.params["app"], c.r.Header.Get("X-Nook-Server"), c.str("to")
	self := ""
	if i := s.links(); i.Self != nil {
		self = i.Self.ID
	}
	if from == "" {
		from = self
	}
	if to == "" {
		to = self
	}
	if from == to {
		return nil, fail(400, "pick another server to move it to")
	}
	var log strings.Builder
	done := func(ok bool, extra map[string]any) map[string]any {
		r := map[string]any{"ok": ok, "code": map[bool]int{true: 0, false: 1}[ok], "cmd": "move " + app + " to " + to, "out": log.String()}
		for k, v := range extra {
			r[k] = v
		}
		return r
	}
	errText := func(resp *rpc.Response, err error) string {
		if err != nil {
			return err.Error()
		}
		return resp.Error
	}

	out, err := s.call(from, c.me, rpc.Request{Op: "appMoveOut", Params: map[string]string{"app": app}})
	if err != nil || out.Error != "" {
		log.WriteString("couldn't take it off " + from + ": " + errText(out, err) + "\n")
		return done(false, nil), nil
	}
	var got struct {
		Bundle json.RawMessage `json:"bundle"`
		Out    string          `json:"out"`
	}
	json.Unmarshal(out.Data, &got)
	log.WriteString(got.Out)

	in, err := s.call(to, c.me, rpc.Request{Op: "appMoveIn", Set: map[string]string{"bundle": string(got.Bundle)}})
	if err != nil || in.Error != "" {
		log.WriteString("couldn't set it up on " + to + ": " + errText(in, err) + "\n")
		back, err := s.call(from, c.me, rpc.Request{Op: "appMoveBack", Params: map[string]string{"app": app}})
		if err != nil || !back.OK {
			log.WriteString("and couldn't start it again on " + from + ": " + errText(back, err) + " (start it there: Sync)\n")
		} else {
			log.WriteString(back.Out)
		}
		return done(false, nil), nil
	}
	var res struct {
		Token   string `json:"token"`
		Out     string `json:"out"`
		Warning string `json:"warning"`
	}
	json.Unmarshal(in.Data, &res)
	log.WriteString(res.Out)
	if res.Warning != "" {
		log.WriteString(res.Warning + "\n")
	}

	del, err := s.call(from, c.me, rpc.Request{Op: "deleteApp", Params: map[string]string{"app": app, "confirm": app}})
	if err != nil || !del.OK {
		log.WriteString("it's moved, but removing the old copy failed: " + errText(del, err) + " (it's stopped there: delete it on that server)\n")
	} else {
		log.WriteString("removed the old copy\n")
	}
	log.WriteString("Now point its domains' DNS at the new server, and give its repo the new deploy token.\n")
	return done(true, map[string]any{"token": res.Token, "server": to}), nil
}
