package main

import (
	"fmt"
	"github.com/sparkstonepdx/nook/internal/core"
	"strings"
)

func bindCmd(h *core.Host, args []string) {
	usage := fmt.Errorf("usage: nook-admin bind APP SERVICE [--provider ID] [--as NAME=NEWNAME ...]")
	var pos []string
	provider := ""
	as := map[string]string{}
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--provider" && i+1 < len(args):
			provider = args[i+1]
			i++
		case args[i] == "--as" && i+1 < len(args):
			from, to, ok := strings.Cut(args[i+1], "=")
			if !ok {
				die(usage)
			}
			as[from] = to
			i++
		default:
			pos = append(pos, args[i])
		}
	}
	if len(pos) != 2 {
		die(usage)
	}
	die(h.Bind(pos[0], pos[1], provider, as))
}

func servicesCmd(h *core.Host, args []string) {
	if len(args) == 0 || args[0] == "list" {
		v := core.ServicesOverview()
		fmt.Println("Providers:")
		for _, p := range v.Providers {
			state := "ready"
			if !p.Ready {
				state = p.Note
			}
			fmt.Printf("  %-12s %-28s %s\n", p.Interface, p.ID, state)
		}
		fmt.Println("Bindings:")
		for _, b := range v.Bindings {
			fmt.Printf("  %-16s %-12s from %-28s env %s\n", b.App, b.Interface, b.Provider, strings.Join(b.Env, " "))
		}
		if len(v.Requests) > 0 {
			fmt.Println("Waiting (asked for in nook.json, not bound):")
			for _, r := range v.Requests {
				fmt.Printf("  %-16s %-12s (sites: %s)\n", r.App, r.Interface, strings.Join(r.Sites, ", "))
			}
		}
		return
	}
	switch {
	default:
		die(fmt.Errorf("usage: nook-admin services [list]"))
	}
}
