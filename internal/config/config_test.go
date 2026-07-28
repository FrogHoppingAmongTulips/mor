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
}
