package main

import (
	"strings"
	"testing"
	"time"

	"mor/internal/config"
	"mor/internal/stats"
	"mor/internal/store"
)

// testEnv builds an env backed by temporary files, so quota logic can be tested
// without a server anywhere near it.
func testEnv(t *testing.T) *env {
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
	cfg := config.NewDefault()
	cfg.EnsureDefaults()
	return &env{cfg: cfg, st: st, stats: stt}
}

// person adds one key per protocol under a shared subscription token, the way
// «Создать» does it.
func person(t *testing.T, e *env, name string, protos ...string) []*store.User {
	t.Helper()
	token := "sub-" + name
	out := []*store.User{}
	for _, p := range protos {
		u, err := e.st.Add(&store.User{Name: name, Proto: p})
		if err != nil {
			t.Fatal(err)
		}
		if err := e.st.SetSub(u.ID, token); err != nil {
			t.Fatal(err)
		}
		u.Sub = token
		out = append(out, u)
	}
	return out
}

// The cap belongs to the person: spending it on one protocol must close the
// others too, or switching protocols hands out the allowance a second time.
func TestLimitCountsAcrossProtocols(t *testing.T) {
	e := testEnv(t)
	g := person(t, e, "телефон", store.ProtoHy2, store.ProtoReality)
	for _, u := range g {
		if err := e.st.SetLimit(u.ID, 10<<20); err != nil {
			t.Fatal(err)
		}
	}
	// Six megabytes on each of the two keys: neither alone passes the cap.
	now := time.Now()
	e.stats.Add(g[0].ID, 6<<20, now)
	e.stats.Add(g[1].ID, 6<<20, now)

	users := e.st.List()
	bad := blocked(e, users)
	for _, u := range users {
		if !bad[u.ID] {
			t.Errorf("ключ %s (%s) должен быть отключён: 12 МБ при лимите 10 МБ", u.Name, u.Proto)
		}
	}
	if got := len(e.live()); got != 0 {
		t.Errorf("в движки попало %d ключей, ждали 0", got)
	}
}

// Under the cap nobody is touched.
func TestUnderLimitStaysLive(t *testing.T) {
	e := testEnv(t)
	g := person(t, e, "ноут", store.ProtoHy2, store.ProtoReality)
	for _, u := range g {
		if err := e.st.SetLimit(u.ID, 10<<30); err != nil {
			t.Fatal(err)
		}
	}
	e.stats.Add(g[0].ID, 1<<30, time.Now())

	if bad := blocked(e, e.st.List()); len(bad) != 0 {
		t.Errorf("никого не должно быть отключено, отключено %d", len(bad))
	}
	if got := len(e.live()); got != 2 {
		t.Errorf("в движках %d ключей, ждали 2", got)
	}
}

// One person's cap must not touch anybody else.
func TestLimitDoesNotLeakBetweenPeople(t *testing.T) {
	e := testEnv(t)
	greedy := person(t, e, "жадный", store.ProtoHy2)
	quiet := person(t, e, "тихий", store.ProtoHy2)
	if err := e.st.SetLimit(greedy[0].ID, 1<<20); err != nil {
		t.Fatal(err)
	}
	e.stats.Add(greedy[0].ID, 5<<20, time.Now())

	bad := blocked(e, e.st.List())
	if !bad[greedy[0].ID] {
		t.Error("исчерпавший лимит остался включённым")
	}
	if bad[quiet[0].ID] {
		t.Error("чужой лимит отключил постороннего")
	}
}

// A key with no cap is never blocked, however much it spends.
func TestNoLimitNeverBlocks(t *testing.T) {
	e := testEnv(t)
	g := person(t, e, "без лимита", store.ProtoHy2)
	e.stats.Add(g[0].ID, 900<<30, time.Now())

	if blocked(e, e.st.List())[g[0].ID] {
		t.Error("ключ без лимита отключён")
	}
}

// Expiry and the cap are two doors to the same room.
func TestExpiredIsBlockedToo(t *testing.T) {
	e := testEnv(t)
	g := person(t, e, "истёк", store.ProtoReality)
	if err := e.st.SetExpiry(g[0].ID, time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if !blocked(e, e.st.List())[g[0].ID] {
		t.Error("истёкший ключ должен быть отключён")
	}
}

// Raising the cap must let the key back in — otherwise "продлить" would be a
// button that does nothing until the next restart.
func TestRaisingLimitRestores(t *testing.T) {
	e := testEnv(t)
	g := person(t, e, "телефон", store.ProtoHy2)
	if err := e.st.SetLimit(g[0].ID, 1<<20); err != nil {
		t.Fatal(err)
	}
	e.stats.Add(g[0].ID, 5<<20, time.Now())
	if !blocked(e, e.st.List())[g[0].ID] {
		t.Fatal("ключ должен быть отключён до подъёма лимита")
	}

	if err := e.st.SetLimit(g[0].ID, 0); err != nil {
		t.Fatal(err)
	}
	if blocked(e, e.st.List())[g[0].ID] {
		t.Error("после снятия лимита ключ должен снова пускать")
	}
}

// One line, both halves, in any order and either alphabet.
func TestParseLimits(t *testing.T) {
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	day := 24 * time.Hour

	cases := []struct {
		in    string
		after time.Duration // 0: no deadline
		bytes uint64        // 0: no cap
	}{
		{"", 0, 0},
		{"30d", 30 * day, 0},
		{"10гб", 0, 10 << 30},
		{"30d 10гб", 30 * day, 10 << 30},
		{"10гб 30d", 30 * day, 10 << 30},
		{"10 дней 10 гигабайт", 10 * day, 10 << 30},
		{"10 гигабайт 10 дней", 10 * day, 10 << 30},
		{"5 days 500мб", 5 * day, 500 << 20},
		{"12h", 12 * time.Hour, 0},
		{"1.5тб", 0, 1<<40 + 1<<39},
	}
	for _, c := range cases {
		got, err := parseLimits(c.in, now)
		if err != nil {
			t.Errorf("parseLimits(%q): %v", c.in, err)
			continue
		}
		if c.after == 0 {
			if !got.until.IsZero() {
				t.Errorf("parseLimits(%q): срок не ждали, получили %v", c.in, got.until)
			}
		} else if want := now.Add(c.after); !got.until.Equal(want) {
			t.Errorf("parseLimits(%q): срок %v, хотели %v", c.in, got.until, want)
		}
		if got.bytes != c.bytes {
			t.Errorf("parseLimits(%q): лимит %d, хотели %d", c.in, got.bytes, c.bytes)
		}
	}
}

// "10m" has meant ten months since long before caps existed; megabytes are
// written "10мб" or "10mb". Getting this backwards would silently give somebody
// ten megabytes instead of ten months.
func TestParseLimitsMonthsNotMegabytes(t *testing.T) {
	now := time.Now()
	l, err := parseLimits("10m", now)
	if err != nil {
		t.Fatal(err)
	}
	if l.bytes != 0 {
		t.Errorf("«10m» принято за объём: %d байт", l.bytes)
	}
	if want := now.AddDate(0, 10, 0); !l.until.Equal(want) {
		t.Errorf("«10m» — срок %v, хотели %v", l.until, want)
	}

	l, err = parseLimits("10mb", now)
	if err != nil {
		t.Fatal(err)
	}
	if l.bytes != 10<<20 || !l.until.IsZero() {
		t.Errorf("«10mb» должно быть 10 МБ без срока, получили %+v", l)
	}
}

func TestParseLimitsRejects(t *testing.T) {
	now := time.Now()
	for _, bad := range []string{"30d 10d", "10гб 5мб", "завтра", "10 попугаев", "30d чушь"} {
		if l, err := parseLimits(bad, now); err == nil {
			t.Errorf("parseLimits(%q) принято как %+v", bad, l)
		}
	}
}

func TestLimitsText(t *testing.T) {
	now := time.Now()
	if got := (limits{}).text(); got != "без ограничений" {
		t.Errorf("пусто: %q", got)
	}
	l, _ := parseLimits("10гб", now)
	if got := l.text(); got != "лимит 10 ГБ" {
		t.Errorf("только объём: %q", got)
	}
	l, _ = parseLimits("30d 10гб", now)
	if !strings.Contains(l.text(), "лимит 10 ГБ") || !strings.Contains(l.text(), "осталось") {
		t.Errorf("оба: %q", l.text())
	}
}

func TestQuotaText(t *testing.T) {
	if got := (quota{used: 1 << 30}).text(); got != "1.0 ГБ" {
		t.Errorf("без лимита: %q", got)
	}
	if got := (quota{used: 1 << 30, limit: 10 << 30}).text(); got != "1.0 ГБ из 10 ГБ" {
		t.Errorf("с лимитом: %q", got)
	}
}

func TestParseSize(t *testing.T) {
	cases := map[string]uint64{
		"10гб":  10 << 30,
		"10 ГБ": 10 << 30,
		"500мб": 500 << 20,
		"1.5тб": 1<<40 + 1<<39,
		"50":    50 << 30,
		"2gb":   2 << 30,
		"100k":  100 << 10,
	}
	for in, want := range cases {
		got, err := stats.Parse(in)
		if err != nil {
			t.Errorf("Parse(%q): %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("Parse(%q) = %d, хотели %d", in, got, want)
		}
	}
	for _, bad := range []string{"", "гб", "0", "-5", "10 попугаев", "999999тб"} {
		if _, err := stats.Parse(bad); err == nil {
			t.Errorf("Parse(%q) должен был не понять", bad)
		}
	}
}
