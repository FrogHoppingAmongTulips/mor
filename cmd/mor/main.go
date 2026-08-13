package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"mor/internal/auditlog"
	"mor/internal/config"
	"mor/internal/hysteria"
	"mor/internal/keys"
	"mor/internal/period"
	"mor/internal/proxy"
	"mor/internal/qr"
	"mor/internal/stats"
	"mor/internal/store"
	"mor/internal/sub"
	"mor/internal/xray"
)

var version = "dev"

// stdin is shared: two buffered readers over the same terminal would eat each
// other's input, and hidden prompts read from the menu's stream.
var stdin = bufio.NewReader(os.Stdin)

func main() {
	log.SetFlags(0)

	if len(os.Args) < 2 {
		if interactive() {
			runMenu()
		} else {
			shell()
		}
		return
	}
	switch os.Args[1] {
	case "serve":
		serve()
	case "setup":
		setup(os.Args[2:])
	case "version", "-v", "--version":
		fmt.Println(version)
	case "help", "-h", "--help":
		printHelp()
	default:
		if !run(os.Args[1], os.Args[2:]) {
			os.Exit(2)
		}
	}
}

func shell() {
	if _, err := load(); err != nil {
		log.Fatalf("нет конфига — сначала установи: mor setup (%v)", err)
	}
	printHeader()

	for {
		fmt.Print("\nmor> ")
		text, err := stdin.ReadString('\n')
		if err != nil && text == "" {
			fmt.Println()
			return
		}
		line := strings.TrimSpace(text)
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		cmd, args := parts[0], parts[1:]
		if cmd == "exit" || cmd == "quit" || cmd == "q" {
			return
		}
		run(cmd, args)
	}
}

func run(cmd string, args []string) bool {
	switch cmd {
	case "user", "add":
		cmdUser(args)
	case "list", "ls":
		cmdList()
	case "qr", "show":
		cmdShow(args)
	case "del", "rm":
		cmdDelete(args)
	case "limit":
		cmdLimit(args)
	case "dns":
		cmdDNS(args)
	case "sni":
		cmdSNI(args)
	case "port":
		cmdPort(args)
	case "host", "address":
		cmdHost(args)
	case "proto", "protocol":
		cmdProto(args)
	case "sub", "subscription":
		cmdSub(args)
	case "panel":
		cmdPanel(args)
	case "status", "info":
		cmdStatus()
	case "check":
		cmdCheck(args)
	case "update":
		cmdUpdate()
	case "clear", "cls":
		cmdClear()
	case "help", "?":
		printHelp()
	default:
		fmt.Printf("не знаю команду «%s». Напиши help\n", cmd)
		return false
	}
	return true
}

func printHelp() {
	fmt.Print(`  user <имя>            ключ на всех протоколах, одна ссылка
  user --hy2|--reality|--enc|--ss <имя>
                        ключ только на одном протоколе
  list                  все ключи
  qr <номер>            показать ключ ещё раз
  del <номер>           удалить ключ
  limit <номер> <срок и/или объём>
                        30d · 10gb · 30d 10gb · off — снять
  dns <ip>              резолвер для всех клиентов
  sni <домен>           сайт, которым притворяется сервер
  port <номер>          порт Hysteria2
  host <домен|ip>       адрес в ссылках — домен переживает переезд
  proto [on|off <id>]   что стоит, что включено · включить/выключить протокол
  sub [on|off|port N]   автовыбор: одна ссылка на все протоколы
  panel [password P|on|off|port N]
                        веб-панель — без пароля не запустится
  status                состояние сервера
  check                 доходят ли порты снаружи
  update                поставить свежую версию mor
  clear                 очистить экран
  exit                  выйти
`)
}

func printHeader() {
	fmt.Printf("\n  MOR %s\n", version)
	fmt.Println("\n  user <имя> — создать ключ · help — все команды · exit — выйти")
}

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

func serve() {
	e, err := load()
	if err != nil {
		log.Fatalf("нет конфига — сначала установи: mor setup (%v)", err)
	}

	if err := e.cfg.Save(); err != nil {
		log.Printf("предупреждение: запись конфига: %v", err)
	}
	applyAll(e)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	g := newGuard()
	go collectLoop(ctx, e, g)

	if e.cfg.SubOn() {
		go func() {
			h := sub.New(e.st,
				func(u *store.User) (proxy.Proxy, bool) { return proxyFor(e.cfg, u) },
				e.cfg.PublicHost,
				func(id string) (uint64, uint64) { return e.stats.Get(id).Total, limitOf(e, id) })
			if err := sub.Serve(ctx, e.cfg.SubPort, h); err != nil {
				log.Printf("предупреждение: подписка на :%d не поднялась: %v", e.cfg.SubPort, err)
			}
		}()
		log.Printf("подписки раздаются на :%d", e.cfg.SubPort)
	}

	if e.cfg.WebOn() {
		go startWebPanel(ctx, e)
	}

	log.Printf("mor %s: проверка ключей на 127.0.0.1:%d", version, hysteria.AuthPort)
	tooMany := func(u *store.User, addr string) bool {
		return !e.ipLimits.Allow(u.ID, addr, u.IPLimit)
	}
	if err := hysteria.StartAuthServer(ctx, e.st, g.has, tooMany); err != nil {
		log.Fatal(err)
	}
}

func applyAll(e *env) {
	users := e.live()
	if e.cfg.On(store.ProtoHy2) {
		if changed, err := e.hy.ApplyIfChanged(); err != nil {
			log.Printf("предупреждение: Hysteria2: %v", err)
		} else if changed {
			log.Print("конфиг Hysteria2 обновлён")
		}
	}
	if (e.cfg.On(store.ProtoReality) || e.cfg.On(store.ProtoEnc) || e.cfg.On(store.ProtoSS)) && xray.Installed() {
		if changed, err := e.xr.ApplyIfChanged(users); err != nil {
			log.Printf("предупреждение: Xray: %v", err)
		} else if changed {
			log.Print("конфиг Xray обновлён")
		}
	}
}

func setup(args []string) {
	fs := flag.NewFlagSet("setup", flag.ExitOnError)
	host := fs.String("host", "", "публичный IP/домен сервера")
	port := fs.Int("port", 2096, "порт Hysteria2 (UDP)")
	sni := fs.String("sni", config.DefaultSNI, "домен маскировки")
	dns := fs.String("dns", config.DefaultDNS, "DNS сервера")
	force := fs.Bool("force", false, "перезаписать существующий конфиг")
	_ = fs.Parse(args)

	paths := config.DefaultPaths()
	if _, err := os.Stat(paths.ConfigFile); err == nil && !*force {
		log.Fatalf("конфиг уже существует: %s (перезапись — флаг --force)", paths.ConfigFile)
	}

	cfg := config.NewDefault()
	cfg.SetPath(paths.ConfigFile)
	cfg.VPNPort = *port
	cfg.SetSNI(*sni)
	cfg.DNS = *dns
	cfg.EnsureDefaults()

	pubHost := firstNonEmpty(*host, detectHost())
	if !config.ValidHost(pubHost) {
		log.Fatalf("не удалось определить публичный адрес сервера (получено %q).\n"+
			"Укажи его вручную: mor setup --host <публичный-IP-или-домен> --force", pubHost)
	}
	cfg.PublicHost = pubHost
	fatal(cfg.Save())

	st, err := store.Open(paths.DataFile)
	fatal(err)
	users := st.List()

	if err := hysteria.New(cfg, paths).WriteConfig(); err != nil {
		log.Printf("предупреждение: конфиг Hysteria2: %v", err)
	}
	if err := xray.New(cfg, paths).WriteConfig(users); err != nil {
		log.Printf("предупреждение: конфиг Xray: %v", err)
	}
	fmt.Printf("  настроено: %s\n", cfg.PublicHost)
}

type env struct {
	cfg   *config.Config
	st    *store.Store
	hy    *hysteria.Manager
	xr    *xray.Manager
	stats *stats.Stats
	hist  *stats.History
	audit *auditlog.Log
	paths config.Paths

	// ipLimits lives only in memory: counting concurrent devices needs no
	// history, and keeping one would mean recording where people connect from.
	ipLimits *hysteria.IPTracker
}

func load() (*env, error) {
	paths := config.DefaultPaths()
	cfg, err := config.Load(paths.ConfigFile)
	if err != nil {
		return nil, err
	}
	cfg.SetPath(paths.ConfigFile)
	st, err := store.Open(paths.DataFile)
	if err != nil {
		return nil, err
	}
	st2, err := stats.Open(paths.StatsFile)
	if err != nil {
		return nil, err
	}
	hist, err := stats.OpenHistory(paths.HistoryFile)
	if err != nil {
		return nil, err
	}
	al, err := auditlog.Open(paths.AuditLogFile)
	if err != nil {
		return nil, err
	}
	return &env{
		cfg:      cfg,
		st:       st,
		hy:       hysteria.New(cfg, paths),
		xr:       xray.New(cfg, paths),
		stats:    st2,
		hist:     hist,
		audit:    al,
		paths:    paths,
		ipLimits: hysteria.NewIPTracker(),
	}, nil
}

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
	case store.ProtoEnc:
		if !xray.Installed() {
			return nil, fmt.Errorf("Xray не установлен — переустанови mor")
		}
		u.UUID = keys.UUID()
		u.SNI = ""
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
	if proto == store.ProtoReality || proto == store.ProtoEnc || proto == store.ProtoSS {
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
	case store.ProtoReality, store.ProtoEnc, store.ProtoSS:
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
		if u.Proto != store.ProtoReality && u.Proto != store.ProtoEnc && u.Proto != store.ProtoSS {
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

func detectHost() string {
	c, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return "127.0.0.1"
	}
	defer c.Close()
	if a, ok := c.LocalAddr().(*net.UDPAddr); ok {
		return a.IP.String()
	}
	return "127.0.0.1"
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func fatal(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
