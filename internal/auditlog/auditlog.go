// Package auditlog keeps a short, disk-backed history of admin actions for
// the web panel's "Логи" screen — who got banned, whose traffic was reset,
// which protocol was flipped. This is the only log mor keeps: nothing records
// who connected from where, which is the point of running a VPN at all.
package auditlog

import (
	"encoding/json"
	"os"
	"sync"
	"time"

	"mor/internal/fsutil"
)

type Event struct {
	Action string    `json:"action"`
	Target string    `json:"target,omitempty"`
	At     time.Time `json:"at"`
}

// keep is enough for a busy admin's day without the file becoming something
// you wait on.
const keep = 300

type Log struct {
	mu     sync.Mutex
	path   string
	events []Event
}

func Open(path string) (*Log, error) {
	l := &Log{path: path}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return l, nil
		}
		return nil, err
	}
	var events []Event
	if err := json.Unmarshal(b, &events); err != nil {
		// A damaged log file is not worth failing startup over: it holds
		// history, not keys.
		return l, nil
	}
	l.events = events
	return l, nil
}

// Add records one admin action, newest first, and trims to the retention cap.
func (l *Log) Add(action, target string, at time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append([]Event{{Action: action, Target: target, At: at}}, l.events...)
	if len(l.events) > keep {
		l.events = l.events[:keep]
	}
}

// Recent returns up to n events, newest first.
func (l *Log) Recent(n int) []Event {
	l.mu.Lock()
	defer l.mu.Unlock()
	if n > len(l.events) {
		n = len(l.events)
	}
	out := make([]Event, n)
	copy(out, l.events[:n])
	return out
}

func (l *Log) Save() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	b, err := json.Marshal(l.events)
	if err != nil {
		return err
	}
	return fsutil.WriteAtomic(l.path, b, 0o600)
}
