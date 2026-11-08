package core

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/sparkstonepdx/nook/internal/config"
	"github.com/sparkstonepdx/nook/internal/grants"
	"github.com/sparkstonepdx/nook/internal/rpc"
)

// UpdateHooks lets the updater plug into the helper without an import cycle.
var UpdateHooks struct {
	Status func() (any, error)
	Now    func(out *bytes.Buffer) error
}

// ServeHelper runs the root helper: a unix socket only root and the dashboard's user may use.
func ServeHelper() error {
	web, err := user.Lookup(config.WebUser)
	if err != nil {
		return fmt.Errorf("user %s missing; run nook-admin install", config.WebUser)
	}
	webUID, _ := strconv.Atoi(web.Uid)
	webGID, _ := strconv.Atoi(web.Gid)
	dir := filepath.Dir(config.HelperSocket)
	os.MkdirAll(dir, 0o750)
	os.Chown(dir, 0, webGID)
	os.Chmod(dir, 0o750)
	os.Remove(config.HelperSocket)
	ln, err := net.Listen("unix", config.HelperSocket)
	if err != nil {
		return err
	}
	os.Chown(config.HelperSocket, 0, webGID)
	os.Chmod(config.HelperSocket, 0o660)
	log.Printf("helper listening on %s", config.HelperSocket)
	for {
		c, err := ln.Accept()
		if err != nil {
			continue
		}
		go handleHelper(c.(*net.UnixConn), webUID)
	}
}

func peerUID(c *net.UnixConn) (int, error) {
	raw, err := c.SyscallConn()
	if err != nil {
		return -1, err
	}
	var cred *syscall.Ucred
	var cerr error
	raw.Control(func(fd uintptr) {
		cred, cerr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	})
	if cerr != nil {
		return -1, cerr
	}
	return int(cred.Uid), nil
}

func handleHelper(c *net.UnixConn, webUID int) {
	defer c.Close()
	c.SetDeadline(time.Now().Add(10 * time.Minute))
	uid, err := peerUID(c)
	if err != nil || (uid != 0 && uid != webUID) {
		json.NewEncoder(c).Encode(rpc.Response{Error: "not allowed", Status: 403})
		return
	}
	req, err := rpc.ReadRequest(c)
	if err != nil {
		json.NewEncoder(c).Encode(rpc.Response{Error: "bad request", Status: 400})
		return
	}
	if !rpc.Ops[req.Op] {
		json.NewEncoder(c).Encode(rpc.Response{Error: "unknown operation", Status: 404})
		return
	}
	log.Printf("%s: %s %v", req.By, req.Op, req.Params)
	json.NewEncoder(c).Encode(Dispatch(req))
}

func data(v any, err error) rpc.Response {
	if err != nil {
		return rpc.Response{Error: err.Error(), Status: 400}
	}
	b, _ := json.Marshal(v)
	return rpc.Response{OK: true, Data: b}
}

func command(cmd string, run func(h *Host) error) rpc.Response {
	var out bytes.Buffer
	h := New(&out)
	err := run(h)
	r := rpc.Response{OK: err == nil, Cmd: cmd, Out: out.String()}
	if err != nil {
		r.Code = 1
		if r.Out != "" {
			r.Out += "\n"
		}
		r.Out += err.Error()
	}
	return r
}

// Dispatch maps one helper request onto core. Every parameter is validated again here.
func Dispatch(req *rpc.Request) rpc.Response {
	p := req.Params
	if p == nil {
		p = map[string]string{}
	}
	h := New(&bytes.Buffer{})
	switch req.Op {
	case "overview":
		return data(h.Overview(), nil)
	case "domains":
		return data(h.Domains(), nil)
	case "createApp":
		var created string
		r := command("nook-admin create "+p["name"], func(h *Host) error {
			out, err := h.Create(CreateOpts{Name: p["name"], Memory: p["memory"], CPU: p["cpu"], Tasks: p["tasks"], Repo: p["repo"]})
			created = out
			return err
		})
		r.Out += created
		return r
	case "deleteApp":
		return command("nook-admin delete "+p["app"], func(h *Host) error { return h.Delete(p["app"], p["confirm"]) })
	case "syncApp":
		return command("nook-admin sync "+p["app"], func(h *Host) error { return h.Sync(p["app"]) })
	case "syncAll":
		return command("nook-admin sync --all", func(h *Host) error { return h.SyncAll() })
	case "reloadCaddy":
		return command("systemctl reload caddy", func(h *Host) error { return h.ReloadCaddy() })
	case "siteOp":
		return command(p["op"]+" "+p["app"]+"/"+p["site"], func(h *Host) error { return h.SiteOp(p["app"], p["site"], p["op"]) })
	case "siteLogs":
		return data(SiteLogs(p["app"], p["site"]))
	case "syncLog":
		out, err := SyncLog(p["app"])
		return data(map[string]string{"log": out}, err)
	case "envGet":
		v, err := EnvList(p["app"])
		if err != nil {
			return data(nil, err)
		}
		vars := []map[string]string{}
		for _, k := range v.Keys() {
			val, _ := v.Get(k)
			vars = append(vars, map[string]string{"key": k, "value": val})
		}
		running := []string{}
		for _, s := range RunningSites(p["app"]) {
			running = append(running, s)
		}
		return data(map[string]any{"vars": vars, "running": running}, nil)
	case "envApply":
		return command("nook-admin env apply "+p["app"], func(h *Host) error { return h.EnvApply(p["app"], req.Set, req.Unset, true) })
	case "grant":
		g := grants.Grant{App: p["app"], Kind: p["kind"], Name: p["name"]}
		verb := "add"
		if p["op"] == "remove" {
			verb = "rm"
		}
		return command("nook-admin grant "+verb+" "+g.String(), func(h *Host) error { return h.Grant(verb == "add", g) })
	case "setDefaultDomain":
		return command("nook-admin default-domain "+p["domain"], func(h *Host) error { return h.SetDefaultDomain(p["domain"]) })
	case "setDashboardHost":
		return command("nook-admin dashboard-host "+p["host"], func(h *Host) error { return h.SetDashboardHost(p["host"]) })
	case "setIPAccess":
		return command("nook-admin ip-access "+p["on"], func(h *Host) error { return h.SetIPAccess(p["on"] == "true") })
	case "updateStatus":
		if UpdateHooks.Status == nil {
			return data(nil, errors.New("updates aren't available"))
		}
		return data(UpdateHooks.Status())
	case "updateNow":
		if UpdateHooks.Now == nil {
			return data(nil, errors.New("updates aren't available"))
		}
		var out bytes.Buffer
		err := UpdateHooks.Now(&out)
		r := rpc.Response{OK: err == nil, Cmd: "nook-admin update", Out: out.String()}
		if err != nil {
			r.Code, r.Out = 1, r.Out+err.Error()
		}
		return r
	}
	_ = h
	return rpc.Response{Error: "unknown operation", Status: 404}
}
