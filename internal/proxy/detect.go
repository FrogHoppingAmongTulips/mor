package proxy

import "strings"

// Detect picks the format a client can read from the name it announces.
//
// Clients do not negotiate: they fetch the subscription and either parse it or
// show an error the owner has to guess at. Sniffing the User-Agent is how every
// panel solves this, and the fallback is the base64 URI list because that is
// what the widest set of apps understands.
func Detect(userAgent string) Format {
	ua := strings.ToLower(userAgent)
	switch {
	case containsAny(ua, "clash", "mihomo", "stash", "meta"):
		return FormatClash
	case containsAny(ua, "sing-box", "singbox", "sfa", "sfi", "sfm", "karing", "hiddify"):
		return FormatSingBox
	default:
		return FormatURI
	}
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// ContentType is what the format has to be served as. Clash refuses YAML sent
// as anything but text, and sing-box wants JSON.
func (f Format) ContentType() string {
	switch f {
	case FormatClash:
		return "text/yaml; charset=utf-8"
	case FormatSingBox:
		return "application/json; charset=utf-8"
	default:
		return "text/plain; charset=utf-8"
	}
}
