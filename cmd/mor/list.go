package main

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// A section shows everything it owns on one screen and stays there: you pick a
// number, it does the thing and says right away what happened. Nothing jumps
// back to the main menu on its own — Enter on an empty line is the way out.

// row is one line of a section: what it is, what it is for, what it says now,
// and what happens when it is picked. The action reports back in plain words.
type row struct {
	label string
	hint  string
	value string
	do    func() (string, bool)
	// sep marks a blank line between groups: it gets no number and cannot be
	// picked, so a setting never looks like one more protocol in the list.
	sep bool
}

type section struct {
	title string
	// state is what is true right now — printed plain, because it is the
	// thing people came to read. note is commentary and stays dim.
	state []string
	note  []string
	rows  []row
	// multi handles several numbers at once — "1 3 4" — when a screen supports it.
	multi  func([]int) (string, bool)
	status string
	ok     bool
}

// pickable lists the rows a number can land on, in the order they are shown.
func (s *section) pickable() []int {
	out := []int{}
	for i, r := range s.rows {
		if !r.sep {
			out = append(out, i)
		}
	}
	return out
}

// walk draws the section and keeps drawing it until the person leaves.
func (m *menu) walk(s *section, reload func(*section)) {
	for {
		if reload != nil {
			reload(s)
		}
		m.drawSection(s)
		live := s.pickable()

		val, ok := m.ask("Номер (Enter — назад)")
		if !ok || val == "" || val == "0" {
			return
		}
		if s.multi != nil && (lower(val) == "all" || lower(val) == "все") {
			all := make([]int, 0, len(live))
			for i := range live {
				all = append(all, i+1)
			}
			s.status, s.ok = s.multi(all)
			continue
		}
		if fields := strings.Fields(val); len(fields) > 1 {
			if s.multi == nil {
				s.status, s.ok = "по одному номеру за раз", false
				continue
			}
			picked, bad := numbers(fields)
			if bad != "" {
				s.status, s.ok = "«"+bad+"» — это не номер", false
				continue
			}
			s.status, s.ok = s.multi(picked)
			continue
		}
		n, err := strconv.Atoi(val)
		if err != nil || n < 1 || n > len(live) {
			s.status, s.ok = didYouMean(val, s.rows), false
			continue
		}
		pick := s.rows[live[n-1]]
		if pick.do == nil {
			continue
		}
		s.status, s.ok = pick.do()
	}
}

func (m *menu) drawSection(s *section) {
	m.page(s.title)
	if len(s.state) > 0 {
		for _, l := range s.state {
			fmt.Println("  " + l)
		}
		fmt.Println()
	}
	if len(s.note) > 0 {
		m.note(s.note...)
	}

	label, hint := 0, 0
	for _, r := range s.rows {
		if r.sep {
			continue
		}
		if n := len([]rune(r.label)); n > label {
			label = n
		}
		if n := len([]rune(r.hint)); n > hint {
			hint = n
		}
	}
	n := 0
	for _, r := range s.rows {
		if r.sep {
			fmt.Println()
			continue
		}
		n++
		line := fmt.Sprintf("  %s%2d%s  %s", bold, n, reset, pad(r.label, label))
		// A row with nothing after the label needs no columns held open for it:
		// padding an empty hint leaves a tail of spaces wrapped in escapes,
		// which TrimRight can no longer see.
		if hint > 0 && (r.hint != "" || r.value != "") {
			line += fmt.Sprintf("  %s%s%s", dim, pad(r.hint, hint), reset)
		}
		if r.value != "" {
			line += "  " + r.value
		}
		fmt.Println(strings.TrimRight(line, " "))
	}

	if s.status != "" {
		fmt.Printf("\n  %s\n", s.status)
	}
	fmt.Println()
}

// numbers turns "1 3 4" into a list, naming the first token that is not one.
func numbers(fields []string) ([]int, string) {
	out := make([]int, 0, len(fields))
	seen := map[int]bool{}
	for _, f := range fields {
		n, err := strconv.Atoi(f)
		if err != nil {
			return nil, f
		}
		if seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	return out, ""
}

func pad(s string, width int) string {
	if n := width - len([]rune(s)); n > 0 {
		return s + strings.Repeat(" ", n)
	}
	return s
}

// restartSelf reloads the daemon so a setting it reads at startup takes hold.
func restartSelf() {
	_ = exec.Command("systemctl", "restart", "mor").Run()
}

// nearestItem answers a wrong choice on the main menu with the nearest match.
func nearestItem(input string) string {
	best, score := "", 1<<30
	for _, it := range menuItems {
		if it.key == "" {
			continue // a spacer has no title to be close to
		}
		if d := distance(input, it.title); d < score {
			best, score = it.title, d
		}
	}
	if best != "" && score <= len([]rune(best))/2 {
		return "нет такого пункта — может быть «" + best + "»?"
	}
	return "нет такого пункта — выбери цифру из списка"
}

// didYouMean answers a typo with the closest thing that would have worked.
func didYouMean(input string, rows []row) string {
	best, score := "", 1<<30
	for _, r := range rows {
		if d := distance(input, r.label); d < score {
			best, score = r.label, d
		}
	}
	if best == "" || score > len([]rune(best))/2 {
		return "нет такой строки — выбери цифру из списка"
	}
	return "нет такой строки — может быть «" + best + "»?"
}

// distance is the usual edit distance, lowercased, on runes.
func distance(a, b string) int {
	ar, br := []rune(lower(a)), []rune(lower(b))
	prev := make([]int, len(br)+1)
	cur := make([]int, len(br)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ar); i++ {
		cur[0] = i
		for j := 1; j <= len(br); j++ {
			cost := 1
			if ar[i-1] == br[j-1] {
				cost = 0
			}
			cur[j] = min3(cur[j-1]+1, prev[j]+1, prev[j-1]+cost)
		}
		copy(prev, cur)
	}
	return prev[len(br)]
}

func min3(a, b, c int) int {
	if b < a {
		a = b
	}
	if c < a {
		a = c
	}
	return a
}

func lower(s string) string {
	out := []rune(s)
	for i, r := range out {
		switch {
		case r >= 'A' && r <= 'Z':
			out[i] = r + 32
		case r >= 'А' && r <= 'Я':
			out[i] = r + 32
		case r == 'Ё':
			out[i] = 'ё'
		}
	}
	return string(out)
}

// quit is the way out of a screen where Enter already means something else.
// Creating a key asks four questions in a row, and each Enter answers one with
// a default rather than leaving — so walking in by accident used to mean
// walking all the way through.
func quit(s string) bool {
	switch lower(strings.TrimSpace(s)) {
	case "0", "выход", "отмена", "q":
		return true
	}
	return false
}
