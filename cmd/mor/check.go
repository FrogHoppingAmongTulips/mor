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

// candidates are ports worth trying when the current one is filtered. They look
// like ordinary web traffic, which is why carriers tend to leave them alone.
var candidates = []int{443, 8443, 2053, 2083, 2087, 2096, 8080, 2052, 2095}
