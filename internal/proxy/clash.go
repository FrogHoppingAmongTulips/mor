package proxy

import (
	"fmt"
	"strconv"
	"strings"
)

// Clash renders the proxy list, a selector and a default rule as Clash Meta
// YAML — the dialect mihomo, Stash and the Clash forks read. The YAML is
// written by hand rather than through a library because the shape is fixed
// and a whole dependency to emit twenty known lines is a poor trade.
func Clash(proxies []Proxy) string {
	proxies = uniqueNames(proxies)
	var b strings.Builder
	b.WriteString("proxies:\n")

	names := make([]string, 0, len(proxies))
	for _, p := range proxies {
		if !p.Supports(FormatClash) {
			continue
		}
		names = append(names, p.Name)
		b.WriteString(p.clashEntry())
	}

	// A selector with a single member is still worth emitting: it is the
	// switch the app shows, and its absence makes the profile look broken.
	b.WriteString("\nproxy-groups:\n")
	b.WriteString("  - name: mor\n    type: select\n    proxies:\n")
	for _, n := range names {
		b.WriteString("      - " + yamlStr(n) + "\n")
	}

	b.WriteString("\nrules:\n  - MATCH,mor\n")
	return b.String()
}

func (p Proxy) clashEntry() string {
	var b strings.Builder
	add := func(k, v string) { b.WriteString("    " + k + ": " + v + "\n") }

	b.WriteString("  - name: " + yamlStr(p.Name) + "\n")
	add("server", yamlStr(p.Server))
	add("port", strconv.Itoa(p.Port))
	add("udp", "true")

	switch p.Kind {
	case Hysteria2:
		add("type", "hysteria2")
		add("password", yamlStr(p.Password))
		add("sni", yamlStr(p.SNI))
		if p.Insecure {
			add("skip-cert-verify", "true")
		}
		if p.Obfs != "" {
			add("obfs", yamlStr(p.Obfs))
			add("obfs-password", yamlStr(p.ObfsPassword))
		}

	case Shadowsocks:
		add("type", "ss")
		add("cipher", yamlStr(p.Method))
		add("password", yamlStr(p.Password))

	case VLESS:
		add("type", "vless")
		add("uuid", yamlStr(p.UUID))
		add("tls", "true")
		add("servername", yamlStr(p.SNI))
		add("client-fingerprint", yamlStr(p.Fingerprint))
		if p.Network == "xhttp" {
			// Clash Meta has no xhttp; its closest carrier is HTTP/2 over the
			// same path, which is what the config below asks the server for.
			add("network", "h2")
			b.WriteString("    h2-opts:\n      path:\n        - " + yamlStr(p.Path) + "\n")
		} else {
			add("network", "tcp")
			add("flow", yamlStr(p.Flow))
		}
		b.WriteString("    reality-opts:\n")
		b.WriteString("      public-key: " + yamlStr(p.PublicKey) + "\n")
		b.WriteString("      short-id: " + yamlStr(p.ShortID) + "\n")
	}
	return b.String()
}

// yamlStr quotes every scalar. Names carry spaces, dots and non-Latin letters,
// any of which turn an unquoted YAML value into something the parser reads as
// a different type — or refuses outright.
// yamlStr quotes a name for the Clash profile. Same reasoning as jsonStr: a
// control character inside a double-quoted YAML scalar is not portable, and a
// newline would split one entry into two.
func yamlStr(s string) string { return jsonStr(s) }

// SingBox renders a complete sing-box configuration: the official clients read
// a whole config, not a proxy fragment, so a bare outbound list would not
// import.
func SingBox(proxies []Proxy) string {
	proxies = uniqueNames(proxies)
	outs := make([]string, 0, len(proxies))
	tags := make([]string, 0, len(proxies))
	for _, p := range proxies {
		if !p.Supports(FormatSingBox) {
			continue
		}
		tags = append(tags, p.Name)
		outs = append(outs, p.singBoxOutbound())
	}

	quoted := make([]string, len(tags))
	for i, t := range tags {
		quoted[i] = jsonStr(t)
	}
	selector := fmt.Sprintf(`{"type":"selector","tag":"mor","outbounds":[%s]}`, strings.Join(quoted, ","))
	all := append([]string{selector}, outs...)
	all = append(all, `{"type":"direct","tag":"direct"}`)

	return fmt.Sprintf(`{
  "log": {"level": "warn"},
  "inbounds": [{"type": "tun", "tag": "tun-in", "address": ["172.19.0.1/30"], "auto_route": true, "strict_route": true}],
  "outbounds": [%s],
  "route": {"final": "mor", "auto_detect_interface": true}
}
`, strings.Join(all, ","))
}

func (p Proxy) singBoxOutbound() string {
	f := []string{
		`"tag":` + jsonStr(p.Name),
		`"server":` + jsonStr(p.Server),
		`"server_port":` + strconv.Itoa(p.Port),
	}
	switch p.Kind {
	case Hysteria2:
		f = append([]string{`"type":"hysteria2"`}, f...)
		f = append(f, `"password":`+jsonStr(p.Password))
		f = append(f, fmt.Sprintf(`"tls":{"enabled":true,"server_name":%s,"insecure":%t}`, jsonStr(p.SNI), p.Insecure))
		if p.Obfs != "" {
			f = append(f, fmt.Sprintf(`"obfs":{"type":%s,"password":%s}`, jsonStr(p.Obfs), jsonStr(p.ObfsPassword)))
		}

	case Shadowsocks:
		f = append([]string{`"type":"shadowsocks"`}, f...)
		f = append(f, `"method":`+jsonStr(p.Method), `"password":`+jsonStr(p.Password))

	case VLESS:
		f = append([]string{`"type":"vless"`}, f...)
		f = append(f, `"uuid":`+jsonStr(p.UUID))
		if p.Network != "xhttp" && p.Flow != "" {
			f = append(f, `"flow":`+jsonStr(p.Flow))
		}
		tls := fmt.Sprintf(
			`"tls":{"enabled":true,"server_name":%s,"utls":{"enabled":true,"fingerprint":%s},"reality":{"enabled":true,"public_key":%s,"short_id":%s}}`,
			jsonStr(p.SNI), jsonStr(p.Fingerprint), jsonStr(p.PublicKey), jsonStr(p.ShortID))
		f = append(f, tls)
		if p.Network == "xhttp" {
			f = append(f, fmt.Sprintf(`"transport":{"type":"http","path":%s}`, jsonStr(p.Path)))
		}
	}
	return "{" + strings.Join(f, ",") + "}"
}

// uniqueNames guarantees what Clash and sing-box require and callers forget:
// a name is an identity there, and two endpoints sharing one make the whole
// profile fail to import. One person's keys all carry that person's name, so
// collisions are the normal case rather than an unlucky one.
func uniqueNames(in []Proxy) []Proxy {
	out := make([]Proxy, len(in))
	copy(out, in)
	seen := make(map[string]int, len(out))
	for i := range out {
		name := out[i].Name
		seen[name]++
		if n := seen[name]; n > 1 {
			out[i].Name = name + " " + strconv.Itoa(n)
		}
	}
	return out
}

// jsonStr quotes a string for the profiles that are assembled by hand.
//
// encoding/json would be the obvious answer, but these files are built as text
// with fixed indentation the clients are used to reading, and swapping to a
// marshaller would rewrite every line of them. What matters is that no
// character a person can type into a name changes the shape of the document:
// a stray control character used to make the whole sing-box profile invalid,
// and an app that cannot parse the profile drops every key in it, not one.
func jsonStr(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '\\' || r == '"':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r == '\n' || r == '\r' || r == '\t':
			b.WriteByte(' ')
		case r < 0x20 || r == 0x7f:
			// Anything else unprintable is dropped rather than escaped: it can
			// only have arrived by accident, and it has no business in a name
			// shown in a phone's server list.
			continue
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
