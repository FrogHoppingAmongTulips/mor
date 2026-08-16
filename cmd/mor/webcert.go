package main

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// acmeHome is where acme.sh installs itself. It keeps its account key, its
// issued certificates and its own renewal cron there.
const acmeHome = "/root/.acme.sh"

func acmeBin() string { return filepath.Join(acmeHome, "acme.sh") }

// issueCert gets a real certificate for the panel and points mor at it.
//
// The work is handed to acme.sh rather than done here: ACME is a protocol with
// account keys, nonces, challenge polling and renewal scheduling, and a
// hand-rolled client is a bad place to learn that. acme.sh is one shell script,
// installs with a single fetch, updates itself and writes its own cron — which
// is what makes this repeatable on a server mor has never seen.
//
// host may be a domain or a bare IP. Let's Encrypt only issues for an IP under
// its short-lived profile (about six days), so that profile is requested
// whenever the target is an address.
func issueCert(e *env, host string) error {
	if host == "" {
		host = e.cfg.PublicHost
	}
	if host == "" {
		return fmt.Errorf("не знаю адрес сервера — задай его: mor host <домен или ip>")
	}
	if err := ensureAcme(); err != nil {
		return err
	}

	args := []string{
		"--issue", "--standalone",
		"--domain", host,
		"--server", "letsencrypt",
		// Renewal has to keep working after mor stops looking: acme.sh's own
		// cron calls this back, and reloadcmd restarts mor so the fresh files
		// are picked up even if the running process somehow missed them.
		// Продление идёт от root, а служба работает не от root: без смены
		// владельца свежий сертификат оказался бы ей недоступен. Запуск службы
		// чинит права и сам, но чинить их сразу дешевле, чем ждать перезапуска.
		"--reloadcmd", "chown -R mor /etc/mor 2>/dev/null; systemctl restart mor",
		"--cert-file", e.paths.WebCertFile,
		"--key-file", e.paths.WebKeyFile,
		"--fullchain-file", e.paths.WebCertFile,
	}
	if net.ParseIP(host) != nil {
		// The renewal window has to be spelled out for this profile. acme.sh
		// renews sixty days before expiry by default, which for a certificate
		// that lives six days means never — it would expire with the cron
		// running and nothing to show for it. Three days leaves half the
		// lifetime to retry in.
		args = append(args, "--cert-profile", "shortlived", "--days", "3")
	}

	fmt.Printf("  выпускаю сертификат для %s…\n", host)
	fmt.Println("  нужен свободный порт 80 снаружи, это занимает до минуты")
	cmd := exec.Command(acmeBin(), args...)
	cmd.Env = append(os.Environ(), "LE_WORKING_DIR="+acmeHome)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("acme.sh: %s", lastLines(string(out), 6))
	}
	if err := os.Chmod(e.paths.WebKeyFile, 0o600); err != nil {
		return err
	}
	return nil
}

// ensureAcme installs acme.sh if it is not there yet.
func ensureAcme() error {
	if fileExists(acmeBin()) {
		return nil
	}
	if _, err := exec.LookPath("curl"); err != nil {
		return fmt.Errorf("нужен curl, установи его и повтори")
	}
	// acme.sh refuses to install without a crontab, and rightly so: a
	// certificate for a bare IP lives about six days, so a server with no cron
	// would quietly serve an expired one by the end of the week.
	if _, err := exec.LookPath("crontab"); err != nil {
		return fmt.Errorf("нужен cron — без него сертификат не будет продлеваться.\n  поставь: apt-get install -y cron  (или dnf install -y cronie)")
	}
	fmt.Println("  ставлю acme.sh…")
	// No contact address is registered. Let's Encrypt accepts an account
	// without one, and inventing a fake address is worse than leaving it
	// empty: it fails validation outright, and a real-looking one would send
	// this server's expiry notices to a stranger.
	script := exec.Command("sh", "-c", "curl -fsSL https://get.acme.sh | sh")
	if out, err := script.CombinedOutput(); err != nil {
		return fmt.Errorf("установка acme.sh: %s", lastLines(string(out), 4))
	}
	if !fileExists(acmeBin()) {
		return fmt.Errorf("acme.sh не установился")
	}
	return nil
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "; ")
}

// cmdPanelCert is "mor panel cert [host]": issue or re-issue the panel's
// certificate. Needed after moving to another server, after the address
// changes, or when a domain replaces a bare IP.
func cmdPanelCert(args []string) {
	e, err := load()
	if err != nil {
		fmt.Println(err)
		return
	}
	host := ""
	if len(args) > 0 {
		host = args[0]
	}
	if err := issueCert(e, host); err != nil {
		fmt.Printf("  не вышло: %v\n", err)
		fmt.Println("  панель продолжает работать на своём сертификате")
		return
	}
	fmt.Printf("  готово: %s\n", certSummary(e.paths.WebCertFile))
	if out, err := exec.Command("systemctl", "restart", "mor").CombinedOutput(); err != nil {
		fmt.Printf("  перезапусти mor вручную: %s\n", lastLines(string(out), 2))
		return
	}
	time.Sleep(time.Second)
	fmt.Printf("  панель: https://%s:%d\n", e.cfg.PublicHost, e.cfg.WebPort)
}
