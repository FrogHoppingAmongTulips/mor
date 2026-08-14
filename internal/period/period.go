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

// Single letters that also name a size — "г" for гигабайт, "м" for мегабайт —
// are deliberately absent: a deadline and a traffic cap can be typed on one
// line, and a token that could mean either would be a coin toss.
var units = map[string]byte{
	// "m" stays months: it was there first, and minutes are asked for as "min".
	"min": 'i', "mins": 'i', "minute": 'i', "minutes": 'i',
	"мин": 'i', "минут": 'i', "минута": 'i', "минуты": 'i', "минуту": 'i',
	"h": 'h', "hour": 'h', "hours": 'h',
	"ч": 'h', "час": 'h', "часа": 'h', "часов": 'h',
	"d": 'd', "day": 'd', "days": 'd',
	"д": 'd', "дн": 'd', "день": 'd', "дня": 'd', "дней": 'd',
	"m": 'm', "month": 'm', "months": 'm',
	"мес": 'm', "месяц": 'm', "месяца": 'm', "месяцев": 'm',
	"y": 'y', "year": 'y', "years": 'y',
	"год": 'y', "года": 'y', "лет": 'y',
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
		return Span{}, fmt.Errorf("сначала число: 30min, 12h, 5d, 3m, 1y")
	}
	n, err := strconv.Atoi(s[:i])
	if err != nil || n < 1 {
		return Span{}, fmt.Errorf("число должно быть больше нуля")
	}
	unit, ok := units[strings.TrimSpace(s[i:])]
	if !ok {
		return Span{}, fmt.Errorf("не понимаю «%s» — можно min, h, d, m, y", s[i:])
	}
	sp := Span{N: n, Unit: unit}
	if sp.years() > maxYears {
		return Span{}, fmt.Errorf("слишком долго — не больше %d лет", maxYears)
	}
	return sp, nil
}

func (s Span) years() float64 {
	switch s.Unit {
	case 'i':
		return float64(s.N) / 60 / 24 / 365
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
	case 'i':
		return t.Add(time.Duration(s.N) * time.Minute)
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

// months is the genitive case, the one a date needs: «31 июля».
var months = [...]string{"января", "февраля", "марта", "апреля", "мая", "июня",
	"июля", "августа", "сентября", "октября", "ноября", "декабря"}

// Date writes a moment the way a person reads it. Go's layouts only speak
// English, so the month is filled in here rather than through a format string.
func Date(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return fmt.Sprintf("%d %s %d, %02d:%02d", t.Day(), months[int(t.Month())-1], t.Year(), t.Hour(), t.Minute())
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
	case d < time.Minute:
		return "меньше минуты"
	case d < time.Hour:
		return "осталось " + word(int(d.Minutes()), "минута", "минуты", "минут")
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
		return "ещё не подключался"
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
