package main

import (
	"fmt"
	"github.com/sparkstonepdx/nook/internal/core"
	"github.com/sparkstonepdx/nook/internal/update"
	"os"
	"strings"
)

func storeCmd(h *core.Host, args []string) {
	usage := fmt.Errorf("usage: nook-admin store [list] | catalogs [add REPO|URL | rm ID] | install ID [--catalog C] [--app NAME] [--site S] [--policy P] [--env K=V ...] | update APP [--force] | policy APP P | check")
	if len(args) == 0 || args[0] == "list" {
		v := core.StoreOverview(len(args) > 1 && args[1] == "--refresh")
		if v.Error != "" {
			fmt.Fprintln(os.Stderr, "catalog:", v.Error)
		}
		fmt.Println("Catalog:")
		for _, a := range v.Catalog {
			from := ""
			if !a.Official {
				from = " [" + a.CatalogName + "]"
			}
			fmt.Printf("  %-14s %-10s %s%s\n", a.ID, a.Version, a.Description, from)
		}
		fmt.Println("Installed:")
		for _, r := range v.Installed {
			note := ""
			if r.Available {
				note = " → " + r.Latest + " available"
			}
			if r.LastError != "" {
				note += " (last update: " + r.LastError + ")"
			}
			fmt.Printf("  %-14s %-10s %-10s policy %s%s\n", r.App, r.ID, r.Version, r.Policy, note)
		}
		return
	}
	switch args[0] {
	case "catalogs":
		switch {
		case len(args) == 1:
			for _, c := range core.StoreOverview(false).Catalogs {
				state := fmt.Sprintf("%d apps", c.Apps)
				if c.Error != "" {
					state = c.Error
				}
				fmt.Printf("  %-24s key %s  %s  %s\n", c.ID, c.Fingerprint, c.URL, state)
			}
		case len(args) == 3 && args[1] == "add":
			s, err := core.AddCatalog(args[2])
			die(err)
			fmt.Printf("added %s (key %s). Its apps are listed as community apps, not reviewed by nook.\n", s.Name, update.KeyFingerprint(s.Key))
		case len(args) == 3 && args[1] == "rm":
			die(core.RemoveCatalog(args[2]))
		default:
			die(usage)
		}
		return
	case "install":
		o := core.InstallOpts{Env: map[string]string{}}
		for i := 1; i < len(args); i++ {
			if i+1 < len(args) {
				switch args[i] {
				case "--catalog":
					o.Catalog = args[i+1]
					i++
					continue
				case "--app":
					o.App = args[i+1]
					i++
					continue
				case "--site":
					o.Site = args[i+1]
					i++
					continue
				case "--policy":
					o.Policy = args[i+1]
					i++
					continue
				case "--version":
					o.Version = args[i+1]
					i++
					continue
				case "--env":
					k, v, ok := strings.Cut(args[i+1], "=")
					if !ok {
						die(usage)
					}
					o.Env[k] = v
					i++
					continue
				}
			}
			if o.ID != "" {
				die(usage)
			}
			o.ID = args[i]
		}
		if o.ID == "" {
			die(usage)
		}
		die(h.StoreInstall(o))
	case "update":
		if len(args) < 2 {
			die(usage)
		}
		die(h.StoreUpdate(args[1], len(args) > 2 && args[2] == "--force"))
	case "policy":
		if len(args) != 3 {
			die(usage)
		}
		die(core.StoreSetPolicy(args[1], args[2]))
	case "check":
		die(h.StoreCheck())
	default:
		die(usage)
	}
}
