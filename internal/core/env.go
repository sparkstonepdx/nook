package core

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/sparkstonepdx/nook/internal/config"
	"github.com/sparkstonepdx/nook/internal/envfile"
)

func envPath(app string) string { return filepath.Join(config.EnvDir, app+".env") }

// EnvList returns the values set with nook-admin env (root-owned /etc/nook/env/<app>.env).
func EnvList(app string) (*envfile.Vars, error) {
	if _, err := LookupApp(app); err != nil {
		return nil, err
	}
	b, _ := os.ReadFile(envPath(app))
	return envfile.Parse(string(b)), nil
}

// EnvApply sets and unsets values, then restarts the app's running sites.
func (h *Host) EnvApply(app string, set map[string]string, unset []string, restart bool) error {
	v, err := EnvList(app)
	if err != nil {
		return err
	}
	for k, val := range set {
		if err := envfile.Check(k, val); err != nil {
			return err
		}
	}
	for _, k := range unset {
		if err := envfile.Check(k, ""); err != nil {
			return err
		}
	}
	for k, val := range set {
		v.Set(k, val)
	}
	for _, k := range unset {
		v.Unset(k)
	}
	os.MkdirAll(config.EnvDir, 0o700)
	if err := config.WriteAtomic(envPath(app), []byte(envfile.Format(v, "managed by nook-admin env; loaded into every running site of this app after its own secrets.env")), 0o600); err != nil {
		return err
	}
	var changed []string
	for k := range set {
		changed = append(changed, "set "+k)
	}
	for _, k := range unset {
		changed = append(changed, "unset "+k)
	}
	h.say("%s", strings.Join(changed, ", "))
	if !restart {
		return nil
	}
	var done []string
	for _, s := range RunningSites(app) {
		if systemctl("restart", UnitName(app, s)) == nil {
			done = append(done, s)
		}
	}
	if len(done) > 0 {
		h.say("restarted: %s", strings.Join(done, ", "))
	} else {
		h.say("no running sites to restart")
	}
	return nil
}
