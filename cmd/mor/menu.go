package main

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"mor/internal/config"
	"mor/internal/period"
	"mor/internal/stats"
	"mor/internal/store"
	"mor/internal/xray"
)

const (
	clearScreen = "\x1b[H\x1b[2J"
	dim         = "\x1b[2m"
	bold        = "\x1b[1m"
	reset       = "\x1b[0m"
)

var menuItems = []struct{ key, title string }{
	{"1", "Создать ключ"},
	{"2", "Временный ключ"},
	{"3", "Список ключей"},
	{"4", "Показать ключ"},
	{"5", "Удалить ключ"},
	{"6", "Статистика"},
	{"7", "DNS"},
	{"8", "SNI"},
	{"9", "Порты"},
	{"10", "Состояние"},
	{"0", "Выход"},
}

type menu struct {
	e   *env
	in  *bufio.Reader
	msg string
}

func interactive() bool {
	fi, err := os.Stdin.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

func runMenu() {
	e, err := load()
	if err != nil {
		fmt.Printf("нет конфига — сначала установи: mor setup (%v)\n", err)
		os.Exit(1)
	}
	m := &menu{e: e, in: bufio.NewReader(os.Stdin)}
	for {
		if fresh, err := load(); err == nil {
			m.e = fresh
		}
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
	fmt.Printf("\n  %sMOR%s\n\n", bold, reset)

	for _, it := range menuItems {
		fmt.Printf("  %s%s%s  %s\n", bold, it.key, reset, it.title)
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
		m.createKey(false)
	case "2":
		m.createKey(true)
	case "3":
		m.listKeys()
	case "4":
		m.showKey()
	case "5":
		m.deleteKey()
	case "6":
		m.showStats()
	case "7":
		m.setDNS()
	case "8":
		m.setSNI()
	case "9":
		m.setPorts()
	case "10":
		m.status()
	default:
		m.msg = "нет такого пункта"
	}
}

func (m *menu) createKey(temporary bool) {
	title := "Создать ключ"
	if temporary {
		title = "Временный ключ"
	}
	m.page(title)
	m.note("Ключ — это доступ для одного устройства: телефона, ноутбука.",
		"Каждый ключ работает по одному протоколу, поэтому сначала выбери его.")
	fmt.Printf("  %s1%s  Hysteria2      самый быстрый, подойдёт в большинстве сетей\n", bold, reset)
	fmt.Printf("  %s2%s  VLESS Reality  проходит там, где Hysteria2 молчит%s\n\n", bold, reset, note(xray.Installed()))

	choice, ok := m.ask("Протокол")
	if !ok || choice == "" {
		m.msg = "отменено"
		return
	}
	proto := store.ProtoHy2
	switch choice {
	case "1":
	case "2":
		proto = store.ProtoReality
	default:
		m.msg = "нет такого протокола"
		return
	}

	m.page("Создать ключ · " + store.ProtoName(proto))
	m.note("Имя нужно только тебе — чтобы потом понять, чьё это устройство.")
	name, ok := m.ask("Имя ключа")
	if !ok || name == "" {
		m.msg = "отменено"
		return
	}

	m.page("Создать ключ · " + name)
	m.note("SNI — сайт, которым будет притворяться это подключение.")
	sni, ok := m.askSNI(config.DefaultSNI)
	if !ok {
		if m.msg == "" {
			m.msg = "отменено"
		}
		return
	}
	var until time.Time
	if temporary {
		m.page("Временный ключ · " + name)
		m.note("Сколько ключ должен работать: число и единица.",
			"h — часы, d — дни, m — месяцы, y — годы. Можно словом: 12hour, 5days.",
			"Считает как есть: 56h — это 2 дня 8 часов.")
		sp, ok2 := m.askPeriod()
		if !ok2 {
			if m.msg == "" {
				m.msg = "отменено"
			}
			return
		}
		until = sp.Add(time.Now())
	}

	u, err := createKey(m.e, name, proto, sni)
	if u == nil {
		m.msg = err.Error()
		return
	}
	if temporary {
		if err := m.e.st.SetExpiry(u.ID, until); err != nil {
			m.msg = err.Error()
			return
		}
		u.ExpiresAt = until
	}
	m.page("Ключ «" + name + "» создан")
	if err != nil {
		fmt.Printf("  предупреждение: %v\n", err)
	}
	if temporary {
		m.note("Ключ перестанет работать сам: " + period.Left(u.ExpiresAt, time.Now()) + ".")
	}
	m.note("Ниже ссылка: отсканируй QR в приложении или скопируй строку целиком.")
	m.printKey(u)
	m.wait()
	m.msg = "«" + name + "» создан"
}

func (m *menu) askPeriod() (period.Span, bool) {
	for {
		val, ok := m.ask("Срок")
		if !ok || val == "" {
			return period.Span{}, false
		}
		sp, err := period.Parse(val)
		if err != nil {
			fmt.Printf("\n  %v\n\n", err)
			continue
		}
		fmt.Printf("  %sэто %s%s\n", dim, sp.String(), reset)
		return sp, true
	}
}

func (m *menu) showStats() {
	u, ok := m.pickKey("Статистика")
	if !ok {
		return
	}
	e := m.e.stats.Get(u.ID)
	now := time.Now()

	m.page("Статистика · «" + u.Name + "»")
	state := period.Ago(e.LastSeen, now)
	if !e.LastSeen.IsZero() && now.Sub(e.LastSeen) < 3*time.Minute {
		state = "работает прямо сейчас"
	}
	fmt.Printf("  протокол    %s\n", store.ProtoName(u.Proto))
	fmt.Printf("  заходил     %s\n", state)
	if !u.ExpiresAt.IsZero() {
		fmt.Printf("  срок        %s\n", period.Left(u.ExpiresAt, now))
	}
	fmt.Printf("  потрачено   %s\n", stats.Human(e.Total))

	months := e.MonthsSorted()
	if len(months) > 0 {
		fmt.Println()
		for i, mu := range months {
			if i >= 12 {
				break
			}
			fmt.Printf("  %-16s %s\n", stats.MonthName(mu.Month), stats.Human(mu.Bytes))
		}
	}
	if e.Total == 0 {
		fmt.Println()
		m.note("Пока пусто. Данные появляются в течение минуты после того,",
			"как ключом начали пользоваться.")
	}
	m.wait()
	m.msg = ""
}

func note(installed bool) string {
	if installed {
		return ""
	}
	return dim + " (не установлен)" + reset
}

func (m *menu) listKeys() {
	m.page("Ключи")
	users := m.e.st.List()
	if len(users) == 0 {
		m.note("Пока пусто. Ключи создаются в первом пункте меню.")
	} else {
		m.note(fmt.Sprintf("Всего ключей: %d.", len(users)))
		now := time.Now()
		for i, u := range users {
			tail := period.Left(u.ExpiresAt, now)
			if tail == "" {
				tail = period.Ago(m.e.stats.Get(u.ID).LastSeen, now)
			}
			fmt.Printf("  %s%d%s  %-22s %-14s %s%s%s\n", bold, i+1, reset, u.Name, store.ProtoName(u.Proto), dim, tail, reset)
		}
	}
	m.wait()
	m.msg = ""
}

func (m *menu) showKey() {
	u, ok := m.pickKey("Показать ключ")
	if !ok {
		return
	}
	m.page("Ключ «" + u.Name + "» · " + store.ProtoName(u.Proto))
	m.note("Ссылка для приложения: Hiddify, Streisand, Happ, NekoBox, Shadowrocket.")
	m.printKey(u)
	m.wait()
	m.msg = ""
}

func (m *menu) deleteKey() {
	users := m.e.st.List()
	m.page("Удалить ключ")
	if len(users) == 0 {
		m.note("Ключей нет.")
		m.wait()
		m.msg = ""
		return
	}
	m.note("Можно сразу несколько: номера через пробел, например 1 3 4.")
	for i, u := range users {
		fmt.Printf("  %s%d%s  %-22s %s%s%s\n", bold, i+1, reset, u.Name, dim, store.ProtoName(u.Proto), reset)
	}
	fmt.Println()

	val, ok := m.ask("Номера ключей")
	if !ok || val == "" {
		m.msg = "отменено"
		return
	}
	picked, bad := pickNumbers(val, users)
	if bad != "" {
		m.msg = "нет ключа с номером " + bad
		return
	}
	if len(picked) == 0 {
		m.msg = "отменено"
		return
	}

	m.page("Удалить " + plural(len(picked)) + "?")
	for _, u := range picked {
		fmt.Printf("  %s\n", u.Name)
	}
	fmt.Println()
	m.note("Эти устройства потеряют доступ сразу. Вернуть те же ключи нельзя.")
	answer, _ := m.ask("Удалить? y/n")
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "y", "yes", "д", "да":
	default:
		m.msg = "отменено"
		return
	}

	done := 0
	for _, u := range picked {
		if err := removeKey(m.e, u); err != nil {
			m.msg = err.Error()
			return
		}
		done++
	}
	if done == 1 {
		m.msg = "«" + picked[0].Name + "» удалён"
		return
	}
	m.msg = "удалено: " + plural(done)
}

func pickNumbers(input string, users []*store.User) ([]*store.User, string) {
	seen := map[int]bool{}
	out := []*store.User{}
	for _, f := range strings.Fields(input) {
		n, err := strconv.Atoi(f)
		if err != nil || n < 1 || n > len(users) {
			return nil, f
		}
		if seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, users[n-1])
	}
	return out, ""
}

func plural(n int) string {
	word := "ключей"
	switch {
	case n%10 == 1 && n%100 != 11:
		word = "ключ"
	case n%10 >= 2 && n%10 <= 4 && (n%100 < 12 || n%100 > 14):
		word = "ключа"
	}
	if n == 1 {
		return word
	}
	return fmt.Sprintf("%d %s", n, word)
}

func (m *menu) setDNS() {
	m.page("DNS")
	m.note("DNS — справочник, который превращает имя сайта в адрес сервера.",
		"Этот сервер видит, какие сайты ты открываешь, а местами ещё и подменяет ответы,",
		"поэтому DNS хостера не используется. Менять есть смысл, если текущий тормозит.")
	fmt.Printf("  сейчас: %s\n\n", m.e.cfg.DNS)
	fmt.Printf("  %s1%s  Cloudflare     1.1.1.1        быстрый, стоит по умолчанию\n", bold, reset)
	fmt.Printf("  %s2%s  Quad9          9.9.9.9        режет вредоносные сайты\n", bold, reset)
	fmt.Printf("  %s3%s  AdGuard        94.140.14.14   режет рекламу\n\n", bold, reset)

	val, ok := m.ask("Номер или свой IP")
	if !ok || val == "" {
		m.msg = "отменено"
		return
	}
	switch val {
	case "1":
		val = "1.1.1.1"
	case "2":
		val = "9.9.9.9"
	case "3":
		val = "94.140.14.14"
	default:
		if !config.ValidIP(val) {
			m.msg = "нужен номер или IP"
			return
		}
	}
	m.e.cfg.DNS = val
	m.save("DNS "+val, store.ProtoHy2, store.ProtoReality)
}

func (m *menu) setSNI() {
	u, ok := m.pickKey("SNI")
	if !ok {
		return
	}
	cur := u.SNI
	if cur == "" {
		cur = m.e.cfg.SNI
		if u.Proto == store.ProtoReality {
			cur = m.e.cfg.Reality.Dest
		}
	}

	m.page("SNI ключа «" + u.Name + "»")
	m.note("SNI — сайт, которым притворяется подключение этого ключа.",
		"У каждого ключа он может быть свой. После смены раздай ссылку заново.")
	fmt.Printf("  сейчас: %s\n\n", cur)

	val, ok := m.askSNI("")
	if !ok {
		return
	}
	if err := m.e.st.SetSNI(u.ID, val); err != nil {
		m.msg = err.Error()
		return
	}
	if u.Proto == store.ProtoReality {
		if err := m.e.xr.Apply(m.e.st.List()); err != nil {
			m.msg = err.Error()
			return
		}
	}
	m.msg = "«" + u.Name + "» → " + val
}

func (m *menu) askSNI(onEmpty string) (string, bool) {
	for {
		if onEmpty != "" {
			fmt.Printf("  %sEnter — %s%s\n", dim, onEmpty, reset)
		} else {
			fmt.Printf("  %sEnter — оставить как есть%s\n", dim, reset)
		}
		fmt.Print("  SNI: www.")
		line, err := m.in.ReadString('\n')
		if err != nil && line == "" {
			return "", false
		}
		v := strings.TrimSpace(line)
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

func (m *menu) setPorts() {
	m.page("Порты")
	m.note("Порт — дверь, в которую стучится приложение. Операторы часто закрывают некоторые",
		"из них целиком: тогда сервер исправен, а телефон пишет «timeout». Помогает смена порта.",
		"После смены раздай ссылки заново — старые перестанут работать.")
	fmt.Printf("  %s1%s  Hysteria2   %d/udp\n", bold, reset, m.e.cfg.VPNPort)
	fmt.Printf("  %s2%s  Reality     %d/tcp\n\n", bold, reset, m.e.cfg.Reality.Port)

	which, ok := m.ask("Какой сменить")
	if !ok || (which != "1" && which != "2") {
		m.msg = "отменено"
		return
	}
	val, ok := m.ask("Новый порт (1–65535)")
	if !ok || val == "" {
		m.msg = "отменено"
		return
	}
	p, err := strconv.Atoi(val)
	if err != nil || p < 1 || p > 65535 {
		m.msg = "порт — число 1–65535"
		return
	}
	switch which {
	case "1":
		m.e.cfg.VPNPort = p
		m.save(fmt.Sprintf("Hysteria2 %d/udp, раздай ссылки заново", p), store.ProtoHy2)
	case "2":
		m.e.cfg.Reality.Port = p
		m.save(fmt.Sprintf("Reality %d/tcp, раздай ссылки заново", p), store.ProtoReality)
	}
}

func (m *menu) status() {
	m.page("Состояние")
	m.note("Если приложение пишет «timeout», сначала смотри сюда.",
		"Всё работает, а подключения нет — скорее всего сеть режет порт, смени его.")
	cmdStatus()
	m.wait()
	m.msg = ""
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
	fmt.Printf("\n  %sEnter — назад в меню%s ", dim, reset)
	_, _ = m.in.ReadString('\n')
}

func (m *menu) pickKey(title string) (*store.User, bool) {
	return m.pickKeyProto(title, "")
}

func (m *menu) pickKeyProto(title, proto string) (*store.User, bool) {
	all := m.e.st.List()
	users := make([]*store.User, 0, len(all))
	for _, u := range all {
		if proto == "" || u.Proto == proto {
			users = append(users, u)
		}
	}
	m.page(title)
	if len(users) == 0 {
		m.note("Подходящих ключей нет.")
		m.wait()
		m.msg = ""
		return nil, false
	}
	for i, u := range users {
		fmt.Printf("  %s%d%s  %-22s %s%s%s\n", bold, i+1, reset, u.Name, dim, store.ProtoName(u.Proto), reset)
	}
	fmt.Println()
	val, ok := m.ask("Номер ключа")
	if !ok || val == "" {
		m.msg = "отменено"
		return nil, false
	}
	n, err := strconv.Atoi(val)
	if err != nil || n < 1 || n > len(users) {
		m.msg = "нет такого ключа"
		return nil, false
	}
	return users[n-1], true
}

func (m *menu) printKey(u *store.User) {
	showKey(m.e, u)
}

func (m *menu) save(what string, protos ...string) {
	if !saveQuiet(m.e, protos...) {
		m.msg = "сохранено, но движок не перезапустился"
		return
	}
	m.msg = what
}

func saveQuiet(e *env, protos ...string) bool {
	if err := e.cfg.Save(); err != nil {
		return false
	}
	users := e.st.List()
	for _, p := range protos {
		var err error
		switch p {
		case store.ProtoHy2:
			err = e.hy.Apply()
		case store.ProtoReality:
			if xray.Installed() {
				err = e.xr.Apply(users)
			}
		}
		if err != nil {
			return false
		}
	}
	return true
}

func withWWW(d string) string {
	if strings.HasPrefix(d, "www.") || strings.Count(d, ".") != 1 {
		return d
	}
	return "www." + d
}
