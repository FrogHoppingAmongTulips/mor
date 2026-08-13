package main

import (
	"context"
	"encoding/json"
	"os"
	"sync"
	"time"

	"mor/internal/fsutil"
	"mor/internal/sysinfo"
)

// sysSample is one minute of system load: the average of the readings taken
// during it. Averaging rather than sampling once a minute keeps a brief spike
// from either vanishing or standing in for the whole minute.
type sysSample struct {
	At  time.Time `json:"at"`
	CPU float64   `json:"cpu"`
	Mem float64   `json:"mem"`
}

const (
	sysSampleEvery = 5 * time.Second // how often the machine is read
	sysBucket      = time.Minute     // one point on the chart
	sysHistoryKeep = 24 * 60         // a day of minutes
	sysSaveEvery   = 5 * time.Minute // how often the day is written out
)

// sysHistory is a day of CPU and memory load at one point per minute, kept on
// disk so the chart survives a restart — a graph that resets to empty every
// time mor is updated cannot answer "was it like this yesterday too".
type sysHistory struct {
	mu      sync.Mutex
	path    string
	samples []sysSample

	// The minute being accumulated. It joins samples only once complete, so
	// the chart never shows a half-finished bucket dipping toward zero.
	bucket time.Time
	sumCPU float64
	sumMem float64
	n      int
}

func openSysHistory(path string) *sysHistory {
	h := &sysHistory{path: path}
	b, err := os.ReadFile(path)
	if err != nil {
		return h
	}
	var samples []sysSample
	if err := json.Unmarshal(b, &samples); err != nil {
		// A damaged file costs a chart, not a server: start over quietly.
		return h
	}
	h.samples = samples
	h.trimLocked()
	return h
}

func (h *sysHistory) all() []sysSample {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]sysSample, len(h.samples))
	copy(out, h.samples)
	return out
}

// add folds one reading into the minute in progress, closing the previous one
// when the clock rolls over.
func (h *sysHistory) add(at time.Time, cpu, mem float64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	min := at.Truncate(sysBucket)
	if h.bucket.IsZero() {
		h.bucket = min
	}
	if !min.Equal(h.bucket) {
		h.closeLocked()
		h.bucket = min
	}
	h.sumCPU += cpu
	h.sumMem += mem
	h.n++
}

func (h *sysHistory) closeLocked() {
	if h.n == 0 {
		return
	}
	h.samples = append(h.samples, sysSample{
		At:  h.bucket,
		CPU: h.sumCPU / float64(h.n),
		Mem: h.sumMem / float64(h.n),
	})
	h.sumCPU, h.sumMem, h.n = 0, 0, 0
	h.trimLocked()
}

func (h *sysHistory) trimLocked() {
	if len(h.samples) > sysHistoryKeep {
		h.samples = h.samples[len(h.samples)-sysHistoryKeep:]
	}
}

func (h *sysHistory) save() error {
	h.mu.Lock()
	b, err := json.Marshal(h.samples)
	h.mu.Unlock()
	if err != nil {
		return err
	}
	return fsutil.WriteAtomic(h.path, b, 0o600)
}

// run reads the machine on a slow ticker of its own: sysinfo.Read blocks for
// about 200ms sampling CPU, which is no business of the once-a-minute traffic
// collector.
func (h *sysHistory) run(ctx context.Context) {
	lastSave := time.Now()
	for {
		s := sysinfo.Read()
		mem := 0.0
		if s.MemTotal > 0 {
			mem = float64(s.MemUsed) / float64(s.MemTotal) * 100
		}
		h.add(time.Now(), s.CPUPercent, mem)

		if time.Since(lastSave) >= sysSaveEvery {
			lastSave = time.Now()
			_ = h.save()
		}
		select {
		case <-ctx.Done():
			h.mu.Lock()
			h.closeLocked()
			h.mu.Unlock()
			_ = h.save()
			return
		case <-time.After(sysSampleEvery):
		}
	}
}
