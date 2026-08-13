package hysteria

import (
	"strings"
	"testing"

	"mor/internal/config"
)

// Scrambling is off unless someone asks for it: switching it on rewrites every
// link, and a default that quietly did that would cut off everyone holding one.
func TestObfsOffByDefault(t *testing.T) {
	cfg := &config.Config{VPNPort: 2096, SNI: "www.cloudflare.com"}
	if got := New(cfg, config.DefaultPaths()).BuildConfig(); strings.Contains(got, "obfs") {
		t.Errorf("обфускация появилась сама:\n%s", got)
	}
}

// Server and app have to agree on the password. If the config gets it and the
// link does not, the app speaks plain QUIC to a server that no longer answers
// it — and the failure looks exactly like a blocked port.
func TestObfsReachesBothSides(t *testing.T) {
	const pass = "s3cr3t-obfs"
	cfg := &config.Config{VPNPort: 2096, SNI: "www.cloudflare.com", HyObfs: pass}

	conf := New(cfg, config.DefaultPaths()).BuildConfig()
	if !strings.Contains(conf, "type: salamander") || !strings.Contains(conf, "password: "+pass) {
		t.Errorf("в конфиге сервера нет обфускации:\n%s", conf)
	}
}

// The resolver block used to be the last thing in the file; obfs now sits in
// front of it, and gluing the two together would produce invalid YAML.
func TestObfsDoesNotSwallowResolver(t *testing.T) {
	cfg := &config.Config{VPNPort: 2096, SNI: "s", DNS: "1.1.1.1", HyObfs: "p"}
	conf := New(cfg, config.DefaultPaths()).BuildConfig()
	if !strings.Contains(conf, "\nresolver:\n") {
		t.Errorf("resolver потерялся или слипся с obfs:\n%s", conf)
	}
	for _, line := range strings.Split(conf, "\n") {
		if strings.HasPrefix(line, "obfs:") && strings.Contains(line, "resolver") {
			t.Errorf("две секции в одной строке: %q", line)
		}
	}
}
