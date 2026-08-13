package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidPublicHost(t *testing.T) {
	cases := []struct {
		host string
		want bool
	}{
		{"", false},
		{"127.0.0.1", false},
		{"0.0.0.0", false},
		{"10.0.0.1", false},
		{"192.168.1.5", false},
		{"169.254.1.1", false},
		{"203.0.113.7", true},
		{"vpn.example.com", true},
		{"2001:db8::1", true},
	}
	for _, c := range cases {
		if got := ValidPublicHost(c.host); got != c.want {
			t.Errorf("ValidPublicHost(%q) = %v, want %v", c.host, got, c.want)
		}
	}
}

func TestValidHost(t *testing.T) {
	cases := []struct {
		host string
		want bool
	}{
		{"vpn.example.com", true},
		{"203.0.113.7", true},
		{"2001:db8::1", true},
		{"127.0.0.1", false},
		// setup --host used to skip this check and bake garbage into every link.
		{"http://evil.com:1234", false},
		{"evil.com/path", false},
		{"evil.com password", false},
	}
	for _, c := range cases {
		if got := ValidHost(c.host); got != c.want {
			t.Errorf("ValidHost(%q) = %v, want %v", c.host, got, c.want)
		}
	}
}

func TestLoadFillsEmptyDNS(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	old := `{"public_host":"203.0.113.7","vpn_port":2096,"sni":"www.microsoft.com","dns":""}`
	if err := os.WriteFile(path, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.DNS != DefaultDNS {
		t.Errorf("DNS = %q, want %q", c.DNS, DefaultDNS)
	}
}

func TestLoadKeepsChosenDNS(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"dns":"9.9.9.9"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.DNS != "9.9.9.9" {
		t.Errorf("DNS = %q, want 9.9.9.9", c.DNS)
	}
}

func TestEnsureDefaultsKeepsKeys(t *testing.T) {
	c := &Config{}
	if !c.EnsureDefaults() {
		t.Fatal("first call must fill an empty config")
	}
	reality := c.Reality.PrivateKey
	if reality == "" || c.Reality.PublicKey == "" {
		t.Fatal("protocol keys were not generated")
	}
	if c.EnsureDefaults() {
		t.Error("second call reported changes on a complete config")
	}
	if c.Reality.PrivateKey != reality {
		t.Error("server keys were regenerated; all issued links would stop working")
	}
	if c.SS.Port == 0 {
		t.Error("SS.Port was not defaulted")
	}
}

func TestProtocolSwitch(t *testing.T) {
	c := NewDefault()
	if !c.On("hy2") || !c.On("reality") {
		t.Fatal("по умолчанию должны работать все протоколы")
	}
	c.SetOn("hy2", false)
	if c.On("hy2") || !c.On("reality") {
		t.Errorf("после выключения hy2: off=%v", c.Off)
	}
	c.SetOn("hy2", false)
	if len(c.Off) != 1 {
		t.Errorf("повторное выключение продублировало запись: %v", c.Off)
	}
	c.SetOn("hy2", true)
	if !c.On("hy2") || len(c.Off) != 0 {
		t.Errorf("после включения: off=%v", c.Off)
	}
}

// The web panel is reachable from anywhere, unlike the terminal — it must not
// come up with no password just because a port number happens to be set.
func TestWebOnRequiresPassword(t *testing.T) {
	c := NewDefault()
	c.EnsureDefaults()
	if c.WebOn() {
		t.Error("панель не должна включаться без пароля")
	}
	c.WebPasswordHash = "salt:hash"
	if !c.WebOn() {
		t.Error("с паролем и портом панель должна быть включена")
	}
	c.WebOff = true
	if c.WebOn() {
		t.Error("WebOff должен перекрывать наличие пароля")
	}
}

// The masquerade domain lives in two places — Hysteria2 proxies to it, Reality
// shakes hands with it — and they must never drift apart.
func TestSNIStaysInSync(t *testing.T) {
	c := NewDefault()
	c.EnsureDefaults()
	if c.Reality.Dest != c.SNI {
		t.Fatalf("свежий конфиг разошёлся: %q vs %q", c.SNI, c.Reality.Dest)
	}

	c.SetSNI("www.apple.com")
	if c.SNI != "www.apple.com" || c.Reality.Dest != "www.apple.com" {
		t.Errorf("после SetSNI: %q vs %q", c.SNI, c.Reality.Dest)
	}

	// An old config kept two different domains. Reality is pickier about which
	// site it can imitate, so its choice must survive the merge.
	old := &Config{SNI: "www.microsoft.com", Reality: Reality{Dest: "dl.google.com"}}
	old.EnsureDefaults()
	if old.SNI != "dl.google.com" || old.Reality.Dest != "dl.google.com" {
		t.Errorf("миграция: %q vs %q", old.SNI, old.Reality.Dest)
	}
}
