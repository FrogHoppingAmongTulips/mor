package main

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Turning a check into an answer: what is wrong in words, and the one button
// that fixes what can be fixed without asking anything further.

func printResults(rows []result) {
	wide := 0
	for _, r := range rows {
		if n := len([]rune(r.target.name)); n > wide {
			wide = n
		}
	}
	for _, r := range rows {
		kind := "tcp"
		if r.target.udp {
			kind = "udp"
		}
		state := "работает"
		switch {
		case !r.engine:
			state = "движок не запущен"
		case !r.held:
			state = "порт никто не слушает"
		case r.remote != "":
			state = r.remote
		case r.target.udp:
			state = "работает · udp снаружи не проверить"
		}
		fmt.Printf("  %s %5d/%s  %-22s %s%s%s\n", pad(r.target.name, wide), r.target.port, kind, state, dim, r.notes, reset)
	}
}

// brokenOnes picks out the protocols that are actually down. The link feed is
// left out on purpose: it has its own line among the problems, and it not
// working is no reason to offer moving a protocol's port.
func brokenOnes(rows []result) []result {
	out := []result{}
	for _, r := range rows {
		if r.bad() && r.target.proto != "" {
			out = append(out, r)
		}
	}
	return out
}

// repairsFor lists everything mor can put right by itself about what the check
// just found. Engines come first: a dead engine explains most of what follows,
// so restarting it may well empty the rest of the list.
func repairsFor(rows []result, problems []problem) []repair {
	out := []repair{}

	units, seen := []string{}, map[string]bool{}
	for _, r := range brokenOnes(rows) {
		// A port nobody reaches is not the engine's fault — it is up and holding
		// the port, and no amount of restarting will unblock the road to it.
		if r.engine && r.held {
			continue
		}
		u := unitOf(r.target.proto)
		if u == "" || seen[u] {
			continue
		}
		seen[u] = true
		units = append(units, u)
	}
	if len(units) > 0 {
		list := units
		out = append(out, repair{
			doing: "перезапускаю " + strings.Join(list, ", "),
			do:    func(*env) error { return restartUnits(list) },
		})
	}

	for _, p := range problems {
		if p.fix != nil {
			out = append(out, *p.fix)
		}
	}
	return out
}

// cutOff picks the protocols the outside world cannot reach although this
// machine is doing everything right. Nothing here can fix that — only a
// different port can.
//
// A stopped engine is not on this list even though nothing reaches it either:
// changing everyone's port because a service needs a restart would be a loud
// fix for the wrong fault.
func cutOff(rows []result) []result {
	out := []result{}
	for _, r := range rows {
		if r.remote == reachNone && r.target.proto != "" && r.engine && r.held {
			out = append(out, r)
		}
	}
	return out
}

// verdict is the whole answer the screen exists to give, in the order a person
// needs it: what is broken first, then anything merely worth knowing. Silence
// from every check is itself the answer, and only then does it say so.
func verdict(rows []result, problems []problem) []string {
	lines := []string{}
	for _, r := range brokenOnes(rows) {
		port := strconv.Itoa(r.target.port)
		// Order matters. A stopped engine answers nothing from outside either,
		// so asking about the outside first would blame the carrier for a
		// service that is simply not running — and send the person off to change
		// a port that was never the problem.
		switch {
		case !r.engine:
			lines = append(lines, "«"+r.target.name+"» не запущен — смотри journalctl -u "+unitOf(r.target.proto)+".")
		case !r.held:
			lines = append(lines, "«"+r.target.name+"» не держит порт "+port+" — смотри journalctl -u "+unitOf(r.target.proto)+".")
		default:
			lines = append(lines, "«"+r.target.name+"» не доходит снаружи — порт "+port+" режут по дороге.")
		}
	}
	if len(lines) == 0 {
		for _, r := range rows {
			if r.remote == reachPartial {
				lines = append(lines, "Порты доходят не из всех стран — там их режут, для остальных всё в порядке.")
				break
			}
		}
	}
	for _, p := range problems {
		lines = append(lines, p.text)
	}
	if len(lines) > 0 {
		return lines
	}
	lines = append(lines, "Всё работает.")
	if note := udpNote(rows); note != "" {
		lines = append(lines, note)
	}
	return lines
}

// udpNote names the protocols whose path nobody can probe from outside, so a
// clean verdict does not claim more than was actually checked.
func udpNote(rows []result) string {
	names := []string{}
	for _, r := range rows {
		if r.target.udp {
			names = append(names, r.target.name)
		}
	}
	if len(names) == 0 {
		return ""
	}
	tail := " идёт по UDP — снаружи его не проверить, тут судит только приложение."
	if len(names) > 1 {
		tail = " идут по UDP — снаружи их не проверить, тут судит только приложение."
	}
	return strings.Join(names, " и ") + tail
}

// repair does everything the check found and mor knows how to undo. It returns
// whether the check should run again: picking a port already probes the outside
// world and ends on its own screen, so there is nothing left to re-ask.
func (m *menu) repair(fixes []repair, cut []result) bool {
	m.page("Исправление")

	for _, f := range fixes {
		fmt.Printf("  %s%s…%s", dim, f.doing, reset)
		if err := f.do(m.e); err != nil {
			fmt.Printf(" %sне вышло: %v%s\n", dim, err, reset)
			continue
		}
		fmt.Printf(" %sготово%s\n", dim, reset)
	}
	if len(fixes) > 0 {
		// Engines need a moment to take their ports back, and checking before
		// they do would report them dead and send the person round again.
		time.Sleep(2 * time.Second)
	}
	if len(cut) == 0 {
		return true
	}
	m.pickPort(cut)
	return false
}

// restart is the menu item. It asks first: picking it by mistake would drop
// every live connection on the server.
func (m *menu) restart() {
	units := m.serviceUnits()

	m.page("Перезапуск сервисов")
	m.note("Перезапущу: " + strings.Join(units, ", ") + ".")
	m.note("Все соединения оборвутся и восстановятся сами за несколько секунд.",
		"Если ты сам сидишь через этот VPN, связь пропадёт и у тебя —",
		"перезапуск при этом дойдёт до конца.",
		"Ключи и настройки не меняются.")

	answer, _ := m.ask("Перезапустить? y/n")
	if !yes(answer) {
		m.msg = "отменено"
		return
	}
	m.restartAll(units)
	m.msg = ""
}

// restartUnits kicks a set of services and reports what did not come back.
//
// Everything goes into a single systemctl call on purpose. The owner is very
// often connected through this very VPN, so restarting Hysteria2 drops their
// own SSH session — and a loop that restarts one unit per command would die
// half way, leaving the rest untouched. Handing systemd the whole list makes it
// systemd's job to finish: it does, even with nobody left to watch.
func restartUnits(units []string) error {
	for _, u := range units {
		_ = exec.Command("systemctl", "reset-failed", u).Run()
	}
	if out, err := exec.Command("systemctl", append([]string{"restart"}, units...)...).CombinedOutput(); err != nil {
		return fmt.Errorf("%s", strings.TrimSpace(string(out)))
	}
	dead := []string{}
	for u, state := range unitStates(units) {
		if state != "active" {
			dead = append(dead, u)
		}
	}
	if len(dead) > 0 {
		return fmt.Errorf("не поднялся: %s", strings.Join(dead, ", "))
	}
	return nil
}

// restartAll kicks everything that is supposed to be running and shows how each
// one ended up.
func (m *menu) restartAll(units []string) {
	m.page("Перезапуск сервисов")
	fmt.Printf("  %sподождите…%s\n\n", dim, reset)

	if err := restartUnits(units); err != nil {
		fmt.Printf("  %s%v%s\n\n", dim, err, reset)
	}

	state := unitStates(units)
	for _, u := range units {
		word := "работает"
		if state[u] != "active" {
			word = "не поднялся — journalctl -u " + u
		}
		fmt.Printf("  %-18s %s%s%s\n", u, dim, word, reset)
	}
	m.wait()
}

// serviceUnits lists the services that should be up right now. Reality and
// Encryption share one Xray, so it is named once.
func (m *menu) serviceUnits() []string {
	units := []string{"mor"}
	seen := map[string]bool{"mor": true}
	for _, p := range protoList {
		if !m.e.cfg.On(p) || !protoInstalled(p) {
			continue
		}
		if u := unitOf(p); !seen[u] {
			seen[u] = true
			units = append(units, u)
		}
	}
	return units
}
