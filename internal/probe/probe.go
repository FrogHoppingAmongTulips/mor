// Package probe answers the question a server cannot answer about itself: does
// the outside world reach this port? A firewall on the machine, a hoster's
// filter and a mobile carrier all look identical from the inside — the port is
// open, the service is running, and the phone still says "timeout".
package probe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// api is a public checker that dials the address from several countries.
const api = "https://check-host.net"

var ErrUnavailable = errors.New("внешняя проверка недоступна — нет ответа от check-host.net")

// Result is one remote node's verdict. Reached is the wider question: did the
// packet get to the server at all? A refused connection means it did — the port
// is simply empty, not blocked on the way.
type Result struct {
	Node    string
	OK      bool
	Reached bool
	Note    string
}

// TCP asks remote nodes to connect to host:port. It is best effort: when the
// checker itself is unreachable, that is reported rather than treated as a
// closed port. Each node is handed to onNode the moment it answers, so a caller
// can show the dialling as it happens instead of a frozen screen.
func TCP(ctx context.Context, host string, port, nodes int, onNode func(Result)) ([]Result, error) {
	if host == "" {
		return nil, errors.New("не задан адрес сервера")
	}
	id, err := start(ctx, host, port, nodes)
	if err != nil {
		return nil, err
	}
	return collect(ctx, id, onNode)
}

// Preferred are the vantage points worth asking. A random three nodes can come
// back as Sweden, Turkey and Singapore — all green, and all answering a
// question nobody asked. What matters is whether the port survives the networks
// the people actually sit behind.
var Preferred = []string{
	"ru1.node.check-host.net",
	"ir1.node.check-host.net",
	"de1.node.check-host.net",
}

func start(ctx context.Context, host string, port, nodes int) (string, error) {
	id, err := startOn(ctx, host, port, nodes, Preferred)
	if err == nil {
		return id, nil
	}
	// A named node can be retired or offline, and then the whole request fails.
	// Better a random vantage point than no answer at all.
	return startOn(ctx, host, port, nodes, nil)
}

func startOn(ctx context.Context, host string, port, nodes int, want []string) (string, error) {
	q := url.Values{}
	q.Set("host", net.JoinHostPort(host, strconv.Itoa(port)))
	q.Set("max_nodes", strconv.Itoa(nodes))
	for _, n := range want {
		q.Add("node", n)
	}
	var doc struct {
		ID    string `json:"request_id"`
		Error string `json:"error"`
	}
	if err := get(ctx, api+"/check-tcp?"+q.Encode(), &doc); err != nil {
		return "", err
	}
	if doc.ID == "" {
		if doc.Error != "" {
			return "", fmt.Errorf("проверка не запустилась: %s", doc.Error)
		}
		return "", ErrUnavailable
	}
	return doc.ID, nil
}

// collect polls until every node reported or the deadline passes. Nodes answer
// one by one, so an early empty result means "still dialling", not "closed".
func collect(ctx context.Context, id string, onNode func(Result)) ([]Result, error) {
	deadline := time.Now().Add(25 * time.Second)
	told := map[string]bool{}
	for {
		var raw map[string]json.RawMessage
		if err := get(ctx, api+"/check-result/"+id, &raw); err != nil {
			return nil, err
		}
		out := make([]Result, 0, len(raw))
		pending := false
		for node, v := range raw {
			if string(v) == "null" {
				pending = true
				continue
			}
			r := verdict(node, v)
			out = append(out, r)
			if onNode != nil && !told[node] {
				told[node] = true
				onNode(r)
			}
		}
		if !pending && len(out) > 0 {
			return out, nil
		}
		if time.Now().After(deadline) {
			if len(out) > 0 {
				return out, nil
			}
			return nil, ErrUnavailable
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

// verdict reads one node's answer: [{"time": 0.1, "address": "1.2.3.4"}] when
// the connection went through, [{"error": "..."}] when it did not.
func verdict(node string, v json.RawMessage) Result {
	r := Result{Node: shortNode(node)}
	var list []struct {
		Time    float64 `json:"time"`
		Error   string  `json:"error"`
		Address string  `json:"address"`
	}
	if err := json.Unmarshal(v, &list); err != nil || len(list) == 0 {
		r.Note = "нет ответа"
		return r
	}
	switch {
	case list[0].Error != "":
		r.Note = list[0].Error
		// "Connection refused" is an answer from the machine itself, so the
		// route is open even though nothing listens on that port.
		r.Reached = strings.Contains(strings.ToLower(list[0].Error), "refused")
	case list[0].Time > 0:
		r.OK, r.Reached = true, true
		r.Note = fmt.Sprintf("%.0f мс", list[0].Time*1000)
	default:
		r.Note = "нет ответа"
	}
	return r
}

// shortNode turns "ru1.node.check-host.net" into "ru1".
func shortNode(node string) string {
	if i := strings.Index(node, "."); i > 0 {
		return node[:i]
	}
	return node
}

func get(ctx context.Context, u string, into any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: код %d", ErrUnavailable, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(into)
}
