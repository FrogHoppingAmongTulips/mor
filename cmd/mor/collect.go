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

func collectLoop(ctx context.Context, e *env) {
	t := time.NewTicker(collectEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			_ = e.stats.Save()
			return
		case <-t.C:
			collect(e)
			dropExpired(e)
			if err := e.stats.Save(); err != nil {
				log.Printf("предупреждение: запись статистики: %v", err)
			}
		}
	}
}

func collect(e *env) {
	now := time.Now()
	if traffic, err := hysteria.Traffic(e.cfg.StatsSecret); err == nil {
		for id, b := range traffic {
			e.stats.Add(id, b, now)
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
		}
	}
}

// dropExpired cuts off keys whose time is up. Hysteria2 checks expiry on every
// connection by itself, so only Reality needs the engine told.
func dropExpired(e *env) {
	if !xray.Installed() {
		return
	}
	for _, u := range e.st.List() {
		if u.Proto != store.ProtoReality || !u.Expired() {
			continue
		}
		if err := e.xr.RemoveUser(u.ID); err != nil {
			log.Printf("предупреждение: %s: %v", u.Name, err)
		}
	}
}
