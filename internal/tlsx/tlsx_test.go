package tlsx

import (
	"fmt"
	"net"
	"testing"
	"time"
)

// One client that connects and says nothing must not stop everyone else: the
// sniffing happens per connection, not inside Accept.
func TestSilentClientDoesNotBlockOthers(t *testing.T) {
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := raw.Addr().(*net.TCPAddr).Port
	ln := New(raw, port, "тест")
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
