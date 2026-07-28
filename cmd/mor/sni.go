package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"time"
)

func checkSNI(domain string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()

	if _, err := net.DefaultResolver.LookupHost(ctx, domain); err != nil {
		return fmt.Errorf("домена %s не существует — проверь написание", domain)
	}

	d := &tls.Dialer{Config: &tls.Config{ServerName: domain, NextProtos: []string{"h2", "http/1.1"}}}
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(domain, "443"))
	if err != nil {
		return fmt.Errorf("%s не отвечает по HTTPS — для маскировки не подойдёт", domain)
	}
	defer conn.Close()

	if tc, ok := conn.(*tls.Conn); ok && tc.ConnectionState().Version < tls.VersionTLS13 {
		return fmt.Errorf("у %s старый TLS — маскировка будет заметна", domain)
	}
	return nil
}
