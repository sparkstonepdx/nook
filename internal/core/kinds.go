package core

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/sparkstonepdx/nook/internal/config"
	"github.com/sparkstonepdx/nook/internal/schedule"
)

// A site folder with an executable run is one of:
//
//	web      (default) a long-running HTTP server on $PORT, served at the site's hostname
//	service  long-running on $PORT with no hostname and no route: reachable only by what's allowed to
//	         reach it (bound apps, #12)
//	cron     run on a schedule (systemd timer); no port, no hostname
//
// chosen by sites/SITE/nook.json: {"kind": "cron", "schedule": "*/15 * * * *", "timeout": 600}.
//
// A static site (no run) reads nook.json too, for {"cors": true} only.

type Manifest struct {
	Kind     string                 `json:"kind"`
	Schedule string                 `json:"schedule,omitempty"`
	Timeout  int                    `json:"timeout,omitempty"`  // cron: seconds before a run is killed (default 3600, max 86400)
	Needs    []string               `json:"needs,omitempty"`    // services this site wants bound (#12)
	Provides map[string]ProvideSpec `json:"provides,omitempty"` // services: interfaces it implements
	Health   string                 `json:"health,omitempty"`   // web and service: a path that answers under 400 when it's working
	CORS     bool                   `json:"cors,omitempty"`     // static: any origin may read its files (Access-Control-Allow-Origin: *)
	calendar string
}

// readManifest reads sites/SITE/nook.json as the app user. No file means a web site.
func (u *AppUser) readManifest(site string) (Manifest, error) {
	m := Manifest{Kind: "web"}
	b, err := u.AsApp(16<<10, "internal-read-manifest", site)
	if err != nil {
		return m, fmt.Errorf("reading nook.json: %v", err)
	}
	if len(strings.TrimSpace(string(b))) == 0 {
		return m, nil
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return m, fmt.Errorf("nook.json: %v", err)
	}
	if err := checkServices(&m); err != nil {
		return m, err
	}
	if m.Health != "" && !healthPathRE.MatchString(m.Health) {
		return m, fmt.Errorf("nook.json: health must be a path like /healthz, not %q", m.Health)
	}
	switch m.Kind {
	case "", "web":
		m.Kind = "web"
	case "service":
	case "cron":
		cal, err := schedule.OnCalendar(m.Schedule)
		if err != nil {
			return m, err
		}
		if out, err := Run("systemd-analyze", "calendar", cal); err != nil && !strings.Contains(out, "not found") && !strings.Contains(err.Error(), "executable file not found") {
			return m, fmt.Errorf("schedule %q: %s", m.Schedule, lastLines(out, 1))
		}
		m.calendar = cal
		if m.Timeout <= 0 {
			m.Timeout = 3600
		}
		if m.Timeout > 86400 {
			m.Timeout = 86400
		}
	default:
		return m, fmt.Errorf("nook.json: kind must be web, service or cron, not %q", m.Kind)
	}
	return m, nil
}

var healthPathRE = regexp.MustCompile(`^/[A-Za-z0-9._~!$&'()*+,;=:@%/?-]{0,199}$`)

func CronUnit(app, site string) string { return "nook-cron-" + app + "--" + site }

// writeCron writes the job's service and timer. Returns true when either changed.
func (h *Host) writeCron(u *AppUser, site string, m Manifest) (bool, error) {
	app := u.Name
	base := filepath.Join("/etc/systemd/system", CronUnit(app, site))
	log := filepath.Join(config.LogDir, app, site+".log")
	svc := fmt.Sprintf(`[Unit]
Description=%[2]s (cron job of app %[1]s)
After=nook-firewall.service

[Service]
Type=oneshot
User=nook-%[1]s
Group=nook-%[1]s
Slice=nook-%[1]s.slice
WorkingDirectory=%[3]s/sites/%[2]s
ExecStart=%[3]s/sites/%[2]s/run
Environment=HOST=127.0.0.1 SITE=%[2]s
%[4]sTimeoutStartSec=%[5]d
StandardOutput=append:%[6]s
StandardError=append:%[6]s
LimitFSIZE=2G
%[7]s`, app, site, u.Home, envLines(app, site), m.Timeout, log, sandbox(u.Home, app, "SocketBindDeny=any\n"))
	timer := fmt.Sprintf(`[Unit]
Description=Schedule for %[2]s (cron job of app %[1]s)

[Timer]
OnCalendar=%[3]s
Persistent=true
AccuracySec=1s

[Install]
WantedBy=timers.target
`, app, site, m.calendar)
	a, err := writeIfChanged(base+".service", svc)
	if err != nil {
		return false, err
	}
	b, err := writeIfChanged(base+".timer", timer)
	return a || b, err
}

func writeIfChanged(path, text string) (bool, error) {
	if old, err := os.ReadFile(path); err == nil && string(old) == text {
		return false, nil
	}
	return true, config.WriteAtomic(path, []byte(text), 0o644)
}

func (h *Host) stopCron(app, site string) {
	systemctl("disable", "--now", CronUnit(app, site)+".timer")
	systemctl("stop", CronUnit(app, site)+".service")
	os.Remove(filepath.Join("/etc/systemd/system", CronUnit(app, site)+".timer"))
	os.Remove(filepath.Join("/etc/systemd/system", CronUnit(app, site)+".service"))
	dropSiteEnv(app, site)
}

// CronJobs lists the app's cron jobs (by their timer files).
func CronJobs(app string) []string {
	files, _ := filepath.Glob(filepath.Join("/etc/systemd/system", "nook-cron-"+app+"--*.timer"))
	var out []string
	for _, f := range files {
		out = append(out, strings.TrimSuffix(strings.TrimPrefix(filepath.Base(f), "nook-cron-"+app+"--"), ".timer"))
	}
	return out
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }
