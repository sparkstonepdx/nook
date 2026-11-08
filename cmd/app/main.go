// app: the user-level side of app-host. It only ever acts as whoever runs it; no root code lives here.
// App users (and their deploy keys, through ssh-gate) use `app site ...`.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/sparkstonepdx/app-host/internal/caddyfile"
	"github.com/sparkstonepdx/app-host/internal/config"
	"github.com/sparkstonepdx/app-host/internal/gate"
	"github.com/sparkstonepdx/app-host/internal/names"
	"github.com/sparkstonepdx/app-host/internal/tarx"
)

var version = "dev"

const usage = `app: deploy and manage this app's sites.

  app site deploy DIR SITE     publish DIR (atomic swap). SITE: "blog" = blog.<default domain>,
                               "client.com" / "www.client.com" = exactly that (needs a grant)
  app site rm SITE             take it down
  app site restart SITE        restart a running app
  app site list                what's in sites/
  app site logs SITE [N]       last N lines (default 100) of a running app's log
  app version

A folder with an executable "run" is started as a service on $PORT; anything else is served as static files.
Admin commands (create, delete, sync, grant, env, invite, update) are in app-host, as root.
`

func die(format string, a ...any) { fmt.Fprintf(os.Stderr, format+"\n", a...); os.Exit(1) }

func main() {
	if len(os.Args) < 2 {
		fmt.Print(usage)
		os.Exit(2)
	}
	switch os.Args[1] {
	case "version":
		fmt.Println(version)
	case "site":
		os.Exit(site(os.Args[2:]))
	case "ssh-gate":
		sshGate()
	case "internal-pack": // run by app-host as this app's user
		if len(os.Args) != 4 {
			os.Exit(2)
		}
		max, _ := strconv.ParseInt(os.Args[3], 10, 64)
		if err := tarx.Pack(os.Args[2], os.Stdout, max); err != nil {
			die("%v", err)
		}
	case "internal-read-env": // run by app-host as this app's user
		readCapped(filepath.Join(home(), "secrets.env"), 64<<10)
	case "internal-read-keys": // migration from the old layout, run as this app's user
		readCapped(filepath.Join(home(), ".ssh", "authorized_keys"), 64<<10)
	case "create", "delete", "sync", "grant", "env", "invite", "update":
		if os.Geteuid() == 0 {
			syscall.Exec(config.HostBin, append([]string{config.HostBin}, os.Args[1:]...), os.Environ())
		}
		die("app %s is an admin command: run app-host %s as root", os.Args[1], os.Args[1])
	default:
		fmt.Print(usage)
		os.Exit(2)
	}
}

func readCapped(path string, max int64) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	buf := make([]byte, max)
	n, _ := f.Read(buf)
	os.Stdout.Write(buf[:n])
}

func home() string {
	if h := os.Getenv("HOME"); h != "" {
		return h
	}
	u, _ := user.Current()
	return u.HomeDir
}

// appName is this user's app (user app-<name>, home /srv/apps/<name>).
func appName() string {
	u, err := user.Current()
	if err == nil && strings.HasPrefix(u.Username, "app-") {
		return strings.TrimPrefix(u.Username, "app-")
	}
	return filepath.Base(home())
}

func site(args []string) int {
	if len(args) == 0 {
		fmt.Print(usage)
		return 2
	}
	sites := filepath.Join(home(), "sites")
	if fi, err := os.Stat(sites); err != nil || !fi.IsDir() {
		die("app: run this as an app user")
	}
	check := func(s string) {
		if !names.Site(s) {
			die("bad site name: '%s' (a subdomain like blog, or a hostname like client.com)", s)
		}
	}
	switch args[0] {
	case "deploy":
		if len(args) != 3 {
			die("usage: app site deploy DIR SITE")
		}
		src, s := args[1], args[2]
		check(s)
		if fi, err := os.Stat(src); err != nil || !fi.IsDir() {
			die("%s isn't a folder", src)
		}
		next, old := filepath.Join(sites, ".new-"+s), filepath.Join(sites, ".old-"+s)
		os.RemoveAll(next)
		os.RemoveAll(old)
		if out, err := exec.Command("cp", "-a", src, next).CombinedOutput(); err != nil {
			die("copy failed: %s", out)
		}
		exec.Command("chmod", "-R", "u+rwX,go-w", next).Run()
		dst := filepath.Join(sites, s)
		if _, err := os.Lstat(dst); err == nil {
			os.Rename(dst, old)
		}
		if err := os.Rename(next, dst); err != nil {
			die("%v", err)
		}
		os.RemoveAll(old)
		kind := "static"
		if fi, err := os.Stat(filepath.Join(dst, "run")); err == nil && fi.Mode()&0o111 != 0 {
			kind = "running app"
		}
		var ino uint64
		if fi, err := os.Stat(dst); err == nil {
			if st, ok := fi.Sys().(*syscall.Stat_t); ok {
				ino = st.Ino
			}
		}
		if waitLive(s, ino) {
			fmt.Printf("deployed %s (%s)\n", s, kind)
			return 0
		}
		fmt.Printf("deployed %s (%s), but it is NOT live: this app isn't allowed that hostname, it's reserved, or another app has it. Ask the server admin.\n", s, kind)
		return 2
	case "rm":
		if len(args) != 2 {
			die("usage: app site rm SITE")
		}
		check(args[1])
		os.RemoveAll(filepath.Join(sites, args[1]))
		os.RemoveAll(filepath.Join(home(), "builds", args[1]))
		fmt.Printf("removed %s\n", args[1])
	case "restart":
		if len(args) != 2 {
			die("usage: app site restart SITE")
		}
		check(args[1])
		if fi, err := os.Stat(filepath.Join(sites, args[1], "run")); err != nil || fi.Mode()&0o111 == 0 {
			die("%s is not a running app", args[1])
		}
		os.WriteFile(filepath.Join(sites, ".restart-"+args[1]), nil, 0o600)
		time.Sleep(3 * time.Second)
		fmt.Printf("restarted %s\n", args[1])
	case "list":
		entries, _ := os.ReadDir(sites)
		var out []string
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), ".") || !e.IsDir() {
				continue
			}
			kind := "static"
			if fi, err := os.Stat(filepath.Join(sites, e.Name(), "run")); err == nil && fi.Mode()&0o111 != 0 {
				kind = "running app"
			}
			out = append(out, fmt.Sprintf("%s  (%s)", e.Name(), kind))
		}
		sort.Strings(out)
		for _, l := range out {
			fmt.Println(l)
		}
	case "logs":
		if len(args) < 2 {
			die("usage: app site logs SITE [N]")
		}
		check(args[1])
		n := "100"
		if len(args) == 3 {
			n = args[2]
		}
		if _, err := strconv.Atoi(n); err != nil {
			die("N must be a number")
		}
		cmd := exec.Command("tail", "-n", n, filepath.Join(config.LogDir, appName(), args[1]+".log"))
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if cmd.Run() != nil {
			fmt.Printf("no log for %s yet\n", args[1])
		}
	default:
		fmt.Print(usage)
		return 2
	}
	return 0
}

// waitLive waits until the sites/ watcher has published this exact deploy: the app's Caddy file records
// each site folder's inode, which changes with every atomic swap.
func waitLive(s string, ino uint64) bool {
	file := filepath.Join(config.CaddyAppsDir, appName()+".caddy")
	want := strconv.FormatUint(ino, 10)
	for i := 0; i < 30; i++ {
		if b, err := os.ReadFile(file); err == nil && caddyfile.Version(string(b), s) == want {
			return true
		}
		time.Sleep(time.Second)
	}
	return false
}

// sshGate is every app user's SSH session (sshd ForceCommand): rsync uploads into builds/ and `app site`.
func sshGate() {
	os.Chdir(home())
	words, err := gate.Split(os.Getenv("SSH_ORIGINAL_COMMAND"))
	if err == nil {
		var kind string
		kind, err = gate.Check(words)
		if err == nil {
			switch kind {
			case gate.App:
				os.Exit(site(words[2:]))
			case gate.Rsync:
				os.MkdirAll(filepath.Join(home(), "builds"), 0o700)
				path, lerr := exec.LookPath("rsync")
				if lerr != nil {
					die("rsync isn't installed on the server")
				}
				syscall.Exec(path, words, []string{"HOME=" + home(), "PATH=/usr/bin:/bin"})
			}
		}
	}
	die("app-host: %v", err)
}
