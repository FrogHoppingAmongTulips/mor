// Package tlsx holds the one trick the panel and the subscription both need:
// answering both TLS and plain HTTP on a single port, so a link that was handed
// out before HTTPS existed still lands where it should.
package tlsx

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"
)

// tlsFirstByte is the ContentType of a TLS handshake record. A browser asked
// for plain HTTP starts with a method name instead — "GET", "POST" — so one
// byte tells the two apart before anything is decrypted.
const tlsFirstByte = 0x16

// sniffWait caps how long a new connection may stay silent while we work out
// which protocol it speaks.
const sniffWait = 10 * time.Second

// RedirectingListener serves TLS on a port, but answers a plain HTTP request
// with a redirect to the same address over HTTPS.
//
// Without it the port greets an old bookmark, or anyone who typed the address
// without a scheme, with "Client sent an HTTP request to an HTTPS server" —
// which is what a Go TLS server says and what nobody can act on. For the
// subscription it is what keeps links handed out before HTTPS working: the app
// asks for http, gets a redirect, and follows it.
//
// Each connection is sniffed in its own goroutine and only then queued: doing
// it inside Accept would let one client that connects and says nothing hold up
// everybody else.
type RedirectingListener struct {
	net.Listener
	port  int
	what  string // what lives on this port, for the redirect body
	ready chan net.Conn
	done  chan struct{}
	once  sync.Once
}

func New(inner net.Listener, port int, what string) *RedirectingListener {
	l := &RedirectingListener{
		Listener: inner,
		port:     port,
		what:     what,
		ready:    make(chan net.Conn),
		done:     make(chan struct{}),
	}
	go l.run()
	return l
}

func (l *RedirectingListener) run() {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			l.once.Do(func() { close(l.done) })
			return
		}
		go l.sniff(c)
	}
}

func (l *RedirectingListener) sniff(c net.Conn) {
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

func (l *RedirectingListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.ready:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *RedirectingListener) Close() error {
	l.once.Do(func() { close(l.done) })
	return l.Listener.Close()
}

func (l *RedirectingListener) redirect(c net.Conn, br *bufio.Reader) {
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
	// The port is carried over rather than dropped: without it the
	// redirect would land on 443, where the panel does not live.
	if h, _, splitErr := net.SplitHostPort(host); splitErr == nil {
		host = h
	}
	target := fmt.Sprintf("https://%s:%d%s", host, l.port, req.URL.RequestURI())
	body := l.what + " работает по https. Открой " + target + "\n"
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
