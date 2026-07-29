package stats

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

type Entry struct {
	Total    uint64            `json:"total"`
	Months   map[string]uint64 `json:"months,omitempty"`
	LastSeen time.Time         `json:"last_seen,omitempty"`
}

type Stats struct {
	mu      sync.Mutex
	path    string
	keys    map[string]*Entry
	modTime time.Time
	size    int64
}

func Open(path string) (*Stats, error) {
	s := &Stats{path: path, keys: map[string]*Entry{}}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(b, &s.keys); err != nil {
		return s, nil
	}
	s.rememberLocked()
	return s, nil
}

func (s *Stats) reloadLocked() {
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
	keys := map[string]*Entry{}
	if err := json.Unmarshal(b, &keys); err != nil {
		return
	}
	s.keys = keys
	s.modTime = fi.ModTime()
	s.size = fi.Size()
}

func (s *Stats) rememberLocked() {
	if fi, err := os.Stat(s.path); err == nil {
		s.modTime = fi.ModTime()
		s.size = fi.Size()
	}
}

// Add records bytes used by a key. Zero deltas only keep the entry alive.
func (s *Stats) Add(id string, bytes uint64, at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reloadLocked()
	e := s.keys[id]
	if e == nil {
		e = &Entry{Months: map[string]uint64{}}
		s.keys[id] = e
	}
	if e.Months == nil {
		e.Months = map[string]uint64{}
	}
	if bytes > 0 {
		e.Total += bytes
		e.Months[at.Format("2006-01")] += bytes
		e.LastSeen = at
	}
}

// Seen marks a key as active without adding traffic.
func (s *Stats) Seen(id string, at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reloadLocked()
	e := s.keys[id]
	if e == nil {
		e = &Entry{Months: map[string]uint64{}}
		s.keys[id] = e
	}
	if at.After(e.LastSeen) {
		e.LastSeen = at
	}
}

func (s *Stats) Get(id string) Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reloadLocked()
	e := s.keys[id]
	if e == nil {
		return Entry{}
	}
	cp := *e
	cp.Months = map[string]uint64{}
	for k, v := range e.Months {
		cp.Months[k] = v
	}
	return cp
}

func (s *Stats) Delete(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reloadLocked()
	delete(s.keys, id)
	_ = s.saveLocked()
}

func (s *Stats) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveLocked()
}

func (s *Stats) saveLocked() error {
	b, err := json.MarshalIndent(s.keys, "", "  ")
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
	s.rememberLocked()
	return nil
}

// Months returns the per-month usage, newest first.
func (e Entry) MonthsSorted() []MonthUsage {
	out := make([]MonthUsage, 0, len(e.Months))
	for m, v := range e.Months {
		out = append(out, MonthUsage{Month: m, Bytes: v})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Month > out[j].Month })
	return out
}

type MonthUsage struct {
	Month string
	Bytes uint64
}

// Human formats bytes for a person: 1.4 ГБ, 340 МБ.
func Human(b uint64) string {
	switch {
	case b == 0:
		return "0"
	case b < 1024*1024:
		return fmt.Sprintf("%.0f КБ", float64(b)/1024)
	case b < 1024*1024*1024:
		return fmt.Sprintf("%.0f МБ", float64(b)/1024/1024)
	case b < 10*1024*1024*1024:
		return fmt.Sprintf("%.1f ГБ", float64(b)/1024/1024/1024)
	default:
		return fmt.Sprintf("%.0f ГБ", float64(b)/1024/1024/1024)
	}
}

// MonthName turns 2026-07 into июль 2026.
func MonthName(m string) string {
	t, err := time.Parse("2006-01", m)
	if err != nil {
		return m
	}
	names := [...]string{"январь", "февраль", "март", "апрель", "май", "июнь",
		"июль", "август", "сентябрь", "октябрь", "ноябрь", "декабрь"}
	return fmt.Sprintf("%s %d", names[int(t.Month())-1], t.Year())
}
