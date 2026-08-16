package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"mor/internal/config"
	"mor/internal/priv"
	"mor/internal/store"
)

// Health checks say nothing when the server is fine.
//
// A screen full of "часы: в порядке · диск: в порядке" reads as solid exactly
// once; after that the eye skips it, and the one line that matters goes with
// it. So every check here returns either nothing at all or a single sentence
// naming what is wrong. Silence means checked and fine — which is why a check
// that could not run says so instead of staying quiet.

type level int

const (
	levelBad     level = iota // broken now
	levelWarn                 // works, heading somewhere bad
	levelUnknown              // could not check
)

type problem struct {
	text  string
	level level
	// code names the problem for the web panel, which speaks more than one
	// language; text stays the Russian the terminal prints. arg carries the
	// one number or list a message needs so the panel can compose its own
	// sentence instead of parsing this one.
	code string
	arg  string
	// fix is what mor can do about this by itself. A problem that carries one
	// turns the check screen from a report into a button: naming a fault and
	// leaving the person to look up the command is half a job.
	fix *repair
}

// repair says what it is about to do before it does it, so the button can be
// read before it is pressed. do returns an error only when the thing genuinely
// did not work — the screen prints that instead of claiming success.
type repair struct {
	doing string
	do    func(e *env) error
}

// certProblem reports a panel certificate that browsers will not accept.
//
// Issuing needs port 80 free, so it can fail at install time and leave the
// panel on its self-signed stand-in. Renewal after that is acme.sh's own cron
// and needs nobody, which is why this is the only place the certificate is
// ever raised — and it says so only when something is actually wrong.
func certProblem(e *env) *problem {
	if !e.cfg.WebOn() || !fileExists(e.paths.WebCertFile) {
		return nil
	}
	if !certIsSelfSigned(e.paths.WebCertFile) {
		return nil
	}
	return &problem{
		text:  "панель на своём сертификате — браузер будет предупреждать",
		code:  "certSelfSigned",
		level: levelWarn,
		fix: &repair{"выпускаю сертификат Let's Encrypt", func(e *env) error {
			return issueCert(e, e.cfg.PublicHost)
		}},
	}
}

// localProblems answers from this machine alone, with no network and no
// waiting. The main menu asks on every redraw, so this has to stay instant.
func localProblems(e *env) []problem {
	out := []problem{}
	add := func(p *problem) {
		if p != nil {
			out = append(out, *p)
		}
	}
	add(clockProblem())
	add(certProblem(e))
	add(diskProblem(e.paths.BaseDir))
	out = append(out, firewallProblems(e)...)
	return out
}

// netProblems need the network and a few seconds, so they run where waiting is
// expected: the check screen and the end of an install.
func netProblems(e *env) []problem {
	out := []problem{}
	add := func(p *problem) {
		if p != nil {
			out = append(out, *p)
		}
	}
	add(dnsProblem(e.cfg.DNS))
	add(maskProblem(e.cfg.SNI))
	add(subProblem(e))
	return out
}

// clockProblem: a server whose clock drifted fails TLS handshakes and counts
// key deadlines wrong, and neither failure says a word about the time.
func clockProblem() *problem {
	out, err := exec.Command("timedatectl", "show", "-p", "NTPSynchronized", "--value").Output()
	if err != nil {
		// No systemd-timesyncd here; the clock is somebody else's business.
		return nil
	}
	if strings.TrimSpace(string(out)) == "yes" {
		return nil
	}
	return &problem{
		text:  "часы сервера не синхронизированы — рукопожатия и сроки ключей могут сбоить",
		code:  "clock",
		level: levelWarn,
		fix: &repair{"включаю синхронизацию часов", func(*env) error {
			if out, err := exec.Command("timedatectl", "set-ntp", "true").CombinedOutput(); err != nil {
				return fmt.Errorf("%s", strings.TrimSpace(string(out)))
			}
			return nil
		}},
	}
}

// diskProblem: with a full disk mor cannot write keys and the engines cannot
// write anything either. The symptoms are wild and never mention the disk.
func diskProblem(dir string) *problem {
	out, err := exec.Command("df", "-Pk", dir).Output()
	if err != nil {
		return &problem{text: "не смог проверить место на диске", code: "diskUnknown", level: levelUnknown}
	}
	return parseDf(string(out))
}

// parseDf reads the last line of `df -Pk`: filesystem, blocks, used, available,
// capacity, mount point.
func parseDf(out string) *problem {
	unknown := &problem{text: "не смог проверить место на диске", code: "diskUnknown", level: levelUnknown}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) < 2 {
		return unknown
	}
	f := strings.Fields(lines[len(lines)-1])
	if len(f) < 5 {
		return unknown
	}
	freeKB, err := strconv.ParseUint(f[3], 10, 64)
	if err != nil {
		return unknown
	}
	used, err := strconv.Atoi(strings.TrimSuffix(f[4], "%"))
	if err != nil {
		return unknown
	}
	switch {
	case freeKB < 100*1024:
		return &problem{text: "на диске почти нет места — ключи могут не сохраниться", code: "diskFull", level: levelBad}
	case used >= 90:
		return &problem{text: fmt.Sprintf("диск занят на %d%%", used), code: "diskBusy", arg: strconv.Itoa(used), level: levelWarn}
	}
	return nil
}

// dnsProblem: a resolver that does not answer leaves every client without a
// single site opening, while the server itself looks perfectly healthy.
func dnsProblem(ip string) *problem {
	if ip == "" {
		return nil
	}
	r := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
			d := net.Dialer{Timeout: 3 * time.Second}
			return d.DialContext(ctx, "udp", net.JoinHostPort(ip, "53"))
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	if _, err := r.LookupHost(ctx, "cloudflare.com"); err != nil {
		return &problem{
			text:  "DNS " + ip + " не отвечает — у людей не будут открываться сайты",
			level: levelBad,
			// The resolver was somebody's deliberate choice, so the button says
			// out loud that it is being replaced rather than quietly doing it.
			fix: &repair{"возвращаю DNS " + config.DefaultDNS, func(e *env) error {
				e.cfg.DNS = config.DefaultDNS
				if !saveQuiet(e, store.ProtoHy2, store.ProtoReality, store.ProtoEnc) {
					return fmt.Errorf("записал, но движок не отозвался")
				}
				return nil
			}},
		}
	}
	return nil
}

// maskProblem: the cover site is checked when it is chosen, but it can die or
// start being blocked months later, and Reality goes down with it.
func maskProblem(domain string) *problem {
	if domain == "" {
		return nil
	}
	if err := checkSNI(domain); err != nil {
		return &problem{
			text:  "маскировка: " + err.Error(),
			level: levelWarn,
			fix: &repair{"возвращаю маскировку " + config.DefaultSNI, func(e *env) error {
				e.cfg.SetSNI(config.DefaultSNI)
				if !saveQuiet(e, store.ProtoHy2, store.ProtoReality) {
					return fmt.Errorf("записал, но движок не отозвался")
				}
				return nil
			}},
		}
	}
	return nil
}

// subProblem: the link people already hold must actually return something.
func subProblem(e *env) *problem {
	if !e.cfg.SubOn() {
		return nil
	}
	token := ""
	for _, u := range e.st.List() {
		if u.Sub != "" {
			token = u.Sub
			break
		}
	}
	if token == "" {
		return nil // nobody has a subscription link yet
	}
	// The scheme has to match what the port actually speaks. Asking an HTTPS
	// subscription over plain http gets a redirect, and following it to
	// 127.0.0.1 fails the certificate — which is issued for the public host —
	// so the check reported a dead subscription on every server that had a
	// real certificate.
	scheme := "http://"
	if subSecure(e) {
		scheme = "https://"
	}
	url := scheme + net.JoinHostPort("127.0.0.1", strconv.Itoa(e.cfg.SubPort)) + "/sub/" + token
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil
	}
	// Talking to ourselves over loopback: the question is whether the
	// subscription answers, not who it claims to be, and the name on the
	// certificate is the public address rather than 127.0.0.1.
	client := &http.Client{
		Timeout: 4 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // loopback liveness probe
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return &problem{text: "раздача ссылок не отвечает — приложения не обновят настройки", level: levelBad, fix: subFix()}
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK || len(body) == 0 {
		return &problem{text: "раздача ссылок отдаёт пустоту — приложения не обновят настройки", level: levelBad, fix: subFix()}
	}
	return nil
}

// firewall

// firewallPorts reports what the firewall lets through, and whether there is a
// firewall to ask at all. A server without one is open already.
func firewallPorts() (map[string]bool, bool) {
	if out, err := priv.Command("ufw", "status").Output(); err == nil && ufwActive(string(out)) {
		return parseUfw(string(out)), true
	}
	if out, err := priv.Command("firewall-cmd", "--list-ports").Output(); err == nil {
		return parseFirewalld(string(out)), true
	}
	return nil, false
}

// ufwActive reads the status line rather than searching the text for "active":
// a disabled firewall prints "Status: inactive", which contains that word and
// would turn a server with no firewall at all into one where every port is shut.
func ufwActive(out string) bool {
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && f[0] == "Status:" {
			return f[1] == "active"
		}
	}
	return false
}

// parseUfw reads the table ufw prints: the port sits in the first column, and
// the same port shows up again as an IPv6 row, which is the same permission.
func parseUfw(out string) map[string]bool {
	open := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		if strings.HasSuffix(f[0], "/tcp") || strings.HasSuffix(f[0], "/udp") {
			open[f[0]] = true
		}
	}
	return open
}

// parseFirewalld reads what firewall-cmd --list-ports prints: one line, ports
// separated by spaces.
func parseFirewalld(out string) map[string]bool {
	open := map[string]bool{}
	for _, p := range strings.Fields(out) {
		open[p] = true
	}
	return open
}

// wantedPorts is what has to be reachable for the server to work at all.
func wantedPorts(e *env) []string {
	out := make([]string, 0, 6)
	for _, t := range targets(e) {
		out = append(out, strconv.Itoa(t.port)+"/"+kindOf(t.udp))
	}
	if e.cfg.WebOn() {
		out = append(out, strconv.Itoa(e.cfg.WebPort)+"/tcp")
	}
	return out
}

// firewallProblems names ports the firewall is holding shut. Without this the
// external check reports "не доходит" and blames the carrier, when the block
// is on this very machine.
func firewallProblems(e *env) []problem {
	open, found := firewallPorts()
	if !found {
		return nil
	}
	shut := []string{}
	for _, p := range wantedPorts(e) {
		if !open[p] {
			shut = append(shut, p)
		}
	}
	if len(shut) == 0 {
		return nil
	}
	return []problem{{
		text:  "закрыт в firewall: " + strings.Join(shut, ", "),
		code:  "firewall",
		arg:   strings.Join(shut, ", "),
		level: levelBad,
		fix: &repair{"открываю порты в firewall", func(e *env) error {
			ensureFirewall(e)
			if len(firewallProblems(e)) > 0 {
				return fmt.Errorf("firewall всё равно их держит")
			}
			return nil
		}},
	}}
}

// ensureFirewall opens every port the server currently needs. It runs after a
// port changes: the installer opened the old one, and nothing else would ever
// open the new one.
func ensureFirewall(e *env) {
	open, found := firewallPorts()
	if !found {
		return
	}
	useUfw := false
	if out, err := priv.Command("ufw", "status").Output(); err == nil && ufwActive(string(out)) {
		useUfw = true
	}
	for _, p := range wantedPorts(e) {
		if open[p] {
			continue
		}
		if useUfw {
			_ = priv.Command("ufw", "allow", p).Run()
			continue
		}
		_ = priv.Command("firewall-cmd", "--permanent", "--add-port="+p).Run()
	}
	if !useUfw {
		_ = priv.Command("firewall-cmd", "--reload").Run()
	}
}

// cache

// The main menu redraws after every action, and asking systemd, df and ufw each
// time turns a menu into a wait. The answer cannot change meaningfully in a few
// seconds, so it is remembered for a few seconds.
var health struct {
	mu   sync.Mutex
	at   time.Time
	list []problem
}

func cachedLocalProblems(e *env) []problem {
	health.mu.Lock()
	defer health.mu.Unlock()
	if time.Since(health.at) < 5*time.Second {
		return health.list
	}
	health.list = localProblems(e)
	health.at = time.Now()
	return health.list
}

// worst returns the single line worth showing, or "" when there is nothing to
// say. Broken outranks a warning, a warning outranks "could not check".
func worst(list []problem) string {
	for _, want := range []level{levelBad, levelWarn, levelUnknown} {
		for _, p := range list {
			if p.level == want {
				return p.text
			}
		}
	}
	return ""
}

// subFix: the link feed lives inside the mor daemon, so the only thing that can
// go wrong on this side is the daemon itself.
func subFix() *repair {
	return &repair{"перезапускаю раздачу ссылок", func(e *env) error {
		if err := restartUnits([]string{"mor"}); err != nil {
			return err
		}
		time.Sleep(2 * time.Second)
		if p := subProblem(e); p != nil {
			return fmt.Errorf("всё равно не отвечает")
		}
		return nil
	}}
}
