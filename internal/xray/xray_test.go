package xray

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

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

// A Shadowsocks client with no password yet must not reach the config: an
// empty password would be a working key nobody can hold.
func TestBuildConfigSS(t *testing.T) {
	cfg := testCfg()
	users := []*store.User{
		{Name: "k", Proto: store.ProtoSS, SSPassword: "secret-pass"},
		{Name: "half-made", Proto: store.ProtoSS},
	}
	b, err := New(cfg, config.DefaultPaths()).BuildConfig(users)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.Contains(s, "secret-pass") || !strings.Contains(s, "shadowsocks") || !strings.Contains(s, SSMethod) {
		t.Errorf("Shadowsocks inbound missing or incomplete:\n%s", s)
	}
	if strings.Count(s, "\"method\"") != 1 {
		t.Errorf("half-made key without a password leaked into the config:\n%s", s)
	}
}

// The masquerade domain is one server-wide setting, so it must show up both as
// the handshake target and as the name clients are told to use.
func TestServerNameIsShared(t *testing.T) {
	cfg := testCfg()
	users := []*store.User{
		{Name: "a", Proto: store.ProtoReality, UUID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"},
	}

	b, err := New(cfg, config.DefaultPaths()).BuildConfig(users)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(b), cfg.Reality.Dest) < 2 {
		t.Errorf("dest и serverNames должны нести один домен:\n%s", b)
	}
}

// An expired key must not reappear when the config is rebuilt — that is what
// happens on every restart and on every new key.
func TestExpiredKeysStayOut(t *testing.T) {
	cfg := testCfg()
	live := &store.User{ID: "live", Name: "живой", Proto: store.ProtoReality, UUID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"}
	dead := &store.User{ID: "dead", Name: "истёк", Proto: store.ProtoReality, UUID: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"}
	dead.ExpiresAt = time.Now().Add(-time.Hour)
	encDead := &store.User{ID: "encdead", Name: "истёк2", Proto: store.ProtoEnc, UUID: "cccccccc-cccc-4ccc-8ccc-cccccccccccc"}
	encDead.ExpiresAt = time.Now().Add(-time.Minute)

	b, err := New(cfg, config.DefaultPaths()).BuildConfig([]*store.User{live, dead, encDead})
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.Contains(s, live.UUID) {
		t.Error("живой ключ пропал из конфига")
	}
	if strings.Contains(s, dead.UUID) || strings.Contains(s, encDead.UUID) {
		t.Error("истёкший ключ вернулся в конфиг")
	}
}

func TestEncryptionInbound(t *testing.T) {
	cfg := config.NewDefault()
	cfg.PublicHost = "203.0.113.7"
	cfg.EnsureDefaults()
	m := New(cfg, config.Paths{})

	u := &store.User{ID: "id1", Name: "телефон", Proto: store.ProtoEnc, UUID: "u-enc-1"}
	b, err := m.BuildConfig([]*store.User{u})
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Inbounds []struct {
			Tag      string `json:"tag"`
			Port     int    `json:"port"`
			Protocol string `json:"protocol"`
			Settings struct {
				Decryption string `json:"decryption"`
				Clients    []struct {
					ID   string `json:"id"`
					Flow string `json:"flow"`
				} `json:"clients"`
			} `json:"settings"`
		} `json:"inbounds"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, in := range doc.Inbounds {
		if in.Tag != "enc-in" {
			continue
		}
		found = true
		if in.Port != cfg.Enc.Port || in.Settings.Decryption != cfg.Enc.Decryption {
			t.Errorf("inbound: порт %d, decryption %q", in.Port, in.Settings.Decryption)
		}
		if len(in.Settings.Clients) != 1 || in.Settings.Clients[0].ID != u.UUID {
			t.Error("ключ клиента не попал в конфиг")
		}
		if in.Settings.Clients[0].Flow != "" {
			t.Error("flow остался — VLESS Encryption такого клиента не примет")
		}
	}
	if !found {
		t.Fatal("нет inbound vless encryption")
	}

}

// Switching a protocol off must take its inbound away, not just stop serving it.
func TestSwitchedOffProtocolHasNoInbound(t *testing.T) {
	cfg := config.NewDefault()
	cfg.PublicHost = "203.0.113.7"
	cfg.EnsureDefaults()
	cfg.SetOn(store.ProtoEnc, false)
	m := New(cfg, config.Paths{})

	b, err := m.BuildConfig(nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "enc-in") {
		t.Error("выключенный VLESS Encryption остался в конфиге")
	}
	if !strings.Contains(string(b), "vless") {
		t.Error("включённый Reality пропал из конфига")
	}
}

// XHTTP changes both ends: the inbound gains its settings and the client loses
// the vision flow, which XHTTP refuses to combine with.
func TestXHTTPTransport(t *testing.T) {
	cfg := config.NewDefault()
	cfg.PublicHost = "203.0.113.7"
	cfg.EnsureDefaults()
	cfg.Reality.Transport = config.TransportXHTTP
	m := New(cfg, config.Paths{})

	u := &store.User{ID: "id1", Name: "ноут", Proto: store.ProtoReality, UUID: "u-1"}
	b, err := m.BuildConfig([]*store.User{u})
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.Contains(s, `"network": "xhttp"`) || !strings.Contains(s, "xhttpSettings") {
		t.Error("в конфиге нет транспорта xhttp")
	}
	if strings.Contains(s, "xtls-rprx-vision") {
		t.Error("flow остался при xhttp — Xray такого клиента не примет")
	}
}
