package stats

import (
	"encoding/json"
	"os"
	"sort"
	"sync"
	"time"

	"mor/internal/fsutil"
)

// History answers "when", where the totals in stats.json answer "how much".
// A graph needs points, and a running total is a single point that keeps moving,
// so the traffic collected each minute is also dropped into time buckets here.
//
// Two resolutions, because two questions get asked. Minutes cover the last few
// hours and are what "скорость" is computed from. Hours cover a month and are
// what a chart is drawn from. Anything longer already lives in stats.json as
// monthly totals, so nothing here needs to be kept for a year.
type History struct {
	mu      sync.Mutex
	path    string
	hours   map[string]map[int64]uint64
	minutes map[string]map[int64]uint64
}

const (
	// keepHours is a month of hourly points: enough for "за неделю" and
	// "за месяц" without holding a year of data nobody looks at.
	keepHours = 30 * 24
	// keepMinutes is three hours: enough to show what is happening right now.
	keepMinutes = 3 * 60
)

type historyFile struct {
	Hours   map[string]map[int64]uint64 `json:"hours"`
	Minutes map[string]map[int64]uint64 `json:"minutes"`
}

func OpenHistory(path string) (*History, error) {
	h := &History{
		path:    path,
		hours:   map[string]map[int64]uint64{},
		minutes: map[string]map[int64]uint64{},
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return h, nil
		}
		return nil, err
	}
	var doc historyFile
	// A damaged history file is not worth failing over: it holds pictures, not
	// keys. Starting over loses graphs, refusing to start loses the VPN.
	if err := json.Unmarshal(b, &doc); err != nil {
		return h, nil
	}
	if doc.Hours != nil {
		h.hours = doc.Hours
	}
	if doc.Minutes != nil {
		h.minutes = doc.Minutes
	}
	return h, nil
}

// Add records traffic against the hour and the minute it happened in.
func (h *History) Add(id string, bytes uint64, at time.Time) {
	if bytes == 0 {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	put(h.hours, id, at.Unix()/3600, bytes)
	put(h.minutes, id, at.Unix()/60, bytes)
}

func put(m map[string]map[int64]uint64, id string, slot int64, bytes uint64) {
	row := m[id]
	if row == nil {
		row = map[int64]uint64{}
		m[id] = row
	}
	row[slot] += bytes
}

// Delete drops a key's history along with the key itself.
func (h *History) Delete(id string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.hours, id)
	delete(h.minutes, id)
}

// Point is one bucket of a series: when it starts and how much went through it.
type Point struct {
	At    time.Time
	Bytes uint64
}

// Series folds several keys — one person's protocols — into one line on a
// chart. The step says how wide a bucket is: an hour for a day of history, a
// day for a month of it.
func (h *History) Series(ids []string, from, to time.Time, step time.Duration) []Point {
	if step <= 0 || !to.After(from) {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()

	// Below an hour the minute buckets are the only ones with the detail;
	// above it the hourly ones are cheaper and reach further back.
	src, slot := h.hours, int64(3600)
	if step < time.Hour {
		src, slot = h.minutes, 60
	}
	secs := int64(step / time.Second)
	buckets := map[int64]uint64{}
	for _, id := range ids {
		for at, b := range src[id] {
			sec := at * slot
			if sec < from.Unix() || sec >= to.Unix() {
				continue
			}
			buckets[sec/secs*secs] += b
		}
	}

	out := make([]Point, 0, len(buckets))
	for sec, b := range buckets {
		out = append(out, Point{At: time.Unix(sec, 0), Bytes: b})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out
}

// Save prunes what is too old to matter and writes the rest.
func (h *History) Save() error {
	h.mu.Lock()
	defer h.mu.Unlock()

	now := time.Now().Unix()
	prune(h.hours, now/3600-keepHours)
	prune(h.minutes, now/60-keepMinutes)

	b, err := json.Marshal(historyFile{Hours: h.hours, Minutes: h.minutes})
	if err != nil {
		return err
	}
	return fsutil.WriteAtomic(h.path, b, 0o600)
}

func prune(m map[string]map[int64]uint64, oldest int64) {
	for id, row := range m {
		for slot := range row {
			if slot < oldest {
				delete(row, slot)
			}
		}
		if len(row) == 0 {
			delete(m, id)
		}
	}
}
