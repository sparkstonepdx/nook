package web

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/sparkstonepdx/nook/internal/config"
	"github.com/sparkstonepdx/nook/internal/names"
	"github.com/sparkstonepdx/nook/internal/scrypt"
)

const (
	sessionDays = 30
	inviteHours = 24
)

// Store keeps dashboard users, the signing secret and used invites in the web user's own folder.
type Store struct {
	Dir    string
	mu     sync.Mutex
	secret []byte
	// OnChange runs (in its own goroutine) after a login is added, changed or removed: linked servers hear of it.
	OnChange func()
}

type User struct {
	Salt      string `json:"salt"`
	Hash      string `json:"hash"`
	Gen       int    `json:"gen"`
	Created   string `json:"created"`
	InvitedBy string `json:"invitedBy"`
	// Role is "" (an admin: the whole dashboard, every app) or "member": no dashboard, only the sites of Apps.
	Role string   `json:"role,omitempty"`
	Apps []string `json:"apps,omitempty"`
	// Updated is when this login last changed, which decides between linked servers' copies (RFC 3339, UTC).
	Updated string `json:"updated,omitempty"`
}

const Member = "member"

func (u User) Admin() bool { return u.Role != Member }

// CanUse: whether u may open the sites of app when its login switch is on.
func (u User) CanUse(app string) bool { return u.Admin() || slices.Contains(u.Apps, app) }

// readNoFollow refuses symlinks, so even root reading this folder can't be pointed elsewhere.
func readNoFollow(path string) ([]byte, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 4096)
	for {
		n, err := f.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if err != nil || len(buf) > 4<<20 {
			return buf, nil
		}
	}
}

func OpenStore(dir string) (*Store, error) {
	os.MkdirAll(dir, 0o700)
	s := &Store{Dir: dir}
	path := filepath.Join(dir, "secret")
	b, err := readNoFollow(path)
	if err != nil {
		key := make([]byte, 32)
		rand.Read(key)
		b = []byte(hex.EncodeToString(key))
		if err := config.WriteAtomic(path, b, 0o600); err != nil {
			return nil, err
		}
	}
	s.secret = []byte(strings.TrimSpace(string(b)))
	return s, nil
}

func (s *Store) users() map[string]User {
	m := map[string]User{}
	if b, err := readNoFollow(filepath.Join(s.Dir, "users.json")); err == nil {
		json.Unmarshal(b, &m)
	}
	return m
}

func (s *Store) saveUsers(m map[string]User) error {
	b, _ := json.MarshalIndent(m, "", "  ")
	return config.WriteAtomic(filepath.Join(s.Dir, "users.json"), b, 0o600)
}

func (s *Store) Users() map[string]User { s.mu.Lock(); defer s.mu.Unlock(); return s.users() }
func (s *Store) HasUsers() bool         { return len(s.Users()) > 0 }

func hashPassword(pw, salt string) string {
	k, _ := scrypt.Key([]byte(pw), []byte(salt), 16384, 8, 1, 64) // Node scryptSync defaults
	return hex.EncodeToString(k)
}

func (s *Store) CheckPassword(name, pw string) bool {
	u, ok := s.Users()[name]
	salt := u.Salt
	if !ok {
		salt = strings.Repeat("0", 32) // same work for unknown users: timing doesn't reveal names
	}
	h := hashPassword(pw, salt)
	return ok && subtle.ConstantTimeCompare([]byte(h), []byte(u.Hash)) == 1
}

// SetPassword creates or updates a user; bumping Gen logs out their other sessions.
func (s *Store) SetPassword(name, pw, invitedBy string) User {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.users()
	prev, existed := m[name]
	salt := make([]byte, 16)
	rand.Read(salt)
	u := User{Salt: hex.EncodeToString(salt), Gen: prev.Gen + 1, Created: prev.Created, InvitedBy: prev.InvitedBy, Role: prev.Role, Apps: prev.Apps, Updated: stamp()}
	u.Hash = hashPassword(pw, u.Salt)
	if !existed {
		u.Created, u.InvitedBy = time.Now().UTC().Format(time.RFC3339), invitedBy
	}
	m[name] = u
	s.saveUsers(m)
	s.forgetRemoved(name)
	s.changed()
	return u
}

// SetAccess makes name an admin (role "") or a member who may use apps.
func (s *Store) SetAccess(name, role string, apps []string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.users()
	u, ok := m[name]
	if !ok {
		return false
	}
	u.Role, u.Apps = role, nil
	if role == Member {
		u.Apps = cleanApps(apps)
	}
	u.Updated = stamp()
	m[name] = u
	s.saveUsers(m)
	s.changed()
	return true
}

func cleanApps(apps []string) []string {
	out := []string{}
	for _, a := range apps {
		if names.App(a) && !slices.Contains(out, a) {
			out = append(out, a)
		}
	}
	slices.Sort(out)
	return out
}

func (s *Store) Remove(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.users()
	if _, ok := m[name]; !ok {
		return false
	}
	delete(m, name)
	s.saveUsers(m)
	r := s.removed()
	r[name] = stamp()
	s.saveRemoved(r)
	s.changed()
	return true
}

func (s *Store) mac(msg string) string {
	h := hmac.New(sha256.New, s.secret)
	h.Write([]byte(msg))
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}

// Session cookie: user.gen.expiresMillis.hmac (same format as the earlier Node server).
func (s *Store) SessionCookie(name string, gen int) string {
	payload := fmt.Sprintf("%s.%d.%d", name, gen, time.Now().Add(sessionDays*24*time.Hour).UnixMilli())
	return fmt.Sprintf("sess=%s.%s; Path=/; HttpOnly; Secure; SameSite=Strict; Max-Age=%d", payload, s.mac(payload), sessionDays*86400)
}

const ClearCookie = "sess=; Path=/; HttpOnly; Secure; SameSite=Strict; Max-Age=0"

func (s *Store) SessionUser(cookie string) string {
	var v string
	for _, part := range strings.Split(cookie, ";") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, "sess=") {
			v = strings.TrimPrefix(part, "sess=")
		}
	}
	f := strings.Split(v, ".")
	if len(f) != 4 {
		return ""
	}
	payload := f[0] + "." + f[1] + "." + f[2]
	if subtle.ConstantTimeCompare([]byte(f[3]), []byte(s.mac(payload))) != 1 {
		return ""
	}
	exp, err := strconv.ParseInt(f[2], 10, 64)
	if err != nil || exp < time.Now().UnixMilli() {
		return ""
	}
	u, ok := s.Users()[f[0]]
	if !ok || strconv.Itoa(u.Gen) != f[1] {
		return ""
	}
	return f[0]
}

// Invites are signed, so `nook-admin invite` (root) can mint one without writing to this folder.
// Format: base64url(expiresUnix|by|nonce[|role|apps]).mac; used nonces are remembered until they expire.
// Without a role it makes an admin; a member invite carries the apps, comma-separated.
func (s *Store) MakeInvite(by string) string { return s.MakeRoleInvite(by, "", nil) }

func (s *Store) MakeRoleInvite(by, role string, apps []string) string {
	nonce := make([]byte, 12)
	rand.Read(nonce)
	raw := fmt.Sprintf("%d|%s|%s", time.Now().Add(inviteHours*time.Hour).Unix(), by, hex.EncodeToString(nonce))
	if role == Member {
		raw += "|" + Member + "|" + strings.Join(cleanApps(apps), ",")
	}
	payload := base64.RawURLEncoding.EncodeToString([]byte(raw))
	return payload + "." + s.mac("invite:"+payload)
}

type invite struct {
	By, Nonce string
	Expires   int64
	Role      string
	Apps      []string
}

func (s *Store) checkInvite(tok string) (*invite, bool) {
	i := strings.LastIndexByte(tok, '.')
	if i <= 0 || subtle.ConstantTimeCompare([]byte(tok[i+1:]), []byte(s.mac("invite:"+tok[:i]))) != 1 {
		return nil, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(tok[:i])
	if err != nil {
		return nil, false
	}
	f := strings.Split(string(raw), "|")
	if len(f) != 3 && len(f) != 5 {
		return nil, false
	}
	exp, _ := strconv.ParseInt(f[0], 10, 64)
	if exp < time.Now().Unix() {
		return nil, false
	}
	used := s.usedInvites()
	if _, ok := used[f[2]]; ok {
		return nil, false
	}
	inv := &invite{By: f[1], Nonce: f[2], Expires: exp}
	if len(f) == 5 && f[3] == Member {
		inv.Role, inv.Apps = Member, cleanApps(strings.Split(f[4], ","))
	}
	return inv, true
}

func (s *Store) usedInvites() map[string]int64 {
	m := map[string]int64{}
	if b, err := readNoFollow(filepath.Join(s.Dir, "used-invites.json")); err == nil {
		json.Unmarshal(b, &m)
	}
	return m
}

func (s *Store) InviteValid(tok string) bool { _, ok := s.checkInvite(tok); return ok }

// UseInvite consumes the invite once.
func (s *Store) UseInvite(tok string) (*invite, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	inv, ok := s.checkInvite(tok)
	if !ok {
		return nil, false
	}
	used := s.usedInvites()
	now := time.Now().Unix()
	for k, exp := range used {
		if exp < now {
			delete(used, k)
		}
	}
	used[inv.Nonce] = inv.Expires
	b, _ := json.Marshal(used)
	config.WriteAtomic(filepath.Join(s.Dir, "used-invites.json"), b, 0o600)
	return inv, true
}
