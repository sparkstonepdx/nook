package main

import (
	"fmt"
	"time"

	"github.com/sparkstonepdx/nook/internal/core"
)

// headroomCmd prints how full the server is, what to add, and each app's memory trend.
func headroomCmd(args []string) {
	rng := "24h"
	if len(args) > 0 {
		rng = args[0]
	}
	v := core.Headroom(rng)
	fmt.Println(v.Summary)
	for _, r := range v.Resources {
		fmt.Printf("  %-7s %-6s %-18s %s\n", r.Name, r.Status, r.Figure, r.Detail)
	}
	for _, a := range v.Advice {
		fmt.Printf("  * %s: %s\n", a.Title, a.Detail)
	}
	if len(v.Apps) > 0 {
		fmt.Println("\nApps (memory now, limit, trend since the last restart):")
	}
	for _, a := range v.Apps {
		limit, trend := "server", "steady"
		if a.OwnLimit {
			limit = fmt.Sprintf("%d MB", a.Limit>>20)
		}
		if a.PerDay > 0 {
			trend = fmt.Sprintf("+%.0f MB/day", a.PerDay/(1<<20))
		}
		if a.FullAt > 0 {
			trend += ", full " + time.Unix(a.FullAt, 0).Format("Mon Jan 2")
		}
		fmt.Printf("  %-16s %6d MB  %-8s %5.1f%% CPU  %s\n", a.App, a.Mem>>20, limit, a.CPU, trend)
	}
}

// directoryCmd lists the sites and which are in the public directory, or adds or removes one.
func directoryCmd(h *core.Host, args []string) {
	switch {
	case len(args) == 2 && (args[0] == "add" || args[0] == "rm"):
		die(h.SetListed(args[1], args[0] == "add"))
	case len(args) == 0:
		for _, e := range core.Directory() {
			mark := " "
			if e.Listed {
				mark = "*"
			}
			fmt.Printf("%s %-40s %s\n", mark, e.Host, e.App)
		}
		fmt.Println("(* listed in the directory)")
	default:
		die(fmt.Errorf("usage: nook-admin directory [add|rm HOST]"))
	}
}
