package webauth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
	"sync"
	"time"

	"mor/internal/fsutil"
)

// Token is a named key for the API, for a script or another program rather
// than a person at a browser.
//
// Only the hash is kept. A token that could be read back out of the config
// would be a second password lying on the disk in the clear, and the whole
// point of issuing one is that it can be handed to something else and revoked
// on its own.
type Token struct {
	Name    string    `json:"name"`
	Hash    string    `json:"hash"`
	Created time.Time `json:"created"`
	LastUse time.Time `json:"last_use,omitzero"`
}

// Tokens is the set of issued API tokens, backed by a file.
type Tokens struct {
	mu    sync.Mutex
	path  string
	list  []Token
	state fsutil.FileState
}

const tokenPrefix = "mor_"

func OpenTokens(path string) *Tokens {
	t := &Tokens{path: path}
	t.reloadLocked()
	return t
}

// reloadLocked re-reads the file if it changed under us.
//
// Tokens are issued from the command line, in a different process from the
// running panel. Without this the panel would go on using whatever list
// existed when it started, and a freshly issued token would be refused until
// the next restart.
func (t *Tokens) reloadLocked() {
	b, ok := t.state.Changed(t.path)
	if !ok {
		return
	}
	var list []Token
	// A damaged file must not lock the owner out of their own panel: the
	// password still works, and tokens can be issued again.
	if json.Unmarshal(b, &list) != nil {
		return
	}
	t.list = list
}

// Issue mints a token and returns it once, in the clear. It is never
// recoverable afterwards.
func (t *Tokens) Issue(name string) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	secret := tokenPrefix + hex.EncodeToString(raw)

	t.mu.Lock()
	defer t.mu.Unlock()
	t.reloadLocked()
	t.list = append(t.list, Token{Name: name, Hash: hashToken(secret), Created: time.Now()})
	if err := t.saveLocked(); err != nil {
		return "", err
	}
	return secret, nil
}

// Valid reports whether the secret matches a live token and records the use.
//
// The comparison walks every token with a constant-time compare: stopping at
// the first mismatch would leak, through timing, how much of a guess was
// right.
func (t *Tokens) Valid(secret string) bool {
	if !strings.HasPrefix(secret, tokenPrefix) {
		return false
	}
	want := hashToken(secret)

	t.mu.Lock()
	defer t.mu.Unlock()
	t.reloadLocked()
	found := -1
	for i := range t.list {
		if subtle.ConstantTimeCompare([]byte(t.list[i].Hash), []byte(want)) == 1 {
			found = i
		}
	}
	if found < 0 {
		return false
	}
	// Last use is written at most once a minute: an API called in a loop would
	// otherwise mean a disk write per request.
	if time.Since(t.list[found].LastUse) > time.Minute {
		t.list[found].LastUse = time.Now()
		_ = t.saveLocked()
	}
	return true
}

// Revoke removes a token by name and says whether there was one.
func (t *Tokens) Revoke(name string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.reloadLocked()
	kept := t.list[:0:0]
	for _, tok := range t.list {
		if tok.Name != name {
			kept = append(kept, tok)
		}
	}
	if len(kept) == len(t.list) {
		return false
	}
	t.list = kept
	_ = t.saveLocked()
	return true
}

// List returns the tokens without their hashes, newest first.
func (t *Tokens) List() []Token {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.reloadLocked()
	out := make([]Token, len(t.list))
	copy(out, t.list)
	for i := range out {
		out[i].Hash = ""
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.After(out[j].Created) })
	return out
}

func (t *Tokens) saveLocked() error {
	b, err := json.MarshalIndent(t.list, "", "  ")
	if err != nil {
		return err
	}
	if err := fsutil.WriteAtomic(t.path, b, 0o600); err != nil {
		return err
	}
	t.state.Remember(t.path)
	return nil
}

// hashToken is a plain SHA-256, not the slow hash the password uses. A token
// is 32 random bytes, so there is no dictionary to run against it — the
// stretching that protects a chosen password buys nothing here and would cost
// 200k iterations on every API call.
func hashToken(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}
