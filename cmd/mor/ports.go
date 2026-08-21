package main

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"mor/internal/config"
	"mor/internal/probe"
	"mor/internal/store"
)

// Choosing a port the outside world can actually reach. A port free on this
// machine can still be cut somewhere on the way, so every candidate is probed
// from elsewhere before it is offered.

// findPort returns the first candidate that is free here and not filtered on the
// way. Nothing listens on a candidate yet, so "connection refused" is the good
// answer: the packet reached the server. A timeout means somebody dropped it.
func findPort(ctx context.Context, host string, busy map[int]bool, udp bool, say func(string)) (int, error) {
	for _, p := range candidates {
		if busy[p] || listening(p, udp) {
			if say != nil {
				say(fmt.Sprintf("%d — занят", p))
			}
			continue
		}
		if udp {
			// Nobody can probe UDP from outside, so a free port is the best
			// answer available.
			return p, nil
		}
		res, err := probe.TCP(ctx, host, p, 2, nil)
		if err != nil {
			return 0, err
		}
		reached := 0
		for _, r := range res {
			if r.Reached {
				reached++
			}
		}
		if reached > 0 {
			if say != nil {
				say(fmt.Sprintf("%d — доходит", p))
			}
			return p, nil
		}
		if say != nil {
			say(fmt.Sprintf("%d — не доходит, режут по дороге", p))
		}
	}
	return 0, nil
}

// pickPortFlow scans for a free, externally-reachable port for one protocol,
// shows progress, confirms with the person and applies the change. It is the
// one place that does this — the "Ports" screen and the broken-port repair
// flow only differ in how the target protocol got picked and what to ask
// before applying, both passed in.
func (m *menu) pickPortFlow(proto, name string, udp bool, confirmQuestion string) (port int, applied bool, msg string) {
	m.page("Подбираю порт для «" + name + "»")
	m.note("Перебираю привычные порты, проверяю каждый снаружи. До минуты.")

	// Every configured port counts as busy, on or off: an off protocol still
	// owns its port in the config, and the subscription server owns its own.
	busy := map[int]bool{}
	for _, r := range portRows(m.e) {
		busy[r.port] = true
	}
	busy[m.e.cfg.SubPort] = true

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	p, err := findPort(ctx, m.e.cfg.PublicHost, busy, udp, func(line string) {
		fmt.Printf("  %s%s%s\n", dim, line, reset)
	})
	if err != nil {
		return 0, false, "проверка снаружи не отвечает — попробуй позже"
	}
	if p == 0 {
		return 0, false, "свободного рабочего порта не нашлось — смени вручную"
	}

	fmt.Printf("\n  %sподходит %d%s\n\n", bold, p, reset)
	answer, _ := m.ask(confirmQuestion)
	if !yes(answer) {
		return 0, false, "отменено"
	}
	if !m.applyPort(proto, p) {
		return 0, false, "порт сохранён, но движок не отозвался"
	}
	return p, true, ""
}

func (m *menu) pickPort(broken []result) {
	target := broken[0]
	if len(broken) > 1 {
		m.page("Подобрать порт")
		for i, r := range broken {
			fmt.Printf("  %s%d%s  %-18s сейчас %d\n", bold, i+1, reset, r.target.name, r.target.port)
		}
		fmt.Println()
		choice, ok := m.ask("Какому протоколу")
		if !ok || choice == "" {
			m.msg = "отменено"
			return
		}
		n, err := strconv.Atoi(choice)
		if err != nil || n < 1 || n > len(broken) {
			m.msg = "нет такого протокола"
			return
		}
		target = broken[n-1]
	}

	port, applied, msg := m.pickPortFlow(target.target.proto, target.target.name, target.target.udp, "Переставить? y/n")
	if !applied {
		m.msg = msg
		return
	}

	// Show the result on its own screen: a one-line message under the menu is
	// too easy to miss, and this change matters.
	m.page("Порт сменён")
	fmt.Printf("  %-18s %d/%s\n\n", target.target.name, port, kindOf(target.target.udp))
	if m.e.cfg.SubOn() {
		m.note("Ссылки у людей обновятся сами в течение часа — раздавать заново не нужно.")
	} else {
		m.note("Ссылки изменились — раздай их заново.")
	}
	m.wait()
}

func kindOf(udp bool) string {
	if udp {
		return "udp"
	}
	return "tcp"
}

// setPort points one protocol at a new port. Keyed by proto, not by display
// name: names change wording, identities do not.
func setPort(cfg *config.Config, proto string, port int) {
	switch proto {
	case store.ProtoReality:
		cfg.Reality.Port = port
	case store.ProtoSS:
		cfg.SS.Port = port
	default:
		cfg.VPNPort = port
	}
}

// applyPort moves one protocol to a new port and restarts what serves it. The
// firewall is opened for the new port here: the installer opened the old one,
// and nothing else would ever open this one — the port would look filtered by
// the carrier when in fact this machine is holding it shut.
func (m *menu) applyPort(proto string, port int) bool {
	setPort(m.e.cfg, proto, port)
	if !saveQuiet(m.e, proto) {
		return false
	}
	ensureFirewall(m.e)
	return true
}
