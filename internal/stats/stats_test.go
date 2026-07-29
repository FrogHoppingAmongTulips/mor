package stats

import (
	"testing"
	"time"
)

func TestAddAccumulates(t *testing.T) {
	s, err := Open(t.TempDir() + "/stats.json")
	if err != nil {
		t.Fatal(err)
	}
	july := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	august := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)

	s.Add("key", 100, july)
	s.Add("key", 50, july)
	s.Add("key", 200, august)

	e := s.Get("key")
	if e.Total != 350 {
		t.Errorf("total = %d, want 350", e.Total)
	}
	if e.Months["2026-07"] != 150 || e.Months["2026-08"] != 200 {
		t.Errorf("months = %v", e.Months)
	}
	if !e.LastSeen.Equal(august) {
		t.Errorf("last seen = %v, want %v", e.LastSeen, august)
	}
}

func TestZeroDeltaKeepsLastSeen(t *testing.T) {
	s, _ := Open(t.TempDir() + "/stats.json")
	at := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	s.Add("key", 100, at)
	s.Add("key", 0, at.Add(time.Hour))

	if got := s.Get("key").LastSeen; !got.Equal(at) {
		t.Errorf("idle traffic must not move last seen: %v", got)
	}
}

func TestSurvivesReload(t *testing.T) {
	path := t.TempDir() + "/stats.json"
	s, _ := Open(path)
	at := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	s.Add("key", 4096, at)
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}

	fresh, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := fresh.Get("key"); got.Total != 4096 {
		t.Errorf("total after reload = %d", got.Total)
	}
}

func TestDeleteRemoves(t *testing.T) {
	path := t.TempDir() + "/stats.json"
	s, _ := Open(path)
	s.Add("key", 10, time.Now())
	s.Delete("key")

	if got := s.Get("key").Total; got != 0 {
		t.Errorf("deleted key still has %d", got)
	}
}

func TestHuman(t *testing.T) {
	cases := []struct {
		in   uint64
		want string
	}{
		{0, "0"},
		{2048, "2 КБ"},
		{5 * 1024 * 1024, "5 МБ"},
		{1536 * 1024 * 1024, "1.5 ГБ"},
		{50 * 1024 * 1024 * 1024, "50 ГБ"},
	}
	for _, c := range cases {
		if got := Human(c.in); got != c.want {
			t.Errorf("Human(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestMonthName(t *testing.T) {
	if got := MonthName("2026-07"); got != "июль 2026" {
		t.Errorf("= %q", got)
	}
	if got := MonthName("мусор"); got != "мусор" {
		t.Errorf("bad input must pass through, got %q", got)
	}
}
