package xray

import "testing"

// Plain port 53 tells the hosting provider every domain every client opens,
// and the tunnel does not cover the hop from the server to the resolver.
func TestDNSGoesOverHTTPS(t *testing.T) {
	cases := map[string]string{
		"1.1.1.1":                       "https+local://1.1.1.1/dns-query",
		"9.9.9.9:5353":                  "https+local://9.9.9.9/dns-query",
		"2606:4700:4700::1111":          "https+local://[2606:4700:4700::1111]/dns-query",
		"":                              "",
		"https://dns.example/dns-query": "https://dns.example/dns-query",
	}
	for in, want := range cases {
		if got := dohURL(in); got != want {
			t.Errorf("dohURL(%q) = %q, ожидалось %q", in, got, want)
		}
	}
}
