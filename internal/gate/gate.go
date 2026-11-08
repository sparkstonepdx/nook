// Package gate decides what an app's deploy key may run over SSH. sshd forces every app-* login through
// `app ssh-gate`, which allows only an rsync upload into ~/builds/<name>/ and `app site ...` commands.
package gate

import (
	"errors"
	"regexp"
	"strings"
)

var (
	shortFlags = regexp.MustCompile(`^-[A-Za-z.]+$`)
	buildPath  = regexp.MustCompile(`^builds/[a-z0-9]([a-z0-9.-]{0,252}[a-z0-9])?/?$`)
	// long options an rsync client legitimately sends to an upload-only server for `rsync -a --delete`
	longOK = map[string]bool{"--server": true, "--delete": true, "--delete-during": true, "--delete-after": true, "--delete-delay": true, "--partial": true, "--numeric-ids": true, "--no-inc-recursive": true, "--safe-links": true}
)

// Kind of an allowed command.
const (
	Rsync = "rsync"
	App   = "app"
)

// Split breaks an SSH_ORIGINAL_COMMAND into words. Quotes and shell syntax aren't supported on purpose.
func Split(cmd string) ([]string, error) {
	if strings.ContainsAny(cmd, "'\"`$;&|<>\\(){}*?~\n\r\t") {
		return nil, errors.New("shell syntax isn't allowed")
	}
	return strings.Fields(cmd), nil
}

// Check returns which command is allowed (Rsync or App) or an error saying why not.
func Check(args []string) (string, error) {
	if len(args) == 0 {
		return "", errors.New("interactive logins aren't allowed; this key can only deploy")
	}
	switch args[0] {
	case "app":
		if len(args) < 2 || args[1] != "site" {
			return "", errors.New("only `app site ...` is allowed")
		}
		return App, nil
	case "rsync":
		return Rsync, checkRsync(args[1:])
	}
	return "", errors.New("only rsync uploads into builds/ and `app site ...` are allowed")
}

func checkRsync(args []string) error {
	if len(args) < 3 || args[0] != "--server" {
		return errors.New("rsync: only uploads are allowed")
	}
	// rsync --server <opts...> . builds/<name>/
	dest, dot, opts := args[len(args)-1], args[len(args)-2], args[1:len(args)-2]
	if dot != "." || !buildPath.MatchString(dest) || strings.Contains(dest, "..") {
		return errors.New("rsync: the destination must be builds/<name>/")
	}
	for _, o := range opts {
		if o == "--sender" {
			return errors.New("rsync: downloads aren't allowed")
		}
		if !longOK[o] && !shortFlags.MatchString(o) {
			return errors.New("rsync: option not allowed: " + o)
		}
	}
	return nil
}
