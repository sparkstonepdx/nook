package core

import (
	"bufio"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/sparkstonepdx/nook/internal/config"
)

// procNet is where the kernel lists TCP sockets (vars for tests).
var procNet = []string{"/proc/net/tcp", "/proc/net/tcp6"}

// Listeners is "port . uid" for every TCP port outside the app range that an app's own user listens on, except
// ports granted as public (publicports.go). The firewall lets only that app (and root, Caddy) connect to them.
func Listeners(s config.Settings) []string {
	apps := map[int]string{} // uid -> app
	for _, a := range Apps() {
		if u, err := LookupApp(a); err == nil {
			apps[u.UID] = a
		}
	}
	seen := map[string]bool{}
	var out []string
	for _, l := range listening(procNet) {
		app, ok := apps[l.uid]
		if !ok || l.port >= s.PortMin && l.port <= s.PortMax || isGranted(app, l.port) {
			continue
		}
		e := fmt.Sprintf("%d . %d", l.port, l.uid)
		if !seen[e] {
			seen[e] = true
			out = append(out, e)
		}
	}
	sort.Strings(out)
	return out
}

func isGranted(app string, port int) bool {
	for _, p := range GrantedPorts(app) {
		if p == port {
			return true
		}
	}
	return false
}

type listener struct{ port, uid int }

// listening reads LISTEN sockets (state 0A) from /proc/net/tcp-style tables: local address "0100007F:23F0", uid
// in the eighth column.
func listening(files []string) []listener {
	var out []listener
	for _, f := range files {
		b, err := os.Open(f)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(b)
		sc.Scan() // header
		for sc.Scan() {
			fs := strings.Fields(sc.Text())
			if len(fs) < 8 || fs[3] != "0A" {
				continue
			}
			_, portHex, ok := strings.Cut(fs[1], ":")
			port, err1 := strconv.ParseInt(portHex, 16, 32)
			uid, err2 := strconv.Atoi(fs[7])
			if !ok || err1 != nil || err2 != nil {
				continue
			}
			out = append(out, listener{int(port), uid})
		}
		b.Close()
	}
	return out
}

// FirewallIfChanged reloads the rules only when they'd differ from what's loaded (the 30-second check).
func FirewallIfChanged() error {
	old, _ := os.ReadFile(config.FirewallFile)
	if string(old) == FirewallRules(config.Load()) {
		return nil
	}
	return Firewall()
}
