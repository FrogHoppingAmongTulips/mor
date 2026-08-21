package main

import (
	"mor/internal/config"
	"mor/internal/hysteria"
	"mor/internal/proxy"
	"mor/internal/store"
	"mor/internal/xray"
)

// proxyFor maps one key onto the endpoint a client dials. This is the single
// place the config turns into something a phone can use — every format the
// panel and the subscription hand out is rendered from what this returns, so
// none of them can quietly disagree about a port or a key.
func proxyFor(cfg *config.Config, u *store.User) (proxy.Proxy, bool) {
	// The protocol goes in the name because one person's keys share a name,
	// and Clash and sing-box treat the name as an identity: duplicates make
	// the profile fail to import rather than merely look confusing.
	p := proxy.Proxy{Name: u.Name + " · " + store.ProtoName(u.Proto), Server: cfg.PublicHost}

	switch u.Proto {
	case store.ProtoHy2:
		sni := firstNonEmpty(u.SNI, cfg.SNI, hysteria.FallbackSNI)
		p.Kind = proxy.Hysteria2
		p.Port = cfg.VPNPort
		p.Password = u.HyToken
		p.SNI = sni
		// The certificate is self-signed, so a client that checks it would
		// refuse to connect at all.
		p.Insecure = true
		if cfg.HyObfs != "" {
			p.Obfs, p.ObfsPassword = "salamander", cfg.HyObfs
		}

	case store.ProtoReality:
		r := cfg.Reality
		p.Kind = proxy.VLESS
		p.Port = r.Port
		p.UUID = u.UUID
		p.Reality = true
		p.SNI = firstNonEmpty(u.SNI, r.Dest)
		p.PublicKey = r.PublicKey
		p.ShortID = r.ShortID
		p.Fingerprint = "chrome"
		p.Network = r.Wire()
		if p.Network == config.TransportXHTTP {
			// XHTTP carries its own framing; the vision flow is not part of it.
			p.Path = r.Path
		} else {
			p.Flow = "xtls-rprx-vision"
		}

	case store.ProtoSS:
		p.Kind = proxy.Shadowsocks
		p.Port = cfg.SS.Port
		p.Password = u.SSPassword
		p.Method = xray.SSMethod

	default:
		return proxy.Proxy{}, false
	}
	return p, true
}
