package main

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mor/internal/config"
)

func tempCertPaths(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	return filepath.Join(dir, "web.crt"), filepath.Join(dir, "web.key")
}

func parseCert(t *testing.T, path string) *x509.Certificate {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(b)
	if block == nil {
		t.Fatal("не PEM")
	}
	c, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// The fallback exists so the panel is never plain HTTP. If it cannot produce a
// usable pair, the panel has nothing to fall back to.
func TestSelfSignedPairLoads(t *testing.T) {
	certPath, keyPath := tempCertPaths(t)
	if err := writeSelfSigned(certPath, keyPath, "203.0.113.7"); err != nil {
		t.Fatal(err)
	}
	if _, err := tls.LoadX509KeyPair(certPath, keyPath); err != nil {
		t.Fatalf("пара не грузится: %v", err)
	}
}

// A certificate for a bare IP must carry it as an IP SAN; browsers reject an
// address hiding in the common name.
func TestSelfSignedPutsIPInSAN(t *testing.T) {
	certPath, keyPath := tempCertPaths(t)
	if err := writeSelfSigned(certPath, keyPath, "203.0.113.7"); err != nil {
		t.Fatal(err)
	}
	c := parseCert(t, certPath)
	if err := c.VerifyHostname("203.0.113.7"); err != nil {
		t.Fatalf("сертификат не подходит для своего же адреса: %v", err)
	}
	if len(c.DNSNames) != 0 {
		t.Fatalf("IP попал в DNS-имена: %v", c.DNSNames)
	}
}

func TestSelfSignedPutsDomainInSAN(t *testing.T) {
	certPath, keyPath := tempCertPaths(t)
	if err := writeSelfSigned(certPath, keyPath, "vpn.example.com"); err != nil {
		t.Fatal(err)
	}
	c := parseCert(t, certPath)
	if err := c.VerifyHostname("vpn.example.com"); err != nil {
		t.Fatalf("сертификат не подходит для своего же домена: %v", err)
	}
}

// The private key is the whole secret; group- or world-readable would hand the
// panel to any other account on the box.
func TestSelfSignedKeyIsPrivate(t *testing.T) {
	certPath, keyPath := tempCertPaths(t)
	if err := writeSelfSigned(certPath, keyPath, "203.0.113.7"); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Fatalf("права на ключ %04o, нужно 0600", perm)
	}
}

// Renewal replaces the files under a running panel every couple of days. If the
// keeper cached the first read forever, the panel would serve an expired
// certificate until somebody restarted it.
func TestCertKeeperPicksUpReplacement(t *testing.T) {
	certPath, keyPath := tempCertPaths(t)
	if err := writeSelfSigned(certPath, keyPath, "203.0.113.7"); err != nil {
		t.Fatal(err)
	}
	k := newCertKeeper(certPath, keyPath)
	first, err := k.get(nil)
	if err != nil {
		t.Fatal(err)
	}

	// A new pair for a different address, with a timestamp the keeper can see.
	if err := writeSelfSigned(certPath, keyPath, "198.51.100.4"); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(time.Second)
	for _, p := range []string{certPath, keyPath} {
		if err := os.Chtimes(p, future, future); err != nil {
			t.Fatal(err)
		}
	}

	second, err := k.get(nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(first.Certificate[0]) == string(second.Certificate[0]) {
		t.Fatal("сертификат не перечитан после замены на диске")
	}
	leaf, err := x509.ParseCertificate(second.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := leaf.VerifyHostname("198.51.100.4"); err != nil {
		t.Fatalf("отдан старый сертификат: %v", err)
	}
}

// acme.sh writes the certificate and the key one after the other. A handshake
// landing between the two must not take the panel down.
func TestCertKeeperSurvivesHalfWrittenPair(t *testing.T) {
	certPath, keyPath := tempCertPaths(t)
	if err := writeSelfSigned(certPath, keyPath, "203.0.113.7"); err != nil {
		t.Fatal(err)
	}
	k := newCertKeeper(certPath, keyPath)
	if _, err := k.get(nil); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(certPath, []byte("-----BEGIN CERTIFICATE-----\nобрывок\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(time.Second)
	if err := os.Chtimes(certPath, future, future); err != nil {
		t.Fatal(err)
	}

	got, err := k.get(nil)
	if err != nil {
		t.Fatalf("панель упала на недописанном сертификате: %v", err)
	}
	leaf, err := x509.ParseCertificate(got.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := leaf.VerifyHostname("203.0.113.7"); err != nil {
		t.Fatal("отдан не прежний рабочий сертификат")
	}
}

func TestCertKeeperFailsWhenNothingToServe(t *testing.T) {
	certPath, keyPath := tempCertPaths(t)
	if _, err := newCertKeeper(certPath, keyPath).get(nil); err == nil {
		t.Fatal("без файлов должна быть ошибка, а не пустой сертификат")
	}
}

// The panel says which certificate it is on, so a browser warning has an
// explanation instead of looking like a break-in.
func TestCertSummaryTellsSelfSignedApart(t *testing.T) {
	certPath, keyPath := tempCertPaths(t)
	if err := writeSelfSigned(certPath, keyPath, "203.0.113.7"); err != nil {
		t.Fatal(err)
	}
	if !certIsSelfSigned(certPath) {
		t.Fatal("свой сертификат не опознан как самоподписанный")
	}
	if got := certSummary(certPath); got == "нет" || got == "повреждён" {
		t.Fatalf("описание сертификата: %q", got)
	}
}

func TestCertSummaryHandlesMissingAndBroken(t *testing.T) {
	certPath, _ := tempCertPaths(t)
	if got := certSummary(certPath); got != "нет" {
		t.Fatalf("для отсутствующего файла: %q", got)
	}
	if err := os.WriteFile(certPath, []byte("не сертификат"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := certSummary(certPath); got != "повреждён" {
		t.Fatalf("для мусора: %q", got)
	}
}

// tlsConfig is what the server actually calls: with no certificate on disk it
// has to produce one rather than refuse to start.
func TestTLSConfigBootstrapsItself(t *testing.T) {
	dir := t.TempDir()
	cfg := config.NewDefault()
	cfg.PublicHost = "203.0.113.7"
	e := &env{cfg: cfg, paths: config.Paths{
		BaseDir:     dir,
		WebCertFile: filepath.Join(dir, "web.crt"),
		WebKeyFile:  filepath.Join(dir, "web.key"),
	}}

	tc, err := tlsConfig(e)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := tc.GetCertificate(&tls.ClientHelloInfo{ServerName: ""})
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := leaf.VerifyHostname("203.0.113.7"); err != nil {
		t.Fatalf("сгенерирован сертификат не для своего адреса: %v", err)
	}
	if tc.MinVersion < tls.VersionTLS12 {
		t.Fatalf("MinVersion %x — ниже TLS 1.2", tc.MinVersion)
	}
	if net.ParseIP(e.cfg.PublicHost) == nil {
		t.Fatal("тест потерял адрес")
	}
}

// A plain HTTP request to the panel's port must come back as a redirect, not
// as "Client sent an HTTP request to an HTTPS server" — the message a Go TLS
// server produces and that nobody can act on.
func TestPlainHTTPGetsRedirected(t *testing.T) {
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := raw.Addr().(*net.TCPAddr).Port
	ln := newRedirectingListener(raw, port)
	defer ln.Close()

	certPath, keyPath := tempCertPaths(t)
	if err := writeSelfSigned(certPath, keyPath, "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	k := newCertKeeper(certPath, keyPath)
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("панель"))
	})}
	srv.TLSConfig = &tls.Config{GetCertificate: k.get, MinVersion: tls.VersionTLS12}
	go srv.ServeTLS(ln, "", "")
	defer srv.Close()

	// Plain HTTP.
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/keys", port))
	if err != nil {
		t.Fatalf("простой HTTP оборвался: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusPermanentRedirect {
		t.Fatalf("код %d, ждали 308", resp.StatusCode)
	}
	want := fmt.Sprintf("https://127.0.0.1:%d/keys", port)
	if got := resp.Header.Get("Location"); got != want {
		t.Fatalf("Location %q, ждали %q — путь и порт должны сохраняться", got, want)
	}
}

// The same listener must keep serving TLS normally.
func TestTLSStillWorksThroughRedirectingListener(t *testing.T) {
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := raw.Addr().(*net.TCPAddr).Port
	ln := newRedirectingListener(raw, port)
	defer ln.Close()

	certPath, keyPath := tempCertPaths(t)
	if err := writeSelfSigned(certPath, keyPath, "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	k := newCertKeeper(certPath, keyPath)
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("панель"))
	})}
	srv.TLSConfig = &tls.Config{GetCertificate: k.get, MinVersion: tls.VersionTLS12}
	go srv.ServeTLS(ln, "", "")
	defer srv.Close()

	client := &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}}
	resp, err := client.Get(fmt.Sprintf("https://127.0.0.1:%d/", port))
	if err != nil {
		t.Fatalf("TLS не прошёл: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "панель" {
		t.Fatalf("тело %q", body)
	}
}

// One client that connects and says nothing must not stop everyone else: the
// sniffing happens per connection, not inside Accept.
func TestSilentClientDoesNotBlockOthers(t *testing.T) {
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := raw.Addr().(*net.TCPAddr).Port
	ln := newRedirectingListener(raw, port)
	defer ln.Close()

	silent, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatal(err)
	}
	defer silent.Close()

	done := make(chan net.Conn, 1)
	go func() {
		c, err := ln.Accept()
		if err == nil {
			done <- c
		}
	}()

	// A real TLS client arriving after the silent one must still be accepted.
	go func() {
		c, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err == nil {
			_, _ = c.Write([]byte{tlsFirstByte, 0x03, 0x01})
		}
	}()

	select {
	case c := <-done:
		c.Close()
	case <-time.After(3 * time.Second):
		t.Fatal("молчащее соединение заблокировало приём остальных")
	}
}

// Every response carries the headers, including error responses and static
// ones — a header applied only to the routes somebody remembered protects
// nothing.
func TestSecurityHeadersOnEveryResponse(t *testing.T) {
	page, err := webFS.ReadFile("web/panel.html")
	if err != nil {
		t.Fatal(err)
	}
	h := withSecurityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/boom" {
			http.Error(w, "нет", http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte("ок"))
	}), contentSecurityPolicy(page))
	srv := httptest.NewServer(h)
	defer srv.Close()

	for _, path := range []string{"/", "/boom"} {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		for header, want := range map[string]string{
			"X-Frame-Options":        "DENY",
			"Referrer-Policy":        "no-referrer",
			"X-Content-Type-Options": "nosniff",
		} {
			if got := resp.Header.Get(header); got != want {
				t.Errorf("%s на %s: %q, ждали %q", header, path, got, want)
			}
		}
		csp := resp.Header.Get("Content-Security-Policy")
		if csp == "" {
			t.Fatalf("нет CSP на %s", path)
		}
		// The two that carry the weight while inline code is still allowed:
		// nothing may be fetched from elsewhere, and nothing may be sent there.
		for _, must := range []string{"default-src 'none'", "connect-src 'self'", "frame-ancestors 'none'"} {
			if !strings.Contains(csp, must) {
				t.Errorf("в CSP нет %q: %s", must, csp)
			}
		}
	}
}

// The policy must name the page's own blocks by hash and allow nothing else —
// 'unsafe-inline' anywhere in script-src would mean an injected script runs.
func TestCSPPinsThePageByHash(t *testing.T) {
	page, err := webFS.ReadFile("web/panel.html")
	if err != nil {
		t.Fatal(err)
	}
	csp := contentSecurityPolicy(page)

	if strings.Contains(csp, "unsafe-inline") || strings.Contains(csp, "unsafe-eval") {
		t.Fatalf("политика ослаблена: %s", csp)
	}
	if !strings.Contains(csp, "script-src 'sha256-") {
		t.Fatalf("скрипт не закреплён хешем: %s", csp)
	}
	if !strings.Contains(csp, "style-src 'sha256-") {
		t.Fatalf("стили не закреплены хешем: %s", csp)
	}
}

// A hash that does not match what the browser sees would block the whole
// panel, so it is computed from exactly the bytes between the tags.
func TestInlineHashMatchesTheBlock(t *testing.T) {
	page := []byte("<html><style>тело-стиля</style><script>тело-скрипта</script></html>")

	want := func(body string) string {
		sum := sha256.Sum256([]byte(body))
		return "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
	}
	if got := inlineHash(page, "<script>", "</script>"); got != want("тело-скрипта") {
		t.Fatalf("скрипт: %s", got)
	}
	if got := inlineHash(page, "<style>", "</style>"); got != want("тело-стиля") {
		t.Fatalf("стиль: %s", got)
	}
}

// A page without the block must deny rather than fall back to allowing
// anything — the panel breaking loudly beats it running unverified code.
func TestInlineHashDeniesWhenBlockMissing(t *testing.T) {
	if got := inlineHash([]byte("<html></html>"), "<script>", "</script>"); got != "'none'" {
		t.Fatalf("без блока: %s", got)
	}
	if got := inlineHash([]byte("<script>без закрытия"), "<script>", "</script>"); got != "'none'" {
		t.Fatalf("без закрывающего тега: %s", got)
	}
}
