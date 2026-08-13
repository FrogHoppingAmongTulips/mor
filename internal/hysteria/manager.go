package hysteria

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"mor/internal/config"
	"mor/internal/fsutil"
	"mor/internal/systemd"
)

const AuthPort = 9797

const StatsPort = 9798

const Service = "hysteria-server"

// FallbackSNI is the site Hysteria2 pretends to be when nothing else is set.
const FallbackSNI = "www.bing.com"

type Manager struct {
	cfg   *config.Config
	paths config.Paths
}

func New(cfg *config.Config, paths config.Paths) *Manager {
	return &Manager{cfg: cfg, paths: paths}
}

func (m *Manager) BuildConfig() string {
	c := m.cfg
	sni := c.SNI
	if sni == "" {
		sni = FallbackSNI
	}
	return fmt.Sprintf(`listen: :%d
tls:
  cert: %s
  key: %s
auth:
  type: http
  http:
    url: http://127.0.0.1:%d/auth
masquerade:
  type: proxy
  proxy:
    url: https://%s
    rewriteHost: true
quic:
  disablePathMTUDiscovery: true
trafficStats:
  listen: 127.0.0.1:%d
  secret: %s
%s%s`, c.VPNPort, m.paths.HyCertFile, m.paths.HyKeyFile, AuthPort, sni, StatsPort, c.StatsSecret, obfsBlock(c.HyObfs), resolverBlock(c.DNS))
}

// obfsBlock scrambles every packet with a shared password, so what goes over
// the wire stops looking like QUIC at all — just random UDP. This is what a
// second engine used to be kept around for: a network that learned to
// recognise Hysteria2 by its handshake no longer has a handshake to look at.
//
// Empty password means off, and off is the default: turning it on changes the
// links, and everyone holding an old one stops connecting until they get a new.
func obfsBlock(password string) string {
	if strings.TrimSpace(password) == "" {
		return ""
	}
	return fmt.Sprintf("obfs:\n  type: salamander\n  salamander:\n    password: %s\n", password)
}

func resolverBlock(dns string) string {
	dns = strings.TrimSpace(dns)
	if dns == "" {
		return ""
	}
	addr := dns
	if _, _, err := net.SplitHostPort(dns); err != nil {
		addr = net.JoinHostPort(dns, "53")
	}
	return fmt.Sprintf("resolver:\n  type: udp\n  udp:\n    addr: %s\n    timeout: 4s\n", addr)
}

func (m *Manager) WriteConfig() error {
	if err := os.MkdirAll(filepath.Dir(m.paths.HyCertFile), 0o755); err != nil {
		return err
	}
	if err := EnsureCert(m.paths.HyCertFile, m.paths.HyKeyFile, FallbackSNI); err != nil {
		return err
	}
	return fsutil.WriteAtomicDir(m.paths.HyConfig, []byte(m.BuildConfig()), 0o755, 0o644)
}

func (m *Manager) ApplyIfChanged() (bool, error) {
	want := m.BuildConfig()
	if have, err := os.ReadFile(m.paths.HyConfig); err == nil && string(have) == want {
		return false, nil
	}
	return true, m.Apply()
}

func (m *Manager) Apply() error {
	if err := m.WriteConfig(); err != nil {
		return err
	}
	if _, err := exec.LookPath("hysteria"); err != nil {
		return nil
	}
	return systemd.Restart(Service)
}
