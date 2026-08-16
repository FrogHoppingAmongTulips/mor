package proxy

import (
	"encoding/json"
	"strings"
	"testing"
)

// Names come from a person typing into a box, and they end up inside YAML, JSON
// and a URL fragment. A name carrying a quote, a newline or a colon must not be
// able to change the shape of any of them — a profile that parses into
// something else is worse than one that fails to parse, because the client
// silently connects somewhere else or drops the other keys.
var hostileNames = []string{
	`обычное`,
	`с "кавычками"`,
	`со \обратным слешем`,
	"с\nпереводом строки",
	"с\rвозвратом каретки",
	`- name: подделка`,
	`x: y`,
	`{"злой":"json"}`,
	`'одинарные'`,
	`### комментарий`,
	`очень ` + strings.Repeat("длинное ", 40),
	`emoji 🙃 и юникод ё`,
	"\t таб в начале",
	`../../etc/passwd`,
	`%0d%0aInjected: header`,
}

func hostileProxies(name string) []Proxy {
	return []Proxy{
		{Name: name, Kind: Hysteria2, Server: "203.0.113.7", Port: 2096, Password: "пароль", SNI: "example.com"},
		{Name: name + " r", Kind: VLESS, Server: "203.0.113.7", Port: 443, UUID: "id", Reality: true,
			SNI: "example.com", PublicKey: "pbk", ShortID: "sid", Fingerprint: "chrome", Network: "tcp"},
		{Name: name + " s", Kind: Shadowsocks, Server: "203.0.113.7", Port: 2099, Password: "п", Method: "aes-256-gcm"},
	}
}

func TestSingBoxStaysValidJSONForAnyName(t *testing.T) {
	for _, name := range hostileNames {
		out := SingBox(hostileProxies(name))
		var v map[string]any
		if err := json.Unmarshal([]byte(out), &v); err != nil {
			t.Errorf("имя %q ломает JSON sing-box: %v", name, err)
			continue
		}
		outs, _ := v["outbounds"].([]any)
		if len(outs) < 3 {
			t.Errorf("имя %q: в профиле %d выходов, ожидалось не меньше 3", name, len(outs))
		}
	}
}

// The Clash profile is YAML built by hand, so this is where an injected line
// would land. Without a YAML parser in the standard library the check is
// structural: the document must keep exactly the sections and the number of
// entries it started with, whatever the name contains.
func TestClashKeepsItsShapeForAnyName(t *testing.T) {
	for _, name := range hostileNames {
		out := Clash(hostileProxies(name))
		// Counted as whole lines: the document opens with "proxies:" and has
		// no newline before it, and the group carries an indented "proxies:"
		// of its own that must not be mistaken for the section.
		lines := strings.Split(out, "\n")
		for _, section := range []string{"proxies:", "proxy-groups:", "rules:"} {
			n := 0
			for _, l := range lines {
				if l == section {
					n++
				}
			}
			if n != 1 {
				t.Errorf("имя %q: секция %s встречается %d раз", name, section, n)
			}
		}
		// Three proxies, each announced by a quoted name. The group has a
		// name line too, and it is the literal "mor", so it is counted apart.
		if got := strings.Count(out, "\n  - name: \""); got != 3 {
			t.Errorf("имя %q: записей proxies %d, ожидалось 3", name, got)
		}
		if got := strings.Count(out, "\n  - name: mor\n"); got != 1 {
			t.Errorf("имя %q: группа mor встречается %d раз", name, got)
		}
		if got := strings.Count(out, "\n      - \""); got != 3 {
			t.Errorf("имя %q: в группе %d участников, ожидалось 3", name, got)
		}
		// Every line that starts an entry must be one of those two shapes;
		// anything else means a name grew a line of its own.
		for _, line := range strings.Split(out, "\n") {
			if !strings.HasPrefix(line, "  - name: ") {
				continue
			}
			if line != "  - name: mor" && !strings.HasPrefix(line, `  - name: "`) {
				t.Errorf("имя %q: запись без кавычек: %q", name, line)
			}
		}
	}
}

// The link list is what most apps read. A newline inside a name would split one
// link into two lines, and the app would take the tail for another server.
func TestURIListHasOneLinePerProxy(t *testing.T) {
	for _, name := range hostileNames {
		list := hostileProxies(name)
		var lines []string
		for _, p := range list {
			if p.Supports(FormatURI) {
				lines = append(lines, p.URI())
			}
		}
		if len(lines) != len(list) {
			t.Fatalf("имя %q: ссылок %d, ключей %d", name, len(lines), len(list))
		}
		for _, l := range lines {
			if strings.ContainsAny(l, "\n\r") {
				t.Errorf("имя %q: перевод строки внутри ссылки %q", name, l)
			}
		}
	}
}
