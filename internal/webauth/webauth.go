// Package webauth is the web panel's own front door: one shared password (not
// a per-person account system — the panel has one owner, same as the
// terminal), and a session token so the browser does not resend it on every
// click.
package webauth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"time"

	"mor/internal/fsutil"
)

// iterations is a manual stretch: nothing in the standard library does PBKDF2,
// and one repeated SHA-256 is the well-known fallback when a dedicated KDF
// is not available. This guards one shared admin password, not a multi-user
// account system, so it does not need to be argon2-grade.
const iterations = 200000

// HashPassword returns "salt:hash", both hex, ready to store in config.json.
func HashPassword(pw string) string {
	salt := make([]byte, 16)
	_, _ = rand.Read(salt)
	sum := derive(pw, salt)
	return hex.EncodeToString(salt) + ":" + hex.EncodeToString(sum[:])
}

// VerifyPassword checks pw against a hash made by HashPassword. An empty or
// malformed hash never matches, so a fresh install with no password set is
// never accidentally open.
func VerifyPassword(hash, pw string) bool {
	salt, want, ok := split(hash)
	if !ok {
		return false
	}
	got := derive(pw, salt)
	return subtle.ConstantTimeCompare(got[:], want) == 1
}

func split(hash string) (salt, want []byte, ok bool) {
	parts := strings.SplitN(hash, ":", 2)
	if len(parts) != 2 {
		return nil, nil, false
	}
	salt, err1 := hex.DecodeString(parts[0])
	want, err2 := hex.DecodeString(parts[1])
	if err1 != nil || err2 != nil || len(salt) == 0 || len(want) == 0 {
		return nil, nil, false
	}
	return salt, want, true
}

func derive(pw string, salt []byte) [32]byte {
	sum := sha256.Sum256(append(append([]byte{}, salt...), pw...))
	for i := 0; i < iterations; i++ {
		sum = sha256.Sum256(sum[:])
	}
	return sum
}

// Sessions issues and checks the cookie token the browser holds after login.
// In-memory only: a restart signs everyone out, same as any daemon that does
// not want a session store to manage.
type Sessions struct {
	mu    sync.Mutex
	byTok map[string]time.Time
	ttl   time.Duration

	// path is where the live sessions are kept between runs. Empty means
	// memory only, which is what the tests and the terminal use.
	path string
}

func NewSessions(ttl time.Duration) *Sessions {
	return &Sessions{byTok: map[string]time.Time{}, ttl: ttl}
}

// OpenSessions is NewSessions with a file behind it, so a restart of mor does
// not log the owner out. Updating the server should not cost a login, and the
// panel restarts itself whenever a certificate is renewed — every couple of
// days with a short-lived one.
//
// A damaged file is not worth failing over: the worst it costs is one login.
func OpenSessions(ttl time.Duration, path string) *Sessions {
	s := &Sessions{byTok: map[string]time.Time{}, ttl: ttl, path: path}
	b, err := os.ReadFile(path)
	if err != nil {
		return s
	}
	var saved map[string]time.Time
	if err := json.Unmarshal(b, &saved); err != nil {
		return s
	}
	now := time.Now()
	for tok, exp := range saved {
		if exp.After(now) {
			s.byTok[tok] = exp
		}
	}
	return s
}

// saveLocked writes the sessions out. The caller holds the lock.
//
// Tokens are as good as the password while they live, so the file is 0600 and
// written atomically — a half-written file read at the next start would just
// look damaged and cost a login, but a readable one costs the server.
func (s *Sessions) saveLocked() {
	if s.path == "" {
		return
	}
	b, err := json.Marshal(s.byTok)
	if err != nil {
		return
	}
	_ = fsutil.WriteAtomic(s.path, b, 0o600)
}

func (s *Sessions) Issue() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	tok := hex.EncodeToString(b)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.byTok[tok] = time.Now().Add(s.ttl)
	s.saveLocked()
	return tok
}

// Valid reports whether tok is a live session, and slides its expiry forward
// so an active person is never logged out mid-use.
func (s *Sessions) Valid(tok string) bool {
	if tok == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	exp, ok := s.byTok[tok]
	if !ok {
		return false
	}
	if time.Now().After(exp) {
		delete(s.byTok, tok)
		s.saveLocked()
		return false
	}
	// The expiry slides forward on every check, but the file is not rewritten
	// each time: the panel polls every 15 seconds, and that would be a disk
	// write per poll for no gain. A restart costs at most the unsaved slide.
	s.byTok[tok] = time.Now().Add(s.ttl)
	return true
}

func (s *Sessions) Revoke(tok string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.byTok, tok)
	s.saveLocked()
}

// Save flushes the sliding expiries that Valid did not write. The daemon calls
// it on the way down.
func (s *Sessions) Save() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.saveLocked()
}

// passwordAlphabet leaves out the characters people misread when copying a
// password off a screen: 0/O, 1/l/I, 5/S.
const passwordAlphabet = "abcdefghijkmnpqrtuvwxyz2346789"

// NewPassword makes the password the installer puts on a fresh server, so the
// panel works the moment mor is installed instead of waiting for the owner to
// think one up.
//
// Sixteen characters out of thirty is about 78 bits — far past anything that
// can be guessed through a login form, and still short enough to retype from a
// phone.
func NewPassword() string {
	const n = 16
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		// Without randomness there is no safe password to return, and a
		// predictable one would be worse than none: the caller leaves the
		// panel off instead.
		return ""
	}
	out := make([]byte, n)
	for i, v := range b {
		out[i] = passwordAlphabet[int(v)%len(passwordAlphabet)]
	}
	return string(out)
}
