package hysteria

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type usage struct {
	Tx uint64 `json:"tx"`
	Rx uint64 `json:"rx"`
}

// Traffic returns bytes used per key since the previous call and resets the counters.
func Traffic(secret string) (map[string]uint64, error) {
	req, err := http.NewRequest("GET", fmt.Sprintf("http://127.0.0.1:%d/traffic?clear=1", StatsPort), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", secret)
	resp, err := (&http.Client{Timeout: 3 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("traffic stats: %s", resp.Status)
	}
	var raw map[string]usage
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}
	out := make(map[string]uint64, len(raw))
	for id, u := range raw {
		out[id] = u.Tx + u.Rx
	}
	return out, nil
}

// Online returns the number of live sessions per key.
func Online(secret string) (map[string]int, error) {
	req, err := http.NewRequest("GET", fmt.Sprintf("http://127.0.0.1:%d/online", StatsPort), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", secret)
	resp, err := (&http.Client{Timeout: 3 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("online stats: %s", resp.Status)
	}
	var out map[string]int
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}
