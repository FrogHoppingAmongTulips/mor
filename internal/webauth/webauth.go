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
	"strings"
	"sync"
	"time"
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
}

func NewSessions(ttl time.Duration) *Sessions {
	return &Sessions{byTok: map[string]time.Time{}, ttl: ttl}
}

func (s *Sessions) Issue() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	tok := hex.EncodeToString(b)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.byTok[tok] = time.Now().Add(s.ttl)
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
		return false
	}
	s.byTok[tok] = time.Now().Add(s.ttl)
	return true
}

func (s *Sessions) Revoke(tok string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.byTok, tok)
}
