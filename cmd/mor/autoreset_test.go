package main

import (
	"testing"
	"time"

	"mor/internal/stats"
	"mor/internal/store"
)

// autoResetEnv builds the smallest env autoReset needs: a store and stats.
func autoResetEnv(t *testing.T) *env {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(dir + "/users.json")
	if err != nil {
		t.Fatal(err)
	}
	stt, err := stats.Open(dir + "/stats.json")
	if err != nil {
		t.Fatal(err)
	}
	return &env{st: st, stats: stt}
}

func addKey(t *testing.T, e *env, name string, auto bool, used uint64) *store.User {
	t.Helper()
	u, err := e.st.Add(&store.User{Name: name, Proto: store.ProtoHy2})
	if err != nil {
		t.Fatal(err)
	}
	if auto {
		if err := e.st.SetAutoReset(u.ID, true); err != nil {
			t.Fatal(err)
		}
	}
	e.stats.Add(u.ID, used, time.Now())
	return u
}

func totalOf(e *env, id string) uint64 { return e.stats.Get(id).Total }

func TestAutoResetClearsTrafficOnNewMonth(t *testing.T) {
	e := autoResetEnv(t)
	u := addKey(t, e, "phone", true, 5<<30)

	autoReset(e)

	if got := totalOf(e, u.ID); got != 0 {
		t.Fatalf("traffic = %d, want 0 after the monthly reset", got)
	}
}

// The loop calls this every minute. Resetting once per month is the whole
// point — a second pass in the same month must be a no-op, or traffic would be
// wiped continuously and no cap could ever be reached.
func TestAutoResetRunsOncePerMonth(t *testing.T) {
	e := autoResetEnv(t)
	u := addKey(t, e, "phone", true, 1<<30)

	autoReset(e)
	e.stats.Add(u.ID, 2<<30, time.Now())
	autoReset(e)

	if got := totalOf(e, u.ID); got != 2<<30 {
		t.Fatalf("traffic = %d, want the 2GiB spent after the reset to survive", got)
	}
}

func TestAutoResetLeavesOtherKeysAlone(t *testing.T) {
	e := autoResetEnv(t)
	off := addKey(t, e, "laptop", false, 3<<30)

	autoReset(e)

	if got := totalOf(e, off.ID); got != 3<<30 {
		t.Fatalf("traffic = %d, want 3GiB untouched on a key without auto-reset", got)
	}
}

// A server that was down on the first of the month has to catch up when it
// comes back, which is exactly what storing the month rather than a timestamp
// buys — a stale stamp still triggers a reset.
func TestAutoResetCatchesUpAfterDowntime(t *testing.T) {
	e := autoResetEnv(t)
	u := addKey(t, e, "phone", true, 4<<30)
	if err := e.st.SetResetMonth(u.ID, "2000-01"); err != nil {
		t.Fatal(err)
	}

	autoReset(e)

	if got := totalOf(e, u.ID); got != 0 {
		t.Fatalf("traffic = %d, want 0 — a stale reset month must still fire", got)
	}
}
