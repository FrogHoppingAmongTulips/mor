package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"mor/internal/config"
	"mor/internal/hysteria"
	"mor/internal/period"
	"mor/internal/store"
	"mor/internal/xray"
)

// The one-shot commands: what `mor list` or `mor dns 9.9.9.9` do when typed
// straight at the shell, without ever drawing the menu.

// cmdUser creates a key. By default it covers every protocol and hands back one
// link, exactly like the menu does; a flag narrows it to a single protocol.
func cmdUser(args []string) {
	proto := ""
	for len(args) > 0 && strings.HasPrefix(args[0], "--") {
		switch args[0] {
		case "--hy2":
			proto = store.ProtoHy2
		case "--reality":
			proto = store.ProtoReality
		case "--enc":
			proto = store.ProtoEnc
		case "--ss":
			proto = store.ProtoSS
		default:
			fmt.Printf("  неизвестный флаг %s\n", args[0])
			return
		}
		args = args[1:]
	}
	name := strings.TrimSpace(strings.Join(args, " "))
	if name == "" {
		fmt.Println("  укажи имя: user телефон")
		return
	}
	e, err := load()
	if err != nil {
		fmt.Println(err)
		return
	}

	if proto != "" {
		u, err := createKey(e, name, proto, "")
		if u == nil {
			fmt.Println(" ", err)
			return
		}
		if err != nil {
			fmt.Println("  предупреждение:", err)
		}
		showKey(e, u)
		return
	}

	made, err := createAccess(e, name, "")
	if len(made) == 0 {
		fmt.Println(" ", errText(err))
		return
	}
	if err != nil {
		fmt.Println("  предупреждение:", err)
	}
	if link := subURL(e, made[0]); link != "" {
		fmt.Printf("\n  %s · %s\n%s\n", name, protoNames(made), link)
		return
	}
	for _, u := range made {
		showKey(e, u)
	}
}

func cmdList() {
	e, err := load()
	if err != nil {
		fmt.Println(err)
		return
	}
	groups := groupKeys(e.st.List())
	if len(groups) == 0 {
		fmt.Println("  ключей нет — создай: user телефон")
		return
	}
	fmt.Println()
	now := time.Now()
	for i, g := range groups {
		kind := store.ProtoName(g[0].Proto)
		if len(g) > 1 {
			kind = fmt.Sprintf("%d протокола", len(g))
		}
		q := quotaOf(e, g)
		note := period.Left(g[0].ExpiresAt, now)
		if q.over() {
			note = "лимит исчерпан"
		}
		fmt.Printf("  %d  %-22s %-14s %-18s %s\n", i+1, g[0].Name, kind, q.text(), note)
	}
}

func cmdShow(args []string) {
	e, g, ok := pick(args)
	if !ok {
		return
	}
	if link := subURL(e, g[0]); link != "" && len(g) > 1 {
		fmt.Printf("\n  %s · %s\n%s\n", g[0].Name, protoNames(g), link)
		return
	}
	for _, u := range g {
		showKey(e, u)
	}
}

func cmdDelete(args []string) {
	e, g, ok := pick(args)
	if !ok {
		return
	}
	if err := removeKeys(e, g); err != nil {
		fmt.Println(" ", err)
		return
	}
	fmt.Printf("  «%s» удалён\n", g[0].Name)
}

func cmdDNS(args []string) {
	e, err := load()
	if err != nil {
		fmt.Println(err)
		return
	}
	if len(args) == 0 {
		fmt.Printf("  сейчас: %s\n", e.cfg.DNS)
		fmt.Println("  сменить: dns 1.1.1.1 · dns 9.9.9.9 · dns 94.140.14.14")
		return
	}
	if !config.ValidIP(args[0]) {
		fmt.Println("  нужен IP, напр. dns 1.1.1.1")
		return
	}
	e.cfg.DNS = args[0]
	save(e, "dns → "+args[0], store.ProtoHy2, store.ProtoReality)
}

func cmdSNI(args []string) {
	e, err := load()
	if err != nil {
		fmt.Println(err)
		return
	}
	if len(args) == 0 {
		fmt.Printf("  сейчас: %s\n", e.cfg.SNI)
		fmt.Println("  сменить: sni www.cloudflare.com · sni www.apple.com")
		return
	}
	domain := args[0]
	if strings.ContainsAny(domain, " /:") {
		fmt.Println("  нужен домен без протокола и порта, напр. sni www.apple.com")
		return
	}
	e.cfg.SetSNI(withWWW(domain))
	save(e, "маскировка → "+e.cfg.SNI, store.ProtoHy2, store.ProtoReality)
}

func cmdPort(args []string) {
	e, err := load()
	if err != nil {
		fmt.Println(err)
		return
	}
	if len(args) == 0 {
		fmt.Printf("  сейчас: %d/udp\n", e.cfg.VPNPort)
		fmt.Println("  сменить: port 2096 — пригодится, если оператор режет текущий")
		return
	}
	port, err := strconv.Atoi(args[0])
	if err != nil || port < 1 || port > 65535 {
		fmt.Println("  порт — число от 1 до 65535")
		return
	}
	e.cfg.VPNPort = port
	if save(e, fmt.Sprintf("порт → %d/udp", port), store.ProtoHy2) {
		ensureFirewall(e)
		fmt.Println("  старые ссылки Hysteria2 больше не работают — раздай новые")
	}
}

func cmdClear() {
	fmt.Print(clearScreen)
	printHeader()
}

func cmdStatus() {
	e, err := load()
	if err != nil {
		fmt.Println(err)
		return
	}
	users := e.st.List()
	kind := "IP"
	if isDomain(e.cfg.PublicHost) {
		kind = "домен"
	}
	fmt.Println()
	fmt.Printf("  адрес       %s (%s)\n", e.cfg.PublicHost, kind)
	fmt.Printf("  ключей      %d\n", len(users))
	fmt.Printf("  dns         %s\n", e.cfg.DNS)
	fmt.Println()
	fmt.Printf("  Hysteria2   %d/udp · ключей %d · %s\n", e.cfg.VPNPort, countProto(users, store.ProtoHy2),
		engineState(e.cfg.On(store.ProtoHy2), true, hysteria.Service))
	fmt.Printf("  Reality     %d/tcp · ключей %d · %s\n", e.cfg.Reality.Port, countProto(users, store.ProtoReality),
		engineState(e.cfg.On(store.ProtoReality), xray.Installed(), xray.Service))
	fmt.Printf("  Encryption  %d/tcp · ключей %d · %s\n", e.cfg.Enc.Port, countProto(users, store.ProtoEnc),
		engineState(e.cfg.On(store.ProtoEnc), xray.Installed(), xray.Service))
	fmt.Printf("  Shadowsocks %d/tcp · ключей %d · %s\n", e.cfg.SS.Port, countProto(users, store.ProtoSS),
		engineState(e.cfg.On(store.ProtoSS), xray.Installed(), xray.Service))
	fmt.Println()
	if e.cfg.SubOn() {
		fmt.Printf("  автовыбор   %d/tcp\n", e.cfg.SubPort)
	} else {
		fmt.Printf("  автовыбор   выключен\n")
	}
	fmt.Printf("  mor         %s\n", serviceState("mor"))
	fmt.Printf("  версия      %s\n", version)
}

func serviceState(unit string) string {
	switch state := unitState(unit); state {
	case "":
		return "нет данных"
	case "active":
		return "работает"
	default:
		return state + " — journalctl -u " + unit
	}
}

func countProto(users []*store.User, proto string) int {
	n := 0
	for _, u := range users {
		if u.Proto == proto {
			n++
		}
	}
	return n
}

func engineState(on, installed bool, unit string) string {
	if !on {
		return "выключен"
	}
	if !installed {
		return "не установлен"
	}
	return serviceState(unit)
}
