package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strings"
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
		fmt.Printf("  %shttps://%s:%d%s\n", dim, m.e.cfg.PublicHost, m.e.cfg.WebPort, reset)
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
