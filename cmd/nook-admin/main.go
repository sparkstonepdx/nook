// nook-admin: the root-only side of nook. App users can't run it.
package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"io"
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
	"github.com/sparkstonepdx/nook/internal/install"
	"github.com/sparkstonepdx/nook/internal/update"
	"github.com/sparkstonepdx/nook/internal/web"
)

var version = "dev"

const usage = `nook-admin: run apps and static sites on this server.  (root)

  nook-admin install [--domain D] [--ip IP]     set up or upgrade this box
  nook-admin create NAME [--memory 256M] [--cpu 50%] [--tasks 256] [--repo owner/name]
  nook-admin delete NAME                         asks you to type the name
  nook-admin limits NAME [--memory 512M] [--cpu 100%] [--tasks 256]   change an app's limits (no restart)
  nook-admin certs [list] | set DOMAIN [--provider cloudflare] (token on stdin) | rm DOMAIN
  nook-admin certs plugin PROVIDER | upgrade-caddy   add a DNS plugin to Caddy (and hold it) / upgrade Caddy keeping plugins
  nook-admin headroom [24h|7d|30d]               how full this server is, and what to add
  nook-admin backup [status|setup|run|snapshots [APP]|restore APP [SNAPSHOT]|skip APP on|off]
                                                 backups to an S3-compatible bucket (restic)
  nook-admin login NAME [on|off]                 put an app's sites behind the dashboard login (or show it)
  nook-admin ports NAME [grant|revoke 25,993]    let an app listen on public ports itself, like mail (or list them)
  nook-admin system [check|update [--security]|auto on|off|restart]   the server's own packages and restarts
  nook-admin directory [add|rm HOST]              sites listed in the public directory (GET /api/v1/directory)
  nook-admin sync NAME | --all                   rebuild routes and services (runs on its own after deploys)
  nook-admin grant list [APP]
  nook-admin grant add|rm APP domain|sub|host NAME
  nook-admin grant add|rm APP port-domain|port-host NAME PORTS   its own public ports, for a domain or at a hostname
  nook-admin env list NAME [--values]
  nook-admin env set NAME KEY [KEY ...]          prompts for each value (stays out of shell history)
  nook-admin env set NAME KEY=value ...
  nook-admin env unset NAME KEY ...
      add --site SITE (one site) or --previews SITE (that site's PR previews); default: the whole app
  nook-admin invite                              one-time dashboard login link
  nook-admin update [--check]                    install the latest signed release
  nook-admin instructions APP [--site S] [--repo OWNER/NAME] [--new-token]   how to deploy to APP, any time
  nook-admin token create APP [--label L]        a deploy token (shown once) for the deploy API
  nook-admin token list [APP] | rm APP ID
  nook-admin store [list] | install ID [--app NAME] [--site S] [--policy P] [--env K=V ...]
  nook-admin store update APP [--force] | policy APP off|notify|patch|minor|all | check
  nook-admin services [list]
  nook-admin bind APP SERVICE [--provider ID] [--as NAME=NEWNAME ...]   e.g. bind blog postgres
  nook-admin unbind APP SERVICE                  (a database is kept)
  nook-admin dns plan|set APP NAME               the records its port grant on NAME needs, through the saved Cloudflare token
  nook-admin migrate APP SERVICE TO              move it to another provider with its data (postgres-17)
  nook-admin subduction                          the Subduction server's URL, peer id and Keyhive contact card
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
	core.UpdateHooks.Now = func(out *bytes.Buffer) error {
		// a backup first, when they're set up; one that fails is said, not a reason to hold the update back
		if core.LoadBackupConfig().Configured() {
			if err := core.New(out).BackupRun("before update"); err != nil {
				fmt.Fprintf(out, "backup before the update: %v\n", err)
			}
		}
		return update.Apply(out, false)
	}
	core.UpdateHooks.Notify = update.Notify
	core.UpdateHooks.Check = func() (any, error) {
		update.Check(io.Discard, false) // the error is recorded in the status
		return update.CurrentStatus()
	}
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
	case "token":
		needRoot()
		tokenCmd(args)
	case "limits":
		needRoot()
		if len(args) < 2 {
			die(fmt.Errorf("usage: nook-admin limits NAME [--memory 512M] [--cpu 100%%] [--tasks 256]"))
		}
		var mem, cpu, tasks string
		for i := 2; i+1 < len(args); i += 2 {
			switch args[i] {
			case "--memory":
				mem = args[i+1]
			case "--cpu":
				cpu = args[i+1]
			case "--tasks":
				tasks = args[i+1]
			default:
				die(fmt.Errorf("unknown option %s", args[i]))
			}
		}
		die(h.SetLimits(args[1], mem, cpu, tasks))
	case "certs":
		needRoot()
		certsCmd(h, args)
	case "headroom":
		needRoot()
		headroomCmd(args)
	case "backup":
		needRoot()
		backupCmd(h, args)
	case "login":
		needRoot()
		loginCmd(h, args)
	case "ports":
		needRoot()
		portsCmd(h, args)
	case "system":
		needRoot()
		systemCmd(h, args)
	case "directory":
		needRoot()
		directoryCmd(h, args)
	case "instructions":
		needRoot()
		instructionsCmd(args)
	case "bind":
		needRoot()
		bindCmd(h, args)
	case "store":
		needRoot()
		storeCmd(h, args)
	case "unbind":
		needRoot()
		if len(args) != 2 {
			die(fmt.Errorf("usage: nook-admin unbind APP SERVICE"))
		}
		die(h.Unbind(args[0], args[1]))
	case "services":
		needRoot()
		servicesCmd(h, args)
	case "dns":
		needRoot()
		switch {
		case len(args) == 3 && args[0] == "plan":
			plan, err := core.PlanDNS(args[1], args[2])
			if err != nil {
				die(err)
			}
			for _, c := range plan.Changes {
				fmt.Printf("%-8s %-5s %s  %s\n", c.Action, c.Type, c.Name, c.Value)
				for _, o := range c.Old {
					fmt.Printf("         was   %s\n", o)
				}
			}
		case len(args) == 3 && args[0] == "set":
			die(h.SetDNS(args[1], args[2]))
		default:
			die(fmt.Errorf("usage: nook-admin dns plan|set APP NAME   (NAME: one of its port-domain or port-host grants)"))
		}
	case "migrate":
		needRoot()
		if len(args) != 3 {
			die(fmt.Errorf("usage: nook-admin migrate APP SERVICE TO  (like: nook-admin migrate shop postgres postgres-17)"))
		}
		die(h.Migrate(args[0], args[1], args[2]))
	case "subduction":
		needRoot()
		id := core.Subduction()
		if !id.Installed {
			die(fmt.Errorf("no Subduction app on this server (extras/subduction-app.sh sets one up)"))
		}
		fmt.Printf("SUBDUCTION_URL=%s\nSYNC_SERVER_PEER_ID=%s\n", id.URL, id.PeerID)
		if id.Card != "" {
			fmt.Printf("SYNC_SERVER_CONTACT_CARD=%s\n", id.Card)
		}
		if id.Problem != "" {
			fmt.Fprintln(os.Stderr, id.Problem)
		}
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
			if err := core.RestartStale(os.Stdout, true); err != nil {
				fmt.Fprintln(os.Stderr, err)
			}
			_, err := update.Check(os.Stdout, notify)
			// the server's own packages: what's waiting, and the security fixes if they install by themselves
			if s, serr := h.SystemCheck(true); serr != nil {
				fmt.Fprintln(os.Stderr, serr)
			} else if notify {
				core.NotifySystem(s)
			}
			die(err)
			return
		}
		die(update.Apply(os.Stdout, false))
	case "move":
		needRoot()
		switch {
		case len(args) == 1 && args[0] == "code":
			code, err := h.PairCode()
			die(err)
			fmt.Printf("On the new server (a fresh install), run within 24 hours:\n\n  nook-admin move from %s\n\nIt stops this server's apps, takes a last backup and restores it there. Keep the code private: it hands over the backups' password.\n", code)
		case len(args) == 2 && args[0] == "from":
			die(h.MoveFrom(args[1]))
		case len(args) == 1 && args[0] == "resume":
			die(h.MoveResume())
		default:
			die(fmt.Errorf("usage: nook-admin move code | move from CODE | move resume"))
		}
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
		if len(args) == 1 && args[0] == "--if-changed" {
			die(core.FirewallIfChanged())
			return
		}
		die(core.Firewall())
	default:
		fmt.Print(usage)
		os.Exit(2)
	}
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
		Handler:           &web.Server{Store: store, Socket: config.HelperSocket, UI: ui, Version: version},
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
