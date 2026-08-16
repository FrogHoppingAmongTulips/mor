package xray

import (
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"strings"

	"mor/internal/config"
	"mor/internal/fsutil"
	"mor/internal/store"
	"mor/internal/systemd"
)

const Service = "xray"

const (
	APIPort    = 10085
	inboundTag = "vless-in"
	encTag     = "enc-in"
	ssTag      = "ss-in"
	apiInbound = "api"
)

// SSMethod is fixed rather than configurable: aes-256-gcm is the one AEAD
// cipher every Shadowsocks client ever shipped understands, which is the
// entire point of carrying this protocol — apps too old or too plain for
// Reality/Hysteria2 still open it.
const SSMethod = "aes-256-gcm"

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
	encClients := []any{}
	ssClients := []any{}
	for _, u := range users {
		// An expired key must not come back when the config is rewritten.
		if u.Expired() {
			continue
		}
		switch u.Proto {
		case store.ProtoReality:
			if u.UUID == "" {
				continue
			}
			clients = append(clients, client(u, r.Wire()))
			// A key with its own cover site only works if Reality answers for it.
			if u.SNI != "" && !seen[u.SNI] {
				seen[u.SNI] = true
				names = append(names, u.SNI)
			}
		case store.ProtoEnc:
			if u.UUID == "" {
				continue
			}
			encClients = append(encClients, encClient(u))
		case store.ProtoSS:
			if u.SSPassword == "" {
				continue
			}
			ssClients = append(ssClients, ssClient(u))
		}
	}
	doc := map[string]any{
		"log": map[string]any{"loglevel": "warning"},
		// DoH for the same reason as Hysteria2's resolver: plain port 53 hands
		// the hosting provider every domain every client opens. The literal
		// address needs no bootstrap resolver of its own.
		"dns":   map[string]any{"servers": []string{dohURL(m.cfg.DNS)}},
		"api":   map[string]any{"tag": apiInbound, "services": []string{"HandlerService", "StatsService"}},
		"stats": map[string]any{},
		"policy": map[string]any{
			"levels": map[string]any{"0": map[string]any{"statsUserUplink": true, "statsUserDownlink": true}},
		},
		"routing": map[string]any{
			"rules": []any{map[string]any{"type": "field", "inboundTag": []string{apiInbound}, "outboundTag": apiInbound}},
		},
		"inbounds":  m.inbounds(clients, names, encClients, ssClients),
		"outbounds": []any{map[string]any{"protocol": "freedom"}},
	}
	return json.MarshalIndent(doc, "", "  ")
}

// inbounds lists what Xray should listen on. A protocol that is switched off
// gets no inbound at all, so its port goes quiet without touching the service.
func (m *Manager) inbounds(clients []any, names []string, encClients []any, ssClients []any) []any {
	r := m.cfg.Reality
	list := []any{map[string]any{
		"tag":      apiInbound,
		"listen":   "127.0.0.1",
		"port":     APIPort,
		"protocol": "dokodemo-door",
		"settings": map[string]any{"address": "127.0.0.1"},
	}}
	if m.cfg.On(store.ProtoReality) {
		list = append(list, map[string]any{
			"tag":      inboundTag,
			"listen":   "0.0.0.0",
			"port":     r.Port,
			"protocol": "vless",
			"settings": map[string]any{
				"clients":    clients,
				"decryption": "none",
			},
			"streamSettings": stream(r, names),
		})
	}
	if m.cfg.On(store.ProtoEnc) {
		list = append(list, map[string]any{
			"tag":      encTag,
			"listen":   "0.0.0.0",
			"port":     m.cfg.Enc.Port,
			"protocol": "vless",
			"settings": map[string]any{
				"clients":    encClients,
				"decryption": m.cfg.Enc.Decryption,
			},
			"streamSettings": map[string]any{"network": "tcp"},
		})
	}
	if m.cfg.On(store.ProtoSS) {
		list = append(list, map[string]any{
			"tag":      ssTag,
			"listen":   "0.0.0.0",
			"port":     m.cfg.SS.Port,
			"protocol": "shadowsocks",
			"settings": map[string]any{
				"clients": ssClients,
				"network": "tcp,udp",
			},
		})
	}
	return list
}

func (m *Manager) WriteConfig(users []*store.User) error {
	b, err := m.BuildConfig(users)
	if err != nil {
		return err
	}
	return fsutil.WriteAtomicDir(m.paths.XrayConfig, b, 0o755, 0o644)
}

func (m *Manager) Apply(users []*store.User) error {
	if err := m.WriteConfig(users); err != nil {
		return err
	}
	if !Installed() {
		return nil
	}
	return systemd.Restart(Service)
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

// stream describes how Reality carries traffic. XHTTP wraps it in ordinary
// looking web requests; the Reality handshake underneath stays the same.
func stream(r config.Reality, names []string) map[string]any {
	s := map[string]any{
		"network":  r.Wire(),
		"security": "reality",
		"realitySettings": map[string]any{
			"dest":        net.JoinHostPort(r.Dest, "443"),
			"serverNames": names,
			"privateKey":  r.PrivateKey,
			"shortIds":    []string{r.ShortID},
		},
	}
	if r.Wire() == config.TransportXHTTP {
		s["xhttpSettings"] = map[string]any{"path": r.Path, "mode": "auto"}
	}
	return s
}

// dohURL turns a resolver address into the DNS-over-HTTPS form Xray accepts.
// Cloudflare, Quad9 and AdGuard all answer /dns-query on their own address, so
// no separate mapping is needed; anything already spelled out as a URL is left
// alone.
func dohURL(dns string) string {
	dns = strings.TrimSpace(dns)
	if dns == "" || strings.Contains(dns, "://") {
		return dns
	}
	if h, _, err := net.SplitHostPort(dns); err == nil {
		dns = h
	}
	if ip := net.ParseIP(dns); ip != nil && ip.To4() == nil {
		dns = "[" + dns + "]"
	}
	return "https+local://" + dns + "/dns-query"
}
