package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"mor/internal/config"
	"mor/internal/period"
	"mor/internal/qr"
	"mor/internal/stats"
	"mor/internal/store"
)

const (
	clearScreen = "\x1b[H\x1b[2J"
	dim         = "\x1b[2m"
	bold        = "\x1b[1m"
	reset       = "\x1b[0m"
)

// Every screen lays its rows on the same grid, so names, numbers and notes line
// up wherever you are in the menu.
const colName = 14

// menuItems keeps its numbering fixed — people learn the digits, not the
// order — and uses blank lines to say which of them belong together. An empty
// key is one of those lines: it is drawn and skipped, never picked.
var menuItems = []struct{ key, title string }{
	{"1", "Создать"},
	{"", ""},
	{"2", "Протоколы"},
	{"3", "Порты"},
	{"4", "DNS"},
	{"5", "SNI"},
	{"", ""},
	{"6", "Проверка"},
	{"7", "Пользователи"},
	{"8", "Обновление"},
	{"9", "Перезапуск"},
	{"", ""},
	{"0", "Выход"},
}

type menu struct {
	e    *env
	in   *bufio.Reader
	msg  string
	warn string
}

func interactive() bool {
	fi, err := os.Stdin.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// utf8Input tells the terminal that what it receives is UTF-8. Without it a
// backspace erases one byte, and a Russian letter is two — so erasing a word
// typed in Russian walks two columns per letter and eats the prompt to the left
// of the cursor. Every screen here asks in Russian, so this is not an edge case.
func utf8Input() {
	cmd := exec.Command("stty", "iutf8")
	cmd.Stdin = os.Stdin
	_ = cmd.Run()
}

func runMenu() {
	e, err := load()
	if err != nil {
		fmt.Printf("нет конфига — сначала установи: mor setup (%v)\n", err)
		os.Exit(1)
	}
	utf8Input()
	m := &menu{e: e, in: stdin}
	for {
		if fresh, err := load(); err == nil {
			m.e = fresh
		}
		m.warn = trouble(m.e)
		m.draw()

		choice, ok := m.ask("Выбери номер")
		if !ok || choice == "0" || choice == "q" || choice == "exit" {
			fmt.Print(clearScreen)
			return
		}
		m.run(choice)
	}
}

func (m *menu) draw() {
	fmt.Print(clearScreen)
	fmt.Printf("\n  %sMOR%s  %s%s%s\n", bold, reset, dim, m.e.cfg.PublicHost, reset)
	// The panel's address is not guessable from the host alone — it depends on
	// a port the owner may have changed — so the header spells it out rather
	// than leaving them to remember it.
	if m.e.cfg.WebOn() {
		fmt.Printf("  %shttp://%s:%d%s\n", dim, m.e.cfg.PublicHost, m.e.cfg.WebPort, reset)
	}
	fmt.Println()

	for _, it := range menuItems {
		if it.key == "" {
			fmt.Println()
			continue
		}
		fmt.Printf("  %s%s%s  %s\n", bold, it.key, reset, it.title)
	}
	if m.warn != "" {
		fmt.Printf("\n  %s%s%s\n", bold, m.warn, reset)
	}
	if m.msg != "" {
		fmt.Printf("\n  %s\n", m.msg)
	}
	fmt.Println()
}

func (m *menu) ask(prompt string) (string, bool) {
	fmt.Printf("  %s: ", prompt)
	line, err := m.in.ReadString('\n')
	if err != nil && line == "" {
		return "", false
	}
	return strings.TrimSpace(line), true
}

func (m *menu) run(choice string) {
	switch choice {
	case "1":
		m.createKey()
	case "2":
		m.protocols()
	case "3":
		m.ports()
	case "4":
		m.dns()
	case "5":
		m.masquerade()
	case "6":
		m.check()
	case "7":
		m.keys()
	case "8":
		m.update()
	case "9":
		m.restart()
	default:
		m.msg = nearestItem(choice)
	}
}

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
	if only != store.ProtoEnc && only != store.ProtoSS {
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

// ports shows what listens where, and keeps the two ways of changing it apart:
// by hand when you know the number, automatically when you do not.
func (m *menu) ports() {
	sec := &section{title: "Порты"}
	m.walk(sec, func(s *section) {
		lines := []string{}
		for _, r := range portRows(m.e) {
			lines = append(lines, fmt.Sprintf(" %-18s %s%d%s/%s", r.name, bold, r.port, reset, r.kind))
		}
		s.state = lines
		s.rows = []row{
			{label: "Сменить вручную", do: m.manualPort},
			{label: "Подобрать автоматически", do: m.autoPort},
		}
	})
	m.msg = ""
}

// manualPort: which protocol, then which number.
func (m *menu) manualPort() (string, bool) {
	rows := portRows(m.e)
	m.page("Сменить порт вручную")
	for i, r := range rows {
		fmt.Printf("  %s%d%s  %-18s %s%d/%s%s\n", bold, i+1, reset, r.name, dim, r.port, r.kind, reset)
	}
	fmt.Println()
	which, ok := m.ask("Какому протоколу (Enter — отмена)")
	if !ok || which == "" {
		return "отменено", false
	}
	n, err := strconv.Atoi(which)
	if err != nil || n < 1 || n > len(rows) {
		return "нет такого протокола", false
	}
	target := rows[n-1]

	fmt.Printf("\n  %sновый порт для «%s», 1–65535 (Enter — отмена)%s\n", dim, target.name, reset)
	val, ok := m.ask("Порт")
	if !ok || val == "" {
		return "отменено", false
	}
	p, err := parsePort(val)
	if err != nil {
		return err.Error(), false
	}
	if !m.applyPort(target.proto, p) {
		return "порт записан, но движок не отозвался — загляни в «Проверку»", false
	}
	return fmt.Sprintf("%s теперь на %d%s", target.name, p, linkTail(m.e)), true
}

// autoPort picks the protocol first, then hands the search to the prober.
func (m *menu) autoPort() (string, bool) {
	rows := portRows(m.e)
	m.page("Подобрать порт автоматически")
	for i, r := range rows {
		fmt.Printf("  %s%d%s  %-18s %s%d%s/%s\n", bold, i+1, reset, r.name, bold, r.port, reset, r.kind)
	}
	fmt.Println()
	which, ok := m.ask("Какому протоколу (Enter — отмена)")
	if !ok || which == "" {
		return "отменено", false
	}
	n, err := strconv.Atoi(which)
	if err != nil || n < 1 || n > len(rows) {
		return "нет такого протокола", false
	}
	target := rows[n-1]

	port, applied, msg := m.pickPortFlow(target.proto, target.name, target.kind == "udp", "Поставить? y/n")
	if !applied {
		return msg, false
	}
	return fmt.Sprintf("%s теперь на %d%s", target.name, port, linkTail(m.e)), true
}

func linkTail(e *env) string {
	if e.cfg.SubOn() {
		return " — у людей обновится само"
	}
	return " — раздай ссылки заново"
}

// dns is one setting for the whole server: which resolver every client uses.
func (m *menu) dns() {
	sec := &section{
		title: "DNS",
	}
	m.walk(sec, func(s *section) {
		s.rows = []row{
			{label: "Cloudflare", value: "1.1.1.1",
				do: func() (string, bool) { return m.setDNS("1.1.1.1") }},
			{label: "Quad9", value: "9.9.9.9",
				do: func() (string, bool) { return m.setDNS("9.9.9.9") }},
			{label: "AdGuard", value: "94.140.14.14",
				do: func() (string, bool) { return m.setDNS("94.140.14.14") }},
			{label: "Свой", do: m.askDNS},
		}
		for i := range s.rows {
			if s.rows[i].value == m.e.cfg.DNS {
				s.rows[i].value += "  · стоит сейчас"
			}
		}
	})
	m.msg = ""
}

func (m *menu) askDNS() (string, bool) {
	fmt.Printf("\n  %sадрес вида 1.1.1.1 (Enter — отмена)%s\n", dim, reset)
	val, ok := m.ask("DNS")
	if !ok || val == "" {
		return "отменено", false
	}
	if !config.ValidIP(val) {
		return "это не похоже на IP — нужен вид 1.1.1.1", false
	}
	return m.setDNS(val)
}

func (m *menu) setDNS(ip string) (string, bool) {
	if ip == m.e.cfg.DNS {
		return "и так стоит " + ip, false
	}
	m.e.cfg.DNS = ip
	if !saveQuiet(m.e, store.ProtoHy2, store.ProtoReality, store.ProtoEnc) {
		return "DNS записан, но движок не отозвался", false
	}
	return "DNS теперь " + ip, true
}

// masquerade is the site every protocol pretends to be. Three ready answers and
// a place to type your own — the prompt already carries the www., the way it
// always did.
func (m *menu) masquerade() {
	// Three examples, no captions: the domain says what it is, and a line of
	// explanation next to each only makes the screen louder.
	presets := []string{"www.cloudflare.com", "dl.google.com", "www.apple.com"}
	status := ""
	for {
		m.page("SNI")
		for i, p := range presets {
			mark := ""
			if p == m.e.cfg.SNI {
				mark = "  · стоит сейчас"
			}
			fmt.Printf("  %s%d%s  %s%s\n", bold, i+1, reset, p, mark)
		}
		fmt.Printf("\n  сейчас: %s\n", m.e.cfg.SNI)
		if status != "" {
			fmt.Printf("\n  %s\n", status)
		}
		fmt.Printf("\n  Выбери номер из списка или впиши свой домен\n\n")
		fmt.Print("  SNI (Enter — назад): www.")

		line, err := m.in.ReadString('\n')
		if err != nil && line == "" {
			m.msg = ""
			return
		}
		val := strings.TrimSpace(line)
		if val == "" {
			m.msg = ""
			return
		}
		if n, e := strconv.Atoi(val); e == nil {
			if n < 1 || n > len(presets) {
				status = "нет такого номера"
				continue
			}
			status, _ = m.setMask(presets[n-1])
			continue
		}
		if strings.ContainsAny(val, " /:") {
			status = "нужен домен, без https:// и без порта"
			continue
		}
		domain := withWWW(val)
		fmt.Printf("  %sпроверяю %s…%s\n", dim, domain, reset)
		if err := checkSNI(domain); err != nil {
			status = err.Error()
			continue
		}
		status, _ = m.setMask(domain)
	}
}

// setMask puts one domain on the whole server.
func (m *menu) setMask(domain string) (string, bool) {
	if domain == m.e.cfg.SNI {
		return "и так притворяемся " + domain, false
	}
	m.e.cfg.SetSNI(domain)
	if !saveQuiet(m.e, store.ProtoHy2, store.ProtoReality) {
		return "записано, но движок не отозвался", false
	}
	return "теперь притворяемся " + domain + " — раздай ссылки заново", true
}

func (m *menu) askSNI(onEmpty string) (string, bool) {
	for {
		if onEmpty != "" {
			// The domain is the state; the words around it are commentary.
			fmt.Printf("  %sEnter — общий для сервера, сейчас %s%s%s\n\n", dim, reset, onEmpty, reset)
		} else {
			fmt.Printf("  %sEnter — оставить как есть%s\n\n", dim, reset)
		}
		fmt.Print("  Домен (0 — выход): ")
		line, err := m.in.ReadString('\n')
		if err != nil && line == "" {
			return "", false
		}
		v := strings.TrimSpace(line)
		if quit(v) {
			return "", false
		}
		if v == "" {
			if onEmpty == "" {
				m.msg = ""
				return "", false
			}
			return onEmpty, true
		}
		if strings.ContainsAny(v, " /:") {
			fmt.Printf("\n  %sнужен домен, без https:// и без порта%s\n\n", dim, reset)
			continue
		}
		domain := withWWW(v)
		fmt.Printf("  %sпроверяю %s…%s\n", dim, domain, reset)
		if err := checkSNI(domain); err != nil {
			fmt.Printf("\n  %v\n\n", err)
			continue
		}
		return domain, true
	}
}

func (m *menu) page(title string) {
	fmt.Print(clearScreen)
	fmt.Printf("\n  %s%s%s\n\n", bold, title, reset)
}

func (m *menu) note(lines ...string) {
	for _, l := range lines {
		fmt.Printf("  %s%s%s\n", dim, l, reset)
	}
	fmt.Println()
}

func (m *menu) wait() {
	fmt.Printf("\n  %sEnter — назад%s ", dim, reset)
	_, _ = m.in.ReadString('\n')
}

func saveQuiet(e *env, protos ...string) bool {
	if err := e.cfg.Save(); err != nil {
		return false
	}
	return applyProtos(e, protos...) == nil
}

func errText(err error) string {
	if err == nil {
		return "не получилось"
	}
	return err.Error()
}

func withWWW(d string) string {
	if strings.HasPrefix(d, "www.") || strings.Count(d, ".") != 1 {
		return d
	}
	return "www." + d
}
