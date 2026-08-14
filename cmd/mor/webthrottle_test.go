package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// The free attempts exist so a typo does not punish the owner; the waiting
// starts only once someone is clearly guessing.
func TestThrottleAllowsFirstAttempts(t *testing.T) {
	th := newLoginThrottle()
	for i := 0; i < throttleFree; i++ {
		if wait := th.retryAfter("10.0.0.1"); wait != 0 {
			t.Fatalf("попытка %d уже заблокирована (%v), а бесплатных должно быть %d", i+1, wait, throttleFree)
		}
		th.fail("10.0.0.1")
	}
	if wait := th.retryAfter("10.0.0.1"); wait <= 0 {
		t.Errorf("после %d неудач ожидание не включилось", throttleFree)
	}
}

func TestThrottleBacksOffAndCaps(t *testing.T) {
	th := newLoginThrottle()
	for i := 0; i < throttleFree+2; i++ {
		th.fail("10.0.0.2")
	}
	first := th.retryAfter("10.0.0.2")
	th.fail("10.0.0.2")
	second := th.retryAfter("10.0.0.2")
	if second <= first {
		t.Errorf("задержка не растёт: было %v, стало %v", first, second)
	}
	// Even a very long attack must not lock an address out beyond the ceiling,
	// or an attacker could keep the owner out of their own panel for good.
	for i := 0; i < 100; i++ {
		th.fail("10.0.0.2")
	}
	if wait := th.retryAfter("10.0.0.2"); wait > throttleMax {
		t.Errorf("задержка %v превысила потолок %v", wait, throttleMax)
	}
}

// A correct password has to clear the record, otherwise the owner stays
// punished for their own earlier typos.
func TestThrottleResetOnSuccess(t *testing.T) {
	th := newLoginThrottle()
	for i := 0; i < throttleFree+3; i++ {
		th.fail("10.0.0.3")
	}
	if th.retryAfter("10.0.0.3") == 0 {
		t.Fatal("подготовка: адрес должен быть заблокирован")
	}
	th.reset("10.0.0.3")
	if wait := th.retryAfter("10.0.0.3"); wait != 0 {
		t.Errorf("после верного пароля адрес всё ещё ждёт %v", wait)
	}
}

// Throttling one address must never slow down another, or a stranger's
// guessing would lock the owner out.
func TestThrottleIsPerAddress(t *testing.T) {
	th := newLoginThrottle()
	for i := 0; i < throttleFree+5; i++ {
		th.fail("10.0.0.4")
	}
	if wait := th.retryAfter("10.0.0.5"); wait != 0 {
		t.Errorf("чужие неудачи задержали другой адрес на %v", wait)
	}
}

func TestThrottleForgetsIdleEntries(t *testing.T) {
	th := newLoginThrottle()
	for i := 0; i < throttleFree+2; i++ {
		th.fail("10.0.0.6")
	}
	th.mu.Lock()
	th.by["10.0.0.6"].last = time.Now().Add(-throttleForget - time.Minute)
	th.mu.Unlock()

	th.retryAfter("10.0.0.7") // any call sweeps
	th.mu.Lock()
	_, still := th.by["10.0.0.6"]
	th.mu.Unlock()
	if still {
		t.Error("запись не забылась после простоя — карта растёт без предела")
	}
}

// Proxy headers are attacker-controlled: honouring them would let one client
// present a fresh identity per request and shrug the limit off entirely.
func TestClientIPIgnoresForwardedHeaders(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/api/login", nil)
	r.RemoteAddr = "203.0.113.9:51234"
	r.Header.Set("X-Forwarded-For", "198.51.100.7")
	r.Header.Set("X-Real-IP", "198.51.100.8")
	if got := clientIP(r); got != "203.0.113.9" {
		t.Errorf("clientIP = %q, ждали 203.0.113.9 — заголовкам доверять нельзя", got)
	}
}
