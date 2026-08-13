package stats

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"mor/internal/fsutil"
)

type Entry struct {
	Total    uint64            `json:"total"`
	Months   map[string]uint64 `json:"months,omitempty"`
	LastSeen time.Time         `json:"last_seen,omitempty"`
}

type Stats struct {
	mu    sync.Mutex
	path  string
	keys  map[string]*Entry
	state fsutil.FileState
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
	s.state.Remember(s.path)
	return s, nil
}

func (s *Stats) reloadLocked() {
	b, changed := s.state.Changed(s.path)
	if !changed {
		return
	}
	keys := map[string]*Entry{}
	if err := json.Unmarshal(b, &keys); err != nil {
		return
	}
	s.keys = keys
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

// Sum folds several keys into one entry — the protocols of one person add up to
// what that person spent. It reads the file once, not once per key.
func (s *Stats) Sum(ids []string) Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reloadLocked()

	out := Entry{Months: map[string]uint64{}}
	for _, id := range ids {
		e := s.keys[id]
		if e == nil {
			continue
		}
		out.Total += e.Total
		for m, v := range e.Months {
			out.Months[m] += v
		}
		if e.LastSeen.After(out.LastSeen) {
			out.LastSeen = e.LastSeen
		}
	}
	return out
}

func (s *Stats) Delete(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reloadLocked()
	delete(s.keys, id)
	_ = s.saveLocked()
}

// Reset clears what a key has spent, keeping LastSeen — a wiped counter is
// not the same thing as a key that never connected.
func (s *Stats) Reset(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reloadLocked()
	e := s.keys[id]
	if e == nil {
		return nil
	}
	e.Total = 0
	e.Months = map[string]uint64{}
	return s.saveLocked()
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
	if err := fsutil.WriteAtomic(s.path, b, 0o600); err != nil {
		return err
	}
	s.state.Remember(s.path)
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

// sizeUnits are the ways a person writes a traffic cap, in both alphabets and
// spelled out — "10 гигабайт" is what someone types who has never used a CLI.
var sizeUnits = map[string]uint64{
	"кб": 1 << 10, "kb": 1 << 10, "k": 1 << 10, "к": 1 << 10,
	"килобайт": 1 << 10, "килобайта": 1 << 10, "килобайтов": 1 << 10,
	"мб": 1 << 20, "mb": 1 << 20, "m": 1 << 20, "м": 1 << 20,
	"мегабайт": 1 << 20, "мегабайта": 1 << 20, "мегабайтов": 1 << 20,
	"гб": 1 << 30, "gb": 1 << 30, "g": 1 << 30, "г": 1 << 30,
	"гигабайт": 1 << 30, "гигабайта": 1 << 30, "гигабайтов": 1 << 30,
	"тб": 1 << 40, "tb": 1 << 40, "t": 1 << 40, "т": 1 << 40,
	"терабайт": 1 << 40, "терабайта": 1 << 40, "терабайтов": 1 << 40,
}

// Parse reads a traffic cap the way a person writes it: 10гб, 500 МБ, 1.5tb.
// A bare number means gigabytes — nobody caps anyone at 50 bytes.
func Parse(s string) (uint64, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, ",", ".")
	if s == "" {
		return 0, fmt.Errorf("пусто")
	}
	i := 0
	for i < len(s) && (s[i] >= '0' && s[i] <= '9' || s[i] == '.') {
		i++
	}
	n, err := strconv.ParseFloat(s[:i], 64)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("сначала число: 10gb, 500mb, 1.5tb")
	}
	unit := strings.TrimSpace(s[i:])
	mult := uint64(1 << 30)
	if unit != "" {
		m, ok := sizeUnits[unit]
		if !ok {
			return 0, fmt.Errorf("не понимаю «%s» — можно kb, mb, gb, tb", unit)
		}
		mult = m
	}
	const maxBytes = 1 << 50 // a petabyte: past this it is a typo, not a plan
	if n*float64(mult) > maxBytes {
		return 0, fmt.Errorf("слишком много — столько не бывает")
	}
	return uint64(n * float64(mult)), nil
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
