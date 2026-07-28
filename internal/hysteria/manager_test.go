package hysteria

import (
	"strings"
	"testing"

	"mor/internal/config"
)

func TestBuildConfigDisablesPathMTUDiscovery(t *testing.T) {
	m := New(&config.Config{VPNPort: 443, SNI: "www.microsoft.com"}, config.DefaultPaths())
	cfg := m.BuildConfig()

	if !strings.Contains(cfg, "disablePathMTUDiscovery: true") {
		t.Fatalf("disablePathMTUDiscovery missing: Hysteria2 would exceed the path MTU and stall:\n%s", cfg)
	}
	if !strings.Contains(cfg, "quic:") {
		t.Errorf("disablePathMTUDiscovery belongs in the quic section:\n%s", cfg)
	}
}

func TestBuildConfigPortAndSNI(t *testing.T) {
	m := New(&config.Config{VPNPort: 28217, SNI: "www.apple.com"}, config.DefaultPaths())
	cfg := m.BuildConfig()

	if !strings.Contains(cfg, "listen: :28217") {
		t.Errorf("port missing from config:\n%s", cfg)
	}
	if !strings.Contains(cfg, "url: https://www.apple.com") {
		t.Errorf("masquerade domain missing from config:\n%s", cfg)
	}
}

func TestBuildConfigSNIFallback(t *testing.T) {
	m := New(&config.Config{VPNPort: 443}, config.DefaultPaths())
	cfg := m.BuildConfig()

	if strings.Contains(cfg, "url: https://\n") {
		t.Fatalf("empty SNI produced a broken masquerade url:\n%s", cfg)
	}
}
