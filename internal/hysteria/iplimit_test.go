package hysteria

import "testing"

func TestIPTrackerZeroLimitAllowsEverything(t *testing.T) {
	tr := NewIPTracker()
	for i, addr := range []string{"1.1.1.1:1", "2.2.2.2:2", "3.3.3.3:3"} {
		if !tr.Allow("key", addr, 0) {
			t.Fatalf("device %d refused with no cap set", i)
		}
	}
}

func TestIPTrackerCapsDistinctDevices(t *testing.T) {
	tr := NewIPTracker()
	if !tr.Allow("key", "1.1.1.1:100", 2) {
		t.Fatal("first device refused")
	}
	if !tr.Allow("key", "2.2.2.2:100", 2) {
		t.Fatal("second device refused")
	}
	if tr.Allow("key", "3.3.3.3:100", 2) {
		t.Fatal("third device admitted past a cap of 2")
	}
}

// Reconnecting is the common case — a client drops and comes back constantly,
// and must never be the thing that pushes its own key over the cap.
func TestIPTrackerReadmitsKnownDevice(t *testing.T) {
	tr := NewIPTracker()
	tr.Allow("key", "1.1.1.1:100", 1)
	if !tr.Allow("key", "1.1.1.1:200", 1) {
		t.Fatal("same address on a new port treated as a second device")
	}
}

func TestIPTrackerCountsPerKey(t *testing.T) {
	tr := NewIPTracker()
	tr.Allow("a", "1.1.1.1:1", 1)
	if !tr.Allow("b", "2.2.2.2:1", 1) {
		t.Fatal("one key's devices counted against another")
	}
}

func TestIPTrackerForgetFreesSlots(t *testing.T) {
	tr := NewIPTracker()
	tr.Allow("key", "1.1.1.1:1", 1)
	if tr.Allow("key", "2.2.2.2:1", 1) {
		t.Fatal("cap not enforced before Forget")
	}
	tr.Forget("key")
	if !tr.Allow("key", "2.2.2.2:1", 1) {
		t.Fatal("slot still held after Forget")
	}
}

func TestIPTrackerActiveCounts(t *testing.T) {
	tr := NewIPTracker()
	tr.Allow("key", "1.1.1.1:1", 3)
	tr.Allow("key", "2.2.2.2:1", 3)
	if got := tr.Active("key"); got != 2 {
		t.Fatalf("Active = %d, want 2", got)
	}
}

// The tracker must be able to tell devices apart without holding an address:
// the fingerprint is what gets stored, and it must not be the address itself.
func TestIPTrackerStoresNoPlaintextAddress(t *testing.T) {
	tr := NewIPTracker()
	const addr = "203.0.113.9"
	tr.Allow("key", addr+":443", 5)
	for fp := range tr.seen["key"] {
		if fp == addr {
			t.Fatal("raw address kept in the tracker")
		}
	}
}

// Two trackers must not agree on a fingerprint, or a table lifted from one
// process could be replayed against another.
func TestIPTrackerSaltIsPerProcess(t *testing.T) {
	a, b := NewIPTracker(), NewIPTracker()
	if a.fingerprint("1.1.1.1") == b.fingerprint("1.1.1.1") {
		t.Fatal("fingerprints match across trackers — salt is not per-process")
	}
}
