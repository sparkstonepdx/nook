package core

// Health checks. A web site or service names a path in its nook.json ("health": "/healthz"); sync records
// it in the site's unit file (X-Nook-Health=), and nook asks for it: after a deploy, on the dashboard, and
// after a store install or update. Anything under 400 is healthy.

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sparkstonepdx/nook/internal/config"
	"github.com/sparkstonepdx/nook/internal/names"
)

// SiteHealthPath is the health path a running site declared ("" if none).
func SiteHealthPath(app, site string) string {
	b, err := os.ReadFile(filepath.Join("/etc/systemd/system", UnitName(app, site)))
	if err != nil {
		return ""
	}
	for _, l := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(l, "X-Nook-Health="); ok {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// probeHealth asks app's site for path once, as Caddy would: on its port, with its hostname for a web
// site (apps that route by hostname need it).
func probeHealth(app, site, path string, web bool, timeout time.Duration) error {
	port := PortOf(app, site)
	if port == 0 {
		return fmt.Errorf("no port yet")
	}
	req, _ := http.NewRequest("GET", fmt.Sprintf("http://127.0.0.1:%d%s", port, path), nil)
	if web {
		req.Host = names.HostOf(site, config.Load().BaseDomain)
		req.Header.Set("X-Forwarded-Proto", "https")
	}
	c := &http.Client{Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := c.Do(req)
	if err != nil {
		if strings.Contains(err.Error(), "connection refused") {
			return fmt.Errorf("nothing is listening on its port")
		}
		if strings.Contains(err.Error(), "Timeout") || strings.Contains(err.Error(), "deadline") {
			return fmt.Errorf("%s didn't answer within %s", path, timeout)
		}
		return err
	}
	resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("%s answered %s", path, resp.Status)
	}
	return nil
}

// waitHealthy asks every 2 s until the site answers healthy or wait runs out; it returns the last problem.
func waitHealthy(app, site, path string, web bool, wait time.Duration) error {
	deadline := time.Now().Add(wait)
	var err error
	for {
		time.Sleep(2 * time.Second)
		if err = probeHealth(app, site, path, web, 3*time.Second); err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return err
		}
	}
}
