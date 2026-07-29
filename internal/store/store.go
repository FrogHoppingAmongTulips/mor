package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"mor/internal/keys"
)

var ErrNotFound = errors.New("ключ не найден")

const (
	ProtoHy2     = "hy2"
	ProtoReality = "reality"
)

func ProtoName(p string) string {
	if p == ProtoReality {
		return "VLESS Reality"
	}
	return "Hysteria2"
}

type User struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Proto     string    `json:"proto"`
	CreatedAt time.Time `json:"created_at"`

	HyToken string `json:"hy_token,omitempty"`
	SNI     string `json:"sni,omitempty"`

	UUID string `json:"uuid,omitempty"`

	ExpiresAt time.Time `json:"expires_at,omitempty"`
}

func (u *User) Expired() bool {
	return !u.ExpiresAt.IsZero() && time.Now().After(u.ExpiresAt)
}

type Store struct {
	mu      sync.Mutex
	path    string
	users   map[string]*User
	modTime time.Time
	size    int64
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
	s.rememberFileStateLocked()
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
	fi, err := os.Stat(s.path)
	if err != nil {
		return
	}
	if fi.ModTime().Equal(s.modTime) && fi.Size() == s.size {
		return
	}
	b, err := os.ReadFile(s.path)
	if err != nil {
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
	s.modTime = fi.ModTime()
	s.size = fi.Size()
}

func (s *Store) rememberFileStateLocked() {
	if fi, err := os.Stat(s.path); err == nil {
		s.modTime = fi.ModTime()
		s.size = fi.Size()
	}
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

func (s *Store) SetSNI(id, sni string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reloadIfChangedLocked()
	u, ok := s.users[id]
	if !ok {
		return ErrNotFound
	}
	u.SNI = strings.TrimSpace(sni)
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
	if dir := filepath.Dir(s.path); dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return err
	}
	s.rememberFileStateLocked()
	return nil
}
