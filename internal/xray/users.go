package xray

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"mor/internal/store"
)

func client(u *store.User) map[string]any {
	return map[string]any{
		"id":    u.UUID,
		"email": u.ID,
		"flow":  "xtls-rprx-vision",
	}
}

func apiServer() string { return "127.0.0.1:" + strconv.Itoa(APIPort) }

// AddUser lets a key in without restarting Xray, so live sessions survive.
func (m *Manager) AddUser(u *store.User) error {
	if !Installed() {
		return nil
	}
	doc := map[string]any{"inbounds": []any{map[string]any{
		"tag":      inboundTag,
		"listen":   "0.0.0.0",
		"port":     m.cfg.Reality.Port,
		"protocol": "vless",
		"settings": map[string]any{
			"clients":    []any{client(u)},
			"decryption": "none",
		},
	}}}
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
func (m *Manager) RemoveUser(id string) error {
	if !Installed() {
		return nil
	}
	out, err := exec.Command("xray", "api", "rmu", "-s", apiServer(), "-tag="+inboundTag, id).CombinedOutput()
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
