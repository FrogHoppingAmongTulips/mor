package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"sync"
	"time"

	"mor/internal/fsutil"
)

// certKeeper serves the panel's TLS certificate and notices when it changes on
// disk.
//
// Reloading matters because the certificate is short-lived: a Let's Encrypt
// certificate issued for a bare IP is good for about six days and is renewed
// every couple of days. Reading it once at startup would mean the panel served
// an expired certificate within the week unless somebody restarted mor by hand.
type certKeeper struct {
	certPath, keyPath string

	mu       sync.Mutex
	cert     *tls.Certificate
	certMod  time.Time
	keyMod   time.Time
	lastFail time.Time
}

func newCertKeeper(certPath, keyPath string) *certKeeper {
	return &certKeeper{certPath: certPath, keyPath: keyPath}
}

// get is the tls.Config callback. It hands back the cached certificate and
// only touches the disk when a file's timestamp moved.
func (k *certKeeper) get(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	k.mu.Lock()
	defer k.mu.Unlock()

	cm, km := modTime(k.certPath), modTime(k.keyPath)
	if k.cert != nil && cm.Equal(k.certMod) && km.Equal(k.keyMod) {
		return k.cert, nil
	}
	cert, err := tls.LoadX509KeyPair(k.certPath, k.keyPath)
	if err != nil {
		// A half-written pair during renewal must not take the panel down:
		// keep serving the previous certificate until the new one is whole.
		if k.cert != nil {
			if time.Since(k.lastFail) > time.Minute {
				k.lastFail = time.Now()
			}
			return k.cert, nil
		}
		return nil, err
	}
	k.cert, k.certMod, k.keyMod = &cert, cm, km
	return k.cert, nil
}

func modTime(path string) time.Time {
	fi, err := os.Stat(path)
	if err != nil {
		return time.Time{}
	}
	return fi.ModTime()
}

// tlsConfig prepares the panel's TLS, writing a self-signed certificate first
// if there is none.
//
// The fallback is the point: a panel that cannot get a real certificate must
// still not serve the password in the clear. A browser warns once on a
// self-signed certificate; plain HTTP warns nobody while showing every key to
// anyone on the path.
func tlsConfig(e *env) (*tls.Config, error) {
	certPath, keyPath := e.paths.WebCertFile, e.paths.WebKeyFile
	if !fileExists(certPath) || !fileExists(keyPath) {
		if err := writeSelfSigned(certPath, keyPath, e.cfg.PublicHost); err != nil {
			return nil, err
		}
	}
	k := newCertKeeper(certPath, keyPath)
	if _, err := k.get(nil); err != nil {
		return nil, err
	}
	return &tls.Config{GetCertificate: k.get, MinVersion: tls.VersionTLS12}, nil
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

// writeSelfSigned makes the stand-in certificate. host goes in as an IP SAN
// when it parses as an address and as a DNS name otherwise, so the certificate
// matches however the panel is reached.
func writeSelfSigned(certPath, keyPath, host string) error {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return err
	}
	tmpl := x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "mor panel"},
		NotBefore:    time.Now().Add(-time.Hour),
		// Long-lived on purpose: a self-signed certificate cannot be revoked
		// and is trusted by hand, so expiry buys nothing and only forces the
		// owner to accept it again.
		NotAfter:              time.Now().AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	if ip := net.ParseIP(host); ip != nil {
		tmpl.IPAddresses = []net.IP{ip}
	} else if host != "" {
		tmpl.DNSNames = []string{host}
	}
	tmpl.IPAddresses = append(tmpl.IPAddresses, net.ParseIP("127.0.0.1"))

	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		return err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err := fsutil.WriteAtomic(certPath, certPEM, 0o600); err != nil {
		return err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return fsutil.WriteAtomic(keyPath, keyPEM, 0o600)
}

// certIsSelfSigned reports whether the certificate in use is mor's own stand-in
// rather than one from a certificate authority — the panel says so, because the
// browser warning it causes should have an explanation somewhere.
func certIsSelfSigned(certPath string) bool {
	b, err := os.ReadFile(certPath)
	if err != nil {
		return false
	}
	block, _ := pem.Decode(b)
	if block == nil {
		return false
	}
	c, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return false
	}
	return c.Subject.CommonName == "mor panel"
}

// certSummary describes the certificate for the panel and the terminal.
func certSummary(certPath string) string {
	b, err := os.ReadFile(certPath)
	if err != nil {
		return "нет"
	}
	block, _ := pem.Decode(b)
	if block == nil {
		return "повреждён"
	}
	c, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return "повреждён"
	}
	kind := "Let's Encrypt"
	if c.Subject.CommonName == "mor panel" {
		kind = "самоподписанный"
	}
	left := time.Until(c.NotAfter)
	if left <= 0 {
		return kind + ", истёк"
	}
	if left < 48*time.Hour {
		return fmt.Sprintf("%s, осталось %d ч", kind, int(left.Hours()))
	}
	return fmt.Sprintf("%s, осталось %d д", kind, int(left.Hours()/24))
}
