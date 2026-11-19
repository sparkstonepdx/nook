package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"github.com/sparkstonepdx/nook/internal/config"
	"github.com/sparkstonepdx/nook/internal/core"
	"github.com/sparkstonepdx/nook/internal/grants"
	"os"
	"strings"
	"time"
)

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
		die(fmt.Errorf("usage: nook-admin grant list [APP] | nook-admin grant add|rm APP domain|sub|host|mail NAME"))
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

func tokenCmd(args []string) {
	usage := fmt.Errorf("usage: nook-admin token create APP [--label L] | list [APP] | rm APP ID")
	if len(args) == 0 {
		die(usage)
	}
	switch args[0] {
	case "create":
		label := ""
		var pos []string
		for i := 1; i < len(args); i++ {
			if args[i] == "--label" && i+1 < len(args) {
				label = args[i+1]
				i++
				continue
			}
			pos = append(pos, args[i])
		}
		if len(pos) != 1 {
			die(usage)
		}
		tok, info, err := core.TokenCreate(pos[0], label)
		die(err)
		fmt.Printf("%s\n\nDeploy token %s for %s. Shown once; store it as the NOOK_TOKEN secret.\nDeploy URL: %s<site>\n", tok, info.ID, pos[0], core.DeployBase(config.Load()))
	case "list":
		app := ""
		if len(args) > 1 {
			app = args[1]
		}
		for _, t := range core.TokenList(app) {
			fmt.Printf("%s  %-16s %s  %s\n", t.ID, t.App, time.Unix(t.Created, 0).Format("2006-01-02"), t.Label)
		}
	case "rm", "revoke":
		if len(args) != 3 {
			die(usage)
		}
		die(core.TokenRevoke(args[1], args[2]))
		fmt.Println("revoked")
	default:
		die(usage)
	}
}

func instructionsCmd(args []string) {
	usage := fmt.Errorf("usage: nook-admin instructions APP [--site SITE] [--repo OWNER/NAME] [--new-token]")
	var pos []string
	site, repo, newToken := "", "", false
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--site" && i+1 < len(args):
			site = args[i+1]
			i++
		case args[i] == "--repo" && i+1 < len(args):
			repo = args[i+1]
			i++
		case args[i] == "--new-token":
			newToken = true
		default:
			pos = append(pos, args[i])
		}
	}
	if len(pos) != 1 {
		die(usage)
	}
	app := pos[0]
	if _, err := core.LookupApp(app); err != nil {
		die(err)
	}
	if repo != "" {
		die(core.SetAppRepo(app, repo))
	}
	tok := ""
	if newToken {
		label := core.AppRepo(app)
		if label == "" {
			label = "instructions"
		}
		t, _, err := core.TokenCreate(app, label)
		die(err)
		tok = t
	}
	fmt.Print(core.Instructions(app, tok, site))
}

// nook-admin login NAME [on|off]: an app's Nook login switch.
func loginCmd(h *core.Host, args []string) {
	switch {
	case len(args) == 1:
		fmt.Println(map[bool]string{true: "on", false: "off"}[core.AppLogin(args[0])])
	case len(args) == 2 && (args[1] == "on" || args[1] == "off"):
		die(h.SetLogin(args[0], args[1] == "on"))
	default:
		die(fmt.Errorf("usage: nook-admin login NAME [on|off]"))
	}
}

func portsCmd(h *core.Host, args []string) {
	switch {
	case len(args) == 1:
		for _, p := range core.GrantedPorts(args[0]) {
			fmt.Println(p)
		}
	case len(args) == 3 && (args[1] == "grant" || args[1] == "revoke"):
		ports, err := core.ParsePorts(args[2])
		die(err)
		die(h.GrantPorts(args[0], ports, args[1] == "revoke"))
	default:
		die(fmt.Errorf("usage: nook-admin ports APP [grant|revoke 25,465,587,993]"))
	}
}

// nook-admin system: the server's own packages.
func systemCmd(h *core.Host, args []string) {
	switch {
	case len(args) == 0:
		s := core.ReadSystemStatus()
		fmt.Printf("%s, kernel %s\n", s.OS, s.Kernel)
		for _, p := range s.Packages {
			tag := ""
			if p.Security {
				tag = " (security)"
			}
			if p.App != "" {
				tag += " (with app " + p.App + ")"
			}
			fmt.Printf("  %s %s -> %s%s\n", p.Name, p.From, p.To, tag)
		}
		if s.Restart {
			fmt.Println("restart needed:", s.RestartWhy)
		}
		if len(s.Stale) > 0 {
			fmt.Println("still on replaced libraries:", strings.Join(s.Stale, ", "))
		}
	case args[0] == "check":
		_, err := h.SystemCheck(false)
		die(err)
	case args[0] == "update":
		_, err := h.SystemUpdate(len(args) > 1 && args[1] == "--security")
		die(err)
	case args[0] == "auto" && len(args) == 2 && (args[1] == "on" || args[1] == "off"):
		die(core.SetSystemAuto(args[1] == "on"))
	case args[0] == "restart":
		die(core.SystemRestart())
	default:
		die(fmt.Errorf("usage: nook-admin system [check|update [--security]|auto on|off|restart]"))
	}
}
