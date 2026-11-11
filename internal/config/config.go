// Package config reads and edits the KEY=value files in /etc/nook.
package config

import (
	"os"
	"regexp"
	"strconv"
	"strings"
)

const (
	ConfigFile   = "/etc/nook/config"
	DashboardEnv = "/etc/nook/dashboard.env"
	UpdateEnv    = "/etc/nook/update.env"
	GrantsFile   = "/etc/nook/grants"
	PortsFile    = "/etc/nook/ports"
	FirewallFile = "/etc/nook/firewall.nft"
	TokensFile   = "/etc/nook/tokens" // deploy token hashes
	BindingsFile = "/etc/nook/bindings.json"
	StoreFile    = "/etc/nook/store.json" // apps installed from the store
	StateDir     = "/etc/nook/state"
	EnvDir       = "/etc/nook/env"
	SSHKeysDir   = "/etc/ssh/nook-keys" // public keys: readable (sshd reads them as the user), root-writable only
	LogDir       = "/var/log/nook"
	WWWDir       = "/var/lib/nook/www"
	CaddyAppsDir = "/etc/caddy/nook"
	AdminCaddy   = "/etc/caddy/nook-dashboard.caddy"
	Caddyfile    = "/etc/caddy/Caddyfile"
	WebStateDir  = "/var/lib/nook-web"
	HelperSocket = "/run/nook/helper.sock"
	WebSocket    = "/run/nook-web.sock" // dashboard; from nook-web.socket, mode 0660 nook-web:caddy
	LockFile     = "/run/nook-sync.lock"
	AppsDir      = "/srv/nook"
	AppBin       = "/usr/local/bin/nook"
	HostBin      = "/usr/local/sbin/nook-admin"
	WebUser      = "nook-web"
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

// Settings is /etc/nook/config with defaults.
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

// Dashboard is /etc/nook/dashboard.env with defaults.
type Dashboard struct {
	Host, IPPort string
	IPAccess     bool
}

func LoadDashboard() Dashboard {
	m := Read(DashboardEnv)
	d := Dashboard{Host: m["DASHBOARD_HOST"], IPPort: m["DASHBOARD_IP_PORT"], IPAccess: m["DASHBOARD_IP_ACCESS"] != "off"}
	if d.IPPort == "" {
		d.IPPort = "8443"
	}
	return d
}
