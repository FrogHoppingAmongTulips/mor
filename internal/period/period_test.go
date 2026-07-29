package period

import (
	"testing"
	"time"
)

func TestParseUnits(t *testing.T) {
	cases := []struct {
		in   string
		want Span
	}{
		{"1h", Span{1, 'h'}},
		{"56h", Span{56, 'h'}},
		{"5d", Span{5, 'd'}},
		{"3m", Span{3, 'm'}},
		{"1y", Span{1, 'y'}},
		{"12hour", Span{12, 'h'}},
		{"2 days", Span{2, 'd'}},
		{"6MONTHS", Span{6, 'm'}},
		{"1year", Span{1, 'y'}},
	}
	for _, c := range cases {
		got, err := Parse(c.in)
		if err != nil {
			t.Errorf("Parse(%q): %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("Parse(%q) = %+v, want %+v", c.in, got, c.want)
		}
	}
}

func TestParseRejects(t *testing.T) {
	for _, in := range []string{"", "h", "0d", "-5d", "5x", "5", "abc", "100y"} {
		if got, err := Parse(in); err == nil {
			t.Errorf("Parse(%q) = %+v, want error", in, got)
		}
	}
}

func TestSpanStringNormalises(t *testing.T) {
	cases := []struct{ in, want string }{
		{"56h", "2 дня 8 часов"},
		{"24h", "1 день"},
		{"1h", "1 час"},
		{"5d", "5 дней"},
		{"45d", "1 месяц 15 дней"},
		{"18m", "1 год 6 месяцев"},
		{"12m", "1 год"},
		{"2y", "2 года"},
	}
	for _, c := range cases {
		sp, err := Parse(c.in)
		if err != nil {
			t.Fatalf("Parse(%q): %v", c.in, err)
		}
		if got := sp.String(); got != c.want {
			t.Errorf("Parse(%q).String() = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestAddMovesDate(t *testing.T) {
	base := time.Date(2026, 1, 31, 12, 0, 0, 0, time.UTC)

	sp, _ := Parse("56h")
	if got := sp.Add(base); !got.Equal(time.Date(2026, 2, 2, 20, 0, 0, 0, time.UTC)) {
		t.Errorf("56h from %v = %v", base, got)
	}
	sp, _ = Parse("1y")
	if got := sp.Add(base).Year(); got != 2027 {
		t.Errorf("1y year = %d", got)
	}
	sp, _ = Parse("1m")
	if got := sp.Add(base); got.Before(base) {
		t.Errorf("1m must move forward, got %v", got)
	}
}

func TestLeft(t *testing.T) {
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	if got := Left(time.Time{}, now); got != "" {
		t.Errorf("zero time = %q, want empty", got)
	}
	if got := Left(now.Add(-time.Hour), now); got != "истёк" {
		t.Errorf("past = %q", got)
	}
	if got := Left(now.Add(30*time.Minute), now); got != "меньше часа" {
		t.Errorf("30m = %q", got)
	}
	if got := Left(now.Add(5*time.Hour), now); got != "осталось 5 часов" {
		t.Errorf("5h = %q", got)
	}
	if got := Left(now.Add(72*time.Hour), now); got != "осталось 3 дня" {
		t.Errorf("72h = %q", got)
	}
}

func TestAgo(t *testing.T) {
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	if got := Ago(time.Time{}, now); got != "не заходил" {
		t.Errorf("zero = %q", got)
	}
	if got := Ago(now.Add(-30*time.Second), now); got != "только что" {
		t.Errorf("30s = %q", got)
	}
	if got := Ago(now.Add(-12*time.Minute), now); got != "12 минут назад" {
		t.Errorf("12m = %q", got)
	}
	if got := Ago(now.Add(-3*time.Hour), now); got != "3 часа назад" {
		t.Errorf("3h = %q", got)
	}
	if got := Ago(now.Add(-30*time.Hour), now); got != "вчера" {
		t.Errorf("30h = %q", got)
	}
	if got := Ago(now.Add(-5*24*time.Hour), now); got != "5 дней назад" {
		t.Errorf("5d = %q", got)
	}
}
