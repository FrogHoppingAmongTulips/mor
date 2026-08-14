package main

import (
	"context"
	"log"
	"time"

	"mor/internal/hysteria"
	"mor/internal/store"
	"mor/internal/xray"
)

const collectEvery = time.Minute

// historyEvery keeps the graph file off the disk most of the time. The buckets
// live in memory between writes, so a kill loses a few minutes of chart detail
// and nothing else — traffic totals are saved every minute either way.
const historyEvery = 5 * time.Minute

func collectLoop(ctx context.Context, e *env, g *guard) {
	t := time.NewTicker(collectEvery)
	defer t.Stop()
	// Keys already cut off, so the engines are not told the same thing every
	// minute for the rest of the server's life.
	cut := map[string]bool{}
	enforce(e, g, cut)
	lastHistory := time.Now()
	for {
		select {
		case <-ctx.Done():
			_ = e.stats.Save()
			_ = e.hist.Save()
			_ = e.audit.Save()
			return
		case <-t.C:
			collect(e)
			autoReset(e)
			enforce(e, g, cut)
			if err := e.stats.Save(); err != nil {
				log.Printf("предупреждение: запись статистики: %v", err)
			}
			if time.Since(lastHistory) >= historyEvery {
				lastHistory = time.Now()
				if err := e.hist.Save(); err != nil {
					log.Printf("предупреждение: запись истории: %v", err)
				}
				if err := e.audit.Save(); err != nil {
					log.Printf("предупреждение: запись журнала действий: %v", err)
				}
			}
		}
	}
}

// autoReset zeroes the counter of everyone on a monthly allowance once the
// calendar month turns.
//
// The month already cleared is stored on the key rather than a timestamp,
// which is what makes this safe to call every minute: a server that was off on
// the first catches up the moment it comes back, and one that has been up all
// day still resets exactly once. A key cut off for spending its cap is let
// back in by the same enforce pass that cut it.
func autoReset(e *env) {
	month := time.Now().Format("2006-01")
	for _, u := range e.st.List() {
		if !u.AutoReset || u.ResetMonth == month {
			continue
		}
		if err := e.stats.Reset(u.ID); err != nil {
			log.Printf("предупреждение: сброс трафика «%s»: %v", u.Name, err)
			continue
		}
		if err := e.st.SetResetMonth(u.ID, month); err != nil {
			log.Printf("предупреждение: отметка сброса «%s»: %v", u.Name, err)
			continue
		}
		log.Printf("«%s»: трафик обнулён на новый месяц", u.Name)
	}
}

func collect(e *env) {
	now := time.Now()
	if traffic, err := hysteria.Traffic(e.cfg.StatsSecret); err == nil {
		for id, b := range traffic {
			e.stats.Add(id, b, now)
			e.hist.Add(id, b, now)
		}
	}
	if online, err := hysteria.Online(e.cfg.StatsSecret); err == nil {
		for id, n := range online {
			if n > 0 {
				e.stats.Seen(id, now)
			}
		}
	}
	if traffic, err := xray.Traffic(); err == nil {
		for id, b := range traffic {
			e.stats.Add(id, b, now)
			e.hist.Add(id, b, now)
		}
	}
}

// enforce brings the engines in line with who is allowed in: keys whose time or
// traffic ran out are cut, and keys that got their allowance back are let in
// again. Hysteria2 needs none of this — it asks the guard on every connection.
func enforce(e *env, g *guard, cut map[string]bool) {
	users := e.st.List()
	bad := blocked(e, users)
	g.replace(bad)

	alive := make(map[string]bool, len(users))
	touched := false
	for _, u := range users {
		alive[u.ID] = true
		if u.Proto == store.ProtoHy2 {
			continue
		}
		switch {
		case bad[u.ID] && !cut[u.ID]:
			if !xray.Installed() {
				continue
			}
			// Marked done only on success: a silent API gets another try next
			// tick instead of leaving the key alive until a restart.
			if err := e.xr.RemoveUser(u.ID, u.Proto); err != nil {
				log.Printf("предупреждение: %s: %v", u.Name, err)
				continue
			}
			cut[u.ID], touched = true, true
			log.Printf("«%s» отключён: %s", u.Name, whyBlocked(u))
		case !bad[u.ID] && cut[u.ID]:
			if !xray.Installed() {
				continue
			}
			if err := e.xr.AddUser(u); err != nil {
				log.Printf("предупреждение: %s: %v", u.Name, err)
				continue
			}
			delete(cut, u.ID)
			touched = true
			log.Printf("«%s» снова пускает", u.Name)
		}
	}
	for id := range cut {
		if !alive[id] {
			delete(cut, id)
		}
	}
	if !touched {
		return
	}
	// Keep the files in step with the running engines, so a restart does not
	// revive somebody who was just cut off.
	live := e.live()
	if xray.Installed() {
		if err := e.xr.WriteConfig(live); err != nil {
			log.Printf("предупреждение: конфиг Xray: %v", err)
		}
	}
}

func whyBlocked(u *store.User) string {
	switch {
	case u.Banned:
		return "забанен вручную"
	case u.Expired():
		return "вышел срок"
	default:
		return "исчерпан лимит трафика"
	}
}
