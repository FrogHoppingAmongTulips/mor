package xray

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"mor/internal/config"
	"mor/internal/store"
)

// client describes one Reality key. The vision flow belongs to plain TCP only —
// XHTTP does its own framing and refuses a client that asks for both.
func client(u *store.User, wire string) map[string]any {
	c := map[string]any{
		"id":    u.UUID,
		"email": u.ID,
	}
	if wire != config.TransportXHTTP {
		c["flow"] = "xtls-rprx-vision"
	}
	return c
}

// encClient is a VLESS Encryption key: the same UUID shape as Reality, without
// the vision flow, which belongs to TLS transports only.
func encClient(u *store.User) map[string]any {
	return map[string]any{
		"id":    u.UUID,
		"email": u.ID,
	}
}

// ssClient is one Shadowsocks key: its own password under the server's one
// fixed method, so removing a key never touches anyone else's.
func ssClient(u *store.User) map[string]any {
	return map[string]any{
		"method":   SSMethod,
		"password": u.SSPassword,
		"email":    u.ID,
	}
}

// TagOf names the inbound a key belongs to.
func TagOf(proto string) string {
	switch proto {
	case store.ProtoEnc:
		return encTag
	case store.ProtoSS:
		return ssTag
	default:
		return inboundTag
	}
}

func apiServer() string { return "127.0.0.1:" + strconv.Itoa(APIPort) }

// AddUser lets a key in without restarting Xray, so live sessions survive.
func (m *Manager) AddUser(u *store.User) error {
	if !Installed() {
		return nil
	}
	inbound := map[string]any{
		"tag":      inboundTag,
		"listen":   "0.0.0.0",
		"port":     m.cfg.Reality.Port,
		"protocol": "vless",
		"settings": map[string]any{
			"clients":    []any{client(u, m.cfg.Reality.Wire())},
			"decryption": "none",
		},
	}
	if u.Proto == store.ProtoEnc {
		inbound = map[string]any{
			"tag":      encTag,
			"listen":   "0.0.0.0",
			"port":     m.cfg.Enc.Port,
			"protocol": "vless",
			"settings": map[string]any{
				"clients":    []any{encClient(u)},
				"decryption": m.cfg.Enc.Decryption,
			},
		}
	}
	if u.Proto == store.ProtoSS {
		inbound = map[string]any{
			"tag":      ssTag,
			"listen":   "0.0.0.0",
			"port":     m.cfg.SS.Port,
			"protocol": "shadowsocks",
			"settings": map[string]any{
				"clients": []any{ssClient(u)},
				"network": "tcp,udp",
			},
		}
	}
	doc := map[string]any{"inbounds": []any{inbound}}
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp("", "mor-adu-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	f.Close()

	out, err := exec.Command("xray", "api", "adu", "-s", apiServer(), f.Name()).CombinedOutput()
	if err != nil && !strings.Contains(string(out), "already exists") {
		return fmt.Errorf("xray api adu: %w: %s", err, out)
	}
	return nil
}

// RemoveUser cuts a key off without restarting Xray.
func (m *Manager) RemoveUser(id, proto string) error {
	if !Installed() {
		return nil
	}
	out, err := exec.Command("xray", "api", "rmu", "-s", apiServer(), "-tag="+TagOf(proto), id).CombinedOutput()
	if err != nil && !strings.Contains(string(out), "not found") {
		return fmt.Errorf("xray api rmu: %w: %s", err, out)
	}
	return nil
}

// Traffic returns bytes per key since the previous call and resets the counters.
func Traffic() (map[string]uint64, error) {
	if !Installed() {
		return nil, nil
	}
	out, err := exec.Command("xray", "api", "statsquery", "-s", apiServer(), "-pattern", "user>>>", "-reset").Output()
	if err != nil {
		return nil, err
	}
	var doc struct {
		Stat []struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		} `json:"stat"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		return nil, err
	}
	res := map[string]uint64{}
	for _, st := range doc.Stat {
		parts := strings.Split(st.Name, ">>>")
		if len(parts) < 2 {
			continue
		}
		v, err := strconv.ParseUint(st.Value, 10, 64)
		if err != nil {
			continue
		}
		res[parts[1]] += v
	}
	return res, nil
}
