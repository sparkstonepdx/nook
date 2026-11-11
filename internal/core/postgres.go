package core

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/sparkstonepdx/nook/internal/config"
)

// The built-in Postgres provider: the distro's PostgreSQL, listening on its unix socket only, with peer
// auth. App "blog" runs as Linux user nook-blog, which Postgres maps to role nook-blog, which may only open
// database nook-blog. No password exists anywhere.

func psql(sql string) (string, error) {
	out, err := exec.Command("runuser", "-u", "postgres", "--", "psql", "-XqAt", "-v", "ON_ERROR_STOP=1", "-c", sql).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func postgresReady() (bool, string) {
	if _, err := exec.LookPath("psql"); err != nil {
		return false, "not installed: nook-admin services install postgres"
	}
	if out, err := psql("select 1"); err != nil || out != "1" {
		return false, "installed but not answering: " + lastLines(out, 1)
	}
	return true, ""
}

func pgIdent(app string) string { return `"nook-` + app + `"` } // app names are [a-z][a-z0-9-]*

// postgresProvision makes the app's role and database if they're missing.
func postgresProvision(app string) error {
	if ok, why := postgresReady(); !ok {
		return fmt.Errorf("postgres: %s", why)
	}
	role := "nook-" + app
	if _, err := psql(fmt.Sprintf(`DO $$ BEGIN IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = '%s') THEN CREATE ROLE %s LOGIN; END IF; END $$`, role, pgIdent(app))); err != nil {
		return fmt.Errorf("postgres role: %v", err)
	}
	has, err := psql(fmt.Sprintf(`SELECT 1 FROM pg_database WHERE datname = '%s'`, role))
	if err != nil {
		return fmt.Errorf("postgres: %v", err)
	}
	if has != "1" {
		if out, err := psql(fmt.Sprintf(`CREATE DATABASE %s OWNER %s`, pgIdent(app), pgIdent(app))); err != nil {
			return fmt.Errorf("postgres database: %s", out)
		}
	}
	if out, err := psql(fmt.Sprintf(`REVOKE ALL ON DATABASE %s FROM PUBLIC`, pgIdent(app))); err != nil {
		return fmt.Errorf("postgres: %s", out)
	}
	return nil
}

// PostgresDropDB deletes an app's database and role. Only by explicit request: unbinding keeps them.
func PostgresDropDB(app string) error {
	for _, b := range readBindings() {
		if b.App == app && b.Provider == "postgres" {
			return fmt.Errorf("%s is still bound to postgres; unbind it first", app)
		}
	}
	if out, err := psql(fmt.Sprintf(`DROP DATABASE IF EXISTS %s WITH (FORCE)`, pgIdent(app))); err != nil {
		return fmt.Errorf("%s", out)
	}
	if out, err := psql(fmt.Sprintf(`DROP ROLE IF EXISTS %s`, pgIdent(app))); err != nil {
		return fmt.Errorf("%s", out)
	}
	return nil
}

const pgHBA = `# managed by nook-admin (the original is kept next to this file as pg_hba.conf.before-nook)
# unix socket only; every role is the Linux user of the same name, and may open only its own database
local   all        postgres   peer
local   sameuser   all        peer
`

const pgConf = `# managed by nook-admin
listen_addresses = ''          # unix socket only: no TCP at all
shared_buffers = 64MB          # small box
max_connections = 50
`

// PostgresInstall installs the distro's PostgreSQL and locks it down for nook.
func PostgresInstall(h *Host) error {
	if _, err := exec.LookPath("pg_ctlcluster"); err != nil || !fileExists("/usr/lib/postgresql") {
		h.say("installing postgresql (apt)")
		if out, err := Run("apt-get", "install", "-y", "-qq", "postgresql"); err != nil {
			return fmt.Errorf("apt-get: %s", lastLines(out, 5))
		}
	}
	confs, _ := filepath.Glob("/etc/postgresql/*/main")
	if len(confs) == 0 {
		return fmt.Errorf("no cluster found under /etc/postgresql")
	}
	dir := confs[len(confs)-1]
	hba := filepath.Join(dir, "pg_hba.conf")
	if b, err := os.ReadFile(hba); err == nil && !strings.Contains(string(b), "managed by nook-admin") {
		os.WriteFile(hba+".before-nook", b, 0o640)
	}
	if err := config.WriteAtomic(hba, []byte(pgHBA), 0o640); err != nil {
		return err
	}
	os.MkdirAll(filepath.Join(dir, "conf.d"), 0o755)
	if err := config.WriteAtomic(filepath.Join(dir, "conf.d", "nook.conf"), []byte(pgConf), 0o644); err != nil {
		return err
	}
	exec.Command("chown", "postgres:postgres", hba, filepath.Join(dir, "conf.d", "nook.conf")).Run()
	version := filepath.Base(filepath.Dir(dir))
	h.say("restarting postgresql %s", version)
	if systemctl("restart", "postgresql") != nil {
		exec.Command("pg_ctlcluster", version, "main", "restart").Run()
	}
	if ok, why := postgresReady(); !ok {
		return fmt.Errorf("postgres didn't come up: %s", why)
	}
	h.say("postgres ready: unix socket only, peer auth, one database per app")
	return nil
}
