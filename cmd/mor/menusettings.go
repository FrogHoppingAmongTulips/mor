package main

import (
	"fmt"
	"strconv"
	"strings"

	"mor/internal/config"
	"mor/internal/store"
	"mor/internal/webauth"
)

// The menu screens about the server itself: which ports it listens on, which
// resolver its clients use, and which site it pretends to be.

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
	if !saveQuiet(m.e, store.ProtoHy2, store.ProtoReality) {
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

// panel is where the web panel gets its password. Before this existed the
// only way to switch it on was `panel password …` from the help text, and the
// menu — which is what most people ever see — said nothing about the panel at
// all until it was already running.
func (m *menu) panel() {
	sec := &section{title: "Пароль"}
	m.walk(sec, func(s *section) {
		c := m.e.cfg
		// The password and the address are already in the header of every
		// screen; repeating them here would be the same two lines twice.
		// The certificate renews itself through acme.sh's cron and is raised
		// only by "Проверка", and only when it is actually broken.
		// The port carries its number in the label rather than a value column:
		// with three rows there is nothing to line it up against.
		s.rows = []row{
			{label: "Случайный пароль", do: m.rollPanelPassword},
			{label: "Свой пароль", do: m.askPanelPassword},
			{label: "Порт " + strconv.Itoa(c.WebPort), do: m.askPanelPort},
		}
	})
	m.msg = ""
}

func (m *menu) askPanelPassword() (string, bool) {
	fmt.Printf("\n  %sот 8 знаков, вводится не отображаясь (Enter — отмена)%s\n", dim, reset)
	val, ok := m.askHidden("Пароль")
	if !ok || val == "" {
		return "отменено", false
	}
	if len([]rune(val)) < 8 {
		return "слишком короткий — от 8 знаков", false
	}
	return m.savePanelPassword(val)
}

func (m *menu) savePanelPassword(pw string) (string, bool) {
	m.e.cfg.SetWebPassword(pw)
	if err := m.e.cfg.Save(); err != nil {
		return err.Error(), false
	}
	ensureFirewall(m.e)
	// The daemon reads the web settings once at startup, so a password set
	// here only takes effect after it comes back up.
	restartSelf()
	return pw, true
}

// rollPanelPassword replaces the password with a fresh generated one — the
// answer to a password that leaked or was shown to the wrong person.
func (m *menu) rollPanelPassword() (string, bool) {
	pw := webauth.NewPassword()
	if pw == "" {
		return "не удалось сгенерировать", false
	}
	return m.savePanelPassword(pw)
}

func (m *menu) askPanelPort() (string, bool) {
	fmt.Printf("\n  %sномер от 1 до 65535 (Enter — отмена)%s\n", dim, reset)
	val, ok := m.ask("Порт")
	if !ok || val == "" {
		return "отменено", false
	}
	p, err := parsePort(val)
	if err != nil {
		return err.Error(), false
	}
	m.e.cfg.WebPort = p
	if err := m.e.cfg.Save(); err != nil {
		return err.Error(), false
	}
	ensureFirewall(m.e)
	restartSelf()
	return "порт " + val, true
}
