// Package rpc is the fixed set of operations the unprivileged dashboard may ask the root helper for,
// over a unix socket. One JSON request per connection.
package rpc

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"syscall"
	"time"
)

type Request struct {
	Op     string            `json:"op"`
	By     string            `json:"by"` // dashboard user, for the log
	Params map[string]string `json:"params,omitempty"`
	Set    map[string]string `json:"set,omitempty"`
	Unset  []string          `json:"unset,omitempty"`
	Secret string            `json:"secret,omitempty"` // a deploy token: checked by the helper, never logged
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
	"updateStatus": true, "updateNow": true, "updateCheck": true,
	"tokenApp": true, "deploy": true, "undeploy": true, "tokenList": true, "tokenCreate": true, "tokenRevoke": true,
	"services": true, "bind": true, "unbind": true, "instructions": true, "setRepo": true, "ciEnv": true,
}

const maxMessage = 4 << 20

func Call(socket string, req Request, timeout time.Duration) (*Response, error) {
	c, err := net.DialTimeout("unix", socket, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("the nook-admin helper isn't running (%v)", err)
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

// CallWithFile is Call that also hands the helper an open file (SCM_RIGHTS), so root reads exactly what the
// caller opened and never a path the caller named.
func CallWithFile(socket string, req Request, f *os.File, timeout time.Duration) (*Response, error) {
	c, err := net.DialTimeout("unix", socket, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("the nook-admin helper isn't running (%v)", err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(timeout))
	b, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	uc := c.(*net.UnixConn)
	if _, _, err := uc.WriteMsgUnix(b, syscall.UnixRights(int(f.Fd())), nil); err != nil {
		return nil, err
	}
	uc.CloseWrite()
	var resp Response
	if err := json.NewDecoder(io.LimitReader(c, maxMessage)).Decode(&resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// ReadRequestFile reads one request and, if the caller sent one, the file passed with it.
func ReadRequestFile(c *net.UnixConn) (*Request, *os.File, error) {
	var buf []byte
	var file *os.File
	chunk, oob := make([]byte, 64<<10), make([]byte, syscall.CmsgSpace(4*4))
	for len(buf) <= 1<<20 {
		n, oobn, _, _, err := c.ReadMsgUnix(chunk, oob)
		if oobn > 0 {
			if msgs, perr := syscall.ParseSocketControlMessage(oob[:oobn]); perr == nil {
				for _, m := range msgs {
					fds, _ := syscall.ParseUnixRights(&m)
					for _, fd := range fds {
						if file == nil {
							file = os.NewFile(uintptr(fd), "upload")
						} else {
							syscall.Close(fd)
						}
					}
				}
			}
		}
		buf = append(buf, chunk[:n]...)
		if errors.Is(err, io.EOF) || (err == nil && n == 0) {
			break
		}
		if err != nil {
			if file != nil {
				file.Close()
			}
			return nil, nil, err
		}
	}
	var req Request
	if err := json.Unmarshal(buf, &req); err != nil {
		if file != nil {
			file.Close()
		}
		return nil, nil, err
	}
	return &req, file, nil
}

func ReadRequest(r io.Reader) (*Request, error) {
	var req Request
	if err := json.NewDecoder(io.LimitReader(r, 1<<20)).Decode(&req); err != nil {
		return nil, err
	}
	return &req, nil
}
