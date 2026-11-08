// Package rpc is the fixed set of operations the unprivileged dashboard may ask the root helper for,
// over a unix socket. One JSON request per connection.
package rpc

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"time"
)

type Request struct {
	Op     string            `json:"op"`
	By     string            `json:"by"` // dashboard user, for the log
	Params map[string]string `json:"params,omitempty"`
	Set    map[string]string `json:"set,omitempty"`
	Unset  []string          `json:"unset,omitempty"`
}

// Response: commands fill OK/Code/Cmd/Out; queries fill Data. Error is a message for the user.
type Response struct {
	OK     bool            `json:"ok"`
	Code   int             `json:"code"`
	Cmd    string          `json:"cmd,omitempty"`
	Out    string          `json:"out,omitempty"`
	Data   json.RawMessage `json:"data,omitempty"`
	Error  string          `json:"error,omitempty"`
	Status int             `json:"status,omitempty"` // HTTP status for errors (400, 404 ...)
}

// Ops the helper accepts. Anything else is refused.
var Ops = map[string]bool{
	"overview": true, "domains": true, "createApp": true, "deleteApp": true, "syncApp": true, "syncAll": true,
	"reloadCaddy": true, "siteOp": true, "siteLogs": true, "syncLog": true, "envGet": true, "envApply": true,
	"grant": true, "setDefaultDomain": true, "setDashboardHost": true, "setIPAccess": true,
	"updateStatus": true, "updateNow": true,
}

const maxMessage = 4 << 20

func Call(socket string, req Request, timeout time.Duration) (*Response, error) {
	c, err := net.DialTimeout("unix", socket, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("the app-host helper isn't running (%v)", err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(timeout))
	if err := json.NewEncoder(c).Encode(req); err != nil {
		return nil, err
	}
	c.(*net.UnixConn).CloseWrite()
	var resp Response
	if err := json.NewDecoder(io.LimitReader(c, maxMessage)).Decode(&resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func ReadRequest(r io.Reader) (*Request, error) {
	var req Request
	if err := json.NewDecoder(io.LimitReader(r, 1<<20)).Decode(&req); err != nil {
		return nil, err
	}
	return &req, nil
}
