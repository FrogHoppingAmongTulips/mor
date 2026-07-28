package hysteria

import (
	"net/url"
	"strings"
	"testing"

	"mor/internal/config"
	"mor/internal/store"
)

func TestLink(t *testing.T) {
	cfg := &config.Config{PublicHost: "203.0.113.7", VPNPort: 28217, SNI: "www.microsoft.com"}
	link := Link(cfg, &store.User{Name: "my key", HyToken: "deadbeef"})

	if !strings.HasPrefix(link, "hysteria2://") {
		t.Fatalf("link must start with hysteria2://, got %q", link)
	}

	u, err := url.Parse(link)
	if err != nil {
		t.Fatalf("link does not parse as a URL: %v", err)
	}
	if u.User.Username() != "deadbeef" {
		t.Errorf("token = %q, want deadbeef", u.User.Username())
	}
	if u.Host != "203.0.113.7:28217" {
		t.Errorf("host = %q, want 203.0.113.7:28217", u.Host)
	}
	if got := u.Query().Get("sni"); got != "www.microsoft.com" {
		t.Errorf("sni = %q, want www.microsoft.com", got)
	}
	if got := u.Query().Get("insecure"); got != "1" {
		t.Errorf("insecure = %q, want 1 (self-signed certificate)", got)
	}
	if u.Fragment != "my key" {
		t.Errorf("fragment = %q, want \"my key\"", u.Fragment)
	}
}

func TestLinkIPv6Host(t *testing.T) {
	cfg := &config.Config{PublicHost: "2001:db8::1", VPNPort: 2096, SNI: "www.apple.com"}
	link := Link(cfg, &store.User{Name: "k", HyToken: "tok"})

	u, err := url.Parse(link)
	if err != nil {
		t.Fatalf("IPv6 link does not parse: %v (%s)", err, link)
	}
	if u.Hostname() != "2001:db8::1" {
		t.Errorf("host = %q, want 2001:db8::1", u.Hostname())
	}
}

func TestLinkSNIFallback(t *testing.T) {
	cfg := &config.Config{PublicHost: "203.0.113.7", VPNPort: 443}
	link := Link(cfg, &store.User{Name: "k", HyToken: "tok"})
	u, err := url.Parse(link)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if u.Query().Get("sni") == "" {
		t.Error("SNI is empty: a default domain should have been used")
	}
}

func TestLinkPerUserSNI(t *testing.T) {
	cfg := &config.Config{PublicHost: "203.0.113.7", VPNPort: 2096, SNI: "www.microsoft.com"}

	own := Link(cfg, &store.User{Name: "key", HyToken: "tok", SNI: "kernel.org"})
	if !strings.Contains(own, "sni=kernel.org") {
		t.Fatalf("per-key SNI missing from the link: %s", own)
	}

	shared := Link(cfg, &store.User{Name: "key", HyToken: "tok"})
	if !strings.Contains(shared, "sni=www.microsoft.com") {
		t.Fatalf("empty per-key SNI should fall back to the shared one: %s", shared)
	}
}
