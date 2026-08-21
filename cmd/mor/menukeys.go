package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"mor/internal/period"
	"mor/internal/qr"
	"mor/internal/stats"
	"mor/internal/store"
)

// The menu screens about keys: making one, listing them, opening one, removing
// one or several.

// createKey asks what the key should carry: every protocol behind one link, or
// a single protocol chosen by hand.
func (m *menu) createKey() {
	live := []string{}
	for _, p := range protoList {
		if m.e.cfg.On(p) && protoInstalled(p) {
			live = append(live, p)
		}
	}
	if len(live) == 0 {
		m.page("Создать ключ")
		m.note("Ни один протокол не работает — загляни в «Проверку».")
		m.wait()
		m.msg = ""
		return
	}

	// Every question here is asked until it gets an answer, and 0 is the only
	// way out. Enter used to pick a default and move on, which meant a wrong
	// keystroke quietly decided what the key would carry.
	only, said := "", ""
	for {
		m.page("Создать ключ")
		fmt.Printf("  %s 1%s  %s\n", bold, reset, "Все сразу")
		for i, p := range live {
			fmt.Printf("  %s%2d%s  %s\n", bold, i+2, reset, store.ProtoName(p))
		}
		if said != "" {
			fmt.Printf("\n  %s\n", said)
		}
		fmt.Println()

		// Nothing has been typed yet, so Enter is free to mean what it means
		// everywhere else in mor. From the next question on it is taken by a
		// default, and the way out becomes 0 — which those questions say.
		choice, ok := m.ask("Номер (Enter — назад)")
		if !ok || choice == "" || quit(choice) {
			m.msg = ""
			return
		}
		n, err := strconv.Atoi(strings.TrimSpace(choice))
		if err != nil || n < 1 || n > len(live)+1 {
			said = "нужен номер из списка"
			continue
		}
		if n > 1 {
			only = live[n-2]
		}
		break
	}

	name, empty := "", false
	for {
		m.page("Создать ключ")
		if empty {
			fmt.Printf("  имя не может быть пустым\n\n")
		}
		val, ok := m.ask("Имя ключа (0 — выход)")
		if !ok || quit(val) {
			m.msg = ""
			return
		}
		if val != "" {
			name = val
			break
		}
		empty = true
	}

	sni := ""
	if only != store.ProtoSS {
		m.page("Создать ключ · " + name)
		val, ok := m.askSNI(m.e.cfg.SNI)
		if !ok {
			m.msg = ""
			return
		}
		if val != m.e.cfg.SNI {
			sni = val
		}
	}

	m.page("Создать ключ · " + name)
	m.note("Ограничения одной строкой, в любом порядке:",
		"   30d        срок",
		"   10gb       объём",
		"   30d 10gb   и то и другое — сработает то, что наступит раньше",
		"Enter — ключ без ограничений.")
	lim, ok := m.askLimits()
	if !ok {
		m.msg = ""
		return
	}

	var made []*store.User
	var err error
	if only == "" {
		made, err = createAccess(m.e, name, sni)
	} else {
		var u *store.User
		u, err = createKey(m.e, name, only, sni)
		if u != nil {
			made = []*store.User{u}
		}
	}
	if len(made) == 0 {
		m.msg = errText(err)
		return
	}
	if !lim.none() {
		for _, u := range made {
			if e := m.e.st.SetExpiry(u.ID, lim.until); e != nil {
				m.msg = e.Error()
				return
			}
			if e := m.e.st.SetLimit(u.ID, lim.bytes); e != nil {
				m.msg = e.Error()
				return
			}
			u.ExpiresAt, u.Limit = lim.until, lim.bytes
		}
	}

	m.page("Ключ «" + name + "» готов")
	if err != nil {
		fmt.Printf("  %sпредупреждение: %v%s\n\n", dim, err, reset)
	}
	fmt.Printf("  %-*s %s\n", colName, "имя", name)
	label := "протоколы"
	if len(made) == 1 {
		label = "протокол"
	}
	fmt.Printf("  %-*s %s\n", colName, label, protoNames(made))
	fmt.Printf("  %-*s %s\n", colName, "создан", period.Date(made[0].CreatedAt))
	if made[0].SNI != "" {
		fmt.Printf("  %-*s %s\n", colName, "притворяется", made[0].SNI)
	}
	if !lim.until.IsZero() {
		fmt.Printf("  %-*s %s\n", colName, "срок", period.Left(lim.until, time.Now()))
	}
	if lim.bytes > 0 {
		fmt.Printf("  %-*s %s\n", colName, "лимит", stats.Human(lim.bytes))
	}
	m.showAccess(made)
	m.wait()
	m.msg = "«" + name + "» создан"
}

// keys is the one screen about people: who exists, what they spend, and what
// can be done to them. One number opens a person, several delete them at once.
func (m *menu) keys() {
	var groups [][]*store.User
	sec := &section{
		title: "Пользователи",
	}
	m.walk(sec, func(s *section) {
		groups = groupKeys(m.e.st.List())
		s.rows = s.rows[:0]
		if len(groups) == 0 {
			s.rows = append(s.rows, row{label: "пока пусто", hint: "ключи создаются в первом пункте меню"})
			return
		}
		now := time.Now()
		for _, g := range groups {
			g := g
			q := quotaOf(m.e, g)
			hint := m.whenSeen(g, now)
			if q.over() {
				hint = "лимит исчерпан"
			}
			s.rows = append(s.rows, row{
				label: g[0].Name,
				hint:  hint,
				value: q.text(),
				do:    func() (string, bool) { return m.oneKey(g) },
			})
		}
		// Deletion stands apart from the list on purpose: while it lived on the
		// same numbers, one number opened a person and two numbers erased them.
		s.rows = append(s.rows, row{sep: true})
		s.rows = append(s.rows, row{
			label: "Удалить",
			do:    func() (string, bool) { return m.dropScreen(groups) },
		})
	})
	m.msg = ""
}

// dropScreen is the only place that asks who to remove. The list itself only
// opens people, so a stray number can never delete anybody.
func (m *menu) dropScreen(groups [][]*store.User) (string, bool) {
	if len(groups) == 0 {
		return "пока некого удалять", false
	}
	m.page("Удалить пользователей")
	now := time.Now()
	for i, g := range groups {
		fmt.Printf("  %s%2d%s  %-22s %s%s%s\n", bold, i+1, reset, g[0].Name, dim, m.whenSeen(g, now), reset)
	}
	fmt.Println()
	m.note("Номера через пробел (1 3 4), all — всех, Enter — отмена.")

	val, ok := m.ask("Кого удалить")
	if !ok || val == "" {
		return "отменено", false
	}
	picked := []int{}
	if lower(val) == "all" || lower(val) == "все" {
		for i := range groups {
			picked = append(picked, i+1)
		}
	} else {
		nums, bad := numbers(strings.Fields(val))
		if bad != "" {
			return "«" + bad + "» — это не номер", false
		}
		picked = nums
	}
	return m.dropMany(groups, picked)
}

// whenSeen says the useful half: how long a temporary key has left, or when the
// person was last online.
func (m *menu) whenSeen(g []*store.User, now time.Time) string {
	if left := period.Left(g[0].ExpiresAt, now); left != "" {
		return left
	}
	seen := m.lastSeen(g)
	if seen.IsZero() {
		return "ещё не подключался"
	}
	return "был " + period.Ago(seen, now)
}

// dropMany deletes several people in one go, the way the old numbered list did.
func (m *menu) dropMany(groups [][]*store.User, picked []int) (string, bool) {
	if len(groups) == 0 {
		return "пока некого удалять", false
	}
	chosen := make([][]*store.User, 0, len(picked))
	names := make([]string, 0, len(picked))
	for _, n := range picked {
		if n < 1 || n > len(groups) {
			return fmt.Sprintf("нет пользователя с номером %d", n), false
		}
		chosen = append(chosen, groups[n-1])
		names = append(names, groups[n-1][0].Name)
	}

	m.page("Удалить " + plural(len(chosen)) + "?")
	for _, n := range names {
		fmt.Printf("  %s\n", n)
	}
	fmt.Println()
	m.note("Эти устройства потеряют доступ сразу. Вернуть те же ключи нельзя.")
	answer, _ := m.ask("Удалить? y/n")
	if !yes(answer) {
		return "отменено", false
	}
	all := []*store.User{}
	for _, g := range chosen {
		all = append(all, g...)
	}
	if err := removeKeys(m.e, all); err != nil {
		return err.Error(), false
	}
	return "удалено: " + strings.Join(names, ", "), true
}

func (m *menu) lastSeen(g []*store.User) time.Time {
	var seen time.Time
	for _, u := range g {
		if t := m.e.stats.Get(u.ID).LastSeen; t.After(seen) {
			seen = t
		}
	}
	return seen
}

// oneKey shows everything about one person and what can be done with them.
func (m *menu) oneKey(g []*store.User) (string, bool) {
	head := g[0]
	now := time.Now()
	e := m.e.stats.Sum(ids(g))

	q := quotaOf(m.e, g)

	m.page("Ключ «" + head.Name + "»")
	state := period.Ago(e.LastSeen, now)
	if !e.LastSeen.IsZero() && now.Sub(e.LastSeen) < 3*time.Minute {
		state = "подключён прямо сейчас"
	}
	fmt.Printf("  %-*s %s\n", colName, "протоколы", protoNames(g))
	fmt.Printf("  %-*s %s\n", colName, "создан", period.Date(head.CreatedAt))
	fmt.Printf("  %-*s %s\n", colName, "заходил", state)
	fmt.Printf("  %-*s %s\n", colName, "потрачено", q.text())
	if !head.ExpiresAt.IsZero() {
		fmt.Printf("  %-*s %s\n", colName, "срок", period.Left(head.ExpiresAt, now))
	}
	if q.over() {
		fmt.Printf("\n  %sлимит исчерпан — этот ключ сейчас не пускает%s\n", bold, reset)
	}
	if months := e.MonthsSorted(); len(months) > 0 {
		fmt.Println()
		for i, mu := range months {
			if i >= 6 {
				break
			}
			fmt.Printf("  %s%-*s %s%s\n", dim, colName, stats.MonthName(mu.Month), stats.Human(mu.Bytes), reset)
		}
	}

	m.showAccess(g)

	limit := "Изменить ограничения"
	if head.ExpiresAt.IsZero() && q.limit == 0 {
		limit = "Ограничить по времени или трафику"
	}
	fmt.Printf("\n  %s1%s  %s\n", bold, reset, limit)
	fmt.Printf("  %s2%s  Удалить ключ\n\n", bold, reset)
	switch choice, _ := m.ask("Номер (Enter — назад)"); choice {
	case "1":
		return m.setLimits(g)
	case "2":
		return m.dropKey(g)
	default:
		return "", false
	}
}

func (m *menu) dropKey(g []*store.User) (string, bool) {
	m.page("Удалить «" + g[0].Name + "»?")
	m.note("Устройство потеряет доступ сразу. Вернуть тот же ключ нельзя.")
	answer, _ := m.ask("Удалить? y/n")
	if !yes(answer) {
		return "отменено", false
	}
	if err := removeKeys(m.e, g); err != nil {
		return err.Error(), false
	}
	return "«" + g[0].Name + "» удалён", true
}

// showAccess prints what the owner sends to a person: one link when the server
// can switch protocols by itself, separate links otherwise.
func (m *menu) showAccess(g []*store.User) {
	if link := subURL(m.e, g[0]); link != "" && len(g) > 1 {
		m.showSub(g)
		return
	}
	// No single link: hand over the separate ones, QR for the first.
	fmt.Println()
	if a, err := qr.ASCII(keyText(m.e.cfg, g[0])); err == nil {
		fmt.Print(indent(a))
	}
	for _, u := range g {
		fmt.Printf("  %s%s%s\n  %s\n", dim, store.ProtoName(u.Proto), reset, keyText(m.e.cfg, u))
	}
}
