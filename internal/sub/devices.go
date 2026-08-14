package sub

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"mor/internal/fsutil"
)

// DeviceWindow is how long a device keeps its slot after it last asked for the
// subscription. Apps re-read it every few hours, so a phone in use holds its
// place; one that was replaced or given back lets go after a month, and the
// owner does not have to clear anything by hand.
const DeviceWindow = 30 * 24 * time.Hour

// Devices counts the distinct devices that fetched one person's subscription.
//
// This is the half of the device limit the Xray protocols cannot do. Hysteria2
// is asked on every connection and can be told no; VLESS and Shadowsocks never
// report anything per connection, so the only moment mor sees a device at all
// is when its app comes for the subscription. Counting there is weaker — it
// stops a link being passed around, not a config already copied out by hand —
// and that is exactly what it is for.
//
// Nothing identifying is stored. The device id an app sends is folded through
// HMAC with a salt kept in the file itself, so the table can answer "same
// device as before" and nothing else. The subscription token is hashed the
// same way rather than written down: it is the one secret in the link.
type Devices struct {
	mu    sync.Mutex
	path  string
	salt  []byte
	seen  map[string]map[string]time.Time // key fingerprint -> device fingerprint -> last seen
	state fsutil.FileState
}

type devicesFile struct {
	Salt string                          `json:"salt"`
	Seen map[string]map[string]time.Time `json:"seen"`
}

// OpenDevices loads the table, or starts an empty one with a fresh salt.
func OpenDevices(path string) *Devices {
	d := &Devices{path: path, seen: map[string]map[string]time.Time{}}
	d.reloadLocked()
	if len(d.salt) == 0 {
		d.salt = make([]byte, 32)
		_, _ = rand.Read(d.salt)
	}
	return d
}

// reloadLocked picks up writes from another process — the panel and the
// subscription server are the same process today, but `mor` on the command
// line is not.
func (d *Devices) reloadLocked() {
	b, ok := d.state.Changed(d.path)
	if !ok {
		return
	}
	var f devicesFile
	if json.Unmarshal(b, &f) != nil {
		return
	}
	salt, err := hex.DecodeString(f.Salt)
	if err != nil || len(salt) == 0 {
		return
	}
	// The salt must survive with its table: a new one would turn every known
	// device into an unknown one and silently double everybody's limit.
	d.salt, d.seen = salt, f.Seen
	if d.seen == nil {
		d.seen = map[string]map[string]time.Time{}
	}
}

func (d *Devices) fingerprint(s string) string {
	mac := hmac.New(sha256.New, d.salt)
	_, _ = mac.Write([]byte(s))
	return hex.EncodeToString(mac.Sum(nil)[:16])
}

// Allow reports whether this device may take the subscription, and records it
// when the answer is yes. A limit of zero or less means no cap, and so does a
// request that carries no device id at all: refusing those would break every
// client that does not send one.
func (d *Devices) Allow(token, device string, limit int) bool {
	if limit <= 0 || device == "" {
		return true
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.reloadLocked()

	key, fp, now := d.fingerprint(token), d.fingerprint(device), time.Now()
	list := d.seen[key]
	if list == nil {
		list = map[string]time.Time{}
		d.seen[key] = list
	}
	for other, at := range list {
		if now.Sub(at) >= DeviceWindow {
			delete(list, other)
		}
	}
	// A device that already holds a slot always gets back in: re-reading the
	// subscription must never be what pushes somebody over their own limit.
	if _, held := list[fp]; !held && len(list) >= limit {
		return false
	}
	list[fp] = now
	d.saveLocked()
	return true
}

// Count is how many devices currently hold a slot on this subscription.
func (d *Devices) Count(token string) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.reloadLocked()

	now := time.Now()
	n := 0
	for _, at := range d.seen[d.fingerprint(token)] {
		if now.Sub(at) < DeviceWindow {
			n++
		}
	}
	return n
}

// Forget clears one subscription's devices — for a key that was deleted, and
// for the owner handing the limit back after somebody changed their phone.
func (d *Devices) Forget(token string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.reloadLocked()

	if _, ok := d.seen[d.fingerprint(token)]; !ok {
		return
	}
	delete(d.seen, d.fingerprint(token))
	d.saveLocked()
}

func (d *Devices) saveLocked() {
	b, err := json.Marshal(devicesFile{Salt: hex.EncodeToString(d.salt), Seen: d.seen})
	if err != nil {
		return
	}
	// A failed write is not worth refusing a subscription over: the count
	// carries on in memory and the worst case is that a restart forgets it.
	if fsutil.WriteAtomic(d.path, b, 0o600) == nil {
		d.state.Remember(d.path)
	}
}

// deviceID is the id a client app sends about itself. Hiddify and the apps
// that follow it use x-hwid; the rest send nothing, and are not counted.
func deviceID(r *http.Request) string {
	for _, h := range []string{"x-hwid", "x-device-id", "hwid"} {
		if v := r.Header.Get(h); v != "" {
			return v
		}
	}
	return ""
}
