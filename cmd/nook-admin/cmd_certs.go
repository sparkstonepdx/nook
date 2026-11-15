package main

import (
	"bufio"
	"fmt"
	"github.com/sparkstonepdx/nook/internal/core"
	"os"
	"strings"
	"time"
)

func certsCmd(h *core.Host, args []string) {
	if len(args) == 0 || args[0] == "list" {
		v := core.Certs()
		fmt.Printf("Caddy %s (uses wildcards for subdomains: %v, held: %v)\n", v.Caddy.Version, v.Caddy.PreferWildcard, v.Caddy.Held)
		for _, p := range v.Providers {
			fmt.Printf("  %s plugin: %v\n", p.Name, v.Caddy.Modules[p.ID])
		}
		for _, d := range v.Domains {
			kind := "one certificate per hostname"
			if d.Wildcard {
				kind = fmt.Sprintf("wildcard via %s: %s %s %s", d.Provider, d.Status, d.Expires, d.Detail)
				if d.Retry != "" {
					if t, err := time.Parse(time.RFC3339, d.Retry); err == nil {
						kind += " Next try: " + t.Local().Format("Mon Jan 2 3:04 PM MST") + "."
					}
				}
			}
			fmt.Printf("%-28s %s  (%s)\n", d.Domain, kind, strings.Join(d.For, ", "))
		}
		return
	}
	switch {
	case args[0] == "set" && len(args) >= 2:
		provider := "cloudflare"
		for i, a := range args {
			if a == "--provider" && i+1 < len(args) {
				provider = args[i+1]
			}
		}
		fmt.Fprint(os.Stderr, "API token: ")
		tok, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		die(h.SetWildcard(args[1], provider, strings.TrimSpace(tok)))
	case args[0] == "rm" && len(args) == 2:
		die(h.RemoveWildcard(args[1]))
	case args[0] == "plugin" && len(args) == 2:
		die(h.AddDNSPlugin(args[1]))
	case args[0] == "upgrade-caddy":
		die(h.UpgradeCaddy())
	default:
		die(fmt.Errorf("usage: nook-admin certs [list] | set DOMAIN [--provider P] | rm DOMAIN | plugin P | upgrade-caddy"))
	}
}
