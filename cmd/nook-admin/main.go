// nook-admin: the root-only side of nook. App users can't run it.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/user"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/sparkstonepdx/nook/dashboard"
	"github.com/sparkstonepdx/nook/internal/config"
	"github.com/sparkstonepdx/nook/internal/core"
	"github.com/sparkstonepdx/nook/internal/grants"
	"github.com/sparkstonepdx/nook/internal/install"
	"github.com/sparkstonepdx/nook/internal/update"
	"github.com/sparkstonepdx/nook/internal/web"
)

var version = "dev"

const usage = `nook-admin: run apps and static sites on this server.  (root)

  nook-admin install [--domain D] [--ip IP]     set up or upgrade this box
  nook-admin create NAME [--memory 256M] [--cpu 50%] [--tasks 256] [--repo owner/name]
  nook-admin delete NAME                         asks you to type the name
  nook-admin sync NAME | --all                   rebuild routes and services (runs on its own after deploys)
  nook-admin grant list [APP]
  nook-admin grant add|rm APP domain|sub|host NAME
  nook-admin env list NAME [--values]
  nook-admin env set NAME KEY [KEY ...]          prompts for each value (stays out of shell history)
  nook-admin env set NAME KEY=value ...
  nook-admin env unset NAME KEY ...
      add --site SITE (one site) or --previews SITE (that site's PR previews); default: the whole app
  nook-admin invite                              one-time dashboard login link
  nook-admin update [--check]                    install the latest signed release
  nook-admin firewall                            reload the rules that keep apps off each other's ports
  nook-admin version

Internal: helper (root API for the dashboard), web (the dashboard; runs as nook-web)
`

func die(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func needRoot() {
	if os.Geteuid() != 0 {
		die(fmt.Errorf("run as root"))
	}
}

func main() {
	update.Version = version
	core.UpdateHooks.Status = update.CurrentStatus
	core.UpdateHooks.Now = func(out *bytes.Buffer) error { return update.Apply(out, false) }
	if len(os.Args) < 2 {
		fmt.Print(usage)
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]
	h := core.New(os.Stdout)
	switch cmd {
	case "version":
		fmt.Println(version)
	case "install":
		needRoot()
		fs := flag.NewFlagSet("install", flag.ExitOnError)
		domain := fs.String("domain", "", "default domain")
		ip := fs.String("ip", "", "server's public IP")
		upgrade := fs.Bool("upgrade", false, "")
		noApt := fs.Bool("no-apt", false, "don't apt-get install missing packages")
		fs.Parse(args)
		die(install.Run(install.Opts{BaseDomain: *domain, SSHHost: *ip, Upgrade: *upgrade, NoApt: *noApt, Out: os.Stdout}))
	case "create":
		needRoot()
		if len(args) < 1 {
			die(fmt.Errorf("usage: nook-admin create NAME [--memory 256M] [--cpu 50%%] [--tasks 256] [--repo owner/name]"))
		}
		fs := flag.NewFlagSet("create", flag.ExitOnError)
		mem := fs.String("memory", "", "")
		cpu := fs.String("cpu", "", "")
		tasks := fs.String("tasks", "", "")
		repo := fs.String("repo", "", "")
		fs.Parse(args[1:])
		out, err := h.Create(core.CreateOpts{Name: args[0], Memory: *mem, CPU: *cpu, Tasks: *tasks, Repo: *repo})
		die(err)
		fmt.Print(out)
	case "delete":
		needRoot()
		if len(args) != 1 {
			die(fmt.Errorf("usage: nook-admin delete NAME"))
		}
		fmt.Printf("This deletes /srv/nook/%s and everything in it. Type the app name to confirm: ", args[0])
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		die(h.Delete(args[0], strings.TrimSpace(line)))
	case "sync":
		needRoot()
		if len(args) == 1 && args[0] == "--all" {
			die(h.SyncAll())
		} else if len(args) == 1 {
			die(h.Sync(args[0]))
		} else {
			die(fmt.Errorf("usage: nook-admin sync NAME | --all"))
		}
	case "grant":
		needRoot()
		grantCmd(h, args)
	case "env":
		needRoot()
		envCmd(h, args)
	case "invite":
		needRoot()
		store, err := web.OpenStore(config.WebStateDir)
		die(err)
		if wu, err := user.Lookup(config.WebUser); err == nil {
			uid, _ := strconv.Atoi(wu.Uid)
			gid, _ := strconv.Atoi(wu.Gid)
			os.Chown(config.WebStateDir+"/secret", uid, gid)
		}
		s, d := config.Load(), config.LoadDashboard()
		tok := store.MakeInvite("")
		if d.Host != "" {
			fmt.Printf("https://%s/invite?t=%s\n", d.Host, tok)
		}
		if d.IPAccess {
			fmt.Printf("https://%s:%s/invite?t=%s   (self-signed; accept the warning)\n", s.SSHHost, d.IPPort, tok)
		}
	case "update":
		needRoot()
		check, notify := false, false
		for _, a := range args {
			check = check || a == "--check"
			notify = notify || a == "--notify"
		}
		if check {
			_, err := update.Check(os.Stdout, notify)
			die(err)
			return
		}
		die(update.Apply(os.Stdout, false))
	case "helper":
		needRoot()
		log.SetFlags(0)
		die(core.ServeHelper())
	case "web":
		serveWeb()
	case "admin-caddy":
		needRoot()
		die(h.AdminCaddy())
	case "firewall":
		needRoot()
		die(core.Firewall())
	default:
		fmt.Print(usage)
		os.Exit(2)
	}
}

func grantCmd(h *core.Host, args []string) {
	if len(args) >= 1 && args[0] == "list" {
		gl, _ := grants.Load(config.GrantsFile)
		if len(args) == 2 {
			gl = gl.For(args[1])
		}
		for _, g := range gl {
			fmt.Printf("%-16s %s\n", g.App, g.Label())
		}
		return
	}
	if len(args) != 4 || (args[0] != "add" && args[0] != "rm") {
		die(fmt.Errorf("usage: nook-admin grant list [APP] | nook-admin grant add|rm APP domain|sub|host NAME"))
	}
	die(h.Grant(args[0] == "add", grants.Grant{App: args[1], Kind: args[2], Name: args[3]}))
}

func envCmd(h *core.Host, args []string) {
	var flags []string
	var pos []string
	scopeArg := ""
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case (a == "--site" || a == "--previews" || a == "--scope") && i+1 < len(args):
			i++
			if a == "--scope" {
				scopeArg = args[i]
			} else {
				scopeArg = strings.TrimPrefix(a, "--") + ":" + args[i]
			}
		case strings.HasPrefix(a, "--"):
			flags = append(flags, a)
		default:
			pos = append(pos, a)
		}
	}
	scope, err := core.ParseScope(scopeArg)
	die(err)
	has := func(f string) bool {
		for _, x := range flags {
			if x == f {
				return true
			}
		}
		return false
	}
	if len(pos) < 2 {
		die(fmt.Errorf("usage: nook-admin env list|set|unset|apply NAME ..."))
	}
	verb, app, rest := pos[0], pos[1], pos[2:]
	switch verb {
	case "list":
		v, err := core.EnvList(app, scope)
		die(err)
		for _, k := range v.Keys() {
			if has("--values") {
				val, _ := v.Get(k)
				fmt.Printf("%s=%s\n", k, val)
			} else {
				fmt.Println(k)
			}
		}
	case "set":
		set := map[string]string{}
		in := bufio.NewReader(os.Stdin)
		tty := isTTY()
		for _, a := range rest {
			if i := strings.IndexByte(a, '='); i > 0 {
				set[a[:i]] = a[i+1:]
				continue
			}
			if tty {
				fmt.Fprintf(os.Stderr, "%s: ", a)
			}
			line, err := in.ReadString('\n')
			if err != nil && line == "" {
				die(fmt.Errorf("no value given for %s", a))
			}
			set[a] = strings.TrimRight(line, "\r\n")
		}
		die(h.EnvApply(app, scope, set, nil, !has("--no-restart")))
	case "unset":
		die(h.EnvApply(app, scope, nil, rest, !has("--no-restart")))
	case "apply":
		var j struct {
			Set   map[string]string `json:"set"`
			Unset []string          `json:"unset"`
		}
		die(json.NewDecoder(os.Stdin).Decode(&j))
		die(h.EnvApply(app, scope, j.Set, j.Unset, !has("--no-restart")))
	default:
		die(fmt.Errorf("usage: nook-admin env list|set|unset|apply NAME ..."))
	}
}

func isTTY() bool {
	fi, err := os.Stdin.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

func serveWeb() {
	if os.Geteuid() == 0 {
		die(fmt.Errorf("the dashboard runs as %s, not root (systemctl start nook-web)", config.WebUser))
	}
	ui, err := fs.Sub(dashboard.Public, "public")
	die(err)
	store, err := web.OpenStore(config.WebStateDir)
	die(err)
	srv := &http.Server{
		Handler:           &web.Server{Store: store, Socket: config.HelperSocket, UI: ui},
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      11 * time.Minute,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    32 << 10,
	}
	// The listening socket comes from nook-web.socket (systemd socket activation): a unix socket only root and
	// Caddy can open, so apps on this box can't reach the dashboard directly or forge X-Forwarded-For.
	if os.Getenv("LISTEN_PID") != strconv.Itoa(os.Getpid()) || os.Getenv("LISTEN_FDS") != "1" {
		die(fmt.Errorf("start the dashboard with systemctl start nook-web (it needs the socket from nook-web.socket)"))
	}
	ln, err := net.FileListener(os.NewFile(3, "nook-web.sock"))
	die(err)
	log.Printf("dashboard on %s", config.WebSocket)
	syscall.Umask(0o077)
	die(srv.Serve(ln))
}
