package core

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/sparkstonepdx/nook/internal/config"
)

// A service whose binary was replaced (an update swaps nook-admin by renaming a new file over it) keeps running
// the old code until it restarts. An update restarts the dashboard
// services, but if that ever doesn't take (it didn't on one server, for several releases) the page stays old
// while the footer reads the new version. These find and restart such services.

var dashboardUnits = []string{"nook-web.service", "nook-helper.service"}

// staleUnit reports the unit's main PID and whether that process runs something other than the binary at bin
// now. Stat on /proc/PID/exe reaches the running file even after it's been replaced, so comparing files works on
// any filesystem (the " (deleted)" mark on the link doesn't show on overlayfs).
func staleUnit(unit, bin string) (string, bool) {
	out, err := exec.Command("systemctl", "show", "-p", "MainPID", "--value", unit).Output()
	pid := strings.TrimSpace(string(out))
	if err != nil || pid == "" || pid == "0" {
		return "", false
	}
	running, err1 := os.Stat("/proc/" + pid + "/exe")
	onDisk, err2 := os.Stat(bin)
	return pid, err1 == nil && err2 == nil && !os.SameFile(running, onDisk)
}

// RestartStale restarts the dashboard web process if it runs a replaced binary, waits, and checks it took. With
// helperToo, the helper is restarted the same way; leave it out when called from the helper or its children, since
// restarting it would end the caller.
func RestartStale(out io.Writer, helperToo bool) error {
	for _, unit := range dashboardUnits {
		if unit == "nook-helper.service" && !helperToo {
			continue
		}
		pid, stale := staleUnit(unit, config.HostBin)
		if !stale {
			continue
		}
		fmt.Fprintf(out, "%s (pid %s) is running a replaced binary: restarting it\n", unit, pid)
		if o, err := exec.Command("systemctl", "restart", unit).CombinedOutput(); err != nil {
			return fmt.Errorf("couldn't restart %s: %v %s", unit, err, strings.TrimSpace(string(o)))
		}
		if pid, stale := staleUnit(unit, config.HostBin); stale {
			return fmt.Errorf("%s is still running the old binary (pid %s) after a restart; see journalctl -u %s", unit, pid, unit)
		}
	}
	return nil
}
