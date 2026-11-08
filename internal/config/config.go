// Package config reads and edits the KEY=value files in /etc/apps.
package config

import (
	"os"
	"regexp"
	"strconv"
	"strings"
)

const (
	ConfigFile    = "/etc/apps/config"
	DashboardEnv  = "/etc/apps/dashboard.env"
	UpdateEnv     = "/etc/apps/update.env"
	GrantsFile    = "/etc/apps/grants"
	PortsFile     = "/etc/apps/ports"
	StateDir      = "/etc/apps/state"
	EnvDir        = "/etc/apps/env"
	SSHKeysDir    = "/etc/ssh/app-host-keys" // public keys: readable (sshd reads them as the user), root-writable only
	LogDir        = "/var/log/apps"
	WWWDir        = "/var/lib/app-host/www"
	CaddyAppsDir  = "/etc/caddy/apps"
	AdminCaddy    = "/etc/caddy/admin.caddy"
	Caddyfile     = "/etc/caddy/Caddyfile"
	WebStateDir   = "/var/lib/app-host-web"
	HelperSocket  = "/run/app-host/helper.sock"
	LockFile      = "/run/app-sync.lock"
	AppsDir       = "/srv/apps"
	AppBin        = "/usr/local/bin/app"
	HostBin       = "/usr/local/sbin/app-host"
	WebUser       = "app-host-web"
)

var line = regexp.MustCompile(`^([A-Z_][A-Z0-9_]*)=("?)(.*)$`)

// Read parses KEY=value lines; a missing file is an empty map.
func Read(path string) map[string]string {
	m := map[string]string{}
	b, err := os.ReadFile(path)
	if err != nil {
		return m
	}
	for _, l := range strings.Split(string(b), "\n") {
		if g := line.FindStringSubmatch(l); g != nil {
			v := g[3]
			if g[2] == `"` {
				v = strings.TrimSuffix(v, `"`)
			}
			m[g[1]] = v
		}
	}
	return m
}

// Set replaces KEY=... in path (or appends it), keeping every other line. Mode 0600.
func Set(path, key, value string) error {
	b, _ := os.ReadFile(path)
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	done := false
	for i, l := range lines {
		if strings.HasPrefix(l, key+"=") {
			lines[i], done = key+"="+value, true
		}
	}
	if !done {
		if len(lines) == 1 && lines[0] == "" {
			lines = nil
		}
		lines = append(lines, key+"="+value)
	}
	return WriteAtomic(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600)
}

// WriteAtomic writes via an exclusive temp file and rename (never follows a link at path).
func WriteAtomic(path string, data []byte, mode os.FileMode) error {
	tmp := path + ".tmp-" + strconv.Itoa(os.Getpid())
	os.Remove(tmp)
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

// Settings is /etc/apps/config with defaults.
type Settings struct {
	BaseDomain, SSHHost, MemoryMax, CPUQuota, TasksMax string
	PortMin, PortMax                                   int
	Reserved                                           []string
	SiteMaxBytes                                       int64
}

func Load() Settings {
	m := Read(ConfigFile)
	get := func(k, def string) string {
		if v := m[k]; v != "" {
			return v
		}
		return def
	}
	atoi := func(k string, def int) int {
		if n, err := strconv.Atoi(m[k]); err == nil {
			return n
		}
		return def
	}
	maxBytes, err := strconv.ParseInt(m["SITE_MAX_BYTES"], 10, 64)
	if err != nil || maxBytes <= 0 {
		maxBytes = 1 << 30
	}
	return Settings{
		BaseDomain: get("BASE_DOMAIN", "sparkstonepdx.com"), SSHHost: get("SSH_HOST", ""),
		MemoryMax: get("APP_MEMORY_MAX", "256M"), CPUQuota: get("APP_CPU_QUOTA", "50%"), TasksMax: get("APP_TASKS_MAX", "256"),
		PortMin: atoi("PORT_MIN", 20000), PortMax: atoi("PORT_MAX", 29999),
		Reserved:     strings.Fields(get("RESERVED_SUBDOMAINS", "www mail admin")),
		SiteMaxBytes: maxBytes,
	}
}

// Dashboard is /etc/apps/dashboard.env with defaults.
type Dashboard struct {
	Host, Port, IPPort string
	IPAccess           bool
}

func LoadDashboard() Dashboard {
	m := Read(DashboardEnv)
	d := Dashboard{Host: m["DASHBOARD_HOST"], Port: m["DASHBOARD_PORT"], IPPort: m["DASHBOARD_IP_PORT"], IPAccess: m["DASHBOARD_IP_ACCESS"] != "off"}
	if d.Port == "" {
		d.Port = "19999"
	}
	if d.IPPort == "" {
		d.IPPort = "8443"
	}
	return d
}
