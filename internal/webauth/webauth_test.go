package webauth

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestHashAndVerifyRoundTrip(t *testing.T) {
	hash := HashPassword("correct horse")
	if !VerifyPassword(hash, "correct horse") {
		t.Error("правильный пароль не прошёл проверку")
	}
	if VerifyPassword(hash, "wrong password") {
		t.Error("неправильный пароль прошёл проверку")
	}
}

func TestHashIsSaltedDifferently(t *testing.T) {
	a := HashPassword("same password")
	b := HashPassword("same password")
	if a == b {
		t.Error("два хеша одного пароля совпали — соль не работает")
	}
	if !VerifyPassword(a, "same password") || !VerifyPassword(b, "same password") {
		t.Error("оба хеша должны проверяться одним и тем же паролем")
	}
}

func TestVerifyRejectsMalformedHash(t *testing.T) {
	for _, h := range []string{"", "no-colon-here", "not-hex:also-not-hex", ":", "aa:"} {
		if VerifyPassword(h, "anything") {
			t.Errorf("сломанный хеш %q не должен ничего пропускать", h)
		}
	}
}

func TestSessionsIssueAndValid(t *testing.T) {
	s := NewSessions(time.Hour)
	tok := s.Issue()
	if !s.Valid(tok) {
		t.Error("свежая сессия должна быть валидной")
	}
	if s.Valid("случайный-чужой-токен") {
		t.Error("случайный токен не должен проходить")
	}
}

func TestSessionsRevoke(t *testing.T) {
	s := NewSessions(time.Hour)
	tok := s.Issue()
	s.Revoke(tok)
	if s.Valid(tok) {
		t.Error("отозванная сессия не должна оставаться валидной")
	}
}

func TestSessionsExpire(t *testing.T) {
	s := NewSessions(-time.Second) // already expired the moment it's issued
	tok := s.Issue()
	if s.Valid(tok) {
		t.Error("просроченная сессия не должна быть валидной")
	}
}

// A restart of mor must not log the owner out. Updating the server or renewing
// the certificate restarts it, and with a short-lived certificate that happens
// every couple of days.
func TestSessionsSurviveRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.json")

	first := OpenSessions(time.Hour, path)
	tok := first.Issue()

	second := OpenSessions(time.Hour, path)
	if !second.Valid(tok) {
		t.Fatal("сессия не пережила перезапуск")
	}
}

func TestRevokedSessionStaysRevokedAfterRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.json")

	first := OpenSessions(time.Hour, path)
	tok := first.Issue()
	first.Revoke(tok)

	if OpenSessions(time.Hour, path).Valid(tok) {
		t.Fatal("отозванная сессия вернулась после перезапуска")
	}
}

// An expired token in the file must not be honoured just because it was
// written down.
func TestExpiredSessionsAreNotRestored(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.json")

	first := OpenSessions(time.Millisecond, path)
	tok := first.Issue()
	time.Sleep(5 * time.Millisecond)

	if OpenSessions(time.Millisecond, path).Valid(tok) {
		t.Fatal("протухшая сессия восстановлена")
	}
}

// Tokens are as good as the password while they live.
func TestSessionFileIsPrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.json")
	OpenSessions(time.Hour, path).Issue()

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Fatalf("права %04o, нужно 0600", perm)
	}
}

// A damaged file costs one login, not a panel that refuses to start.
func TestDamagedSessionFileIsSurvivable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.json")
	if err := os.WriteFile(path, []byte("{не json"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := OpenSessions(time.Hour, path)
	tok := s.Issue()
	if !s.Valid(tok) {
		t.Fatal("после повреждённого файла сессии не выдаются")
	}
}

// Memory-only sessions stay the default for the terminal and the tests.
func TestSessionsWithoutPathWriteNothing(t *testing.T) {
	s := NewSessions(time.Hour)
	tok := s.Issue()
	if !s.Valid(tok) {
		t.Fatal("сессия без файла не работает")
	}
	s.Revoke(tok)
	if s.Valid(tok) {
		t.Fatal("отзыв не сработал")
	}
}
