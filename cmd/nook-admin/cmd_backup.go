package main

import (
	"bufio"
	"cmp"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/sparkstonepdx/nook/internal/core"
)

// nook-admin backup ...: see core/backup.go.
func backupCmd(h *core.Host, args []string) {
	sub := "status"
	if len(args) > 0 {
		sub, args = args[0], args[1:]
	}
	flag := func(name string) string {
		for i, a := range args {
			if a == name && i+1 < len(args) {
				return args[i+1]
			}
		}
		return ""
	}
	switch sub {
	case "status":
		s := core.GetBackupStatus()
		if !s.Configured {
			fmt.Println("Backups aren't set up. nook-admin backup setup --repo s3:https://ENDPOINT/BUCKET/nook --key KEY_ID (the secret on stdin)")
			return
		}
		fmt.Printf("to %s daily at %s, keeping %s (daily,weekly,monthly); this server is %s\n", s.Repo, s.Time, s.Keep, s.ServerID)
		switch {
		case s.Running:
			fmt.Printf("running since %s\n", time.Unix(s.Started, 0).Format(time.DateTime))
		case s.Finished > 0 && s.OK:
			fmt.Printf("last: %s, ok\n", time.Unix(s.Finished, 0).Format(time.DateTime))
		case s.Finished > 0:
			fmt.Printf("last: %s, failed: %s\n", time.Unix(s.Finished, 0).Format(time.DateTime), s.Error)
		}
		for _, a := range s.Apps {
			fmt.Printf("  %-16s %-10s %s\n", a.App, a.Snapshot[:min(8, len(a.Snapshot))], a.Error)
		}
		if len(s.Skip) > 0 {
			fmt.Printf("left out: %s\n", strings.Join(s.Skip, ", "))
		}
	case "setup":
		// the secret and an existing repo's password come on stdin (secret first), never on the command line
		in := bufio.NewScanner(os.Stdin)
		read := func() string {
			if in.Scan() {
				return strings.TrimSpace(in.Text())
			}
			return ""
		}
		s := core.BackupSetup{Repo: flag("--repo"), KeyID: flag("--key"), Time: flag("--time"), Keep: flag("--keep")}
		if strings.HasPrefix(s.Repo, "s3:") {
			s.Secret = read()
		}
		if slices.Contains(args, "--existing") {
			s.Password = read()
		}
		pw, err := h.SetBackup(s)
		die(err)
		if pw != "" {
			fmt.Printf("\nThe backups' password, shown once. Keep it somewhere other than this server: without it\nnobody can read the backups, you included.\n\n  %s\n\n", pw)
		}
	case "run":
		die(h.BackupRun(cmp.Or(flag("--reason"), "by hand")))
	case "snapshots":
		app := ""
		if len(args) > 0 && !strings.HasPrefix(args[0], "--") {
			app = args[0]
		}
		snaps, err := core.BackupSnapshots(app, flag("--from"))
		die(err)
		for _, s := range snaps {
			fmt.Printf("%s  %s  %s\n", s.Short, s.Time.Local().Format(time.DateTime), strings.Join(s.Tags, ","))
		}
	case "restore":
		if len(args) < 1 {
			die(fmt.Errorf("usage: nook-admin backup restore APP [SNAPSHOT]"))
		}
		snap := "latest"
		if len(args) > 1 {
			snap = args[1]
		}
		die(h.BackupRestoreApp(args[0], snap))
	case "skip":
		if len(args) != 2 || (args[1] != "on" && args[1] != "off") {
			die(fmt.Errorf("usage: nook-admin backup skip APP on|off"))
		}
		die(h.SetBackupSkip(args[0], args[1] == "on"))
	case "json":
		b, _ := json.MarshalIndent(core.GetBackupStatus(), "", "  ")
		fmt.Println(string(b))
	default:
		die(fmt.Errorf("usage: nook-admin backup [status|setup|run|snapshots|restore|skip]"))
	}
}
