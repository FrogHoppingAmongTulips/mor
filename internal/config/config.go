package config

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"sync"

	"mor/internal/keys"
)

type Paths struct {
	BaseDir    string
	ConfigFile string
	DataFile   string
	StatsFile  string
	HyConfig   string
	HyCertFile string
	HyKeyFile  string
	XrayConfig string
}

func DefaultPaths() Paths {
	base := envOr("MOR_DIR", "/etc/mor")
	return Paths{
		BaseDir:    base,
		ConfigFile: filepath.Join(base, "config.json"),
		DataFile:   filepath.Join(base, "users.json"),
		StatsFile:  filepath.Join(base, "stats.json"),
		HyConfig:   envOr("MOR_HY_CONFIG", "/etc/hysteria/config.yaml"),
		HyCertFile: envOr("MOR_HY_CERT", "/etc/hysteria/server.crt"),
		HyKeyFile:  envOr("MOR_HY_KEY", "/etc/hysteria/server.key"),
		XrayConfig: envOr("MOR_XRAY_CONFIG", "/usr/local/etc/xray/config.json"),
	}
}

const DefaultDNS = "1.1.1.1"

const DefaultSNI = "www.cloudflare.com"

type Config struct {
	PublicHost string `json:"public_host"`

	VPNPort int    `json:"vpn_port"`
	SNI     string `json:"sni"`
	DNS     string `json:"dns"`

	Reality Reality `json:"reality"`

	StatsSecret string `json:"stats_secret"`

	mu   sync.Mutex
	path string
}

type Reality struct {
	Port       int    `json:"port"`
	Dest       string `json:"dest"`
	PrivateKey string `json:"private_key"`
	PublicKey  string `json:"public_key"`
	ShortID    string `json:"short_id"`
}

func NewDefault() *Config {
	return &Config{
		VPNPort: 2096,
		SNI:     DefaultSNI,
		DNS:     DefaultDNS,
	}
}

func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	c := &Config{}
	if err := json.Unmarshal(b, c); err != nil {
		return nil, err
	}
	c.path = path
	c.EnsureDefaults()
	return c, nil
}

func (c *Config) EnsureDefaults() bool {
	changed := false
	set := func(cond bool, fn func()) {
		if cond {
			fn()
			changed = true
		}
	}

	set(c.DNS == "", func() { c.DNS = DefaultDNS })
	set(c.VPNPort == 0, func() { c.VPNPort = 2096 })
	set(c.SNI == "", func() { c.SNI = DefaultSNI })

	set(c.Reality.Port == 0, func() { c.Reality.Port = 443 })
	set(c.Reality.Dest == "", func() { c.Reality.Dest = DefaultSNI })
	set(c.Reality.ShortID == "", func() { c.Reality.ShortID = keys.ShortID() })
	set(c.StatsSecret == "", func() { c.StatsSecret = keys.Token() })
	set(c.Reality.PrivateKey == "" || c.Reality.PublicKey == "", func() {
		if priv, pub, err := keys.RealityPair(); err == nil {
			c.Reality.PrivateKey, c.Reality.PublicKey = priv, pub
		}
	})

	return changed
}

func (c *Config) Save() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(c.path, b, 0o600)
}

func (c *Config) SetPath(p string) { c.path = p }

func ValidPublicHost(host string) bool {
	if host == "" {
		return false
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return true
	}
	return !ip.IsLoopback() && !ip.IsPrivate() && !ip.IsLinkLocalUnicast() && !ip.IsUnspecified()
}

func ValidIP(s string) bool { return net.ParseIP(s) != nil }

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func atomicWrite(path string, data []byte, perm os.FileMode) error {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, perm); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
