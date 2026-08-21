package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"mor/internal/config"
	"mor/internal/keys"
	"mor/internal/period"
	"mor/internal/qr"
	"mor/internal/store"
	"mor/internal/xray"
)

// Creating, syncing and removing a key — the operations both the menu and the
// commands call, kept in one place so the two can never drift apart.

func createKey(e *env, name, proto, sni string) (*store.User, error) {
	if !e.cfg.On(proto) {
		return nil, fmt.Errorf("%s выключен — включи его в пункте «Протоколы»", store.ProtoName(proto))
	}
	u := &store.User{Name: name, Proto: proto, SNI: sni}
	switch proto {
	case store.ProtoReality:
		if !xray.Installed() {
			return nil, fmt.Errorf("Xray не установлен — переустанови mor")
		}
		u.UUID = keys.UUID()
	case store.ProtoSS:
		if !xray.Installed() {
			return nil, fmt.Errorf("Xray не установлен — переустанови mor")
		}
		u.SSPassword = keys.Token()
		u.SNI = ""
	default:
		u.HyToken = keys.Token()
	}
	saved, err := e.st.Add(u)
	if err != nil {
		return nil, err
	}
	if err := syncProto(e, proto); err != nil {
		return saved, err
	}
	if proto == store.ProtoReality || proto == store.ProtoSS {
		// Letting the key in live saves a restart. If the API is not answering,
		// restart instead — otherwise the link would not work until something
		// else happened to restart Xray.
		if err := e.xr.AddUser(saved); err != nil {
			if err := e.xr.Apply(e.live()); err != nil {
				return saved, err
			}
		}
	}
	return saved, nil
}

// syncProto rewrites the engine config for protocols Xray serves. Hysteria2
// reads its keys through the auth callback, so it needs nothing here.
func syncProto(e *env, proto string) error {
	switch proto {
	case store.ProtoReality, store.ProtoSS:
		if xray.Installed() {
			return e.xr.WriteConfig(e.live())
		}
	}
	return nil
}

// removeKeys deletes several keys with one config rewrite per engine instead of
// one per key, so deleting ten people costs the same as deleting one.
func removeKeys(e *env, list []*store.User) error {
	protos := map[string]bool{}
	for _, u := range list {
		if err := e.st.Delete(u.ID); err != nil {
			return err
		}
		e.stats.Delete(u.ID)
		e.hist.Delete(u.ID)
		e.ipLimits.Forget(u.ID)
		// A deleted key must take its device table with it, or a name reused
		// later would inherit somebody else's count.
		if u.Sub != "" {
			e.devices.Forget(u.Sub)
		}
		protos[u.Proto] = true
	}
	for p := range protos {
		if err := syncProto(e, p); err != nil {
			return err
		}
	}
	// The keys are out of the config; cutting their live sessions is what the
	// API adds. When it is silent, one restart covers everybody.
	for _, u := range list {
		if u.Proto != store.ProtoReality && u.Proto != store.ProtoSS {
			continue
		}
		if err := e.xr.RemoveUser(u.ID, u.Proto); err != nil {
			return e.xr.Apply(e.live())
		}
	}
	return nil
}

func save(e *env, what string, protos ...string) bool {
	if err := e.cfg.Save(); err != nil {
		fmt.Println("  не удалось сохранить:", err)
		return false
	}
	if err := applyProtos(e, protos...); err != nil {
		fmt.Printf("  сохранено, но движок не перезапустился: %v\n", err)
		return false
	}
	fmt.Printf("  %s\n", what)
	return true
}

// pick resolves a number from `list` into that person's keys. Numbering follows
// the grouped list, so `qr 2` and `del 2` mean the same row the list showed.
func pick(args []string) (*env, []*store.User, bool) {
	e, err := load()
	if err != nil {
		fmt.Println(err)
		return nil, nil, false
	}
	if len(args) == 0 {
		fmt.Println("  укажи номер ключа из list, напр. qr 1")
		return nil, nil, false
	}
	groups := groupKeys(e.st.List())
	if n, err := strconv.Atoi(args[0]); err == nil {
		if n < 1 || n > len(groups) {
			fmt.Printf("  нет ключа с номером %d — посмотри list\n", n)
			return nil, nil, false
		}
		return e, groups[n-1], true
	}
	for _, g := range groups {
		for _, u := range g {
			if u.ID == args[0] {
				return e, g, true
			}
		}
	}
	fmt.Println("  такого ключа нет — посмотри list")
	return nil, nil, false
}

func keyText(cfg *config.Config, u *store.User) string {
	p, ok := proxyFor(cfg, u)
	if !ok {
		return ""
	}
	return p.URI()
}

func showKey(e *env, u *store.User) {
	text := keyText(e.cfg, u)
	fmt.Printf("  %-*s %s\n", colName, "имя", u.Name)
	fmt.Printf("  %-*s %s\n", colName, "протокол", store.ProtoName(u.Proto))
	fmt.Printf("  %-*s %s\n", colName, "создан", period.Date(u.CreatedAt))
	if !u.ExpiresAt.IsZero() {
		fmt.Printf("  %-*s %s\n", colName, "срок", period.Left(u.ExpiresAt, time.Now()))
	}
	fmt.Println()
	if a, err := qr.ASCII(text); err == nil {
		fmt.Print(indent(a))
	}
	fmt.Printf("  %s\n", text)
}

// indent lines everything up with the rest of the screen, QR blocks included.
func indent(block string) string {
	lines := strings.Split(strings.TrimRight(block, "\n"), "\n")
	for i, l := range lines {
		lines[i] = "  " + l
	}
	return strings.Join(lines, "\n") + "\n"
}
