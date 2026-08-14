package hysteria

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"net"
	"sync"
	"time"
)

// IPWindow is how long a device counts as still connected after its last
// handshake. Hysteria2 asks on every connection, so a client that is actually
// in use keeps refreshing its slot; one that walked away frees it.
const IPWindow = 5 * time.Minute

// IPTracker counts how many distinct devices hold a key at once.
//
// It never stores an address. Each one is folded through HMAC with a salt
// generated at startup and never written anywhere, so the tracker can answer
// "is this the same device as before" and nothing else — not which device, not
// where it is. The salt dies with the process, and so does the whole table:
// counting concurrent devices needs no history, and keeping any would work
// against the only thing a VPN is for.
type IPTracker struct {
	mu   sync.Mutex
	salt []byte
	seen map[string]map[string]time.Time // key ID -> device fingerprint -> last seen
}

func NewIPTracker() *IPTracker {
	salt := make([]byte, 32)
	_, _ = rand.Read(salt)
	return &IPTracker{salt: salt, seen: map[string]map[string]time.Time{}}
}

func (t *IPTracker) fingerprint(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	mac := hmac.New(sha256.New, t.salt)
	_, _ = mac.Write([]byte(host))
	return string(mac.Sum(nil))
}

// Allow reports whether this device may connect on this key, and records it
// when the answer is yes. A limit of zero or less means no cap.
func (t *IPTracker) Allow(id, addr string, limit int) bool {
	if limit <= 0 {
		return true
	}
	fp := t.fingerprint(addr)
	now := time.Now()

	t.mu.Lock()
	defer t.mu.Unlock()

	devices := t.seen[id]
	if devices == nil {
		devices = map[string]time.Time{}
		t.seen[id] = devices
	}
	for other, at := range devices {
		if now.Sub(at) >= IPWindow {
			delete(devices, other)
		}
	}
	// A device already holding a slot always gets back in — reconnecting must
	// never be what pushes somebody over their own limit.
	if _, held := devices[fp]; held || len(devices) < limit {
		devices[fp] = now
		return true
	}
	return false
}

// Forget drops a key's devices — used when the key is deleted, so a stale
// table cannot keep answering about something that no longer exists.
func (t *IPTracker) Forget(id string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.seen, id)
}

// Active reports how many devices currently hold the key.
func (t *IPTracker) Active(id string) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	now, n := time.Now(), 0
	for _, at := range t.seen[id] {
		if now.Sub(at) < IPWindow {
			n++
		}
	}
	return n
}
