package hysteria

import (
	"strings"
	"testing"

	"mor/internal/config"
)

// Queries go out over HTTPS, not port 53: in the clear they hand whoever runs
// the machine every domain every client opens, and the tunnel does not cover
// the hop from the server to the resolver.
func TestResolverBlock(t *testing.T) {
	withDNS := New(&config.Config{VPNPort: 2096, SNI: "www.microsoft.com", DNS: "1.1.1.1"}, config.DefaultPaths()).BuildConfig()
	if !strings.Contains(withDNS, "type: https") || !strings.Contains(withDNS, "addr: 1.1.1.1:443") {
		t.Fatalf("resolver missing or not over https:\n%s", withDNS)
	}
	if strings.Contains(withDNS, "type: udp") {
		t.Fatalf("запросы всё ещё идут открыто:\n%s", withDNS)
	}

	noDNS := New(&config.Config{VPNPort: 2096, SNI: "kernel.org"}, config.DefaultPaths()).BuildConfig()
	if strings.Contains(noDNS, "resolver:") {
		t.Fatalf("empty DNS must not produce a section:\n%s", noDNS)
	}

	// A port written by hand belonged to plain DNS; over HTTPS the resolver is
	// reached on 443 regardless.
	withPort := New(&config.Config{VPNPort: 2096, DNS: "9.9.9.9:5353"}, config.DefaultPaths()).BuildConfig()
	if !strings.Contains(withPort, "addr: 9.9.9.9:443") {
		t.Fatalf("адрес резолвера собран неверно:\n%s", withPort)
	}

	v6 := New(&config.Config{VPNPort: 2096, DNS: "2606:4700:4700::1111"}, config.DefaultPaths()).BuildConfig()
	if !strings.Contains(v6, "addr: [2606:4700:4700::1111]:443") {
		t.Fatalf("IPv6 resolver written without brackets or port:\n%s", v6)
	}
}
