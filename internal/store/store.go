package store

import (
	"encoding/json"
	"errors"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"mor/internal/fsutil"
	"mor/internal/keys"
)

var ErrNotFound = errors.New("ключ не найден")

const (
	ProtoHy2     = "hy2"
	ProtoReality = "reality"
	ProtoEnc     = "enc"
	ProtoSS      = "ss"
)

func ProtoName(p string) string {
	switch p {
	case ProtoReality:
		return "VLESS+Reality"
	case ProtoEnc:
		return "VLESS Encryption"
	case ProtoSS:
		return "Shadowsocks"
	default:
		return "Hysteria2"
	}
}

type User struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Proto     string    `json:"proto"`
	CreatedAt time.Time `json:"created_at"`

	// Sub ties together the keys of one person. Every protocol gets its own key
	// and its own secret, but they share this token and one subscription link.
	Sub string `json:"sub,omitempty"`

	HyToken    string `json:"hy_token,omitempty"`
	UUID       string `json:"uuid,omitempty"`
	SSPassword string `json:"ss_password,omitempty"`

	// SNI is this key's own cover site. Empty means the server-wide one.
	SNI string `json:"sni,omitempty"`

	ExpiresAt time.Time `json:"expires_at,omitzero"`

	// Limit caps how much traffic this person may spend, in bytes. It is stored
	// on every key of one person and counted across all of them together, so
	// switching protocols cannot be used to spend the cap twice. Zero means no
	// cap at all.
	Limit uint64 `json:"limit,omitempty"`

	// Banned is a manual kill switch, independent of expiry and traffic —
	// the owner cut this person off on purpose, not because a number ran out.
	Banned bool `json:"banned,omitempty"`

	// IPLimit caps how many devices may hold a connection at once, so one key
	// handed round a dozen people stops working for the twelfth. Zero means no
	// cap. Only Hysteria2 can enforce it: the Xray protocols never report a
	// per-connection address, so there is nothing there to count.
	IPLimit int `json:"ip_limit,omitempty"`

	// AutoReset zeroes this person's traffic when the calendar month turns, so
	// a monthly allowance renews itself instead of waiting to be cleared by hand.
	AutoReset bool `json:"auto_reset,omitempty"`

	// ResetMonth is the month ("2006-01") the counter was last cleared for.
	// Keeping the month rather than a timestamp is what makes the reset safe to
	// re-run: a server that was off on the first of the month catches up when it
	// comes back, and a server that ticks all day still only resets once.
	ResetMonth string `json:"reset_month,omitempty"`
}

func (u *User) Expired() bool {
	return !u.ExpiresAt.IsZero() && time.Now().After(u.ExpiresAt)
}

type Store struct {
	mu    sync.Mutex
	path  string
	users map[string]*User
	state fsutil.FileState
}

func Open(path string) (*Store, error) {
	s := &Store{path: path, users: map[string]*User{}}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, err
	}
	list, err := decode(b)
	if err != nil {
		return nil, err
	}
	for _, u := range list {
		s.users[u.ID] = u
	}
	s.state.Remember(path)
	return s, nil
}

func decode(b []byte) ([]*User, error) {
	var list []*User
	if err := json.Unmarshal(b, &list); err != nil {
		return nil, err
	}
	for _, u := range list {
		if u.Proto == "" {
			u.Proto = ProtoHy2
		}
	}
	return list, nil
}

func (s *Store) reloadIfChangedLocked() {
	b, changed := s.state.Changed(s.path)
	if !changed {
		return
	}
	list, err := decode(b)
	if err != nil {
		return
	}
	users := make(map[string]*User, len(list))
	for _, u := range list {
		users[u.ID] = u
	}
	s.users = users
}

func (s *Store) List() []*User {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reloadIfChangedLocked()
	out := make([]*User, 0, len(s.users))
	for _, u := range s.users {
		cp := *u
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}

func (s *Store) Add(u *User) (*User, error) {
	if u == nil {
		return nil, errors.New("пустой ключ")
	}
	cp := *u
	if strings.TrimSpace(cp.Name) == "" {
		cp.Name = "Без имени"
	}
	if cp.Proto == "" {
		cp.Proto = ProtoHy2
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reloadIfChangedLocked()
	cp.ID = keys.UUID()
	cp.CreatedAt = time.Now()
	s.users[cp.ID] = &cp
	if err := s.persistLocked(); err != nil {
		return nil, err
	}
	out := cp
	return &out, nil
}

func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reloadIfChangedLocked()
	if _, ok := s.users[id]; !ok {
		return ErrNotFound
	}
	delete(s.users, id)
	return s.persistLocked()
}

func (s *Store) SetExpiry(id string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reloadIfChangedLocked()
	u, ok := s.users[id]
	if !ok {
		return ErrNotFound
	}
	u.ExpiresAt = at
	return s.persistLocked()
}

// SetName renames one key. The name is a label for the owner's benefit only —
// nothing in the engines or the links is keyed by it, so renaming never
// invalidates anything already handed out.
func (s *Store) SetName(id, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reloadIfChangedLocked()
	u, ok := s.users[id]
	if !ok {
		return ErrNotFound
	}
	u.Name = name
	return s.persistLocked()
}

// SetLimit caps a key's traffic. Zero lifts the cap.
func (s *Store) SetLimit(id string, bytes uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reloadIfChangedLocked()
	u, ok := s.users[id]
	if !ok {
		return ErrNotFound
	}
	u.Limit = bytes
	return s.persistLocked()
}

func (s *Store) SetIPLimit(id string, n int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reloadIfChangedLocked()
	u, ok := s.users[id]
	if !ok {
		return ErrNotFound
	}
	if n < 0 {
		n = 0
	}
	u.IPLimit = n
	return s.persistLocked()
}

func (s *Store) SetAutoReset(id string, on bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reloadIfChangedLocked()
	u, ok := s.users[id]
	if !ok {
		return ErrNotFound
	}
	u.AutoReset = on
	return s.persistLocked()
}

// SetResetMonth records that this key's counter has been cleared for a given
// month, so the monthly reset runs once and not on every tick of that month.
func (s *Store) SetResetMonth(id, month string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reloadIfChangedLocked()
	u, ok := s.users[id]
	if !ok {
		return ErrNotFound
	}
	u.ResetMonth = month
	return s.persistLocked()
}

// SetBanned flips the manual kill switch on one key.
func (s *Store) SetBanned(id string, banned bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reloadIfChangedLocked()
	u, ok := s.users[id]
	if !ok {
		return ErrNotFound
	}
	u.Banned = banned
	return s.persistLocked()
}

// BySub returns every live key of one person, ordered as they were created.
// Expired keys are left out: a subscription must not offer dead endpoints.
func (s *Store) BySub(token string) []*User {
	if token == "" {
		return nil
	}
	out := []*User{}
	for _, u := range s.List() {
		if u.Sub == token && !u.Expired() {
			out = append(out, u)
		}
	}
	return out
}

// SetSub stamps a key with a subscription token.
func (s *Store) SetSub(id, token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reloadIfChangedLocked()
	u, ok := s.users[id]
	if !ok {
		return ErrNotFound
	}
	u.Sub = token
	return s.persistLocked()
}

func (s *Store) FindByHyToken(token string) *User {
	if token == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reloadIfChangedLocked()
	for _, u := range s.users {
		if u.Proto == ProtoHy2 && u.HyToken == token {
			if u.Expired() {
				return nil
			}
			cp := *u
			return &cp
		}
	}
	return nil
}

func (s *Store) persistLocked() error {
	list := make([]*User, 0, len(s.users))
	for _, u := range s.users {
		list = append(list, u)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].CreatedAt.Before(list[j].CreatedAt) })
	b, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	if err := fsutil.WriteAtomic(s.path, b, 0o600); err != nil {
		return err
	}
	s.state.Remember(s.path)
	return nil
}
