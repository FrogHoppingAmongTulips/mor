package proxy

import (
	"encoding/base64"
	"encoding/json"
	"net/url"
	"strings"
	"testing"
)

func hy2() Proxy {
	return Proxy{
		Name: "телефон", Kind: Hysteria2, Server: "203.0.113.7", Port: 2096,
		Password: "тк", SNI: "www.cloudflare.com", Insecure: true,
	}
}

func reality() Proxy {
	return Proxy{
		Name: "ноут", Kind: VLESS, Server: "203.0.113.7", Port: 443,
		UUID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", Reality: true,
		SNI: "www.cloudflare.com", PublicKey: "pbk", ShortID: "sid",
		Fingerprint: "chrome", Network: "tcp", Flow: "xtls-rprx-vision",
	}
}

func ss() Proxy {
	return Proxy{
		Name: "планшет", Kind: Shadowsocks, Server: "203.0.113.7", Port: 2099,
		Password: "пароль", Method: "aes-256-gcm",
	}
}

func enc() Proxy {
	return Proxy{
		Name: "enc", Kind: VLESS, Server: "203.0.113.7", Port: 2098,
		UUID: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", Network: "tcp",
		Encryption: "mlkem768x25519plus.native.0rtt.KEY",
	}
}

func TestURIHysteria2(t *testing.T) {
	u, err := url.Parse(hy2().URI())
	if err != nil {
		t.Fatal(err)
	}
	if u.Scheme != "hysteria2" {
		t.Errorf("схема = %q", u.Scheme)
	}
	if u.User.Username() != "тк" {
		t.Errorf("токен = %q", u.User.Username())
	}
	if u.Host != "203.0.113.7:2096" {
		t.Errorf("адрес = %q", u.Host)
	}
	if u.Query().Get("insecure") != "1" {
		t.Error("сертификат самоподписанный — клиент должен знать, что проверять его нечем")
	}
	if u.Fragment != "телефон" {
		t.Errorf("имя = %q", u.Fragment)
	}
}

// Obfuscation only belongs in the link when the server actually scrambles:
// a link promising it to a server that speaks plain QUIC fails in a way that
// looks exactly like a blocked port.
func TestURIObfsOnlyWhenSet(t *testing.T) {
	if q := mustQuery(t, hy2().URI()); q.Get("obfs") != "" {
		t.Error("обфускация появилась сама")
	}
	p := hy2()
	p.Obfs, p.ObfsPassword = "salamander", "s3cr3t"
	q := mustQuery(t, p.URI())
	if q.Get("obfs") != "salamander" || q.Get("obfs-password") != "s3cr3t" {
		t.Errorf("пароль обфускации не доехал: %v", q)
	}
}

func TestURIRealityCarriesHandshake(t *testing.T) {
	q := mustQuery(t, reality().URI())
	for k, want := range map[string]string{
		"security": "reality", "pbk": "pbk", "sid": "sid",
		"sni": "www.cloudflare.com", "fp": "chrome", "flow": "xtls-rprx-vision",
	} {
		if got := q.Get(k); got != want {
			t.Errorf("%s = %q, ждали %q", k, got, want)
		}
	}
}

// XHTTP carries its own framing, and Xray rejects a client that asks for the
// vision flow on top of it.
func TestURIXHTTPDropsFlow(t *testing.T) {
	p := reality()
	p.Network, p.Path, p.Flow = "xhttp", "/abc", ""
	q := mustQuery(t, p.URI())
	if q.Get("type") != "xhttp" || q.Get("path") != "/abc" {
		t.Errorf("транспорт не доехал: %v", q)
	}
	if q.Get("flow") != "" {
		t.Error("flow остался при xhttp")
	}
}

// SIP002: userinfo is method:password in web-safe base64 without padding.
func TestURIShadowsocksSIP002(t *testing.T) {
	u, err := url.Parse(ss().URI())
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base64.RawURLEncoding.DecodeString(u.User.Username())
	if err != nil {
		t.Fatalf("userinfo не в base64url без padding: %v", err)
	}
	if string(raw) != "aes-256-gcm:пароль" {
		t.Errorf("userinfo = %q", raw)
	}
}

func TestURIIPv6HostBracketed(t *testing.T) {
	p := hy2()
	p.Server = "2001:db8::1"
	u, err := url.Parse(p.URI())
	if err != nil {
		t.Fatalf("ссылка с IPv6 не разбирается: %v", err)
	}
	if u.Hostname() != "2001:db8::1" {
		t.Errorf("адрес = %q", u.Hostname())
	}
}

// VLESS Encryption is new enough that only Xray-based clients reading raw URIs
// understand it. Offering it to the others is worse than leaving it out: one
// unparsable entry can take the whole profile down and leave the person with
// nothing instead of the protocols that would have worked.
func TestEncryptionOnlyInURIFormat(t *testing.T) {
	e := enc()
	if !e.Supports(FormatURI) {
		t.Error("VLESS Encryption должен попадать в base64-подписку")
	}
	if e.Supports(FormatClash) || e.Supports(FormatSingBox) {
		t.Error("VLESS Encryption не поддерживается Clash и sing-box — его нельзя туда класть")
	}
	for _, p := range []Proxy{hy2(), reality(), ss()} {
		for _, f := range []Format{FormatURI, FormatClash, FormatSingBox} {
			if !p.Supports(f) {
				t.Errorf("%s должен поддерживаться форматом %d", p.Kind, f)
			}
		}
	}
}

func TestClashYAMLShape(t *testing.T) {
	out := Clash([]Proxy{hy2(), reality(), ss(), enc()})
	for _, want := range []string{
		"proxies:", "type: hysteria2", "type: vless", "type: ss",
		"reality-opts:", "public-key:", "proxy-groups:", "rules:", "MATCH,mor",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("в YAML нет %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "mlkem768") {
		t.Error("VLESS Encryption просочился в Clash, который его не понимает")
	}
	// Every name is quoted: unquoted YAML turns a value with a dot or a
	// non-Latin letter into something else, or refuses to parse at all.
	if !strings.Contains(out, `name: "телефон"`) {
		t.Errorf("имена не в кавычках:\n%s", out)
	}
}

// sing-box clients import a whole configuration, not a fragment: an outbound
// list on its own would not load.
func TestSingBoxIsValidCompleteConfig(t *testing.T) {
	out := SingBox([]Proxy{hy2(), reality(), ss(), enc()})

	var cfg struct {
		Inbounds  []map[string]any `json:"inbounds"`
		Outbounds []map[string]any `json:"outbounds"`
		Route     map[string]any   `json:"route"`
	}
	if err := json.Unmarshal([]byte(out), &cfg); err != nil {
		t.Fatalf("sing-box отдаёт невалидный JSON: %v\n%s", err, out)
	}
	if len(cfg.Inbounds) == 0 || cfg.Route["final"] != "mor" {
		t.Errorf("конфиг неполный: %s", out)
	}

	kinds := map[string]bool{}
	for _, o := range cfg.Outbounds {
		kinds[o["type"].(string)] = true
	}
	for _, want := range []string{"selector", "hysteria2", "vless", "shadowsocks", "direct"} {
		if !kinds[want] {
			t.Errorf("нет outbound типа %q: %s", want, out)
		}
	}
	if strings.Contains(out, "mlkem768") {
		t.Error("VLESS Encryption просочился в sing-box, который его не понимает")
	}
}

// A name with a quote in it must not break the generated config.
func TestRenderersEscapeNames(t *testing.T) {
	p := hy2()
	p.Name = `он сказал "привет"`
	if out := Clash([]Proxy{p}); !strings.Contains(out, `\"привет\"`) {
		t.Errorf("кавычки в имени не экранированы в YAML:\n%s", out)
	}
	var any map[string]any
	if err := json.Unmarshal([]byte(SingBox([]Proxy{p})), &any); err != nil {
		t.Errorf("кавычки в имени сломали JSON: %v", err)
	}
}

func TestDetectPicksFormatByClient(t *testing.T) {
	cases := map[string]Format{
		"clash-verge/1.5":    FormatClash,
		"mihomo/1.18":        FormatClash,
		"Stash/2.0":          FormatClash,
		"sing-box 1.8":       FormatSingBox,
		"SFI/1.9 (sing-box)": FormatSingBox,
		"Karing/1.0":         FormatSingBox,
		"v2rayNG/1.8":        FormatURI,
		"Shadowrocket/2.2":   FormatURI,
		"Happ/1.0":           FormatURI,
		"":                   FormatURI,
		"совершенно неизвестный": FormatURI,
	}
	for ua, want := range cases {
		if got := Detect(ua); got != want {
			t.Errorf("Detect(%q) = %d, ждали %d", ua, got, want)
		}
	}
}

func mustQuery(t *testing.T, raw string) url.Values {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("ссылка не разбирается: %v", err)
	}
	return u.Query()
}

// Clash and sing-box key their selectors by name. Two endpoints sharing one
// makes the profile refuse to import — and a person's keys all carry their
// name, so this is the normal case, not an edge one.
func TestRenderedNamesAreUnique(t *testing.T) {
	list := []Proxy{hy2(), reality(), ss()}
	for i := range list {
		list[i].Name = "телефон"
	}
	out := SingBox(list)
	var cfg struct {
		Outbounds []map[string]any `json:"outbounds"`
	}
	if err := json.Unmarshal([]byte(out), &cfg); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, o := range cfg.Outbounds {
		tag, _ := o["tag"].(string)
		if tag == "" || tag == "mor" || tag == "direct" {
			continue
		}
		if seen[tag] {
			t.Errorf("тег %q встречается дважды — sing-box такой конфиг не примет", tag)
		}
		seen[tag] = true
	}
}
