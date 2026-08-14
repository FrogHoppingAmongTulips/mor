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
	"strings"
	"syscall"

	"mor/internal/auditlog"
	"mor/internal/config"
	"mor/internal/hysteria"
	"mor/internal/proxy"
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
	case "token":
		cmdToken(args)
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
  panel [password P|on|off|port N|cert HOST]
                        веб-панель — без пароля не запустится
  token [new <имя>|rm <имя>]
                        ключи для API — для скриптов и своих программ
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
				func(id string) (uint64, uint64) { return e.stats.Get(id).Total, limitOf(e, id) }).
				TrackDevices(e.devices)
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

	// devices, unlike ipLimits, is kept: it counts apps that fetched the
	// subscription, which happens once every few hours rather than on every
	// connection, so forgetting it on restart would forget the limit itself.
	devices *sub.Devices
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
		devices:  sub.OpenDevices(paths.DevicesFile),
	}, nil
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
