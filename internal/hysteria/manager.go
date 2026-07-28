package hysteria

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"mor/internal/config"
)

const AuthPort = 9797

const Service = "hysteria-server"

const fallbackSNI = "www.bing.com"

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
		sni = fallbackSNI
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
%s`, c.VPNPort, m.paths.HyCertFile, m.paths.HyKeyFile, AuthPort, sni, resolverBlock(c.DNS))
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
	if err := os.MkdirAll(filepath.Dir(m.paths.HyConfig), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(m.paths.HyCertFile), 0o755); err != nil {
		return err
	}
	if err := EnsureCert(m.paths.HyCertFile, m.paths.HyKeyFile, fallbackSNI); err != nil {
		return err
	}
	tmp := m.paths.HyConfig + ".tmp"
	if err := os.WriteFile(tmp, []byte(m.BuildConfig()), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, m.paths.HyConfig)
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
	out, err := exec.Command("systemctl", "restart", Service).CombinedOutput()
	if err != nil {
		return fmt.Errorf("restart %s: %w: %s", Service, err, out)
	}
	return nil
}
