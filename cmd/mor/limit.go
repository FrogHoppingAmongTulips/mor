package main

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"mor/internal/period"
	"mor/internal/stats"
	"mor/internal/store"
	"mor/internal/xray"
)

// A traffic cap belongs to a person, not to a key: one person holds a key per
// protocol, and switching protocols must not hand out the cap a second time.
// So the cap is stored on every key of the person and counted across them all.

// limits is everything one prompt can set about a key: how long it lives and
// how much it may spend. Both are optional, and they are typed on one line —
// "30d 10гб" — because to the person handing out a key they are one decision.
type limits struct {
	until time.Time
	bytes uint64
}

func (l limits) none() bool { return l.until.IsZero() && l.bytes == 0 }

// text describes what was set, for the line under the list.
func (l limits) text() string {
	switch {
	case l.none():
		return "без ограничений"
	case l.bytes == 0:
		return period.Left(l.until, time.Now())
	case l.until.IsZero():
		return "лимит " + stats.Human(l.bytes)
	default:
		return period.Left(l.until, time.Now()) + ", лимит " + stats.Human(l.bytes)
	}
}

// parseLimits reads both halves off one line, in either order and either
// alphabet: "30d", "10гб", "30d 10гб", "10 дней 10 гигабайт".
func parseLimits(s string, now time.Time) (limits, error) {
	var out limits
	for _, tok := range limitTokens(s) {
		// A deadline is tried first: "10m" has meant ten months since long
		// before caps existed, and megabytes are written "10мб" anyway.
		if sp, err := period.Parse(tok); err == nil {
			if !out.until.IsZero() {
				return limits{}, fmt.Errorf("срок указан дважды")
			}
			out.until = sp.Add(now)
			continue
		}
		n, err := stats.Parse(tok)
		if err != nil {
			return limits{}, fmt.Errorf("не понимаю «%s» — можно 30d, 10gb или 30d 10gb", tok)
		}
		if out.bytes != 0 {
			return limits{}, fmt.Errorf("лимит трафика указан дважды")
		}
		out.bytes = n
	}
	return out, nil
}

// limitTokens splits the line, gluing a number back to the word it belongs to:
// "10 дней" arrives as two fields but means one thing.
func limitTokens(s string) []string {
	f := strings.Fields(strings.ToLower(strings.TrimSpace(s)))
	out := make([]string, 0, len(f))
	for i := 0; i < len(f); i++ {
		if allDigits(f[i]) && i+1 < len(f) && !allDigits(f[i+1]) {
			out = append(out, f[i]+f[i+1])
			i++
			continue
		}
		out = append(out, f[i])
	}
	return out
}

func allDigits(s string) bool {
	for _, r := range s {
		if (r < '0' || r > '9') && r != '.' && r != ',' {
			return false
		}
	}
	return s != ""
}

// quota is what one person is allowed and what they already spent.
type quota struct {
	used  uint64
	limit uint64
}

func (q quota) over() bool { return q.limit > 0 && q.used >= q.limit }

// text writes the traffic column: "1.2 ГБ" without a cap, "1.2 из 10 ГБ" with one.
func (q quota) text() string {
	if q.limit == 0 {
		return stats.Human(q.used)
	}
	return stats.Human(q.used) + " из " + stats.Human(q.limit)
}

// quotaOf sums one person's keys into a single allowance.
func quotaOf(e *env, g []*store.User) quota {
	q := quota{used: e.stats.Sum(ids(g)).Total}
	for _, u := range g {
		if u.Limit > 0 {
			q.limit = u.Limit
			break
		}
	}
	return q
}

// blocked lists the keys that must not let anyone in: the time ran out, or the
// person spent everything they were given.
func blocked(e *env, users []*store.User) map[string]bool {
	out := map[string]bool{}
	for _, g := range groupKeys(users) {
		over := quotaOf(e, g).over()
		for _, u := range g {
			if over || u.Expired() || u.Banned {
				out[u.ID] = true
			}
		}
	}
	return out
}

// limitOf is the cap on one key, for the subscription header.
func limitOf(e *env, id string) uint64 {
	for _, u := range e.st.List() {
		if u.ID == id {
			return u.Limit
		}
	}
	return 0
}

// live returns the keys engines should carry. Blocked keys stay in the store —
// the owner still sees them, and lifting the cap brings them back.
func (e *env) live() []*store.User {
	users := e.st.List()
	bad := blocked(e, users)
	out := make([]*store.User, 0, len(users))
	for _, u := range users {
		if !bad[u.ID] {
			out = append(out, u)
		}
	}
	return out
}

// guard holds the blocked set for Hysteria2, which asks about every single
// connection. Traffic is counted once a minute, so a set refreshed on the same
// tick is exactly as fresh as the numbers behind it — and costs nothing to read.
type guard struct {
	mu  sync.RWMutex
	set map[string]bool
}

func newGuard() *guard { return &guard{set: map[string]bool{}} }

func (g *guard) replace(set map[string]bool) {
	g.mu.Lock()
	g.set = set
	g.mu.Unlock()
}

func (g *guard) has(id string) bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.set[id]
}

// menu

// askLimits is the one question about how long a key lives and how much it may
// spend. Both go on one line in any order, and Enter means no limits at all.
func (m *menu) askLimits() (limits, bool) {
	for {
		val, ok := m.ask("Ограничения (0 — выход)")
		if !ok || quit(val) {
			return limits{}, false
		}
		if val == "" {
			return limits{}, true
		}
		l, err := parseLimits(val, time.Now())
		if err != nil {
			fmt.Printf("\n  %s%v%s\n\n", dim, err, reset)
			continue
		}
		fmt.Printf("  %s%s%s\n", dim, l.text(), reset)
		return l, true
	}
}

// setLimits puts a deadline and a traffic cap on every key of one person.
func (m *menu) setLimits(g []*store.User) (string, bool) {
	q := quotaOf(m.e, g)
	m.page("Ограничения «" + g[0].Name + "»")
	m.note("Срок и лимит трафика одной строкой, в любом порядке:",
		"   30d        срок",
		"   10gb       объём",
		"   30d 10gb   и то и другое — сработает то, что наступит раньше",
		"Трафик считается по всем протоколам человека вместе.",
		"Enter — снять все ограничения.")
	fmt.Printf("  %-*s %s\n", colName, "потрачено", stats.Human(q.used))
	if q.limit > 0 {
		fmt.Printf("  %-*s %s\n", colName, "лимит", stats.Human(q.limit))
	}
	if !g[0].ExpiresAt.IsZero() {
		fmt.Printf("  %-*s %s\n", colName, "срок", period.Left(g[0].ExpiresAt, time.Now()))
	}
	fmt.Println()

	l, ok := m.askLimits()
	if !ok {
		return "отменено", false
	}
	for _, u := range g {
		if err := m.e.st.SetExpiry(u.ID, l.until); err != nil {
			return err.Error(), false
		}
		if err := m.e.st.SetLimit(u.ID, l.bytes); err != nil {
			return err.Error(), false
		}
	}
	// A limit that just came off has to let the key back into the engines, and
	// one that is already spent has to cut it — both are the same rewrite.
	if err := applyLive(m.e, g); err != nil {
		return "записано, но движок не отозвался — загляни в «Проверку»", false
	}
	name := "«" + g[0].Name + "»"
	if l.none() {
		return name + " теперь без ограничений", true
	}
	if l.bytes > 0 && q.used >= l.bytes {
		return name + " — " + l.text() + ", он уже исчерпан", true
	}
	return name + " — " + l.text(), true
}

// applyLive brings the engines in line with who may connect right now, without
// restarting anything it does not have to. Changing one person's limit must not
// drop everybody else's sessions — and a few changes in a row used to restart
// Xray often enough that systemd refused to start it again.
func applyLive(e *env, changed []*store.User) error {
	users := e.live()
	if xray.Installed() {
		// The file is what a restart would read, so it is kept current even
		// though nothing is restarted here.
		if err := e.xr.WriteConfig(users); err != nil {
			return err
		}
		bad := blocked(e, e.st.List())
		for _, u := range changed {
			if u.Proto != store.ProtoReality && u.Proto != store.ProtoEnc && u.Proto != store.ProtoSS {
				continue
			}
			var err error
			if bad[u.ID] {
				err = e.xr.RemoveUser(u.ID, u.Proto)
			} else {
				err = e.xr.AddUser(u)
			}
			// The API is the quiet path; a restart is the loud one, and it is
			// only worth taking when the quiet one is unavailable.
			if err != nil {
				return e.xr.Apply(users)
			}
		}
	}
	return nil
}

// cli

func cmdLimit(args []string) {
	e, g, ok := pick(args)
	if !ok {
		return
	}
	if len(args) < 2 {
		q := quotaOf(e, g)
		fmt.Printf("  %s — потрачено %s\n", g[0].Name, q.text())
		if !g[0].ExpiresAt.IsZero() {
			fmt.Printf("  срок: %s\n", period.Left(g[0].ExpiresAt, time.Now()))
		}
		fmt.Println("  сменить: limit 1 10gb · limit 1 30d 10gb · limit 1 off")
		return
	}

	var l limits
	if rest := strings.Join(args[1:], " "); !isOff(rest) {
		parsed, err := parseLimits(rest, time.Now())
		if err != nil {
			fmt.Println(" ", err)
			return
		}
		l = parsed
	}
	for _, u := range g {
		if err := e.st.SetExpiry(u.ID, l.until); err != nil {
			fmt.Println(" ", err)
			return
		}
		if err := e.st.SetLimit(u.ID, l.bytes); err != nil {
			fmt.Println(" ", err)
			return
		}
	}
	if err := applyLive(e, g); err != nil {
		fmt.Printf("  записано, но движок не отозвался: %v\n", err)
		return
	}
	if l.none() {
		fmt.Printf("  «%s» теперь без ограничений\n", g[0].Name)
		return
	}
	fmt.Printf("  «%s» — %s\n", g[0].Name, l.text())
}

func isOff(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "off", "нет", "снять", "0":
		return true
	}
	return false
}
