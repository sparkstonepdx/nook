package core

// What managers did on this server (#111): every request from a managed link, allowed or refused, with who at the
// manager asked, kept in /var/lib/nook/link-audit.jsonl (the last 2000) and shown on the Server page.

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/sparkstonepdx/nook/internal/config"
)

// AuditEntry is one request from a manager.
type AuditEntry struct {
	Time    int64  `json:"t"`
	Server  string `json:"server"` // the managing server's address
	By      string `json:"by,omitempty"`
	Op      string `json:"op"`
	Detail  string `json:"detail,omitempty"`
	Allowed bool   `json:"allowed"`
}

const auditKeep = 2000

var (
	auditFile = func() string { return filepath.Join(config.LibDir, "link-audit.jsonl") }
	auditMu   sync.Mutex
)

// auditDetail is the parameters worth showing, short: never secrets (those travel in Set) or whole pages.
func auditDetail(p map[string]string) string {
	var parts []string
	for k, v := range p {
		if k == "html" || k == "confirm" {
			continue
		}
		if len(v) > 60 {
			v = v[:60] + "…"
		}
		parts = append(parts, k+"="+v)
	}
	sort.Strings(parts)
	return strings.Join(parts, " ")
}

func linkAudit(l Link, by, op, detail string, allowed bool) {
	auditMu.Lock()
	defer auditMu.Unlock()
	e, _ := json.Marshal(AuditEntry{Time: time.Now().Unix(), Server: l.Name, By: by, Op: op, Detail: detail, Allowed: allowed})
	f, err := os.OpenFile(auditFile(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	f.Write(append(e, '\n'))
	f.Close()
	if fi, err := os.Stat(auditFile()); err == nil && fi.Size() > auditKeep*400 {
		all := readAudit()
		if len(all) > auditKeep {
			all = all[len(all)-auditKeep:]
		}
		var b strings.Builder
		for _, a := range all {
			j, _ := json.Marshal(a)
			b.Write(append(j, '\n'))
		}
		config.WriteAtomic(auditFile(), []byte(b.String()), 0o600)
	}
}

func readAudit() []AuditEntry {
	f, err := os.Open(auditFile())
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []AuditEntry
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var e AuditEntry
		if json.Unmarshal(sc.Bytes(), &e) == nil {
			out = append(out, e)
		}
	}
	return out
}

// LinkAudit is the newest n entries, newest first, leaving out looking (overview, logs...) unless all.
func LinkAudit(n int, all bool) []AuditEntry {
	auditMu.Lock()
	defer auditMu.Unlock()
	list := readAudit()
	out := []AuditEntry{}
	for i := len(list) - 1; i >= 0 && len(out) < n; i-- {
		if all || !lookOps[list[i].Op] || !list[i].Allowed {
			out = append(out, list[i])
		}
	}
	return out
}

// lookOps only read: left out of the short list.
var lookOps = map[string]bool{"overview": true, "headroom": true, "domains": true, "siteLogs": true, "syncLog": true, "instructions": true,
	"envGet": true, "tokenList": true, "dnsPlan": true, "notFoundGet": true, "certs": true, "store": true, "storeJobs": true, "storeImage": true,
	"services": true, "directory": true, "updateStatus": true, "systemStatus": true, "backupStatus": true, "backupSnapshots": true, "appDisk": true}
