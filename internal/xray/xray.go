package xray

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"

	"mor/internal/config"
	"mor/internal/store"
)

const Service = "xray"

type Manager struct {
	cfg   *config.Config
	paths config.Paths
}

func New(cfg *config.Config, paths config.Paths) *Manager {
	return &Manager{cfg: cfg, paths: paths}
}

func (m *Manager) BuildConfig(users []*store.User) ([]byte, error) {
	r := m.cfg.Reality
	clients := []any{}
	names := []string{r.Dest}
	seen := map[string]bool{r.Dest: true}
	for _, u := range users {
		if u.Proto != store.ProtoReality || u.UUID == "" {
			continue
		}
		clients = append(clients, map[string]any{
			"id":   u.UUID,
			"flow": "xtls-rprx-vision",
		})
		if u.SNI != "" && !seen[u.SNI] {
			seen[u.SNI] = true
			names = append(names, u.SNI)
		}
	}
	doc := map[string]any{
		"log": map[string]any{"loglevel": "warning"},
		"dns": map[string]any{"servers": []string{m.cfg.DNS}},
		"inbounds": []any{map[string]any{
			"listen":   "0.0.0.0",
			"port":     r.Port,
			"protocol": "vless",
			"settings": map[string]any{
				"clients":    clients,
				"decryption": "none",
			},
			"streamSettings": map[string]any{
				"network":  "tcp",
				"security": "reality",
				"realitySettings": map[string]any{
					"dest":        net.JoinHostPort(r.Dest, "443"),
					"serverNames": names,
					"privateKey":  r.PrivateKey,
					"shortIds":    []string{r.ShortID},
				},
			},
		}},
		"outbounds": []any{map[string]any{"protocol": "freedom"}},
	}
	return json.MarshalIndent(doc, "", "  ")
}

func (m *Manager) WriteConfig(users []*store.User) error {
	b, err := m.BuildConfig(users)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(m.paths.XrayConfig), 0o755); err != nil {
		return err
	}
	tmp := m.paths.XrayConfig + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, m.paths.XrayConfig)
}

func (m *Manager) Apply(users []*store.User) error {
	if err := m.WriteConfig(users); err != nil {
		return err
	}
	if !Installed() {
		return nil
	}
	out, err := exec.Command("systemctl", "restart", Service).CombinedOutput()
	if err != nil {
		return fmt.Errorf("restart %s: %w: %s", Service, err, out)
	}
	return nil
}

func (m *Manager) ApplyIfChanged(users []*store.User) (bool, error) {
	want, err := m.BuildConfig(users)
	if err != nil {
		return false, err
	}
	if have, err := os.ReadFile(m.paths.XrayConfig); err == nil && string(have) == string(want) {
		return false, nil
	}
	return true, m.Apply(users)
}

func Installed() bool {
	_, err := exec.LookPath("xray")
	return err == nil
}

func Link(cfg *config.Config, u *store.User) string {
	r := cfg.Reality
	sni := u.SNI
	if sni == "" {
		sni = r.Dest
	}
	q := url.Values{}
	q.Set("type", "tcp")
	q.Set("security", "reality")
	q.Set("sni", sni)
	q.Set("fp", "chrome")
	q.Set("pbk", r.PublicKey)
	q.Set("sid", r.ShortID)
	q.Set("flow", "xtls-rprx-vision")
	link := url.URL{
		Scheme:   "vless",
		User:     url.User(u.UUID),
		Host:     net.JoinHostPort(cfg.PublicHost, strconv.Itoa(r.Port)),
		RawQuery: q.Encode(),
		Fragment: u.Name,
	}
	return link.String()
}
