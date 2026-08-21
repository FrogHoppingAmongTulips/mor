package main

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"mor/internal/config"
	"mor/internal/proxy"
	"mor/internal/store"
	"mor/internal/sub"
	"mor/internal/webauth"
)

// screen runs one menu screen with canned input and returns what it printed.
func screen(t *testing.T, e *env, input string, show func(*menu)) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w

	done := make(chan string)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()

	show(&menu{e: e, in: bufio.NewReader(strings.NewReader(input))})

	w.Close()
	os.Stdout = old
	return <-done
}

func panelEnv(t *testing.T, hash string) *env {
	t.Helper()
	dir := t.TempDir()
	cfg := config.NewDefault()
	// The real config always comes through Load, which fills the ports in.
	cfg.EnsureDefaults()
	cfg.SetPath(dir + "/config.json")
	cfg.PublicHost = "203.0.113.7"
	cfg.WebPasswordHash = hash
	return &env{cfg: cfg, paths: config.Paths{WebCertFile: dir + "/web.crt"}}
}

// The menu is what most people ever see. Until this screen existed it said
// nothing about the panel at all, and the only way to switch it on was a line
// in `help` — which is exactly the question this came from.
func TestPanelScreenOffersThePasswordWhenThereIsNone(t *testing.T) {
	out := screen(t, panelEnv(t, ""), "\n", func(m *menu) { m.panel() })

	for _, want := range []string{"Пароль", "Случайный пароль", "Свой пароль", "Порт 9090"} {
		if !strings.Contains(out, want) {
			t.Errorf("на экране нет %q:\n%s", want, out)
		}
	}
}

// The screen is actions and nothing else: the address and the password are in
// the header of every screen already, and the certificate looks after itself.
func TestPanelScreenCarriesNoText(t *testing.T) {
	e := panelEnv(t, "")
	e.cfg.SetWebPassword("мойпароль123")
	out := screen(t, e, "\n", func(m *menu) { m.panel() })

	for _, unwanted := range []string{"https://", "мойпароль123", "сертификат", "Выключить"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("на экране лишнее «%s»:\n%s", unwanted, out)
		}
	}
	// Three rows, nothing else. The numbers are bold, so the escapes have to
	// go before counting them.
	plain := ansi.ReplaceAllString(out, "")
	for _, want := range []string{"1  Случайный пароль", "2  Свой пароль", "3  Порт 9090"} {
		if !strings.Contains(plain, want) {
			t.Errorf("нет строки «%s»:\n%s", want, plain)
		}
	}
	if strings.Contains(plain, "4  ") {
		t.Errorf("появился четвёртый пункт:\n%s", plain)
	}
}

var ansi = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

func TestPanelIsInTheMainMenu(t *testing.T) {
	found := false
	for _, it := range menuItems {
		if it.title == "Пароль" {
			found = true
		}
	}
	if !found {
		t.Fatal("пункта «Пароль» нет в главном меню")
	}
}

func TestShortPasswordIsRefused(t *testing.T) {
	e := panelEnv(t, "")
	m := &menu{e: e, in: bufio.NewReader(strings.NewReader("1234567\n"))}
	msg, ok := m.askPanelPassword()
	if ok {
		t.Fatal("пароль из семи знаков приняли")
	}
	if !strings.Contains(msg, "8") {
		t.Errorf("непонятная причина отказа: %q", msg)
	}
	if e.cfg.WebPasswordHash != "" {
		t.Error("пароль всё-таки записан")
	}
}

// The installer generates one so the panel works out of the box.
func TestGeneratedPasswordIsUsable(t *testing.T) {
	seen := map[string]bool{}
	for range 50 {
		pw := webauth.NewPassword()
		if len(pw) != 16 {
			t.Fatalf("длина %d, ожидалось 16: %q", len(pw), pw)
		}
		if strings.ContainsAny(pw, "0O1lI5S") {
			t.Errorf("в пароле легко спутать знаки: %q", pw)
		}
		if seen[pw] {
			t.Fatalf("пароль повторился: %q", pw)
		}
		seen[pw] = true
	}
}

// The hash and the readable copy are written together, so a login can never
// disagree with what the menu shows.
func TestSetWebPasswordKeepsBothInStep(t *testing.T) {
	c := config.NewDefault()
	c.SetWebPassword("пароль-которым-входят")

	if c.WebPassword != "пароль-которым-входят" {
		t.Errorf("читаемая копия: %q", c.WebPassword)
	}
	if !webauth.VerifyPassword(c.WebPasswordHash, c.WebPassword) {
		t.Error("показанным паролем нельзя войти")
	}
}

// The subscription check talks to the port over whatever it actually speaks.
// When the subscription moved to HTTPS this probe stayed on http, followed the
// redirect to 127.0.0.1, failed the certificate — issued for the public host,
// not for loopback — and reported a dead subscription on every server that had
// a real certificate.
func TestSubscriptionCheckSpeaksTLSWhenTheSubscriptionDoes(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir + "/users.json")
	if err != nil {
		t.Fatal(err)
	}
	u, err := st.Add(&store.User{Name: "телефон", Proto: store.ProtoHy2})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetSub(u.ID, "a1b2c3"); err != nil {
		t.Fatal(err)
	}

	// Named for the public host and signed by nobody. The common name is what
	// tells mor's own stand-in apart, so a different one makes this count as a
	// real certificate — which is what puts the subscription on https.
	certPath, keyPath := dir+"/web.crt", dir+"/web.key"
	if err := writeNamedCert(t, certPath, keyPath, "203.0.113.7"); err != nil {
		t.Fatal(err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	cfg := config.NewDefault()
	cfg.EnsureDefaults()
	cfg.SetPath(dir + "/config.json")
	cfg.PublicHost = "203.0.113.7"
	cfg.SubPort = port
	e := &env{cfg: cfg, st: st, paths: config.Paths{WebCertFile: certPath, WebKeyFile: keyPath}}

	if !subSecure(e) {
		t.Fatal("подписка не считается защищённой — проверять нечего")
	}

	tlsCfg, err := tlsConfig(e)
	if err != nil {
		t.Fatal(err)
	}
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	h := sub.New(st, func(*store.User) (proxy.Proxy, bool) {
		return proxy.Proxy{Name: "телефон", Kind: proxy.Hysteria2, Server: "203.0.113.7", Port: 443, Password: "п"}, true
	}, "сервер", nil)
	go sub.Serve(ctx, port, h, tlsCfg)

	// The server needs a moment to bind before the check means anything.
	var got *problem
	for range 50 {
		got = subProblem(e)
		if got == nil {
			return
		}
		time.Sleep(40 * time.Millisecond)
	}
	t.Fatalf("подписка по https объявлена нерабочей: %s", got.text)
}

func writeNamedCert(t *testing.T, certPath, keyPath, host string) error {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	tmpl := x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: host},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IPAddresses:  []net.IP{net.ParseIP(host)},
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		return err
	}
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		return err
	}
	kd, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	return os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kd}), 0o600)
}
