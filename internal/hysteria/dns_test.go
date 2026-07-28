package hysteria

import (
	"strings"
	"testing"

	"mor/internal/config"
)

func TestResolverBlock(t *testing.T) {
	withDNS := New(&config.Config{VPNPort: 2096, SNI: "www.microsoft.com", DNS: "1.1.1.1"}, config.DefaultPaths()).BuildConfig()
	if !strings.Contains(withDNS, "resolver:") || !strings.Contains(withDNS, "addr: 1.1.1.1:53") {
		t.Fatalf("resolver missing from config:\n%s", withDNS)
	}

	noDNS := New(&config.Config{VPNPort: 2096, SNI: "kernel.org"}, config.DefaultPaths()).BuildConfig()
	if strings.Contains(noDNS, "resolver:") {
		t.Fatalf("empty DNS must not produce a section:\n%s", noDNS)
	}

	withPort := New(&config.Config{VPNPort: 2096, DNS: "9.9.9.9:5353"}, config.DefaultPaths()).BuildConfig()
	if !strings.Contains(withPort, "addr: 9.9.9.9:5353") {
		t.Fatalf("explicit resolver port was lost:\n%s", withPort)
	}

	v6 := New(&config.Config{VPNPort: 2096, DNS: "2606:4700:4700::1111"}, config.DefaultPaths()).BuildConfig()
	if !strings.Contains(v6, "addr: [2606:4700:4700::1111]:53") {
		t.Fatalf("IPv6 resolver written without brackets or port:\n%s", v6)
	}
}
