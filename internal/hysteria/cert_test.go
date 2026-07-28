package hysteria

import (
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureCertNoSAN(t *testing.T) {
	dir := t.TempDir()
	certFile := filepath.Join(dir, "server.crt")
	keyFile := filepath.Join(dir, "server.key")

	if err := EnsureCert(certFile, keyFile, "www.bing.com"); err != nil {
		t.Fatalf("EnsureCert: %v", err)
	}

	pemBytes, err := os.ReadFile(certFile)
	if err != nil {
		t.Fatalf("read certificate: %v", err)
	}
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		t.Fatal("certificate is not PEM encoded")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse certificate: %v", err)
	}

	if len(cert.DNSNames) != 0 {
		t.Fatalf("certificate has SAN (DNSNames=%v); it breaks the Hysteria2 handshake", cert.DNSNames)
	}
}

func TestEnsureCertIdempotent(t *testing.T) {
	dir := t.TempDir()
	certFile := filepath.Join(dir, "server.crt")
	keyFile := filepath.Join(dir, "server.key")

	if err := EnsureCert(certFile, keyFile, "www.bing.com"); err != nil {
		t.Fatalf("first EnsureCert: %v", err)
	}
	first, err := os.ReadFile(certFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := EnsureCert(certFile, keyFile, "www.bing.com"); err != nil {
		t.Fatalf("second EnsureCert: %v", err)
	}
	second, err := os.ReadFile(certFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatal("EnsureCert regenerated an existing certificate instead of keeping it")
	}
}
