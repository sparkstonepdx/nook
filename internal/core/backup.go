package core

// Backups (#68): restic snapshots to an S3-compatible bucket (R2, B2, any S3), daily and before each nook update.
//
//	the server   /etc/nook, the dashboard's users, nook's state, Caddy's certificates, deploy keys, app limits,
//	             and which uid each app had (one snapshot, tags nook + server)
//	each app     /srv/nook/APP, less builds/ and the folders its nook.json skips (one snapshot each, tags nook +
//	             app:APP). A site's {"backup": {"run": "./backup.sh"}} writes a consistent dump first; an app
//	             with no hook is frozen (its cgroup paused, nothing restarts) while it's read.
//
// Every snapshot carries the server's id as its restic host, so servers can share a bucket: each forgets and
// prunes only its own, and an app's snapshot from one server can be restored on another (adopting, moving).
//
// Settings are /etc/nook/backup.env (0600): BACKUP_REPO (s3:https://ENDPOINT/BUCKET/PATH), AWS_ACCESS_KEY_ID,
// AWS_SECRET_ACCESS_KEY, RESTIC_PASSWORD (shown once at setup: keep it elsewhere too, it's the only way to read
// the backups), BACKUP_TIME (HH:MM), BACKUP_KEEP (daily,weekly,monthly). Apps left out are in backup.json.

import (
	"bufio"
	"bytes"
	"cmp"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/sparkstonepdx/nook/internal/config"
	"github.com/sparkstonepdx/nook/internal/envfile"
)

var (
	backupEnv    = "/etc/nook/backup.env"
	backupJSON   = "/etc/nook/backup.json"   // {"skip": ["app", ...]}
	serverIDFile = "/etc/nook/server-id"     // this server's id: the restic host on its snapshots
	backupDir    = config.LibDir + "/backup" // status.json, users.json, the lock
	resticBin    = "/usr/local/lib/nook/restic"
)

var (
	backupTimeRE = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d$`)
	backupKeepRE = regexp.MustCompile(`^\d{1,3},\d{1,3},\d{1,3}$`)
	backupRepoRE = regexp.MustCompile(`^(s3:https://[^\s]+|sftp:[^\s]+|rest:https://[^\s]+|/[^\s]*)$`)
	skipPathRE   = regexp.MustCompile(`^[A-Za-z0-9._-][A-Za-z0-9._/-]{0,200}$`)
)

// BackupSpec is a site's nook.json "backup": a script that writes a consistent dump into data/ before the app is
// read (sqlite3 .backup, pg_dump), and folders under the app's home to leave out (caches, things it rebuilds).
type BackupSpec struct {
	Run  string   `json:"run,omitempty"`
	Skip []string `json:"skip,omitempty"`
}

type BackupConfig struct {
	Repo     string   `json:"repo"`
	KeyID    string   `json:"keyId,omitempty"`
	Time     string   `json:"time"` // HH:MM, server time
	Keep     string   `json:"keep"` // daily,weekly,monthly
	ServerID string   `json:"serverId"`
	Skip     []string `json:"skip"` // apps left out
	password string
	secret   string
}

func (c BackupConfig) Configured() bool { return c.Repo != "" && c.password != "" }

func LoadBackupConfig() BackupConfig {
	m := config.Read(backupEnv)
	c := BackupConfig{Repo: m["BACKUP_REPO"], KeyID: m["AWS_ACCESS_KEY_ID"], secret: m["AWS_SECRET_ACCESS_KEY"], password: m["RESTIC_PASSWORD"],
		Time: m["BACKUP_TIME"], Keep: m["BACKUP_KEEP"], ServerID: serverID(), Skip: []string{}}
	if !backupTimeRE.MatchString(c.Time) {
		c.Time = "03:17"
	}
	if !backupKeepRE.MatchString(c.Keep) {
		c.Keep = "7,4,6"
	}
	if b, err := os.ReadFile(backupJSON); err == nil {
		var j struct {
			Skip []string `json:"skip"`
		}
		if json.Unmarshal(b, &j) == nil && j.Skip != nil {
			c.Skip = j.Skip
		}
	}
	return c
}

// serverID is this server's stable id, made once: its restic host, so servers can share a bucket.
func serverID() string {
	if b, err := os.ReadFile(serverIDFile); err == nil {
		if id := strings.TrimSpace(string(b)); id != "" {
			return id
		}
	}
	h, _ := os.Hostname()
	h = strings.ToLower(regexp.MustCompile(`[^a-zA-Z0-9-]`).ReplaceAllString(h, "-"))
	b := make([]byte, 3)
	rand.Read(b)
	id := strings.Trim(h, "-") + "-" + fmt.Sprintf("%x", b)
	os.MkdirAll(filepath.Dir(serverIDFile), 0o755)
	config.WriteAtomic(serverIDFile, []byte(id+"\n"), 0o644)
	return id
}

// resticCmd runs restic against the configured repo. Secrets go in its environment, never on its command line.
var resticCmd = func(c BackupConfig, args ...string) *exec.Cmd {
	cmd := exec.Command(resticBin, args...)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=/root", "RESTIC_REPOSITORY=" + c.Repo, "RESTIC_PASSWORD=" + c.password,
		"AWS_ACCESS_KEY_ID=" + c.KeyID, "AWS_SECRET_ACCESS_KEY=" + c.secret, "RESTIC_CACHE_DIR=" + filepath.Join(backupDir, "cache")}
	return cmd
}

func restic(c BackupConfig, args ...string) ([]byte, error) {
	var stderr strings.Builder
	cmd := resticCmd(c, args...)
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if i := strings.LastIndexByte(msg, '\n'); i >= 0 {
			msg = msg[i+1:]
		}
		return out, fmt.Errorf("restic %s: %s", args[0], cmp.Or(msg, err.Error()))
	}
	return out, nil
}

// ---- setup ----

type BackupSetup struct {
	Repo, KeyID, Secret, Time, Keep string
	Password                        string // to connect to an existing repo (a restore, a second server); empty makes a new one
}

// SetBackup saves the destination and makes (or opens) the repo. It returns the password when it made one: shown
// once, so it can be kept somewhere other than this server.
func (h *Host) SetBackup(s BackupSetup) (string, error) {
	s.Repo = strings.TrimSpace(s.Repo)
	if !backupRepoRE.MatchString(s.Repo) {
		return "", errors.New("the destination looks like s3:https://ACCOUNT.r2.cloudflarestorage.com/BUCKET/nook")
	}
	if strings.HasPrefix(s.Repo, "s3:") && (s.KeyID == "" || s.Secret == "") {
		return "", errors.New("an S3 bucket needs its access key id and secret")
	}
	if s.Time != "" && !backupTimeRE.MatchString(s.Time) {
		return "", errors.New("the time is HH:MM, like 03:17")
	}
	if s.Keep != "" && !backupKeepRE.MatchString(s.Keep) {
		return "", errors.New("keep is daily,weekly,monthly, like 7,4,6")
	}
	if err := EnsureRestic(h.Out); err != nil {
		return "", err
	}
	made := ""
	if s.Password == "" {
		b := make([]byte, 24)
		rand.Read(b)
		s.Password = base64.RawURLEncoding.EncodeToString(b)
		made = s.Password
	}
	c := LoadBackupConfig()
	c.Repo, c.KeyID, c.secret, c.password = s.Repo, s.KeyID, s.Secret, s.Password
	if s.Time != "" {
		c.Time = s.Time
	}
	if s.Keep != "" {
		c.Keep = s.Keep
	}
	if made != "" {
		h.say("making a new, encrypted repository at %s", c.Repo)
		if _, err := restic(c, "init"); err != nil {
			if strings.Contains(err.Error(), "already") {
				return "", errors.New("there's already a backup repository there: give its password to use it")
			}
			return "", err
		}
	} else if _, err := restic(c, "cat", "config"); err != nil {
		return "", fmt.Errorf("couldn't open the repository with that password: %v", err)
	}
	lines := fmt.Sprintf("BACKUP_REPO=%s\nAWS_ACCESS_KEY_ID=%s\nAWS_SECRET_ACCESS_KEY=%s\nRESTIC_PASSWORD=%s\nBACKUP_TIME=%s\nBACKUP_KEEP=%s\n",
		c.Repo, c.KeyID, c.secret, c.password, c.Time, c.Keep)
	if err := config.WriteAtomic(backupEnv, []byte(lines), 0o600); err != nil {
		return "", err
	}
	if err := writeBackupTimer(c.Time); err != nil {
		return made, err
	}
	h.say("backups go to %s daily at %s (server %s)", c.Repo, c.Time, c.ServerID)
	return made, nil
}

// SetBackupSkip leaves app out of backups, or puts it back.
func (h *Host) SetBackupSkip(app string, skip bool) error {
	if _, err := LookupApp(app); err != nil {
		return err
	}
	c := LoadBackupConfig()
	c.Skip = slices.DeleteFunc(c.Skip, func(a string) bool { return a == app })
	if skip {
		c.Skip = append(c.Skip, app)
	}
	slices.Sort(c.Skip)
	b, _ := json.MarshalIndent(map[string]any{"skip": c.Skip}, "", "  ")
	if err := config.WriteAtomic(backupJSON, append(b, '\n'), 0o644); err != nil {
		return err
	}
	h.say("%s: %s", app, map[bool]string{true: "left out of backups", false: "backed up"}[skip])
	return nil
}

// backupUnitDir and backupSystemctl are vars for tests.
var (
	backupUnitDir   = "/etc/systemd/system"
	backupSystemctl = systemctl
)

func writeBackupTimer(at string) error {
	timer := fmt.Sprintf("[Unit]\nDescription=nook backups, daily\n\n[Timer]\nOnCalendar=*-*-* %s:00\nRandomizedDelaySec=10m\nPersistent=true\n\n[Install]\nWantedBy=timers.target\n", at)
	unit := "[Unit]\nDescription=nook backup\nAfter=network-online.target\nWants=network-online.target\n\n[Service]\nType=oneshot\nExecStart=/usr/local/sbin/nook-admin backup run --reason scheduled\nNice=10\nIOSchedulingClass=idle\n"
	if err := config.WriteAtomic(filepath.Join(backupUnitDir, "nook-backup.timer"), []byte(timer), 0o644); err != nil {
		return err
	}
	if err := config.WriteAtomic(filepath.Join(backupUnitDir, "nook-backup.service"), []byte(unit), 0o644); err != nil {
		return err
	}
	backupSystemctl("daemon-reload")
	return backupSystemctl("enable", "--now", "nook-backup.timer")
}

// ---- running ----

type AppBackup struct {
	App      string `json:"app"`
	Snapshot string `json:"snapshot,omitempty"`
	Added    uint64 `json:"added"` // new data this run, after dedup and before compression
	Size     uint64 `json:"size"`  // everything it read
	Files    uint64 `json:"files"`
	Frozen   bool   `json:"frozen,omitempty"` // paused while read (no backup hook)
	Hooks    int    `json:"hooks,omitempty"`  // backup hooks that ran
	Error    string `json:"error,omitempty"`
}

type BackupStatus struct {
	Configured bool        `json:"configured"`
	Repo       string      `json:"repo,omitempty"`
	ServerID   string      `json:"serverId"`
	Time       string      `json:"time"`
	Keep       string      `json:"keep"`
	Skip       []string    `json:"skip"`
	Running    bool        `json:"running"`
	Started    int64       `json:"started,omitempty"`
	Finished   int64       `json:"finished,omitempty"`
	Reason     string      `json:"reason,omitempty"` // scheduled, update, by hand
	OK         bool        `json:"ok"`
	Error      string      `json:"error,omitempty"`
	Server     *AppBackup  `json:"server,omitempty"`
	Apps       []AppBackup `json:"apps"`
	Checked    int64       `json:"checked,omitempty"` // last restic check
	CheckError string      `json:"checkError,omitempty"`
	LastOK     int64       `json:"lastOk,omitempty"`
}

func statusPath() string { return filepath.Join(backupDir, "status.json") }

func readBackupStatus() BackupStatus {
	var s BackupStatus
	if b, err := os.ReadFile(statusPath()); err == nil {
		json.Unmarshal(b, &s)
	}
	return s
}

func writeBackupStatus(s BackupStatus) {
	os.MkdirAll(backupDir, 0o700)
	b, _ := json.MarshalIndent(s, "", "  ")
	config.WriteAtomic(statusPath(), b, 0o644)
}

// GetBackupStatus is the last run and the settings, without secrets.
func GetBackupStatus() BackupStatus {
	c := LoadBackupConfig()
	s := readBackupStatus()
	s.Configured, s.Repo, s.ServerID, s.Time, s.Keep, s.Skip = c.Configured(), c.Repo, c.ServerID, c.Time, c.Keep, c.Skip
	if s.Apps == nil {
		s.Apps = []AppBackup{}
	}
	if s.Running && !backupLocked() { // the run died without saying so
		s.Running, s.OK, s.Error = false, false, "the backup stopped without finishing"
	}
	return s
}

func lockPath() string { return filepath.Join(backupDir, "lock") }

func backupLocked() bool {
	f, err := os.OpenFile(lockPath(), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return false
	}
	defer f.Close()
	if syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		return true
	}
	syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return false
}

// resticSummary is the last line of `restic backup --json`.
type resticSummary struct {
	MessageType string `json:"message_type"`
	SnapshotID  string `json:"snapshot_id"`
	DataAdded   uint64 `json:"data_added"`
	TotalBytes  uint64 `json:"total_bytes_processed"`
	TotalFiles  uint64 `json:"total_files_processed"`
}

func backupPaths(c BackupConfig, tags []string, paths, excludes []string) (AppBackup, error) {
	args := []string{"backup", "--json", "--host", c.ServerID, "--one-file-system"}
	for _, t := range tags {
		args = append(args, "--tag", t)
	}
	for _, e := range excludes {
		args = append(args, "--exclude", e)
	}
	var have []string
	for _, p := range paths {
		if _, err := os.Lstat(p); err == nil {
			have = append(have, p)
		}
	}
	if len(have) == 0 {
		return AppBackup{}, errors.New("nothing to back up")
	}
	out, err := restic(c, append(args, have...)...)
	var r AppBackup
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		var s resticSummary
		if json.Unmarshal(sc.Bytes(), &s) == nil && s.MessageType == "summary" {
			r.Snapshot, r.Added, r.Size, r.Files = s.SnapshotID, s.DataAdded, s.TotalBytes, s.TotalFiles
		}
	}
	if err != nil && r.Snapshot != "" { // exit 3: some files couldn't be read, but the snapshot is there
		r.Error = err.Error()
		return r, nil
	}
	return r, err
}

// serverPaths is what the server snapshot holds; missing ones are skipped.
var serverPaths = func() []string {
	slices, _ := filepath.Glob("/etc/systemd/system/nook-*.slice")
	return append([]string{"/etc/nook", config.WebStateDir, config.LibDir, "/var/lib/caddy/.local/share/caddy", "/etc/ssh/nook-keys"}, slices...)
}

// writeUsers records each app's uid and gid, so a restore on a fresh server gives files back to the right owner.
func writeUsers() error {
	m := map[string]map[string]int{}
	for _, a := range Apps() {
		if u, err := LookupApp(a); err == nil {
			m[a] = map[string]int{"uid": u.UID, "gid": u.GID}
		}
	}
	b, _ := json.MarshalIndent(m, "", "  ")
	return config.WriteAtomic(filepath.Join(backupDir, "users.json"), b, 0o600)
}

// BackupRun takes the server snapshot and one per app, forgets old ones and, weekly, checks a sample.
func (h *Host) BackupRun(reason string) (err error) {
	c := LoadBackupConfig()
	if !c.Configured() {
		return errors.New("backups aren't set up yet")
	}
	os.MkdirAll(backupDir, 0o700)
	lock, err := os.OpenFile(lockPath(), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		return errors.New("a backup is already running")
	}
	if err := EnsureRestic(h.Out); err != nil {
		return err
	}
	prev := readBackupStatus()
	st := BackupStatus{Running: true, Started: time.Now().Unix(), Reason: reason, Apps: []AppBackup{}, Checked: prev.Checked, CheckError: prev.CheckError, LastOK: prev.LastOK}
	writeBackupStatus(st)
	defer func() {
		st.Running, st.Finished = false, time.Now().Unix()
		st.OK = err == nil
		if err != nil {
			st.Error = err.Error()
			backupNotify("nook: backup failed", err.Error())
		} else {
			st.LastOK = st.Finished
		}
		writeBackupStatus(st)
	}()

	// unlocked restic locks from a run that was killed
	restic(c, "unlock")

	writeUsers()
	h.say("server: nook's settings, users, certificates")
	sb, err := backupPaths(c, []string{"nook", "server"}, serverPaths(),
		[]string{config.WWWDir, config.RuntimeDir, filepath.Join(config.LibDir, ".store-*"), filepath.Join(backupDir, "cache"), filepath.Join(backupDir, "lock"), "/etc/nook/backup.env"})
	if err != nil {
		return fmt.Errorf("server: %v", err)
	}
	st.Server = &sb
	writeBackupStatus(st)

	var failed []string
	for _, app := range Apps() {
		if slices.Contains(c.Skip, app) {
			h.say("%s: left out", app)
			continue
		}
		r := h.backupApp(c, app)
		if r.Error != "" && r.Snapshot == "" {
			failed = append(failed, app)
			h.say("%s: %s", app, r.Error)
		} else {
			h.say("%s: %s read, %s new", app, sizeText(float64(r.Size)), sizeText(float64(r.Added)))
		}
		st.Apps = append(st.Apps, r)
		writeBackupStatus(st)
	}

	keep := strings.Split(c.Keep, ",") // daily, weekly, monthly: checked by backupKeepRE
	d, w, m := keep[0], keep[1], keep[2]
	h.say("forgetting old snapshots (keep %s daily, %s weekly, %s monthly)", d, w, m)
	if _, ferr := restic(c, "forget", "--host", c.ServerID, "--tag", "nook", "--group-by", "host,tags",
		"--keep-daily", d, "--keep-weekly", w, "--keep-monthly", m, "--prune"); ferr != nil {
		h.say("%v", ferr) // not worth failing the run: the snapshots are in
	}
	if time.Since(time.Unix(st.Checked, 0)) > 7*24*time.Hour {
		h.say("checking a sample of the repository (weekly)")
		_, cerr := restic(c, "check", "--read-data-subset=5%")
		st.Checked, st.CheckError = time.Now().Unix(), ""
		if cerr != nil {
			st.CheckError = cerr.Error()
			backupNotify("nook: backup check failed", cerr.Error())
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("couldn't back up %s", strings.Join(failed, ", "))
	}
	return nil
}

// backupNotify is update.Notify (a var for tests, and to keep the import out of the way).
var backupNotify = func(title, msg string) {
	if UpdateHooks.Notify != nil {
		UpdateHooks.Notify(title, msg)
	}
}

// backupApp runs the app's backup hooks, or freezes it while it's read, then snapshots its folder.
func (h *Host) backupApp(c BackupConfig, app string) AppBackup {
	r := AppBackup{App: app}
	u, err := LookupApp(app)
	if err != nil {
		r.Error = err.Error()
		return r
	}
	excludes := []string{filepath.Join(u.Home, "builds")} // rebuilt by the next deploy
	for _, site := range siteNames(u) {
		m, err := u.readManifest(site)
		if err != nil || m.Backup == nil {
			continue
		}
		for _, s := range m.Backup.Skip {
			if skipPathRE.MatchString(s) && !strings.Contains(s, "..") {
				excludes = append(excludes, filepath.Join(u.Home, filepath.Clean(s)))
			}
		}
		if m.Backup.Run != "" {
			if err := runBackupHook(u, site, m.Backup.Run); err != nil {
				r.Error = fmt.Sprintf("%s's backup hook: %v", site, err)
				return r
			}
			r.Hooks++
		}
	}
	if r.Hooks == 0 {
		slice := "nook-" + app + ".slice"
		if systemctl("freeze", slice) == nil {
			r.Frozen = true
			defer systemctl("thaw", slice)
		}
	}
	got, err := backupPaths(c, []string{"nook", "app:" + app}, []string{u.Home}, excludes)
	got.App, got.Frozen, got.Hooks = app, r.Frozen, r.Hooks
	if err != nil {
		got.Error = err.Error()
	}
	return got
}

// siteNames is the app's site folders.
func siteNames(u *AppUser) []string {
	var out []string
	if d, err := openDirNoFollow(filepath.Join(u.Home, "sites")); err == nil {
		for _, s := range d.names() {
			if !strings.HasPrefix(s, ".") {
				out = append(out, s)
			}
		}
		d.close()
	}
	return out
}

var hookRE = regexp.MustCompile(`^\./[A-Za-z0-9._-][A-Za-z0-9._/-]{0,100}$`)

// runBackupHook runs a site's backup command as the app user, in the site's folder, for up to 10 minutes.
func runBackupHook(u *AppUser, site, run string) error {
	_, err := runSiteHook(u, site, run, nil, map[string]string{"NOOK_BACKUP": "1"}, 10*time.Minute)
	return err
}

// runSiteHook runs a script in a site's folder as the app user, with the site's environment plus extra, and
// returns what it printed on stdout (up to 64 KB). stderr goes in the error when it fails.
func runSiteHook(u *AppUser, site, run string, args []string, extra map[string]string, timeout time.Duration) (string, error) {
	if !hookRE.MatchString(run) || strings.Contains(run, "..") {
		return "", fmt.Errorf("a hook is a script in the site folder, like ./backup.sh, not %q", run)
	}
	dir := filepath.Join(u.Home, "sites", site)
	cmd := exec.Command(filepath.Join(dir, run), args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: uint32(u.UID), Gid: uint32(u.GID), Groups: []uint32{}}}
	cmd.Dir = dir
	cmd.Env = hookEnv(u, site, extra)
	stdout := &limited{b: &bytes.Buffer{}, max: 64 << 10}
	stderr := &limited{b: &bytes.Buffer{}, max: 4 << 10}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	if err := cmd.Start(); err != nil {
		return "", err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			return "", fmt.Errorf("%v: %s", err, strings.TrimSpace(stderr.b.String()+"\n"+stdout.b.String()))
		}
		return stdout.b.String(), nil
	case <-time.After(timeout):
		cmd.Process.Kill()
		return "", fmt.Errorf("still running after %s", timeout)
	}
}

// hookEnv is the environment the site runs with (its database URL, bound services), general to specific, then extra.
func hookEnv(u *AppUser, site string, extra map[string]string) []string {
	env := map[string]string{}
	for _, f := range envFiles(u.Name, site) {
		b, _ := os.ReadFile(f)
		vars := envfile.Parse(string(b))
		for _, k := range vars.Keys() {
			env[k], _ = vars.Get(k)
		}
	}
	env["HOME"], env["USER"], env["PATH"] = u.Home, "nook-"+u.Name, appPath(u.Name)
	if bins := runtimeBins(u.Name); len(bins) > 0 {
		env["NOOK_RUNTIME_BIN"] = bins[0]
	}
	for k, v := range extra {
		env[k] = v
	}
	var out []string
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	return out
}

// ---- snapshots and restoring ----

type Snapshot struct {
	ID    string    `json:"id"`
	Short string    `json:"short_id"`
	Time  time.Time `json:"time"`
	Host  string    `json:"hostname"`
	Tags  []string  `json:"tags"`
	Paths []string  `json:"paths"`
}

// BackupSnapshots lists app's snapshots, newest first: this server's, or with from set, another server's (an app
// adopted or moved from it).
func BackupSnapshots(app, from string) ([]Snapshot, error) {
	c := LoadBackupConfig()
	if !c.Configured() {
		return nil, errors.New("backups aren't set up yet")
	}
	host := cmp.Or(from, c.ServerID)
	tag := "app:" + app
	if app == "" {
		tag = "server"
	}
	out, err := restic(c, "snapshots", "--json", "--host", host, "--tag", "nook,"+tag)
	if err != nil {
		return nil, err
	}
	var s []Snapshot
	if err := json.Unmarshal(out, &s); err != nil {
		return nil, err
	}
	slices.SortFunc(s, func(a, b Snapshot) int { return b.Time.Compare(a.Time) })
	return s, nil
}

// BackupRestoreApp puts app's data back from a snapshot ("latest" or an id). The data there now is set aside as
// data.before-restore-TIME first. Restic writes into a root-only folder beside the apps, ownership is set from
// there, and only then is it moved in, so nothing the app can touch is written through.
func (h *Host) BackupRestoreApp(app, snapshot string) error {
	c := LoadBackupConfig()
	if !c.Configured() {
		return errors.New("backups aren't set up yet")
	}
	u, err := LookupApp(app)
	if err != nil {
		return err
	}
	if snapshot == "" {
		snapshot = "latest"
	}
	if snapshot != "latest" && !regexp.MustCompile(`^[0-9a-f]{8,64}$`).MatchString(snapshot) {
		return errors.New("a snapshot is an id like 4f2a9c1e, or latest")
	}
	if err := EnsureRestic(h.Out); err != nil {
		return err
	}
	stage := filepath.Join(config.AppsDir, ".restore", app+"-"+strconv.FormatInt(time.Now().Unix(), 10))
	if err := os.MkdirAll(stage, 0o700); err != nil {
		return err
	}
	os.Chmod(filepath.Dir(stage), 0o700)
	defer os.RemoveAll(stage)
	data := filepath.Join(u.Home, "data")
	h.say("restoring %s's data from snapshot %s", app, snapshot)
	args := []string{"restore", snapshot, "--target", stage, "--include", data}
	if snapshot == "latest" {
		args = append(args, "--host", c.ServerID, "--tag", "nook,app:"+app)
	}
	if _, err := restic(c, args...); err != nil {
		return err
	}
	got := filepath.Join(stage, data) // restic keeps the full path under the target
	if fi, err := os.Lstat(got); err != nil || !fi.IsDir() {
		return errors.New("that snapshot has no data folder for " + app)
	}
	// give everything to the app's user, without following any link
	filepath.Walk(got, func(p string, fi os.FileInfo, err error) error {
		if err == nil {
			os.Lchown(p, u.UID, u.GID)
		}
		return nil
	})
	slice := "nook-" + app + ".slice"
	frozen := systemctl("freeze", slice) == nil
	aside := data + ".before-restore-" + time.Now().Format("20060102-150405")
	if _, err := os.Lstat(data); err == nil {
		if err := os.Rename(data, aside); err != nil {
			if frozen {
				systemctl("thaw", slice)
			}
			return err
		}
		h.say("the data that was there is in %s", filepath.Base(aside))
	}
	if err := os.Rename(got, data); err != nil {
		os.Rename(aside, data)
		if frozen {
			systemctl("thaw", slice)
		}
		return err
	}
	if frozen {
		systemctl("thaw", slice)
	}
	// the app has new data under it: start it over
	for _, s := range siteNames(u) {
		systemctl("try-restart", UnitName(app, s))
	}
	h.say("%s's data is back from %s; it restarted", app, snapshot)
	return nil
}

// backupAdvice is a line for the Server page when backups failed or haven't run for a day and a half.
func backupAdvice(s BackupStatus, now time.Time) (Advice, bool) {
	switch {
	case !s.Configured || s.Running:
		return Advice{}, false
	case s.Finished > 0 && !s.OK:
		return Advice{Level: "tight", Title: "The last backup failed", Detail: s.Error + ". Run one from the Backups section once it's fixed."}, true
	case s.CheckError != "":
		return Advice{Level: "tight", Title: "The backup check found a problem", Detail: s.CheckError}, true
	case s.LastOK == 0 || now.Sub(time.Unix(s.LastOK, 0)) > 36*time.Hour:
		return Advice{Level: "watch", Title: "No backup in over a day", Detail: "The daily backup hasn't finished since " + map[bool]string{true: "it was set up", false: time.Unix(s.LastOK, 0).Format("Mon Jan 2 15:04")}[s.LastOK == 0] + ". Run one from the Backups section."}, true
	}
	return Advice{}, false
}
