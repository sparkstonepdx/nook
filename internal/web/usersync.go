package web

// Keeping logins in step between linked servers (#111): each server keeps its own copy of every login, so a
// broken link or a server out of step never locks anyone out; while linked, the newest copy of each wins.
// Removals are kept as dated tombstones, so a removed login isn't brought back by a server that still had it.

import (
	"encoding/json"
	"path/filepath"
	"time"

	"github.com/sparkstonepdx/nook/internal/config"
	"github.com/sparkstonepdx/nook/internal/names"
)

// stamp is now, for Updated and tombstones: RFC 3339 with nanoseconds, which sort as strings.
func stamp() string { return time.Now().UTC().Format("2006-01-02T15:04:05.000000000Z07:00") }

func (s *Store) removed() map[string]string {
	m := map[string]string{}
	if b, err := readNoFollow(filepath.Join(s.Dir, "removed.json")); err == nil {
		json.Unmarshal(b, &m)
	}
	return m
}

func (s *Store) saveRemoved(m map[string]string) error {
	b, _ := json.Marshal(m)
	return config.WriteAtomic(filepath.Join(s.Dir, "removed.json"), b, 0o600)
}

func (s *Store) forgetRemoved(name string) {
	if r := s.removed(); r[name] != "" {
		delete(r, name)
		s.saveRemoved(r)
	}
}

func (s *Store) changed() {
	if s.OnChange != nil {
		go s.OnChange()
	}
}

// UsersExport is every login and every removal, for a linked server.
type UsersExport struct {
	Users   map[string]User   `json:"users"`
	Removed map[string]string `json:"removed"`
}

func (s *Store) Export() UsersExport {
	s.mu.Lock()
	defer s.mu.Unlock()
	return UsersExport{Users: s.users(), Removed: s.removed()}
}

// Merge takes a linked server's logins: for each name the newest change wins, whether it's the login or its
// removal. A login neither side has dated (from before linking) is left as each side has it. True if anything
// here changed.
func (s *Store) Merge(in UsersExport) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	users, removed := s.users(), s.removed()
	changed := false
	for name, t := range in.Removed {
		if t > removed[name] {
			removed[name] = t
		}
		if u, ok := users[name]; ok && t > u.Updated {
			delete(users, name)
			changed = true
		}
	}
	for name, u := range in.Users {
		if u.Updated == "" || u.Updated <= removed[name] || !names.DashboardUser(name) {
			continue
		}
		if cur, ok := users[name]; !ok || u.Updated > cur.Updated {
			u.Apps = cleanApps(u.Apps)
			if u.Role != Member {
				u.Role, u.Apps = "", nil
			}
			users[name] = u
			changed = true
		}
	}
	if changed {
		s.saveUsers(users)
	}
	s.saveRemoved(removed)
	return changed
}
