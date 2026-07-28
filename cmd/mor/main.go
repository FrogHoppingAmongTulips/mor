package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"mor/internal/config"
	"mor/internal/hysteria"
	"mor/internal/keys"
	"mor/internal/qr"
	"mor/internal/store"
	"mor/internal/xray"
)

var version = "dev"

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
	e, err := load()
	if err != nil {
		log.Fatalf("нет конфига — сначала установи: mor setup (%v)", err)
	}
	printHeader(e)

	in := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("\nmor> ")
		if !in.Scan() {
			fmt.Println()
			return
		}
		line := strings.TrimSpace(in.Text())
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
	case "dns":
		cmdDNS(args)
	case "sni":
		cmdSNI(args)
	case "port":
		cmdPort(args)
	case "status", "info":
		cmdStatus()
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
	fmt.Print(`  user [--reality] <имя>         создать ключ
  list                           все ключи
  qr <номер>                     показать ключ ещё раз
  del <номер>                    удалить ключ
  dns <ip>                       резолвер для всех клиентов
  sni <домен>                    маскировка Hysteria2
  port <номер>                   порт Hysteria2
  status                         состояние сервера
  clear                          очистить экран
  exit                           выйти
`)
}

func printHeader(e *env) {
	fmt.Printf("\n  MOR %s\n", version)
	fmt.Println("\n  user <имя> — создать ключ · help — все команды · exit — выйти")
}

func cmdUser(args []string) {
	proto := store.ProtoHy2
	for len(args) > 0 && strings.HasPrefix(args[0], "--") {
		switch args[0] {
		case "--reality":
			proto = store.ProtoReality
		case "--hy2":
			proto = store.ProtoHy2
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
	u, err := createKey(e, name, proto, "")
	if u == nil {
		fmt.Println(" ", err)
		return
	}

	if err != nil {
		fmt.Println("  предупреждение:", err)
	}
	showKey(e, u)
}

func cmdList() {
	e, err := load()
	if err != nil {
		fmt.Println(err)
		return
	}
	users := e.st.List()
	if len(users) == 0 {
		fmt.Println("  ключей нет — создай: user телефон")
		return
	}
	fmt.Println()
	for i, u := range users {
		fmt.Printf("  %d  %-22s %s\n", i+1, u.Name, store.ProtoName(u.Proto))
	}
}

func cmdShow(args []string) {
	e, u, ok := pick(args)
	if !ok {
		return
	}
	showKey(e, u)
}

func cmdDelete(args []string) {
	e, u, ok := pick(args)
	if !ok {
		return
	}
	if err := e.st.Delete(u.ID); err != nil {
		fmt.Println(err)
		return
	}
	if err := applyProto(e, u.Proto); err != nil {
		fmt.Println(" ", err)
	}
	fmt.Printf("  «%s» удалён\n", u.Name)
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
	e.cfg.SNI = withWWW(domain)
	save(e, "маскировка → "+e.cfg.SNI, store.ProtoHy2)
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
		fmt.Println("  старые ссылки Hysteria2 больше не работают — раздай новые")
	}
}

func cmdClear() {
	e, err := load()
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Print(clearScreen)
	printHeader(e)
}

func cmdStatus() {
	e, err := load()
	if err != nil {
		fmt.Println(err)
		return
	}
	users := e.st.List()
	fmt.Println()
	fmt.Printf("  адрес       %s\n", e.cfg.PublicHost)
	fmt.Printf("  ключей      %d\n", len(users))
	fmt.Printf("  dns         %s\n", e.cfg.DNS)
	fmt.Println()
	fmt.Printf("  Hysteria2   %d/udp · ключей %d · %s\n", e.cfg.VPNPort, countProto(users, store.ProtoHy2), serviceState(hysteria.Service))
	fmt.Printf("  Reality     %d/tcp · ключей %d · %s\n", e.cfg.Reality.Port, countProto(users, store.ProtoReality), protoState(xray.Installed(), xray.Service))
	fmt.Println()
	fmt.Printf("  mor         %s\n", serviceState("mor"))
	fmt.Printf("  версия      %s\n", version)
}

func serviceState(unit string) string {
	out, err := exec.Command("systemctl", "is-active", unit).CombinedOutput()
	state := strings.TrimSpace(string(out))
	if state == "" {
		if err != nil {
			return "нет данных"
		}
		return "нет данных"
	}
	if state == "active" {
		return "работает"
	}
	return state + " — journalctl -u " + unit
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

func protoState(installed bool, unit string) string {
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

	log.Printf("mor %s: проверка ключей на 127.0.0.1:%d", version, hysteria.AuthPort)
	if err := hysteria.StartAuthServer(ctx, e.st); err != nil {
		log.Fatal(err)
	}
}

func applyAll(e *env) {
	users := e.st.List()
	if changed, err := e.hy.ApplyIfChanged(); err != nil {
		log.Printf("предупреждение: Hysteria2: %v", err)
	} else if changed {
		log.Print("конфиг Hysteria2 обновлён")
	}
	if xray.Installed() {
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
	cfg.SNI = *sni
	cfg.DNS = *dns
	cfg.EnsureDefaults()

	pubHost := firstNonEmpty(*host, detectHost())
	if !config.ValidPublicHost(pubHost) {
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
	paths config.Paths
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
	return &env{
		cfg:   cfg,
		st:    st,
		hy:    hysteria.New(cfg, paths),
		xr:    xray.New(cfg, paths),
		paths: paths,
	}, nil
}

func createKey(e *env, name, proto, sni string) (*store.User, error) {
	u := &store.User{Name: name, Proto: proto, SNI: sni}
	switch proto {
	case store.ProtoReality:
		if !xray.Installed() {
			return nil, fmt.Errorf("Xray не установлен — переустанови mor")
		}
		u.UUID = keys.UUID()
	default:
		u.HyToken = keys.Token()
	}
	saved, err := e.st.Add(u)
	if err != nil {
		return nil, err
	}
	if err := applyProto(e, proto); err != nil {
		return saved, err
	}
	return saved, nil
}

func applyProto(e *env, proto string) error {
	users := e.st.List()
	switch proto {
	case store.ProtoReality:
		if xray.Installed() {
			return e.xr.Apply(users)
		}
	}
	return nil
}

func save(e *env, what string, protos ...string) bool {
	if err := e.cfg.Save(); err != nil {
		fmt.Println("  не удалось сохранить:", err)
		return false
	}
	users := e.st.List()
	for _, p := range protos {
		var err error
		switch p {
		case store.ProtoHy2:
			err = e.hy.Apply()
		case store.ProtoReality:
			if xray.Installed() {
				err = e.xr.Apply(users)
			}
		}
		if err != nil {
			fmt.Printf("  сохранено, но %s не перезапустился: %v\n", store.ProtoName(p), err)
			return false
		}
	}
	fmt.Printf("  %s\n", what)
	return true
}

func pick(args []string) (*env, *store.User, bool) {
	e, err := load()
	if err != nil {
		fmt.Println(err)
		return nil, nil, false
	}
	if len(args) == 0 {
		fmt.Println("  укажи номер ключа из list, напр. qr 1")
		return nil, nil, false
	}
	users := e.st.List()
	if n, err := strconv.Atoi(args[0]); err == nil {
		if n < 1 || n > len(users) {
			fmt.Printf("  нет ключа с номером %d — посмотри list\n", n)
			return nil, nil, false
		}
		return e, users[n-1], true
	}
	for _, u := range users {
		if u.ID == args[0] {
			return e, u, true
		}
	}
	fmt.Println("  такого ключа нет — посмотри list")
	return nil, nil, false
}

func keyText(cfg *config.Config, u *store.User) string {
	if u.Proto == store.ProtoReality {
		return xray.Link(cfg, u)
	}
	return hysteria.Link(cfg, u)
}

func showKey(e *env, u *store.User) {
	text := keyText(e.cfg, u)
	fmt.Println()
	if a, err := qr.ASCII(text); err == nil {
		fmt.Println(a)
	}
	fmt.Printf("  %s · %s\n%s\n", u.Name, store.ProtoName(u.Proto), text)
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

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func fatal(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
