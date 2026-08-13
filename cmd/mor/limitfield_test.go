package main

import (
	"testing"
	"time"
)

// The panel's two boxes are labelled, so each one carries its own default
// unit. Getting this wrong is silent: a bare number in the deadline box used
// to parse as gigabytes, which cleared the deadline instead of setting one.
func TestParsePeriodFieldBareNumberMeansDays(t *testing.T) {
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	got, err := parsePeriodField("22", now)
	if err != nil {
		t.Fatalf("parsePeriodField(22) = %v", err)
	}
	if want := now.AddDate(0, 0, 22); !got.Equal(want) {
		t.Fatalf("got %v, want %v — a bare number in the deadline box is days", got, want)
	}
}

func TestParsePeriodFieldKeepsExplicitUnits(t *testing.T) {
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	got, err := parsePeriodField("3m", now)
	if err != nil {
		t.Fatal(err)
	}
	if want := now.AddDate(0, 3, 0); !got.Equal(want) {
		t.Fatalf("got %v, want %v — 3m is three months", got, want)
	}
}

func TestParsePeriodFieldEmptyMeansNoDeadline(t *testing.T) {
	got, err := parsePeriodField("  ", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !got.IsZero() {
		t.Fatalf("got %v, want zero time for an empty box", got)
	}
}

func TestParseTrafficFieldBareNumberMeansGigabytes(t *testing.T) {
	got, err := parseTrafficField("22")
	if err != nil {
		t.Fatalf("parseTrafficField(22) = %v", err)
	}
	if want := uint64(22) << 30; got != want {
		t.Fatalf("got %d, want %d", got, want)
	}
}

func TestParseTrafficFieldEmptyMeansNoCap(t *testing.T) {
	got, err := parseTrafficField("")
	if err != nil {
		t.Fatal(err)
	}
	if got != 0 {
		t.Fatalf("got %d, want 0 for an empty box", got)
	}
}

// The two boxes are read independently, so the same text in both is fine —
// it used to be concatenated into one line and rejected as "traffic given
// twice", which is what the operator hit when asking for 22 days and 22 GB.
func TestBothFieldsAcceptTheSameBareNumber(t *testing.T) {
	now := time.Now()
	until, err := parsePeriodField("22", now)
	if err != nil {
		t.Fatalf("deadline: %v", err)
	}
	bytes, err := parseTrafficField("22")
	if err != nil {
		t.Fatalf("traffic: %v", err)
	}
	if until.IsZero() || bytes == 0 {
		t.Fatal("22 in both boxes must set both a deadline and a cap")
	}
}

func TestParseFieldsRejectNonsense(t *testing.T) {
	if _, err := parsePeriodField("завтра", time.Now()); err == nil {
		t.Fatal("nonsense accepted as a deadline")
	}
	if _, err := parseTrafficField("много"); err == nil {
		t.Fatal("nonsense accepted as a traffic cap")
	}
}
