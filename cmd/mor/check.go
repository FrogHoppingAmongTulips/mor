package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"mor/internal/config"
	"mor/internal/probe"
	"mor/internal/store"
)

// checkTarget is one thing worth testing: a port something listens on.
type checkTarget struct {
	name  string
	port  int
	udp   bool
	proto string
}

func targets(e *env) []checkTarget {
	out := []checkTarget{}
	if e.cfg.On(store.ProtoHy2) {
		out = append(out, checkTarget{store.ProtoName(store.ProtoHy2), e.cfg.VPNPort, true, store.ProtoHy2})
	}
	if e.cfg.On(store.ProtoReality) {
		out = append(out, checkTarget{store.ProtoName(store.ProtoReality), e.cfg.Reality.Port, false, store.ProtoReality})
	}
	if e.cfg.On(store.ProtoEnc) {
		out = append(out, checkTarget{store.ProtoName(store.ProtoEnc), e.cfg.Enc.Port, false, store.ProtoEnc})
	}
	if e.cfg.On(store.ProtoSS) {
		out = append(out, checkTarget{store.ProtoName(store.ProtoSS), e.cfg.SS.Port, false, store.ProtoSS})
	}
	if e.cfg.SubOn() {
		out = append(out, checkTarget{"Раздача ссылок", e.cfg.SubPort, false, ""})
	}
	return out
}

// listening reports whether anything on this machine holds the port. It asks
// the kernel rather than trying to take the port itself: a UDP socket opened on
// :: lets a second bind succeed, and the probe then called a live engine dead.
func listening(port int, udp bool) bool {
	files := []string{"/proc/net/tcp", "/proc/net/tcp6"}
	if udp {
		files = []string{"/proc/net/udp", "/proc/net/udp6"}
	}
	asked := false
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		asked = true
		if holdsPort(string(b), port, udp) {
			return true
		}
	}
	if asked {
		return false
	}
	return canBind(port, udp)
}

// tcpListen is the state a socket waiting for connections reports in /proc.
const tcpListen = "0A"

// holdsPort reads one /proc/net table. Every line carries the local address as
// HEXADDR:HEXPORT, and TCP additionally says whether it is listening — a port
// with only an outgoing connection on it is not being served.
func holdsPort(table string, port int, udp bool) bool {
	want := fmt.Sprintf("%04X", port)
	for i, line := range strings.Split(table, "\n") {
		if i == 0 {
			continue // header
		}
		f := strings.Fields(line)
		if len(f) < 4 {
			continue
		}
		colon := strings.LastIndex(f[1], ":")
		if colon < 0 || f[1][colon+1:] != want {
			continue
		}
		if udp || f[3] == tcpListen {
			return true
		}
	}
	return false
}

// canBind is the fallback where /proc is not available: if the port can be
// taken, nobody was holding it.
func canBind(port int, udp bool) bool {
	addr := net.JoinHostPort("0.0.0.0", strconv.Itoa(port))
	if udp {
		c, err := net.ListenPacket("udp", addr)
		if err != nil {
			return true
		}
		c.Close()
		return false
	}
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return true
	}
	l.Close()
	return false
}

// unitState asks systemd how a service is doing: "active", "failed", "" when
// systemd itself is not there.
func unitState(unit string) string {
	out, _ := exec.Command("systemctl", "is-active", unit).CombinedOutput()
	return strings.TrimSpace(string(out))
}

// unitStates asks about several services in one call. The main screen checks
// its engines on every redraw, and one exec instead of four is the difference
// between a menu that appears and a menu that arrives.
func unitStates(units []string) map[string]string {
	res := make(map[string]string, len(units))
	if len(units) == 0 {
		return res
	}
	out, _ := exec.Command("systemctl", append([]string{"is-active"}, units...)...).Output()
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	for i, u := range units {
		if i < len(lines) {
			res[u] = strings.TrimSpace(lines[i])
		}
	}
	return res
}

// result is one line of the report: what was tested and how it went.
type result struct {
	target checkTarget
	engine bool   // the program that serves this port is running
	held   bool   // something actually holds the port
	remote string // "доходит", "не доходит", "" when not testable
	notes  string
}

// broken is the one rule for "is this a fault": the engine has to be running
// and something has to hold the port. The panel and the terminal both ask
// this, and asking it in two places is how they come to disagree.
func (r result) broken() bool {
	return !r.engine || !r.held
}

// How a port looks from the outside. Partial is not a fault: some countries
// block some ports, and the server can do nothing about it.
const (
	reachOK      = "доходит"
	reachPartial = "доходит не отовсюду"
	reachNone    = "не доходит"
)

func (r result) bad() bool { return !r.engine || !r.held || r.remote == reachNone }

// localCheck answers what the server can see about itself: is the engine
// running and does anything actually hold the port.
func localCheck(e *env) []result {
	list := targets(e)
	units := []string{"mor"}
	for _, t := range list {
		if t.proto != "" {
			units = append(units, unitOf(t.proto))
		}
	}
	state := unitStates(units)

	out := make([]result, 0, len(list))
	for _, t := range list {
		r := result{target: t, held: listening(t.port, t.udp)}
		if t.proto == "" {
			r.engine = state["mor"] == "active"
		} else {
			r.engine = !protoInstalled(t.proto) || state[unitOf(t.proto)] == "active"
		}
		out = append(out, r)
	}
	return out
}

// trouble names the first thing that is wrong, for the main screen. Someone
// whose VPN stopped working should be told where to look without hunting for
// it — that is the whole reason «Проверка» exists. It says nothing at all when
// the server is fine.
func trouble(e *env) string {
	for _, r := range localCheck(e) {
		if !r.broken() {
			continue
		}
		if !r.engine {
			return "«" + r.target.name + "» не запущен — открой «Проверку», пункт 6"
		}
		return "«" + r.target.name + "» не держит порт " + strconv.Itoa(r.target.port) + " — открой «Проверку», пункт 6"
	}
	return worst(cachedLocalProblems(e))
}

// remoteCheck fills in the half the server cannot answer alone: whether the
// outside world reaches those ports. UDP is skipped — nobody can probe it.
//
// progress, if given, is told what is being worked on right now: the port about
// to be probed, then each node as it answers. It gets the name only — where and
// how that is shown is the caller's business, and the menu keeps it to one line
// that rewrites itself instead of a page of output nobody reads.
func remoteCheck(host string, rows []result, progress func(port, node string)) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	for i := range rows {
		if rows[i].target.udp {
			continue
		}
		var onNode func(probe.Result)
		if progress != nil {
			progress(rows[i].target.name, "")
			name := rows[i].target.name
			onNode = func(r probe.Result) { progress(name, r.Node) }
		}
		res, err := probe.TCP(ctx, host, rows[i].target.port, 3, onNode)
		switch {
		case errors.Is(err, probe.ErrUnavailable):
			rows[i].notes = "проверить снаружи не вышло"
		case err != nil:
			rows[i].notes = err.Error()
		default:
			ok := 0
			for _, one := range res {
				if one.OK {
					ok++
				}
				rows[i].notes += fmt.Sprintf(" %s:%s", one.Node, one.Note)
			}
			switch {
			case ok == 0:
				rows[i].remote = reachNone
			case ok < len(res):
				rows[i].remote = reachPartial
			default:
				rows[i].remote = reachOK
			}
		}
	}
}

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

// cmdCheck reports the state of the server. With --fast it only asks what this
// machine can answer instantly, which is what the installer needs: it must say
// whether the thing it just installed actually came up, not wait a minute for
// nodes abroad to answer.
func cmdCheck(args []string) {
	fast := false
	for _, a := range args {
		if a == "--fast" || a == "fast" {
			fast = true
		}
	}
	e, err := load()
	if err != nil {
		fmt.Println(err)
		if fast {
			os.Exit(1)
		}
		return
	}
	fmt.Println()
	rows := localCheck(e)
	if !fast {
		remoteCheck(e.cfg.PublicHost, rows, nil)
	}
	printResults(rows)

	// Everything else only speaks up when something is wrong.
	found := append(localProblems(e), netProblems(e)...)
	for _, p := range found {
		fmt.Printf("  %s\n", p.text)
	}
	if !fast {
		return
	}
	broken := false
	for _, r := range rows {
		if !r.engine || !r.held {
			broken = true
		}
	}
	for _, p := range found {
		if p.level == levelBad {
			broken = true
		}
	}
	if broken {
		fmt.Printf("\n  что-то не поднялось — подробности: journalctl -u mor\n")
		os.Exit(1)
	}
}

// check is the one screen that answers "does it work?". It looks at what runs
// here, has machines abroad knock on every port, and then says the only thing
// that was asked: it works, or here is exactly what is wrong.
//
// The work shows on a single line that rewrites itself. This used to print the
// answer of every node and then wait for Enter before showing the verdict — a
// screenful of detail standing between the question and its answer, and a
// keypress to get past it.
func (m *menu) check() {
	// The loop is the point of the fix button: repair, then look again and say
	// where that left things, without making anyone walk back into the section.
	for {
		m.page("Проверка")

		// Padded to a width no status line reaches, so each message wipes the
		// one before it instead of leaving a tail of it on screen. The padding
		// counts letters, not bytes: %-64s counts Cyrillic twice and pads none.
		say := func(text string) { fmt.Printf("\r  %s%s%s", dim, pad(text, 64), reset) }

		say("смотрю, что запущено на сервере…")
		rows := localCheck(m.e)

		say("прошу серверы из других стран постучаться в наши порты…")
		remoteCheck(m.e.cfg.PublicHost, rows, func(port, node string) {
			line := "проверяю «" + port + "» снаружи"
			if node != "" {
				line += " · " + node
			}
			say(line + "…")
		})
		say("")
		fmt.Print("\r")

		problems := append(localProblems(m.e), netProblems(m.e)...)
		for _, l := range verdict(rows, problems) {
			fmt.Println("  " + l)
		}
		fmt.Println()

		fixes := repairsFor(rows, problems)
		cut := cutOff(rows)

		// Nothing to fix means nothing to offer: the screen answered its
		// question and gets out of the way.
		if len(fixes) == 0 && len(cut) == 0 {
			m.wait()
			m.msg = ""
			return
		}

		// The button says what it will do before it is pressed. Picking a new
		// port changes what every client connects to, so it is named plainly
		// rather than hidden behind the word "исправить".
		what := make([]string, 0, len(fixes)+1)
		for _, f := range fixes {
			what = append(what, f.doing)
		}
		if len(cut) > 0 {
			what = append(what, "подберу порт, до которого доходит")
		}
		fmt.Printf("  %s1%s  Исправить    %s%s%s\n\n", bold, reset, dim, strings.Join(what, ", "), reset)

		if choice, _ := m.ask("Номер (Enter — назад)"); choice != "1" {
			m.msg = ""
			return
		}
		if !m.repair(fixes, cut) {
			return
		}
	}
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

// candidates are ports worth trying when the current one is filtered. They look
// like ordinary web traffic, which is why carriers tend to leave them alone.
var candidates = []int{443, 8443, 2053, 2083, 2087, 2096, 8080, 2052, 2095}

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
	case store.ProtoEnc:
		cfg.Enc.Port = port
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
