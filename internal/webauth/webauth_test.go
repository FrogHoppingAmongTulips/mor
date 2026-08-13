package webauth

import (
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
