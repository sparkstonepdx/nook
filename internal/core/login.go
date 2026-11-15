package core

// An app's login switch: with it on, every site of the app (static or running, PR previews too) is behind
// nook's own login. Dashboard users allowed on the app get in after logging in at the dashboard's address;
// deploy tokens still work for scripts. The switch is a file in the state folder, so it survives deploys.

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/sparkstonepdx/nook/internal/config"
)

func loginPath(app string) string { return filepath.Join(config.StateDir, app+".login") }

// AppLogin reports whether app's sites are behind nook's login.
func AppLogin(app string) bool {
	_, err := os.Stat(loginPath(app))
	return err == nil
}

// SetLogin turns app's login switch on or off and republishes its sites.
func (h *Host) SetLogin(app string, on bool) error {
	if _, err := LookupApp(app); err != nil {
		return err
	}
	if on && config.LoadDashboard().Host == "" {
		return errors.New("set the dashboard's address first (Domains page): people log in there")
	}
	os.MkdirAll(config.StateDir, 0o700)
	if on {
		if err := config.WriteAtomic(loginPath(app), []byte("on\n"), 0o600); err != nil {
			return err
		}
		h.say("%s: sites need a nook login", app)
	} else {
		if err := os.Remove(loginPath(app)); err != nil && !os.IsNotExist(err) {
			return err
		}
		h.say("%s: sites are open again", app)
	}
	return h.Sync(app)
}

// syncLoginApps republishes every app with its login on (their sites send people to the dashboard's address).
func (h *Host) syncLoginApps() {
	for _, a := range Apps() {
		if AppLogin(a) {
			if err := h.Sync(a); err != nil {
				h.say("%s: %v", a, err)
			}
		}
	}
}
