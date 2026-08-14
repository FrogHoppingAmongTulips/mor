package main

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"net"
	"os"
	"path/filepath"
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
