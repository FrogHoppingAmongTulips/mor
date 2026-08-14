package config

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"mor/internal/fsutil"
	"mor/internal/keys"
)

type Paths struct {
	BaseDir      string
	ConfigFile   string
	DataFile     string
	StatsFile    string
	HistoryFile  string
	AuditLogFile string
	SysHistFile  string
	WebCertFile  string
	WebKeyFile   string
	SessionsFile string
	TokensFile   string
	DevicesFile  string
	HyConfig     string
	HyCertFile   string
	HyKeyFile    string
	XrayConfig   string
}

func DefaultPaths() Paths {
	base := envOr("MOR_DIR", "/etc/mor")
	return Paths{
		BaseDir:      base,
		ConfigFile:   filepath.Join(base, "config.json"),
		DataFile:     filepath.Join(base, "users.json"),
		StatsFile:    filepath.Join(base, "stats.json"),
		HistoryFile:  filepath.Join(base, "history.json"),
		AuditLogFile: filepath.Join(base, "audit.json"),
		SysHistFile:  filepath.Join(base, "syshist.json"),
		WebCertFile:  filepath.Join(base, "web.crt"),
		WebKeyFile:   filepath.Join(base, "web.key"),
		SessionsFile: filepath.Join(base, "sessions.json"),
		TokensFile:   filepath.Join(base, "tokens.json"),
		DevicesFile:  filepath.Join(base, "devices.json"),
		HyConfig:     envOr("MOR_HY_CONFIG", "/etc/hysteria/config.yaml"),
		HyCertFile:   envOr("MOR_HY_CERT", "/etc/hysteria/server.crt"),
		HyKeyFile:    envOr("MOR_HY_KEY", "/etc/hysteria/server.key"),
		XrayConfig:   envOr("MOR_XRAY_CONFIG", "/usr/local/etc/xray/config.json"),
	}
}

const DefaultDNS = "1.1.1.1"

const DefaultSNI = "www.cloudflare.com"

type Config struct {
	PublicHost string `json:"public_host"`

	VPNPort int    `json:"vpn_port"`
	SNI     string `json:"sni"`
	DNS     string `json:"dns"`

	// HyObfs scrambles Hysteria2 packets with this password so they stop
	// looking like QUIC. Empty means off: switching it on rewrites every
	// Hysteria2 link, and anyone holding an old one stops connecting.
	HyObfs string `json:"hy_obfs,omitempty"`

	Reality Reality `json:"reality"`

	Enc Enc `json:"enc"`

	SS SS `json:"ss"`

	// SubPort serves subscription links; SubOff takes the server down without
	// forgetting which port it used.
	SubPort int  `json:"sub_port"`
	SubOff  bool `json:"sub_off,omitempty"`

	// WebPasswordHash is "salt:hash" (see internal/webauth), empty until the
	// owner sets one. The panel refuses to start with no password set — a web
	// login is reachable from anywhere, unlike the terminal.
	WebPasswordHash string `json:"web_password_hash,omitempty"`
	WebPort         int    `json:"web_port"`
	WebOff          bool   `json:"web_off,omitempty"`

	// Off lists protocols the owner switched off. Empty means everything runs.
	Off []string `json:"off,omitempty"`

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

	// Transport is how Reality carries the traffic: plain TCP, or XHTTP, which
	// looks like ordinary web requests and survives DPI that kills plain TCP.
	Transport string `json:"transport,omitempty"`
	Path      string `json:"path,omitempty"`
}

const (
	TransportTCP   = "tcp"
	TransportXHTTP = "xhttp"
)

func (r Reality) Wire() string {
	if r.Transport == TransportXHTTP {
		return TransportXHTTP
	}
	return TransportTCP
}

// Enc is VLESS Encryption: Xray's own transport-level encryption, no TLS and no
// certificate involved. The server holds Decryption, clients carry Encryption —
// the two halves of one X25519 pair wrapped in Xray's format string.
type Enc struct {
	Port       int    `json:"port"`
	Decryption string `json:"decryption"`
	Encryption string `json:"encryption"`
}

// SS is plain Shadowsocks: no TLS, no masquerade, one AEAD method for the
// whole server. Its only job is opening in apps that never learned Reality or
// Hysteria2 — every password is per-key, set on the user record.
type SS struct {
	Port int `json:"port"`
}

// Xray spells the mode into the key itself: 600s is how long the server keeps a
// session alive, 0rtt lets the client send data with its first packet.
const (
	encServerPrefix = "mlkem768x25519plus.native.600s."
	encClientPrefix = "mlkem768x25519plus.native.0rtt."
)

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

	set(c.SubPort <= 0, func() { c.SubPort = 8880 })
	set(c.WebPort <= 0, func() { c.WebPort = 9090 })
	set(c.Enc.Port == 0, func() { c.Enc.Port = 2098 })
	set(c.SS.Port == 0, func() { c.SS.Port = 2099 })
	set(c.Enc.Decryption == "" || c.Enc.Encryption == "", func() {
		if priv, pub, err := keys.RealityPair(); err == nil {
			c.Enc.Decryption = encServerPrefix + priv
			c.Enc.Encryption = encClientPrefix + pub
		}
	})

	set(c.Reality.Port == 0, func() { c.Reality.Port = 443 })
	// One masquerade domain for the whole server. Older configs carried two, and
	// Reality is the pickier of the pair, so its value wins the one-time merge.
	set(c.Reality.Dest != "" && c.Reality.Dest != c.SNI, func() { c.SNI = c.Reality.Dest })
	set(c.Reality.Dest != c.SNI, func() { c.Reality.Dest = c.SNI })
	set(c.Reality.ShortID == "", func() { c.Reality.ShortID = keys.ShortID() })
	set(c.Reality.Transport == "", func() { c.Reality.Transport = TransportTCP })
	set(c.Reality.Path == "", func() { c.Reality.Path = "/" + keys.ShortID() })
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
	return fsutil.WriteAtomic(c.path, b, 0o600)
}

func (c *Config) SetPath(p string) { c.path = p }

// SetSNI changes the site every protocol pretends to be. Hysteria2 proxies to
// it and Reality shakes hands with it, so the two must never drift apart —
// which is why nothing outside this package writes them separately.
func (c *Config) SetSNI(domain string) {
	c.SNI = domain
	c.Reality.Dest = domain
}

// SubOn reports whether subscription links are served.
func (c *Config) SubOn() bool { return !c.SubOff && c.SubPort > 0 }

// WebOn reports whether the web panel should be listening — it needs a
// password set, same as the terminal needs SSH: no anonymous door to the
// server's controls.
func (c *Config) WebOn() bool { return !c.WebOff && c.WebPort > 0 && c.WebPasswordHash != "" }

// On reports whether a protocol is allowed to run and to take new keys.
func (c *Config) On(proto string) bool {
	for _, p := range c.Off {
		if p == proto {
			return false
		}
	}
	return true
}

// SetOn switches a protocol on or off. The caller saves the config.
func (c *Config) SetOn(proto string, on bool) {
	rest := c.Off[:0:0]
	for _, p := range c.Off {
		if p != proto {
			rest = append(rest, p)
		}
	}
	if !on {
		rest = append(rest, proto)
	}
	c.Off = rest
}

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

// ValidHost reports whether s is safe to bake into public links: a
// non-private IP, or a domain-shaped string without scheme/port/path
// characters that don't belong in a bare hostname (e.g. "http://evil.com:1234").
func ValidHost(s string) bool {
	if !ValidPublicHost(s) {
		return false
	}
	if net.ParseIP(s) != nil {
		return true
	}
	return !strings.ContainsAny(s, " /:")
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
