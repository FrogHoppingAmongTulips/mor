package xray

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"mor/internal/config"
	"mor/internal/store"
)

func testCfg() *config.Config {
	c := &config.Config{PublicHost: "203.0.113.7", DNS: "1.1.1.1"}
	c.EnsureDefaults()
	return c
}

func TestBuildConfigClients(t *testing.T) {
	cfg := testCfg()
	users := []*store.User{
		{Name: "phone", Proto: store.ProtoReality, UUID: "11111111-1111-4111-8111-111111111111"},
		{Name: "laptop", Proto: store.ProtoHy2, HyToken: "tok"},
	}

	b, err := New(cfg, config.DefaultPaths()).BuildConfig(users)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("Xray config does not parse as JSON: %v", err)
	}
	s := string(b)
	if !strings.Contains(s, "11111111-1111-4111-8111-111111111111") {
		t.Error("Reality key missing from the config")
	}
	if strings.Contains(s, "tok") {
		t.Error("keys of other protocols leaked into the Xray config")
	}
}

func TestBuildConfigReality(t *testing.T) {
	cfg := testCfg()
	users := []*store.User{{Name: "k", Proto: store.ProtoReality, UUID: "44444444-4444-4444-8444-444444444444"}}
	b, err := New(cfg, config.DefaultPaths()).BuildConfig(users)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{"\"reality\"", "privateKey", "shortIds", "xtls-rprx-vision", cfg.Reality.Dest + ":443"} {
		if !strings.Contains(s, want) {
			t.Errorf("config is missing %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, cfg.Reality.PublicKey) {
		t.Error("the public key belongs in the link, not the server config")
	}
}

func TestLink(t *testing.T) {
	cfg := testCfg()
	u := &store.User{Name: "my key", Proto: store.ProtoReality, UUID: "22222222-2222-4222-8222-222222222222"}

	link := Link(cfg, u)
	parsed, err := url.Parse(link)
	if err != nil {
		t.Fatalf("link does not parse: %v (%s)", err, link)
	}
	if parsed.Scheme != "vless" {
		t.Errorf("scheme = %q, want vless", parsed.Scheme)
	}
	if parsed.User.Username() != u.UUID {
		t.Errorf("uuid = %q", parsed.User.Username())
	}
	q := parsed.Query()
	if q.Get("pbk") != cfg.Reality.PublicKey {
		t.Error("link carries no server public key")
	}
	if q.Get("security") != "reality" || q.Get("sni") != cfg.Reality.Dest {
		t.Errorf("Reality parameters were lost: %v", q)
	}
	if parsed.Fragment != "my key" {
		t.Errorf("key name = %q", parsed.Fragment)
	}
}

func TestLinkIPv6Host(t *testing.T) {
	cfg := testCfg()
	cfg.PublicHost = "2001:db8::1"
	link := Link(cfg, &store.User{Name: "k", UUID: "33333333-3333-4333-8333-333333333333"})

	parsed, err := url.Parse(link)
	if err != nil {
		t.Fatalf("IPv6 link does not parse: %v (%s)", err, link)
	}
	if parsed.Hostname() != "2001:db8::1" {
		t.Errorf("host = %q", parsed.Hostname())
	}
}

func TestBuildConfigServerNames(t *testing.T) {
	cfg := testCfg()
	users := []*store.User{
		{Name: "a", Proto: store.ProtoReality, UUID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", SNI: "www.apple.com"},
		{Name: "b", Proto: store.ProtoReality, UUID: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"},
	}

	b, err := New(cfg, config.DefaultPaths()).BuildConfig(users)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.Contains(s, "www.apple.com") {
		t.Errorf("a per-key SNI must be accepted by the server:\n%s", s)
	}
	if !strings.Contains(s, cfg.Reality.Dest) {
		t.Errorf("the default name must stay in serverNames:\n%s", s)
	}
	if strings.Count(s, cfg.Reality.Dest) < 2 {
		t.Errorf("dest and serverNames must both carry the default:\n%s", s)
	}
}

func TestLinkPerKeySNI(t *testing.T) {
	cfg := testCfg()
	own := Link(cfg, &store.User{Name: "k", UUID: "cccccccc-cccc-4ccc-8ccc-cccccccccccc", SNI: "www.apple.com"})
	if !strings.Contains(own, "sni=www.apple.com") {
		t.Fatalf("per-key SNI missing from the link: %s", own)
	}
	shared := Link(cfg, &store.User{Name: "k", UUID: "dddddddd-dddd-4ddd-8ddd-dddddddddddd"})
	if !strings.Contains(shared, "sni="+cfg.Reality.Dest) {
		t.Fatalf("empty per-key SNI should fall back to the server name: %s", shared)
	}
}
