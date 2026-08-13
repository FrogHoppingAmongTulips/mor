package main

import (
	"testing"

	"mor/internal/config"
	"mor/internal/store"
)

// The ports screen shows full protocol names, but a change must land by proto
// identity: a wording change once sent the Reality port into Hysteria2's slot.
func TestSetPortByProto(t *testing.T) {
	cfg := config.NewDefault()
	cfg.EnsureDefaults()

	setPort(cfg, store.ProtoReality, 8443)
	setPort(cfg, store.ProtoEnc, 2053)
	setPort(cfg, store.ProtoHy2, 2087)
	setPort(cfg, store.ProtoSS, 8964)

	if cfg.Reality.Port != 8443 {
		t.Errorf("Reality: хотели 8443, стоит %d", cfg.Reality.Port)
	}
	if cfg.Enc.Port != 2053 {
		t.Errorf("Encryption: хотели 2053, стоит %d", cfg.Enc.Port)
	}
	if cfg.VPNPort != 2087 {
		t.Errorf("Hysteria2: хотели 2087, стоит %d", cfg.VPNPort)
	}
	if cfg.SS.Port != 8964 {
		t.Errorf("Shadowsocks: хотели 8964, стоит %d", cfg.SS.Port)
	}
}

// Turning off the one protocol still standing would strand every future key,
// so the screen needs to know when that is about to happen.
func TestLastOn(t *testing.T) {
	cfg := config.NewDefault()
	cfg.EnsureDefaults()

	for _, p := range baseProtocols {
		if lastOn(cfg, p) {
			t.Errorf("%s посчитан последним, пока включены все", p)
		}
	}

	for _, p := range baseProtocols {
		if p != store.ProtoSS {
			cfg.SetOn(p, false)
		}
	}
	if !lastOn(cfg, store.ProtoSS) {
		t.Error("Shadowsocks — единственный включённый, но lastOn говорит нет")
	}
	if lastOn(cfg, store.ProtoHy2) {
		t.Error("Hysteria2 уже выключен, не он последний")
	}
}

// Every row of the ports screen must carry the proto its change applies to.
func TestPortRowsCarryProto(t *testing.T) {
	cfg := config.NewDefault()
	cfg.EnsureDefaults()
	e := &env{cfg: cfg}
	for _, r := range portRows(e) {
		if r.proto == "" {
			t.Errorf("строка «%s» без протокола", r.name)
		}
		if r.name != store.ProtoName(r.proto) {
			t.Errorf("строка «%s» не совпадает с именем протокола %q", r.name, r.proto)
		}
	}
}

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"6.9.0", "6.9.0", 0},
		{"6.10.0", "6.9.0", 1},
		{"6.9.1", "6.9.0", 1},
		{"7.0.0", "6.99.99", 1},
		{"6.9", "6.9.0", 0},
	}
	for _, c := range cases {
		if got := compareVersions(c.a, c.b); got != c.want {
			t.Errorf("compareVersions(%q, %q) = %d, хотели %d", c.a, c.b, got, c.want)
		}
	}
}

func TestPlural(t *testing.T) {
	cases := map[int]string{1: "ключ", 2: "2 ключа", 5: "5 ключей", 11: "11 ключей", 21: "21 ключ", 104: "104 ключа"}
	for n, want := range cases {
		if got := plural(n); got != want {
			t.Errorf("plural(%d) = %q, хотели %q", n, got, want)
		}
	}
}

func TestWithWWW(t *testing.T) {
	cases := map[string]string{
		"apple.com":        "www.apple.com",
		"dl.google.com":    "dl.google.com",
		"www.apple.com":    "www.apple.com",
		"cdn.jsdelivr.net": "cdn.jsdelivr.net",
	}
	for in, want := range cases {
		if got := withWWW(in); got != want {
			t.Errorf("withWWW(%q) = %q, хотели %q", in, got, want)
		}
	}
}

// One person's keys fold into one row; keys without a subscription stand alone.
func TestGroupKeys(t *testing.T) {
	users := []*store.User{
		{ID: "1", Name: "a", Sub: "t1"},
		{ID: "2", Name: "a", Sub: "t1"},
		{ID: "3", Name: "b"},
		{ID: "4", Name: "c", Sub: "t2"},
	}
	groups := groupKeys(users)
	if len(groups) != 3 {
		t.Fatalf("групп %d, хотели 3", len(groups))
	}
	if len(groups[0]) != 2 || groups[0][0].ID != "1" {
		t.Errorf("первая группа собрана неверно: %+v", groups[0])
	}
}

// "1 3 3 x" — duplicates collapse, the first non-number is named.
func TestNumbers(t *testing.T) {
	got, bad := numbers([]string{"1", "3", "3"})
	if bad != "" || len(got) != 2 || got[0] != 1 || got[1] != 3 {
		t.Errorf("numbers(1 3 3) = %v, %q", got, bad)
	}
	if _, bad := numbers([]string{"1", "x"}); bad != "x" {
		t.Errorf("не назван плохой токен: %q", bad)
	}
}

func TestDidYouMean(t *testing.T) {
	rows := []row{{label: "Сменить вручную"}, {label: "Подобрать автоматически"}}
	if got := didYouMean("смнить вручную", rows); got != "нет такой строки — может быть «Сменить вручную»?" {
		t.Errorf("опечатка не распознана: %q", got)
	}
	if got := didYouMean("абвгд", rows); got != "нет такой строки — выбери цифру из списка" {
		t.Errorf("чушь не должна подсказывать: %q", got)
	}
}
