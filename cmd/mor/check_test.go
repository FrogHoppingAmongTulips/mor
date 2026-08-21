package main

import (
	"strconv"
	"strings"
	"testing"

	"mor/internal/store"
	"mor/internal/xray"
)

// Real /proc/net tables, trimmed. The UDP one is what a dual-stack sing-box
// looks like: the bind probe called this port free while the engine held it.
const udp6Table = `  sl  local_address                         remote_address                        st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode ref pointer drops
  832: 00000000000000000000000000000000:0004 00000000000000000000000000000000:0000 07 00000000:00000000 00:00000000 00000000     0        0 12345 2 0000000000000000 0
 1024: 00000000000000000000000000000000:0820 00000000000000000000000000000000:0000 07 00000000:00000000 00:00000000 00000000     0        0 54321 2 0000000000000000 0
`

const tcpTable = `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 00000000:01BB 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 11111 1 0000000000000000 100 0 0 10 0
   1: 0100007F:0016 0100007F:C350 01 00000000:00000000 00:00000000 00000000     0        0 22222 1 0000000000000000 20 4 30 10 -1
`

func TestHoldsPortUDPDualStack(t *testing.T) {
	if !holdsPort(udp6Table, 4, true) {
		t.Error("порт 4 занят sing-box, а проверка его не видит")
	}
	if !holdsPort(udp6Table, 2080, true) {
		t.Error("порт 2080 занят, а проверка его не видит")
	}
	if holdsPort(udp6Table, 2097, true) {
		t.Error("свободный порт показан занятым")
	}
}

// A port with only an outgoing connection on it is not being served, and saying
// otherwise would hide a dead engine.
func TestHoldsPortTCPNeedsListen(t *testing.T) {
	if !holdsPort(tcpTable, 443, false) {
		t.Error("слушающий 443 не распознан")
	}
	if holdsPort(tcpTable, 22, false) {
		t.Error("исходящее соединение принято за слушающий порт")
	}
}

// Ports are hex in /proc, so a decimal reading would match the wrong line.
func TestHoldsPortReadsHex(t *testing.T) {
	if holdsPort(tcpTable, 443+0x10000, false) {
		t.Error("совпадение по обрезанному номеру порта")
	}
	if holdsPort(udp6Table, 820, true) {
		t.Error("десятичное 820 совпало с шестнадцатеричным 0820")
	}
}

// The check screen exists to answer one question, so its answer is worth
// pinning down: a healthy server must say so, and a broken one must name what
// is broken rather than leave the person to read a table.

func okRow(name string, port int, udp bool, proto string) result {
	return result{
		target: checkTarget{name: name, port: port, udp: udp, proto: proto},
		engine: true, held: true, remote: reachOK,
	}
}

func TestVerdictAllGood(t *testing.T) {
	rows := []result{okRow("Reality", 443, false, "reality")}
	got := verdict(rows, nil)
	if len(got) != 1 || got[0] != "Всё работает." {
		t.Errorf("здоровый сервер должен просто сказать, что работает, а сказал: %v", got)
	}
}

// A UDP protocol was never reached from outside, so a bare "everything works"
// would promise more than was tested.
func TestVerdictAdmitsUDPUnchecked(t *testing.T) {
	rows := []result{okRow("Reality", 443, false, "reality")}
	if len(verdict(rows, nil)) != 1 {
		t.Fatal("без UDP-протоколов оговорки быть не должно")
	}
	rows = append(rows, okRow("Hysteria2", 2080, true, "hy2"))
	got := verdict(rows, nil)
	if len(got) != 2 || !contains(got[1], "Hysteria2") || !contains(got[1], "UDP") {
		t.Errorf("про непроверяемый UDP надо сказать: %v", got)
	}
}

func TestVerdictNamesWhatIsBroken(t *testing.T) {
	dead := okRow("Reality", 443, false, "reality")
	dead.engine, dead.held, dead.remote = false, false, ""

	cut := okRow("Shadowsocks", 2099, false, "ss")
	cut.remote = reachNone

	silent := okRow("Hysteria2", 2096, true, store.ProtoHy2)
	silent.held = false

	got := verdict([]result{dead, cut, silent}, nil)
	if len(got) != 3 {
		t.Fatalf("три поломки — три строки, а вышло: %v", got)
	}
	if !contains(got[0], "Reality") || !contains(got[0], "не запущен") {
		t.Errorf("остановленный движок описан неверно: %q", got[0])
	}
	if !contains(got[1], "2099") || !contains(got[1], "не доходит") {
		t.Errorf("зарезанный порт описан неверно: %q", got[1])
	}
	if !contains(got[2], "Hysteria2") || !contains(got[2], "не держит порт") {
		t.Errorf("движок без порта описан неверно: %q", got[2])
	}
	for _, line := range got {
		if contains(line, "Всё работает") {
			t.Error("сломанный сервер не должен отчитываться, что работает")
		}
	}
}

// Trouble found outside the ports — a full disk, a clock adrift — has to reach
// the person too, and must not be drowned out by the cheerful line.
func TestVerdictKeepsOtherProblems(t *testing.T) {
	rows := []result{okRow("Reality", 443, false, "reality")}
	got := verdict(rows, []problem{{text: "на диске меньше 100 МБ", level: levelBad}})
	if len(got) != 1 || !contains(got[0], "диске") {
		t.Errorf("проблема с диском потерялась: %v", got)
	}
}

// Some countries block some ports and the server can do nothing about it, so
// this is worth a word but is not a fault to fix.
func TestVerdictPartialIsNotAFailure(t *testing.T) {
	row := okRow("Reality", 443, false, "reality")
	row.remote = reachPartial
	got := verdict([]result{row}, nil)
	if len(got) != 1 || !contains(got[0], "не из всех стран") {
		t.Errorf("частичная доступность описана неверно: %v", got)
	}
	if len(brokenOnes([]result{row})) != 0 {
		t.Error("частичная доступность не поломка — чинить нечего")
	}
}

// The link feed has its own line among the problems; a dead one must not make
// the screen offer to move a protocol's port.
func TestBrokenOnesIgnoresLinkFeed(t *testing.T) {
	feed := okRow("Раздача ссылок", 8880, false, "")
	feed.engine = false
	if len(brokenOnes([]result{feed})) != 0 {
		t.Error("раздача ссылок попала в список протоколов для подбора порта")
	}
}

func contains(hay, needle string) bool {
	return len(hay) >= len(needle) && strings.Contains(hay, needle)
}

// The fix button is only worth showing when there is something behind it, and
// what it promises has to match what it would actually do.

func TestRepairsRestartsDeadEnginesOnce(t *testing.T) {
	// Reality and Shadowsocks share one Xray: restarting it twice would be a
	// second, pointless drop of everyone's connections.
	reality := okRow("VLESS+Reality", 443, false, store.ProtoReality)
	reality.engine = false
	ss := okRow("Shadowsocks", 2099, false, store.ProtoSS)
	ss.engine = false

	got := repairsFor([]result{reality, ss}, nil)
	if len(got) != 1 {
		t.Fatalf("два протокола на одном движке — одна починка, вышло %d: %v", len(got), got)
	}
	if strings.Count(got[0].doing, xray.Service) != 1 {
		t.Errorf("движок назван не один раз: %q", got[0].doing)
	}
}

// A port the outside world cannot reach belongs to a healthy engine holding it
// perfectly well. Restarting it fixes nothing and drops live sessions for free.
func TestRepairsLeaveBlockedPortAlone(t *testing.T) {
	cut := okRow("VLESS+Reality", 443, false, store.ProtoReality)
	cut.remote = reachNone

	if got := repairsFor([]result{cut}, nil); len(got) != 0 {
		t.Errorf("зарезанный порт не лечится перезапуском, а предложено: %v", got)
	}
	if len(cutOff([]result{cut})) != 1 {
		t.Error("зарезанный порт должен попасть в подбор порта")
	}
}

func TestRepairsCarryProblemFixes(t *testing.T) {
	fixable := problem{text: "закрыт в firewall: 443/tcp", level: levelBad,
		fix: &repair{doing: "открываю порты в firewall", do: func(*env) error { return nil }}}
	// A full disk is real and worth saying, but mor has nothing safe to do
	// about it, so it must not put a button under a promise it cannot keep.
	hopeless := problem{text: "на диске почти нет места", level: levelBad}

	got := repairsFor(nil, []problem{fixable, hopeless})
	if len(got) != 1 || got[0].doing != "открываю порты в firewall" {
		t.Errorf("в кнопку попало не то: %v", got)
	}
}

func TestRepairsEmptyOnHealthyServer(t *testing.T) {
	rows := []result{okRow("VLESS+Reality", 443, false, store.ProtoReality)}
	if got := repairsFor(rows, nil); len(got) != 0 {
		t.Errorf("на исправном сервере чинить нечего, а предложено: %v", got)
	}
	if len(cutOff(rows)) != 0 {
		t.Error("исправный порт попал в подбор порта")
	}
}

// The link feed is not a protocol: its port is not something to go hunting a
// replacement for.
func TestCutOffSkipsLinkFeed(t *testing.T) {
	feed := okRow("Раздача ссылок", 8880, false, "")
	feed.remote = reachNone
	if len(cutOff([]result{feed})) != 0 {
		t.Error("раздаче ссылок предложено менять порт")
	}
}

// A stopped engine answers nothing from outside either. Reported the wrong way
// round, that reads as "the carrier is blocking you" and sends the person off
// to change a port that was never at fault — the live test found exactly this.
func TestDeadEngineIsNotBlamedOnTheCarrier(t *testing.T) {
	dead := okRow("VLESS+Reality", 443, false, store.ProtoReality)
	dead.engine, dead.held, dead.remote = false, false, reachNone

	got := verdict([]result{dead}, nil)
	if len(got) != 1 || !contains(got[0], "не запущен") {
		t.Errorf("остановленный движок описан как что-то другое: %v", got)
	}
	if len(cutOff([]result{dead})) != 0 {
		t.Error("остановленному движку предложено менять порт")
	}
}

// The engine is up and holding the port, and still nothing arrives: this one
// really is the road, and a new port really is the answer.
func TestHealthyEngineUnreachableIsAPortProblem(t *testing.T) {
	cut := okRow("VLESS+Reality", 443, false, store.ProtoReality)
	cut.remote = reachNone

	got := verdict([]result{cut}, nil)
	if len(got) != 1 || !contains(got[0], "режут по дороге") {
		t.Errorf("зарезанный порт описан неверно: %v", got)
	}
	if len(cutOff([]result{cut})) != 1 {
		t.Error("живой движок с зарезанным портом должен попасть в подбор порта")
	}
}

// The main menu now has blank lines in it. A spacer is drawn and skipped, so it
// must never turn into something pickable or something a typo gets pointed at.

func TestMenuSpacersAreNotItems(t *testing.T) {
	seen := map[string]bool{}
	var order []string
	for _, it := range menuItems {
		if it.key == "" {
			if it.title != "" {
				t.Errorf("разделитель с названием: %q", it.title)
			}
			continue
		}
		if it.title == "" {
			t.Errorf("пункт %q без названия", it.key)
		}
		if seen[it.key] {
			t.Errorf("номер %q встречается дважды", it.key)
		}
		seen[it.key] = true
		order = append(order, it.key)
	}
	// Numbers people already know must stay where they were: a list that reads
	// 1 7 2 3 is harder to scan than the solid block the spacers came to break.
	// Exit is last and always zero.
	if len(order) == 0 || order[len(order)-1] != "0" {
		t.Fatalf("выход не последний: %v", order)
	}
	for i, key := range order[:len(order)-1] {
		if key != strconv.Itoa(i+1) {
			t.Errorf("номера идут не по порядку: %v", order)
			break
		}
	}
}

func TestNearestItemNeverSuggestsBlank(t *testing.T) {
	for _, input := range []string{"", "  ", "xyzzy", "проверк", "выхо"} {
		if got := nearestItem(input); strings.Contains(got, "«»") {
			t.Errorf("на %q предложен пустой пункт: %q", input, got)
		}
	}
}

// Creating a key asks four questions, and every Enter answers one with a
// default instead of leaving. Somebody who opened the screen by mistake needs
// one answer that works at every step — and it has to be the same answer.
func TestQuitWordsAreAcceptedEverywhere(t *testing.T) {
	for _, in := range []string{"0", "выход", "отмена", "q", " 0 ", "Выход", "ОТМЕНА"} {
		if !quit(in) {
			t.Errorf("%q должно выпускать из создания ключа", in)
		}
	}
	// A name is free text, so only the words above may mean "let me out" —
	// anything else is somebody naming their phone.
	for _, in := range []string{"", "ноут", "0-телефон", "10gb", "30d", "телефон0"} {
		if quit(in) {
			t.Errorf("%q — это не выход, а ввод", in)
		}
	}
}
