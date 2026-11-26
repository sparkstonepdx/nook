package core

// A one-off: moving the Subduction app set up by hand (extras/subduction-app.sh, app "subduction") onto the
// catalog's Subduction, from the dashboard alone. Remove this file, its ops and the Apps page card once the move
// is done on every server that had the hand setup.
//
// The move: the old app's site is parked (its folder moved out of sites/, which stops it; nothing is deleted),
// the catalog app is installed as a new app with the old one's data copied in before its first start, on the same
// site, so clients see the same address, key and documents. If any step fails the new app is removed and the old
// site is put back. Once it's running the move waits: test, then keep it (the old app is deleted) or go back
// (the new app is deleted and the old one restarted).

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/sparkstonepdx/nook/internal/config"
)

const subductionNewApp = "subduction-2"

// SubductionMove is where the move is: "" (not started), moving, testing (waiting for keep or go back), failed
// (the old one is back), reverted (gone back on purpose) or done (the old app deleted).
type SubductionMove struct {
	State   string    `json:"state"`
	Step    string    `json:"step,omitempty"`
	Error   string    `json:"error,omitempty"`
	Out     string    `json:"out,omitempty"`
	NewApp  string    `json:"newApp,omitempty"`
	Updated time.Time `json:"updated"`
	// Offer: there's a hand-set-up Subduction here to move (the dashboard shows the card).
	Offer bool `json:"offer"`
}

var subductionMoveMu sync.Mutex

var subductionMovePath = func() string { return filepath.Join(config.LibDir, "subduction-move.json") }

func readSubductionMove() SubductionMove {
	var m SubductionMove
	if b, err := os.ReadFile(subductionMovePath()); err == nil {
		json.Unmarshal(b, &m)
	}
	if m.State == "moving" && time.Since(m.Updated) > 30*time.Minute { // the helper restarted part way
		m.State, m.Error = "failed", "the move stopped part way (nook restarted). Check that subduction is serving, or go back."
	}
	return m
}

func saveSubductionMove(m SubductionMove) {
	m.Updated = time.Now()
	m.Offer = false
	b, _ := json.Marshal(m)
	config.WriteAtomic(subductionMovePath(), b, 0o644)
}

// subductionMovable: the hand setup is here (app subduction with its site and key) and the store doesn't manage it.
func subductionMovable() error {
	u, err := LookupApp(SubductionApp)
	if err != nil {
		return fmt.Errorf("there's no app %s", SubductionApp)
	}
	if _, managed := readInstalled()[SubductionApp]; managed {
		return fmt.Errorf("%s already comes from the store", SubductionApp)
	}
	if !slices.Contains(SiteNames(SubductionApp), SubductionSite) {
		return fmt.Errorf("%s has no site %s", SubductionApp, SubductionSite)
	}
	if _, err := os.Stat(filepath.Join(u.Home, "data", "subduction.key")); err != nil {
		return fmt.Errorf("%s has no key at data/subduction.key", SubductionApp)
	}
	if _, err := LookupApp(subductionNewApp); err == nil {
		return fmt.Errorf("there's already an app %s", subductionNewApp)
	}
	return nil
}

// SubductionMoveStatus is the move's state, and whether there's one to offer.
func SubductionMoveStatus() SubductionMove {
	m := readSubductionMove()
	if m.State == "" || m.State == "reverted" || m.State == "failed" {
		m.Offer = subductionMovable() == nil
	}
	return m
}

func parkedDir(home string) string { return filepath.Join(home, "parked") }

// StartSubductionMove starts the move in the background; SubductionMoveStatus follows it.
func StartSubductionMove() error {
	subductionMoveMu.Lock()
	defer subductionMoveMu.Unlock()
	if s := readSubductionMove().State; s == "moving" || s == "testing" {
		return fmt.Errorf("a move is already %s", s)
	}
	if err := subductionMovable(); err != nil {
		return err
	}
	if _, err := findCatalogApp(OfficialCatalog, "subduction"); err != nil {
		return fmt.Errorf("the catalog has no Subduction: %v", err)
	}
	m := SubductionMove{State: "moving", Step: "Starting", NewApp: subductionNewApp}
	saveSubductionMove(m)
	go func() {
		var out strings.Builder
		h := New(&out)
		step := func(s string) { m.Step = s; saveSubductionMove(m) }
		err := h.moveSubduction(step)
		m.Out = out.String()
		if err != nil {
			step("Putting the old server back")
			if rerr := h.unmoveSubduction(); rerr != nil {
				err = fmt.Errorf("%v; then putting the old server back failed too: %v", err, rerr)
			}
			m.State, m.Error, m.Step = "failed", err.Error(), ""
		} else {
			m.State, m.Step = "testing", ""
		}
		m.Out = out.String()
		saveSubductionMove(m)
	}()
	return nil
}

func (h *Host) moveSubduction(step func(string)) error {
	old, err := LookupApp(SubductionApp)
	if err != nil {
		return err
	}
	step("Stopping the old server")
	if err := os.MkdirAll(parkedDir(old.Home), 0o700); err != nil {
		return err
	}
	if err := os.Rename(filepath.Join(old.Home, "sites", SubductionSite), filepath.Join(parkedDir(old.Home), SubductionSite)); err != nil {
		return fmt.Errorf("parking the old site: %v", err)
	}
	if err := h.Sync(SubductionApp); err != nil {
		return fmt.Errorf("stopping the old server: %v", err)
	}
	if UnitProps(UnitName(SubductionApp, SubductionSite), "ActiveState")["ActiveState"] == "active" {
		return fmt.Errorf("the old server is still running")
	}
	step("Installing Subduction from the catalog, with the old server's documents and key")
	err = h.StoreInstall(InstallOpts{ID: "subduction", App: subductionNewApp, Site: SubductionSite, Policy: "minor",
		prepare: func(app string) error {
			to, err := LookupApp(app)
			if err != nil {
				return err
			}
			return copyAppData(old, to)
		}})
	if err != nil {
		return err
	}
	step("Checking it stays up")
	time.Sleep(15 * time.Second)
	if s := UnitProps(UnitName(subductionNewApp, SubductionSite), "ActiveState")["ActiveState"]; s != "active" {
		return fmt.Errorf("the new server stopped (%s); its log is on the Logs page as %s", s, subductionNewApp)
	}
	return nil
}

// copyAppData copies everything in from's data folder into app's, reading as the old app's user and writing as
// the new one's, so a link in either can't reach anything else.
func copyAppData(from, to *AppUser) error {
	as := func(u *AppUser, args ...string) *exec.Cmd {
		c := exec.Command(args[0], args[1:]...)
		c.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: uint32(u.UID), Gid: uint32(u.GID), Groups: []uint32{}}}
		return c
	}
	pack := as(from, "tar", "-C", filepath.Join(from.Home, "data"), "-cf", "-", ".")
	unpack := as(to, "tar", "-C", filepath.Join(to.Home, "data"), "-xf", "-", "--no-same-owner")
	var perr, uerr strings.Builder
	pack.Stderr, unpack.Stderr = &perr, &uerr
	r, w := io.Pipe()
	pack.Stdout, unpack.Stdin = w, r
	if err := unpack.Start(); err != nil {
		return err
	}
	if err := pack.Start(); err != nil {
		w.Close()
		unpack.Wait()
		return err
	}
	pe := pack.Wait()
	w.Close()
	ue := unpack.Wait()
	if pe != nil || ue != nil {
		return fmt.Errorf("copying the data: %s", strings.TrimSpace(perr.String()+" "+uerr.String()))
	}
	return nil
}

// unmoveSubduction removes the new app and puts the old site back.
func (h *Host) unmoveSubduction() error {
	var errs []string
	if _, err := LookupApp(subductionNewApp); err == nil {
		if err := h.Delete(subductionNewApp, subductionNewApp); err != nil {
			errs = append(errs, "removing "+subductionNewApp+": "+err.Error())
		}
	}
	old, err := LookupApp(SubductionApp)
	if err != nil {
		return err
	}
	parked := filepath.Join(parkedDir(old.Home), SubductionSite)
	if _, err := os.Stat(parked); err == nil {
		if err := os.Rename(parked, filepath.Join(old.Home, "sites", SubductionSite)); err != nil {
			errs = append(errs, "putting the old site back: "+err.Error())
		}
	}
	if err := h.Sync(SubductionApp); err != nil {
		errs = append(errs, "starting the old server: "+err.Error())
	}
	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return nil
}

// FinishSubductionMove ends a move waiting for a test: keep deletes the old app (its data with it), back removes
// the new one and restarts the old.
func (h *Host) FinishSubductionMove(keep bool) error {
	subductionMoveMu.Lock()
	defer subductionMoveMu.Unlock()
	m := readSubductionMove()
	if m.State != "testing" && !(m.State == "failed" && !keep) {
		return fmt.Errorf("there's no move waiting for that")
	}
	if !keep {
		if err := h.unmoveSubduction(); err != nil {
			return err
		}
		m.State, m.Error = "reverted", ""
		saveSubductionMove(m)
		return nil
	}
	if err := h.Delete(SubductionApp, SubductionApp); err != nil {
		return err
	}
	m.State = "done"
	saveSubductionMove(m)
	return nil
}
