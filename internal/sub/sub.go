// Package sub serves subscriptions: one link per person, holding every protocol
// that person has. Client apps re-read it, test the endpoints and pick a live
// one themselves, so a dead protocol stops being the owner's problem.
package sub

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"mor/internal/proxy"
	"mor/internal/store"
	"mor/internal/tlsx"
)

// DefaultPort is where the subscription server listens. Nothing sensitive is
// exposed: without the token every path answers 404.
const DefaultPort = 8880

const path = "/sub/"

// Body is the payload apps expect: the links, one per line, base64 encoded.
func Body(links []string) string {
	return base64.StdEncoding.EncodeToString([]byte(strings.Join(links, "\n")))
}

// URL is what the owner hands to a person. It says https only when the server
// is actually serving it: a link promising TLS to a port that answers plain
// text fails in the app with nothing to explain it.
func URL(host string, port int, token string, secure bool) string {
	scheme := "http://"
	if secure {
		scheme = "https://"
	}
	return scheme + net.JoinHostPort(host, strconv.Itoa(port)) + path + token
}

// Proxies describes one key as an endpoint. The description comes from the
// caller because only cmd knows how the config maps onto each protocol.
type Proxies func(u *store.User) (proxy.Proxy, bool)

// Quota reports what one key spent and what it is allowed. Apps draw a bar out
// of the pair, so the cap travels to the phone without the owner explaining it.
type Quota func(id string) (used, limit uint64)

type Server struct {
	st      *store.Store
	proxies Proxies
	title   string
	quota   Quota
	devices *Devices
}

func New(st *store.Store, proxies Proxies, title string, quota Quota) *Server {
	return &Server{st: st, proxies: proxies, title: title, quota: quota}
}

// TrackDevices turns on the device limit. Without it the subscription is
// served to anyone holding the link, which is how mor behaved before and how
// it still behaves for any key with no limit set.
func (s *Server) TrackDevices(d *Devices) *Server {
	s.devices = d
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.URL.Path, path) {
		http.NotFound(w, r)
		return
	}
	// An explicit suffix wins over sniffing: it is the escape hatch for a
	// client whose User-Agent says nothing useful.
	token := strings.TrimPrefix(r.URL.Path, path)
	format := proxy.Detect(r.UserAgent())
	if i := strings.IndexByte(token, '/'); i >= 0 {
		switch token[i+1:] {
		case "clash":
			format = proxy.FormatClash
		case "singbox", "sing-box":
			format = proxy.FormatSingBox
		case "raw", "v2ray":
			format = proxy.FormatURI
		default:
			http.NotFound(w, r)
			return
		}
		token = token[:i]
	}
	if token == "" {
		http.NotFound(w, r)
		return
	}
	users := s.st.BySub(token)
	if len(users) == 0 {
		http.NotFound(w, r)
		return
	}

	// The cap is the person's, and every key of one person carries the same
	// number, so the first is as good as any.
	if s.devices != nil && !s.devices.Allow(token, deviceID(r), users[0].IPLimit) {
		http.Error(w, "слишком много устройств на этом ключе", http.StatusForbidden)
		return
	}

	list := make([]proxy.Proxy, 0, len(users))
	var total, allowed uint64
	var expiry time.Time
	for _, u := range users {
		if p, ok := s.proxies(u); ok {
			list = append(list, p)
		}
		if s.quota != nil {
			used, limit := s.quota(u.ID)
			total += used
			// One cap covers the person, not each protocol separately.
			if limit > allowed {
				allowed = limit
			}
		}
		if !u.ExpiresAt.IsZero() && (expiry.IsZero() || u.ExpiresAt.Before(expiry)) {
			expiry = u.ExpiresAt
		}
	}

	w.Header().Set("Content-Type", format.ContentType())
	w.Header().Set("Profile-Update-Interval", "12")
	w.Header().Set("Profile-Title", title(s.title, users[0].Name))
	w.Header().Set("Subscription-Userinfo", userinfo(total, allowed, expiry))
	_, _ = w.Write([]byte(Render(list, format)))
}

// Render turns the endpoints into whatever the client reads.
func Render(list []proxy.Proxy, format proxy.Format) string {
	switch format {
	case proxy.FormatClash:
		return proxy.Clash(list)
	case proxy.FormatSingBox:
		return proxy.SingBox(list)
	default:
		links := make([]string, 0, len(list))
		for _, p := range list {
			if p.Supports(proxy.FormatURI) {
				links = append(links, p.URI())
			}
		}
		return Body(links)
	}
}

// title is what the app shows in its server list.
func title(server, name string) string {
	t := name
	if server != "" {
		t = server + " · " + name
	}
	return "base64:" + base64.StdEncoding.EncodeToString([]byte(t))
}

// userinfo is the header apps read to draw traffic and expiry. Upload stays at
// zero: mor counts one number per key, not per direction. A total of zero means
// "no limit" rather than "nothing left" — that is how apps read it.
func userinfo(used, limit uint64, expiry time.Time) string {
	s := fmt.Sprintf("upload=0; download=%d; total=%d", used, limit)
	if !expiry.IsZero() {
		s += fmt.Sprintf("; expire=%d", expiry.Unix())
	}
	return s
}

// Serve runs until the context is cancelled. A busy port is reported rather
// than retried: the owner picks another one in the menu.
//
// With a TLS config the port answers both: TLS as normal, and a plain HTTP
// request with a redirect to the same address over https. That is what keeps
// every link handed out before HTTPS working — the app asks for http, gets a
// redirect and follows it, and nobody has to reissue anything.
func Serve(ctx context.Context, port int, h http.Handler, tlsCfg *tls.Config) error {
	mux := http.NewServeMux()
	mux.Handle(path, h)
	srv := &http.Server{
		Addr:              ":" + strconv.Itoa(port),
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()

	if tlsCfg == nil {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}

	raw, err := net.Listen("tcp", srv.Addr)
	if err != nil {
		return err
	}
	srv.TLSConfig = tlsCfg
	ln := tlsx.New(raw, port, "Подписка")
	if err := srv.ServeTLS(ln, "", ""); err != nil &&
		!errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
		return err
	}
	return nil
}
