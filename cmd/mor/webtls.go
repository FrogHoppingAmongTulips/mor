package main

import (
	"bufio"
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
	"net/http"
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

// tlsFirstByte is the ContentType of a TLS handshake record. A browser asked
// for plain HTTP starts with a method name instead — "GET", "POST" — so one
// byte tells the two apart before anything is decrypted.
const tlsFirstByte = 0x16

// sniffWait caps how long a new connection may stay silent while we work out
// which protocol it speaks.
const sniffWait = 10 * time.Second

// redirectingListener serves TLS on the panel's port, but answers a plain HTTP
// request with a redirect to the same address over HTTPS.
//
// Without it the panel greets an old bookmark, or anyone who typed the address
// without a scheme, with "Client sent an HTTP request to an HTTPS server" —
// which is what a Go TLS server says and what nobody can act on.
//
// Each connection is sniffed in its own goroutine and only then queued: doing
// it inside Accept would let one client that connects and says nothing hold up
// everybody else.
type redirectingListener struct {
	net.Listener
	port  int
	ready chan net.Conn
	done  chan struct{}
	once  sync.Once
}

func newRedirectingListener(inner net.Listener, port int) *redirectingListener {
	l := &redirectingListener{
		Listener: inner,
		port:     port,
		ready:    make(chan net.Conn),
		done:     make(chan struct{}),
	}
	go l.run()
	return l
}

func (l *redirectingListener) run() {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			l.once.Do(func() { close(l.done) })
			return
		}
		go l.sniff(c)
	}
}

func (l *redirectingListener) sniff(c net.Conn) {
	_ = c.SetReadDeadline(time.Now().Add(sniffWait))
	br := bufio.NewReader(c)
	first, err := br.Peek(1)
	if err != nil {
		_ = c.Close()
		return
	}
	_ = c.SetReadDeadline(time.Time{})

	if first[0] != tlsFirstByte {
		l.redirect(c, br)
		return
	}
	select {
	case l.ready <- &peekedConn{Conn: c, r: br}:
	case <-l.done:
		_ = c.Close()
	}
}

func (l *redirectingListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.ready:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *redirectingListener) Close() error {
	l.once.Do(func() { close(l.done) })
	return l.Listener.Close()
}

func (l *redirectingListener) redirect(c net.Conn, br *bufio.Reader) {
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(sniffWait))
	req, err := http.ReadRequest(br)
	if err != nil {
		return
	}
	host := req.Host
	if host == "" {
		host = c.LocalAddr().String()
	}
	// The panel's port is carried over rather than dropped: without it the
	// redirect would land on 443, where the panel does not live.
	if h, _, splitErr := net.SplitHostPort(host); splitErr == nil {
		host = h
	}
	target := fmt.Sprintf("https://%s:%d%s", host, l.port, req.URL.RequestURI())
	body := "Панель работает по https. Открой " + target + "\n"
	fmt.Fprintf(c, "HTTP/1.1 308 Permanent Redirect\r\n"+
		"Location: %s\r\nContent-Type: text/plain; charset=utf-8\r\n"+
		"Content-Length: %d\r\nConnection: close\r\n\r\n%s",
		target, len(body), body)
}

// peekedConn hands back the byte that was read to identify the protocol.
type peekedConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *peekedConn) Read(b []byte) (int, error) { return c.r.Read(b) }
