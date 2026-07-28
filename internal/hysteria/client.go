package hysteria

import (
	"net"
	"net/url"
	"strconv"

	"mor/internal/config"
	"mor/internal/store"
)

func Link(cfg *config.Config, u *store.User) string {
	sni := u.SNI
	if sni == "" {
		sni = cfg.SNI
	}
	if sni == "" {
		sni = fallbackSNI
	}
	q := url.Values{}
	q.Set("insecure", "1")
	q.Set("sni", sni)
	link := url.URL{
		Scheme:   "hysteria2",
		User:     url.User(u.HyToken),
		Host:     net.JoinHostPort(cfg.PublicHost, strconv.Itoa(cfg.VPNPort)),
		RawQuery: q.Encode(),
		Fragment: u.Name,
	}
	return link.String()
}
