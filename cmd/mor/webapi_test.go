package main

import (
	"testing"
	"time"

	"mor/internal/stats"
	"mor/internal/store"
)

// A day of minutes is the cap; anything older has to fall off the front or
// the file grows without end.
func TestSysHistoryTrimsToKeepCap(t *testing.T) {
	h := &sysHistory{}
	start := time.Now().Truncate(time.Minute)
	for i := 0; i < sysHistoryKeep+120; i++ {
		h.add(start.Add(time.Duration(i)*time.Minute), 1, 1)
	}
	if got := len(h.all()); got > sysHistoryKeep {
		t.Errorf("после переполнения хранится %d точек, потолок %d", got, sysHistoryKeep)
	}
}

// Readings inside one minute must average into a single point, not become
// several — the chart is one point per minute by definition.
func TestSysHistoryAveragesWithinMinute(t *testing.T) {
	h := &sysHistory{}
	min := time.Now().Truncate(time.Minute)
	h.add(min, 20, 50)
	h.add(min.Add(5*time.Second), 40, 50)
	h.add(min.Add(time.Minute), 0, 0) // rolls the previous minute over

	got := h.all()
	if len(got) != 1 {
		t.Fatalf("минута дала %d точек, ждали одну", len(got))
	}
	if got[0].CPU != 30 {
		t.Errorf("CPU = %v, ждали среднее 30", got[0].CPU)
	}
}

// The minute in progress must stay out of the series, or the chart would end
// on a half-filled bucket that looks like a sudden drop.
func TestSysHistoryHidesUnfinishedMinute(t *testing.T) {
	h := &sysHistory{}
	h.add(time.Now().Truncate(time.Minute), 99, 99)
	if got := len(h.all()); got != 0 {
		t.Errorf("незавершённая минута уже попала в график (%d точек)", got)
	}
}

func TestSysHistoryOrderPreserved(t *testing.T) {
	h := &sysHistory{}
	start := time.Now().Truncate(time.Minute)
	for i, cpu := range []float64{1, 2, 3} {
		h.add(start.Add(time.Duration(i)*time.Minute), cpu, 0)
	}
	h.add(start.Add(3*time.Minute), 0, 0)
	got := h.all()
	if len(got) != 3 || got[0].CPU != 1 || got[2].CPU != 3 {
		t.Errorf("порядок точек нарушен: %+v", got)
	}
}

// The group id a person is created under must be the exact id later requests
// address it by, or the panel's own "created" response would 404 on refresh.
func TestGroupIDStableAcrossLookup(t *testing.T) {
	g := []*store.User{{ID: "abc", Sub: "tok-1"}, {ID: "def", Sub: "tok-1"}}
	if got := groupID(g); got != "tok-1" {
		t.Errorf("groupID с общей подпиской = %q, хотели tok-1", got)
	}
	solo := []*store.User{{ID: "xyz"}}
	if got := groupID(solo); got != "xyz" {
		t.Errorf("groupID без подписки = %q, хотели xyz", got)
	}
}

// sparkline hands the panel a fixed-length series. A short or empty history
// must still produce exactly sparkHours slots, or the chart would silently
// misalign hours against the wrong buckets.
func TestSparklineAlwaysFixedLength(t *testing.T) {
	h, err := stats.OpenHistory(t.TempDir() + "/history.json")
	if err != nil {
		t.Fatal(err)
	}
	e := &env{hist: h}
	g := []*store.User{{ID: "k1"}}
	got := sparkline(e, g)
	if len(got) != sparkHours {
		t.Fatalf("пустая история дала %d точек, ждали %d", len(got), sparkHours)
	}
	for i, v := range got {
		if v != 0 {
			t.Errorf("точка %d = %d, у пустой истории всё должно быть нулём", i, v)
		}
	}
}

// Traffic recorded now must land in the series, and traffic older than the
// window must not — otherwise a key would look busy long after it went quiet.
func TestSparklinePlacesRecentTrafficOnly(t *testing.T) {
	h, err := stats.OpenHistory(t.TempDir() + "/history.json")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	h.Add("k1", 500, now)
	h.Add("k1", 900, now.Add(-time.Duration(sparkHours+5)*time.Hour))

	e := &env{hist: h}
	got := sparkline(e, []*store.User{{ID: "k1"}})
	if len(got) != sparkHours {
		t.Fatalf("длина %d, ждали %d", len(got), sparkHours)
	}
	var total uint64
	for _, v := range got {
		total += v
	}
	if total != 500 {
		t.Errorf("в окно попало %d байт, ждали ровно 500 — старое не должно учитываться", total)
	}
}
