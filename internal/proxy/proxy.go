// Package proxy describes one endpoint a client can connect to, and renders
// that description into whatever the client actually speaks.
//
// Apps do not agree on a format. Most read a base64 list of URIs; the Clash
// family reads YAML; sing-box reads its own JSON. Describing an endpoint once
// and rendering it three ways keeps the three from drifting apart, which is
// what happens when each format is built by hand from the config.
package proxy

import (
	"encoding/base64"
	"net"
	"net/url"
	"strconv"
)

type Kind string

const (
	Hysteria2   Kind = "hysteria2"
	VLESS       Kind = "vless"
	Shadowsocks Kind = "ss"
)

// Proxy is everything a client needs to reach one endpoint. Fields outside the
// kind's own group are ignored, so a zero value stays harmless.
type Proxy struct {
	Name   string
	Kind   Kind
	Server string
	Port   int

	// Credentials. Which one carries the secret depends on the kind: a UUID
	// for VLESS, a password for Hysteria2 and Shadowsocks.
	UUID     string
	Password string

	// TLS and masquerade.
	SNI      string
	Insecure bool

	// Reality: a TLS handshake borrowed from a real site.
	Reality     bool
	PublicKey   string
	ShortID     string
	Fingerprint string
	Flow        string

	// Transport shape.
	Network string // "tcp" or "xhttp"
	Path    string

	// Hysteria2 packet scrambling.
	Obfs         string
	ObfsPassword string

	// Shadowsocks cipher.
	Method string
}

func (p Proxy) addr() string { return net.JoinHostPort(p.Server, strconv.Itoa(p.Port)) }

// URI renders the one-line form: what a QR code holds and what a base64
// subscription is a list of.
func (p Proxy) URI() string {
	switch p.Kind {
	case Hysteria2:
		q := url.Values{}
		if p.Insecure {
			q.Set("insecure", "1")
		}
		q.Set("sni", p.SNI)
		if p.Obfs != "" {
			q.Set("obfs", p.Obfs)
			q.Set("obfs-password", p.ObfsPassword)
		}
		return (&url.URL{
			Scheme: "hysteria2", User: url.User(p.Password), Host: p.addr(),
			RawQuery: q.Encode(), Fragment: p.Name,
		}).String()

	case Shadowsocks:
		// SIP002: userinfo is method:password in web-safe base64 without
		// padding — the spelling every Shadowsocks client parses.
		userinfo := base64.RawURLEncoding.EncodeToString([]byte(p.Method + ":" + p.Password))
		return (&url.URL{
			Scheme: "ss", User: url.User(userinfo), Host: p.addr(), Fragment: p.Name,
		}).String()

	case VLESS:
		// VLESS здесь всегда Reality: другой его разновидности mor не отдаёт.
		q := url.Values{}
		q.Set("type", p.Network)
		q.Set("security", "reality")
		q.Set("sni", p.SNI)
		q.Set("fp", p.Fingerprint)
		q.Set("pbk", p.PublicKey)
		q.Set("sid", p.ShortID)
		if p.Network == "xhttp" {
			q.Set("path", p.Path)
			q.Set("mode", "auto")
		} else {
			q.Set("flow", p.Flow)
		}
		return (&url.URL{
			Scheme: "vless", User: url.User(p.UUID), Host: p.addr(),
			RawQuery: q.Encode(), Fragment: p.Name,
		}).String()
	}
	return ""
}

// Format is what a particular client can read.
type Format int

const (
	FormatURI Format = iota // base64 list of URIs — the v2ray-style subscription
	FormatClash
	FormatSingBox
)

// Supports reports whether a format can express this endpoint.
//
// Every protocol mor serves is expressible in all three, so this is true
// throughout. It stays as a hook: the next protocol added may not be, and the
// renderers already ask.
func (p Proxy) Supports(Format) bool { return true }
