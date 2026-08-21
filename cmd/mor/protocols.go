package main

import (
	"fmt"
	"net"
	"strconv"
	"strings"

	"mor/internal/config"
	"mor/internal/hysteria"
	"mor/internal/keys"
	"mor/internal/priv"
	"mor/internal/store"
	"mor/internal/xray"
)

// protoList is every protocol mor knows, in the order they are shown.
var protoList = []string{store.ProtoHy2, store.ProtoReality, store.ProtoSS}

// baseProtocols come with mor and always run.
var baseProtocols = []string{store.ProtoHy2, store.ProtoReality, store.ProtoSS}

// unitOf names the systemd service behind a protocol. Reality and Shadowsocks
// share Xray, so switching one off must not stop the other.
func unitOf(proto string) string {
	switch proto {
	case store.ProtoHy2:
		return hysteria.Service
	default:
		return xray.Service
	}
}

// applyProtos writes configs and brings each engine to the state the config asks
// for: running when the protocol is on, stopped when it is off. Reality and
// Shadowsocks share one Xray, so it only stops when both are off — otherwise the
// switched-off one simply loses its inbound.
func applyProtos(e *env, protos ...string) error {
	users := e.live()
	xrayTouched := false
	for _, p := range protos {
		switch p {
		case store.ProtoReality, store.ProtoSS:
			xrayTouched = true
			continue
		}
		if !e.cfg.On(p) {
			if err := stopUnit(hysteria.Service, protoInstalled(p)); err != nil {
				return err
			}
			continue
		}
		if err := e.hy.Apply(); err != nil {
			return err
		}
		enable(hysteria.Service)
	}
	if !xrayTouched || !xray.Installed() {
		return nil
	}
	if !e.cfg.On(store.ProtoReality) && !e.cfg.On(store.ProtoSS) {
		return stopUnit(xray.Service, true)
	}
	if err := e.xr.Apply(users); err != nil {
		return err
	}
	enable(xray.Service)
	return nil
}

func stopUnit(unit string, installed bool) error {
	if !installed {
		return nil
	}
	out, err := priv.Command("systemctl", "disable", "--now", unit).CombinedOutput()
	if err != nil {
		return fmt.Errorf("остановить %s: %w: %s", unit, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func enable(unit string) {
	_ = priv.Command("systemctl", "enable", unit).Run()
}

// portRow is one line of the ports screen: what listens where. The proto is the
// identity — screens show the name, but changes are keyed by proto, so renaming
// a protocol can never point a change at the wrong port.
type portRow struct {
	proto string
	name  string
	port  int
	kind  string
}

func portRows(e *env) []portRow {
	rows := []portRow{
		{store.ProtoHy2, store.ProtoName(store.ProtoHy2), e.cfg.VPNPort, "udp"},
		{store.ProtoReality, store.ProtoName(store.ProtoReality), e.cfg.Reality.Port, "tcp"},
		{store.ProtoSS, store.ProtoName(store.ProtoSS), e.cfg.SS.Port, "tcp"},
	}
	return rows
}

func parsePort(s string) (int, error) {
	p, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || p < 1 || p > 65535 {
		return 0, fmt.Errorf("порт — число от 1 до 65535")
	}
	return p, nil
}

func isDomain(host string) bool {
	h := strings.TrimSpace(host)
	return h != "" && net.ParseIP(h) == nil
}

// menu: protocols

// protocols answers one question — what runs here — and offers the two things
// that can be changed about it. No explanations: the person reading them was
// trying to learn a mechanism they never needed, and gave up understanding
// anything. What each protocol is belongs in the README, not in a menu.
func (m *menu) protocols() {
	sec := &section{title: "Протоколы"}
	m.walk(sec, func(s *section) {
		rows := make([]row, 0, len(baseProtocols)+3)
		for _, p := range baseProtocols {
			proto := p // each row closes over its own protocol, not the loop var
			rows = append(rows, row{
				label: store.ProtoName(proto),
				value: pair("вкл", "выкл", m.e.cfg.On(proto)),
				do:    func() (string, bool) { return m.toggleProto(proto) },
			})
		}
		rows = append(rows,
			row{sep: true},
			row{label: "Способ связи Reality",
				value: pair("первый", "второй", m.e.cfg.Reality.Wire() == config.TransportTCP),
				do:    m.toggleTransport},
			row{label: "Прятать Hysteria2",
				value: pair("да", "нет", m.e.cfg.HyObfs != ""),
				do:    m.toggleObfs},
		)
		s.rows = rows
	})
	m.msg = ""
}

// toggleProto turns one protocol on or off. Switching off the last one running
// would leave nobody able to connect at all, so that specific case asks twice.
func (m *menu) toggleProto(proto string) (string, bool) {
	name := store.ProtoName(proto)
	on := !m.e.cfg.On(proto)
	if !on && lastOn(m.e.cfg, proto) {
		m.page(name)
		m.note("Это последний включённый протокол.",
			"Выключив его, подключиться не сможет никто, пока не включишь что-то обратно.")
		answer, _ := m.ask("Всё равно выключить? y/n")
		if !yes(answer) {
			return "отменено", false
		}
	}
	m.e.cfg.SetOn(proto, on)
	if err := m.e.cfg.Save(); err != nil {
		return err.Error(), false
	}
	if err := applyProtos(m.e, proto); err != nil {
		return "сменено, но движок не отозвался — загляни в «Проверку»", false
	}
	state := "включён"
	if !on {
		state = "выключен"
	}
	return name + " теперь " + state, true
}

// lastOn reports whether proto is the only protocol currently running.
func lastOn(cfg *config.Config, proto string) bool {
	for _, p := range baseProtocols {
		if p != proto && cfg.On(p) {
			return false
		}
	}
	return true
}

// wireName numbers the two ways Reality can work instead of describing them.
// The description was honest and useless: nobody can tell from a sentence which
// one will survive in their network, and the only real instruction is "if it
// stopped working, try the other one".
func wireName(w string) string {
	if w == config.TransportXHTTP {
		return "второй"
	}
	return "первый"
}

// toggleObfs says what the setting is for before changing it. The row shows
// both sides, but the sides mean nothing to someone who has not met them — and
// flipping a setting you cannot name is guessing, not choosing.
func (m *menu) toggleObfs() (string, bool) {
	on := m.e.cfg.HyObfs == ""
	m.page("Прятать Hysteria2 от провайдера")
	// The links warning is not here: it belongs to a change that happened, and
	// it turns up under the list afterwards, next to where "отменено" would be.
	m.note(
		"По умолчанию провайдер видит, что ты пользуешься Hysteria2.",
		"Если прятать — не увидит.")

	ask := "Начать прятать? y/n"
	if !on {
		ask = "Перестать прятать? y/n"
	}
	answer, _ := m.ask(ask)
	if !yes(answer) {
		return "отменено", false
	}
	return m.setObfs(on)
}

func (m *menu) toggleTransport() (string, bool) {
	to := config.TransportXHTTP
	if m.e.cfg.Reality.Wire() == config.TransportXHTTP {
		to = config.TransportTCP
	}
	m.page("Способ связи Reality")
	m.note(
		"Первый способ. Приложение открывает одно соединение и держит его часами.",
		"Всё качается через него.",
		"",
		"Второй способ. То же самое режется на поток коротких запросов —",
		"как будто человек листает страницы сайта, а не сидит на одном.")

	answer, _ := m.ask("Переключить на " + wireName(to) + " способ? y/n")
	if !yes(answer) {
		return "отменено", false
	}
	return m.setTransport(to)
}

// pair shows a two-way setting whole: both sides on one line, the one in force
// lit and the other greyed. The state and the alternative in the same glance.
func pair(a, b string, first bool) string {
	if first {
		return a + dim + "/" + b + reset
	}
	return dim + a + "/" + reset + b
}

func (m *menu) setObfs(on bool) (string, bool) {
	m.e.cfg.HyObfs = ""
	if on {
		m.e.cfg.HyObfs = keys.Token()
	}
	if err := m.e.cfg.Save(); err != nil {
		return err.Error(), false
	}
	if err := applyProtos(m.e, store.ProtoHy2); err != nil {
		return "сменено, но движок не отозвался — загляни в «Проверку»", false
	}
	if m.e.cfg.HyObfs == "" {
		return "больше не прячем — раздай ссылки заново", true
	}
	return "теперь прячем — раздай ссылки заново", true
}

func (m *menu) setTransport(wire string) (string, bool) {
	m.e.cfg.Reality.Transport = wire
	if err := m.e.cfg.Save(); err != nil {
		return err.Error(), false
	}
	if err := applyProtos(m.e, store.ProtoReality); err != nil {
		return "сменено, но движок не отозвался — загляни в «Проверку»", false
	}
	return "теперь " + wireName(wire) + " способ — раздай ссылки заново", true
}

func cmdHost(args []string) {
	e, err := load()
	if err != nil {
		fmt.Println(err)
		return
	}
	if len(args) == 0 {
		fmt.Printf("  сейчас: %s\n", e.cfg.PublicHost)
		fmt.Println("  сменить: host vpn.example.com — домен переживает переезд, IP нет")
		return
	}
	val := strings.TrimSpace(args[0])
	if !config.ValidHost(val) {
		fmt.Println("  нужен публичный IP или домен, напр. host vpn.example.com")
		return
	}
	e.cfg.PublicHost = val
	if err := e.cfg.Save(); err != nil {
		fmt.Println(" ", err)
		return
	}
	fmt.Printf("  адрес → %s · раздай ссылки заново\n", val)
	if err := applyProtos(e, protoList...); err != nil {
		fmt.Printf("  движок не перезапустился: %v — проверь mor status\n", err)
	}
}

// cmdProto lists what runs and, with an argument, switches one on or off —
// the CLI twin of the "Протоколы" menu screen, for scripts and SSH one-liners.
func cmdProto(args []string) {
	e, err := load()
	if err != nil {
		fmt.Println(err)
		return
	}
	if len(args) >= 2 && (args[0] == "on" || args[0] == "off") {
		setProtoCLI(e, args[0] == "on", args[1])
		return
	}
	fmt.Println("\n  стоят на сервере")
	for _, p := range baseProtocols {
		state := "выключен"
		if e.cfg.On(p) {
			state = "работает"
		}
		fmt.Printf("     %-18s %s\n", store.ProtoName(p), state)
	}
	fmt.Printf("\n  включить/выключить: proto on <id> · proto off <id> · id: %s\n", strings.Join(baseProtocols, ", "))
	fmt.Println()
}

func setProtoCLI(e *env, on bool, id string) {
	valid := false
	for _, p := range baseProtocols {
		valid = valid || p == id
	}
	if !valid {
		fmt.Printf("  нет протокола %q — id: %s\n", id, strings.Join(baseProtocols, ", "))
		return
	}
	if !on && lastOn(e.cfg, id) {
		fmt.Println("  это последний включённый протокол — выключив его, подключиться не сможет никто")
	}
	e.cfg.SetOn(id, on)
	if err := e.cfg.Save(); err != nil {
		fmt.Println(" ", err)
		return
	}
	if err := applyProtos(e, id); err != nil {
		fmt.Printf("  сохранено, но движок не отозвался: %v — проверь mor status\n", err)
		return
	}
	state := "включён"
	if !on {
		state = "выключен"
	}
	fmt.Printf("  %s теперь %s\n", store.ProtoName(id), state)
}
