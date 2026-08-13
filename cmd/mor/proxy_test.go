package main

import (
	"net/url"
	"strings"
	"testing"

	"mor/internal/config"
	"mor/internal/hysteria"
	"mor/internal/proxy"
	"mor/internal/store"
)

func proxyTestCfg() *config.Config {
	cfg := config.NewDefault()
	cfg.PublicHost = "203.0.113.7"
	cfg.EnsureDefaults()
	return cfg
}

// Every protocol has to produce a link, and each has to be its own scheme:
// a key that renders as an empty string is one the owner hands over and
// nobody can use.
func TestProxyForCoversEveryProtocol(t *testing.T) {
	cfg := proxyTestCfg()
	want := map[string]string{
		store.ProtoHy2:     "hysteria2",
		store.ProtoReality: "vless",
		store.ProtoEnc:     "vless",
		store.ProtoSS:      "ss",
	}
	for proto, scheme := range want {
		u := &store.User{Name: "телефон", Proto: proto, HyToken: "tok", UUID: "uuid", SSPassword: "pw"}
		p, ok := proxyFor(cfg, u)
		if !ok {
			t.Fatalf("%s: конфиг не построился", proto)
		}
		uri := p.URI()
		if !strings.HasPrefix(uri, scheme+"://") {
			t.Errorf("%s: ссылка = %s, ждали схему %s", proto, uri, scheme)
		}
		if _, err := url.Parse(uri); err != nil {
			t.Errorf("%s: ссылка не разбирается: %v", proto, err)
		}
	}
	if _, ok := proxyFor(cfg, &store.User{Proto: "неизвестный"}); ok {
		t.Error("неизвестный протокол не должен давать конфиг")
	}
}

// The port in the link has to be the port the engine listens on. They come
// from different corners of the config, and a mismatch produces a key that
// looks perfectly valid and connects to nothing.
func TestProxyForUsesConfiguredPorts(t *testing.T) {
	cfg := proxyTestCfg()
	cfg.VPNPort, cfg.Reality.Port, cfg.Enc.Port, cfg.SS.Port = 1111, 2222, 3333, 4444
	want := map[string]int{
		store.ProtoHy2: 1111, store.ProtoReality: 2222,
		store.ProtoEnc: 3333, store.ProtoSS: 4444,
	}
	for proto, port := range want {
		p, _ := proxyFor(cfg, &store.User{Name: "x", Proto: proto})
		if p.Port != port {
			t.Errorf("%s: порт = %d, в конфиге %d", proto, p.Port, port)
		}
	}
}

// A key may pin its own masquerade domain; without one it inherits the
// server's, and failing that a sane default — never an empty SNI, which makes
// the handshake fail in a way that reads like a blocked port.
func TestProxyForSNIFallback(t *testing.T) {
	cfg := proxyTestCfg()
	cfg.SNI = "www.cloudflare.com"

	p, _ := proxyFor(cfg, &store.User{Proto: store.ProtoHy2, SNI: "own.example"})
	if p.SNI != "own.example" {
		t.Errorf("свой SNI ключа проигнорирован: %q", p.SNI)
	}
	p, _ = proxyFor(cfg, &store.User{Proto: store.ProtoHy2})
	if p.SNI != "www.cloudflare.com" {
		t.Errorf("SNI сервера не подхватился: %q", p.SNI)
	}
	cfg.SNI = ""
	p, _ = proxyFor(cfg, &store.User{Proto: store.ProtoHy2})
	if p.SNI != hysteria.FallbackSNI {
		t.Errorf("без SNI должен подставляться запасной, получили %q", p.SNI)
	}
}

// Server and client have to agree on scrambling. If the server config gets the
// password and the link does not, the app speaks plain QUIC to a server that no
// longer answers it — and the failure looks exactly like a blocked port.
func TestObfsReachesServerAndLinkTogether(t *testing.T) {
	cfg := proxyTestCfg()
	u := &store.User{Name: "телефон", Proto: store.ProtoHy2, HyToken: "tok"}

	p, _ := proxyFor(cfg, u)
	if p.Obfs != "" {
		t.Error("обфускация появилась сама")
	}
	if conf := hysteria.New(cfg, config.DefaultPaths()).BuildConfig(); strings.Contains(conf, "obfs") {
		t.Error("обфускация появилась в конфиге сервера сама")
	}

	cfg.HyObfs = "s3cr3t"
	p, _ = proxyFor(cfg, u)
	conf := hysteria.New(cfg, config.DefaultPaths()).BuildConfig()
	if p.ObfsPassword != "s3cr3t" || p.Obfs != "salamander" {
		t.Errorf("в ссылке нет обфускации: %+v", p)
	}
	if !strings.Contains(conf, "password: s3cr3t") {
		t.Errorf("в конфиге сервера нет пароля обфускации:\n%s", conf)
	}
}

// Reality must carry the whole handshake, or the client cannot complete it.
func TestProxyForRealityCarriesKeys(t *testing.T) {
	cfg := proxyTestCfg()
	p, _ := proxyFor(cfg, &store.User{Name: "x", Proto: store.ProtoReality, UUID: "u"})
	if !p.Reality || p.PublicKey == "" || p.ShortID == "" {
		t.Errorf("рукопожатие Reality неполное: %+v", p)
	}
	if p.SNI != cfg.Reality.Dest {
		t.Errorf("SNI = %q, а маскировка настроена на %q", p.SNI, cfg.Reality.Dest)
	}
}

// Hysteria2 serves a self-signed certificate, so a client that verified it
// would refuse every connection.
func TestProxyForHysteriaSkipsCertCheck(t *testing.T) {
	p, _ := proxyFor(proxyTestCfg(), &store.User{Proto: store.ProtoHy2})
	if !p.Insecure {
		t.Error("сертификат самоподписанный — проверку надо отключать явно")
	}
}

// The panel, the terminal and the subscription must all hand out the same
// link. They render from one description precisely so they cannot drift.
func TestKeyTextMatchesProxyURI(t *testing.T) {
	cfg := proxyTestCfg()
	for _, proto := range []string{store.ProtoHy2, store.ProtoReality, store.ProtoEnc, store.ProtoSS} {
		u := &store.User{Name: "телефон", Proto: proto, HyToken: "t", UUID: "u", SSPassword: "p"}
		p, _ := proxyFor(cfg, u)
		if got, want := keyText(cfg, u), p.URI(); got != want {
			t.Errorf("%s: терминал даёт %q, подписка %q", proto, got, want)
		}
	}
}

// Whatever a client asks for, the same key set has to come back — otherwise
// one app shows three protocols and another shows one, with no explanation.
func TestEveryFormatRendersTheSameKeys(t *testing.T) {
	cfg := proxyTestCfg()
	var list []proxy.Proxy
	for _, proto := range []string{store.ProtoHy2, store.ProtoReality, store.ProtoSS} {
		p, _ := proxyFor(cfg, &store.User{Name: "телефон", Proto: proto, HyToken: "t", UUID: "u", SSPassword: "p"})
		list = append(list, p)
	}
	clash, singbox := proxy.Clash(list), proxy.SingBox(list)
	for _, p := range list {
		if !strings.Contains(clash, p.Server) {
			t.Errorf("Clash потерял %s", p.Kind)
		}
		if !strings.Contains(singbox, p.Server) {
			t.Errorf("sing-box потерял %s", p.Kind)
		}
	}
}
