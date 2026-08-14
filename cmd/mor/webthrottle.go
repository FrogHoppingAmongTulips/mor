package main

import (
	"net"
	"net/http"
	"sync"
	"time"
)

// A web panel is reachable from anywhere and guarded by exactly one password,
// so an unlimited guess rate is the whole attack. Failures make the next
// attempt from that address wait, doubling up to a ceiling; a success clears
// the record. Locking by address rather than globally keeps a stranger's
// guessing from locking the owner out of their own server.
const (
	throttleFree   = 5                // attempts before the waiting starts
	throttleStep   = 2 * time.Second  // delay after the first bad attempt past the free ones
	throttleMax    = 5 * time.Minute  // ceiling, so a long attack cannot lock an address out forever
	throttleForget = 30 * time.Minute // idle time after which a record is dropped
)

type throttleEntry struct {
	fails int
	last  time.Time
}

type loginThrottle struct {
	mu sync.Mutex
	by map[string]*throttleEntry
}

func newLoginThrottle() *loginThrottle {
	return &loginThrottle{by: map[string]*throttleEntry{}}
}

// retryAfter reports how long this address must still wait. Zero means it may
// try now.
func (t *loginThrottle) retryAfter(who string) time.Duration {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.sweepLocked()
	e := t.by[who]
	if e == nil || e.fails < throttleFree {
		return 0
	}
	wait := throttleStep << min(e.fails-throttleFree, 10)
	if wait > throttleMax {
		wait = throttleMax
	}
	if elapsed := time.Since(e.last); elapsed < wait {
		return wait - elapsed
	}
	return 0
}

func (t *loginThrottle) fail(who string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	e := t.by[who]
	if e == nil {
		e = &throttleEntry{}
		t.by[who] = e
	}
	e.fails++
	e.last = time.Now()
}

func (t *loginThrottle) reset(who string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.by, who)
}

// sweepLocked drops records nobody has touched in a while, so the map cannot
// grow without bound under a spray of one-shot attempts from many addresses.
func (t *loginThrottle) sweepLocked() {
	for k, e := range t.by {
		if time.Since(e.last) > throttleForget {
			delete(t.by, k)
		}
	}
}

// clientIP identifies the caller for throttling. Proxy headers are deliberately
// ignored: anyone can set them, and trusting them would let an attacker rotate
// their identity at will and defeat the limit entirely.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
