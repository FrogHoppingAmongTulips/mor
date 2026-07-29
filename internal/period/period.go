package period

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

type Span struct {
	N    int
	Unit byte
}

var units = map[string]byte{
	"h": 'h', "hour": 'h', "hours": 'h',
	"d": 'd', "day": 'd', "days": 'd',
	"m": 'm', "month": 'm', "months": 'm',
	"y": 'y', "year": 'y', "years": 'y',
}

const maxYears = 5

func Parse(s string) (Span, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return Span{}, fmt.Errorf("пусто")
	}
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i == 0 {
		return Span{}, fmt.Errorf("сначала число: 12h, 5d, 3m, 1y")
	}
	n, err := strconv.Atoi(s[:i])
	if err != nil || n < 1 {
		return Span{}, fmt.Errorf("число должно быть больше нуля")
	}
	unit, ok := units[strings.TrimSpace(s[i:])]
	if !ok {
		return Span{}, fmt.Errorf("не понимаю «%s» — можно h, d, m, y или hour, day, month, year", s[i:])
	}
	sp := Span{N: n, Unit: unit}
	if sp.years() > maxYears {
		return Span{}, fmt.Errorf("слишком долго — не больше %d лет", maxYears)
	}
	return sp, nil
}

func (s Span) years() float64 {
	switch s.Unit {
	case 'h':
		return float64(s.N) / 24 / 365
	case 'd':
		return float64(s.N) / 365
	case 'm':
		return float64(s.N) / 12
	default:
		return float64(s.N)
	}
}

func (s Span) Add(t time.Time) time.Time {
	switch s.Unit {
	case 'h':
		return t.Add(time.Duration(s.N) * time.Hour)
	case 'd':
		return t.AddDate(0, 0, s.N)
	case 'm':
		return t.AddDate(0, s.N, 0)
	default:
		return t.AddDate(s.N, 0, 0)
	}
}

func (s Span) String() string {
	switch s.Unit {
	case 'h':
		return join(word(s.N/24, "день", "дня", "дней"), word(s.N%24, "час", "часа", "часов"))
	case 'd':
		if s.N >= 365 {
			return join(word(s.N/365, "год", "года", "лет"), word((s.N%365)/30, "месяц", "месяца", "месяцев"))
		}
		if s.N >= 30 {
			return join(word(s.N/30, "месяц", "месяца", "месяцев"), word(s.N%30, "день", "дня", "дней"))
		}
		return word(s.N, "день", "дня", "дней")
	case 'm':
		return join(word(s.N/12, "год", "года", "лет"), word(s.N%12, "месяц", "месяца", "месяцев"))
	default:
		return word(s.N, "год", "года", "лет")
	}
}

// Left describes how much time is left until t.
func Left(until, now time.Time) string {
	if until.IsZero() {
		return ""
	}
	if !until.After(now) {
		return "истёк"
	}
	d := until.Sub(now)
	switch {
	case d < time.Hour:
		return "меньше часа"
	case d < 48*time.Hour:
		return "осталось " + word(int(d.Hours()), "час", "часа", "часов")
	case d < 60*24*time.Hour:
		return "осталось " + word(int(d.Hours())/24, "день", "дня", "дней")
	default:
		return "осталось " + word(int(d.Hours())/24/30, "месяц", "месяца", "месяцев")
	}
}

// Ago describes how long ago t happened.
func Ago(t, now time.Time) string {
	if t.IsZero() {
		return "не заходил"
	}
	d := now.Sub(t)
	switch {
	case d < 2*time.Minute:
		return "только что"
	case d < time.Hour:
		return word(int(d.Minutes()), "минуту", "минуты", "минут") + " назад"
	case d < 24*time.Hour:
		return word(int(d.Hours()), "час", "часа", "часов") + " назад"
	case d < 48*time.Hour:
		return "вчера"
	case d < 30*24*time.Hour:
		return word(int(d.Hours())/24, "день", "дня", "дней") + " назад"
	default:
		return word(int(d.Hours())/24/30, "месяц", "месяца", "месяцев") + " назад"
	}
}

func join(a, b string) string {
	switch {
	case a == "":
		return b
	case b == "":
		return a
	default:
		return a + " " + b
	}
}

func word(n int, one, few, many string) string {
	if n <= 0 {
		return ""
	}
	w := many
	switch {
	case n%10 == 1 && n%100 != 11:
		w = one
	case n%10 >= 2 && n%10 <= 4 && (n%100 < 12 || n%100 > 14):
		w = few
	}
	return fmt.Sprintf("%d %s", n, w)
}
