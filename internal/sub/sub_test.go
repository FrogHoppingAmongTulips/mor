package sub

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"mor/internal/proxy"
	"mor/internal/store"
)

// testProxies gives every protocol a shape the renderers understand, so the
// server's own behaviour is what the tests measure rather than the details of
// one protocol.
func testProxies(u *store.User) (proxy.Proxy, bool) {
	p := proxy.Proxy{Name: u.Name, Server: "203.0.113.7", Port: 443}
	switch u.Proto {
	case store.ProtoHy2:
		p.Kind, p.Password, p.SNI = proxy.Hysteria2, "тк", "example.com"
	case store.ProtoSS:
		p.Kind, p.Password, p.Method = proxy.Shadowsocks, "пароль", "aes-256-gcm"
	case store.ProtoEnc:
		p.Kind, p.UUID, p.Network, p.Encryption = proxy.VLESS, "uuid-enc", "tcp", "enc-blob"
	default:
		p.Kind, p.UUID, p.Reality, p.Network = proxy.VLESS, "uuid-reality", true, "tcp"
		p.SNI, p.PublicKey, p.ShortID, p.Fingerprint, p.Flow = "example.com", "pbk", "sid", "chrome", "xtls-rprx-vision"
	}
	return p, true
}

func serverWith(t *testing.T, users []*store.User) (*Server, *store.Store) {
	t.Helper()
	st, err := store.Open(t.TempDir() + "/users.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range users {
		if _, err := st.Add(u); err != nil {
			t.Fatal(err)
		}
	}
	for _, u := range st.List() {
		if err := st.SetSub(u.ID, "тк"+u.Proto[:1]); err != nil {
			t.Fatal(err)
		}
	}
	return New(st, testProxies, "сервер", nil), st
}

func TestServesGroup(t *testing.T) {
	st, _ := store.Open(t.TempDir() + "/users.json")
	for _, p := range []string{store.ProtoHy2, store.ProtoReality, store.ProtoEnc} {
		u, err := st.Add(&store.User{Name: "телефон", Proto: p})
		if err != nil {
			t.Fatal(err)
		}
		if err := st.SetSub(u.ID, "токен123"); err != nil {
			t.Fatal(err)
		}
	}
	s := New(st, testProxies, "сервер", nil)

	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/sub/токен123", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("код %d", w.Code)
	}
	raw, err := base64.StdEncoding.DecodeString(w.Body.String())
	if err != nil {
		t.Fatalf("тело не base64: %v", err)
	}
	lines := strings.Split(string(raw), "\n")
	if len(lines) != 3 {
		t.Errorf("в подписке %d строк, ждали 3: %q", len(lines), lines)
	}
	if w.Header().Get("Profile-Title") == "" {
		t.Error("нет заголовка Profile-Title — приложение не покажет имя")
	}
}

func TestUnknownTokenIsNotFound(t *testing.T) {
	s, _ := serverWith(t, []*store.User{{Name: "телефон", Proto: store.ProtoHy2}})
	for _, path := range []string{"/sub/чужой", "/sub/", "/", "/sub/a/b"} {
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusNotFound {
			t.Errorf("%s вернул %d, ждали 404", path, w.Code)
		}
	}
}

// An expired key must disappear from the subscription: an app that keeps trying
// it would look like the server is broken.
func TestExpiredKeyIsHidden(t *testing.T) {
	st, _ := store.Open(t.TempDir() + "/users.json")
	live, _ := st.Add(&store.User{Name: "живой", Proto: store.ProtoReality})
	dead, _ := st.Add(&store.User{Name: "истёк", Proto: store.ProtoHy2})
	for _, u := range []*store.User{live, dead} {
		if err := st.SetSub(u.ID, "общий"); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.SetExpiry(dead.ID, time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	s := New(st, testProxies, "", nil)

	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/sub/общий", nil))
	raw, _ := base64.StdEncoding.DecodeString(w.Body.String())
	// Names travel in the URI fragment, where non-Latin letters are
	// percent-encoded — so the check is on the decoded fragment, not the
	// raw bytes.
	names := fragments(t, string(raw))
	if slices.Contains(names, "истёк") {
		t.Error("истёкший ключ попал в подписку")
	}
	if !slices.Contains(names, "живой") {
		t.Errorf("живой ключ пропал из подписки: %v", names)
	}
}

// fragments pulls the display name out of every link in a base64 body.
func fragments(t *testing.T, body string) []string {
	t.Helper()
	var out []string
	for _, line := range strings.Split(strings.TrimSpace(body), "\n") {
		if line == "" {
			continue
		}
		u, err := url.Parse(line)
		if err != nil {
			t.Fatalf("нераспознанная ссылка %q: %v", line, err)
		}
		out = append(out, u.Fragment)
	}
	return out
}

func TestUserinfoHeader(t *testing.T) {
	st, _ := store.Open(t.TempDir() + "/users.json")
	u, _ := st.Add(&store.User{Name: "телефон", Proto: store.ProtoHy2})
	if err := st.SetSub(u.ID, "т"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetExpiry(u.ID, time.Now().Add(48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	s := New(st, testProxies, "",
		func(string) (uint64, uint64) { return 1234, 0 })

	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/sub/т", nil))
	got := w.Header().Get("Subscription-Userinfo")
	if !strings.Contains(got, "download=1234") || !strings.Contains(got, "expire=") {
		t.Errorf("Subscription-Userinfo = %q", got)
	}
	if !strings.Contains(got, "total=0") {
		t.Errorf("без лимита total должен быть 0: %q", got)
	}
}

// The cap has to reach the phone: apps draw a traffic bar out of this header,
// which is how a person sees what is left without asking the owner.
func TestUserinfoCarriesLimit(t *testing.T) {
	st, _ := store.Open(t.TempDir() + "/users.json")
	u, _ := st.Add(&store.User{Name: "телефон", Proto: store.ProtoHy2, Limit: 10 << 30})
	if err := st.SetSub(u.ID, "т"); err != nil {
		t.Fatal(err)
	}
	s := New(st, testProxies, "",
		func(string) (uint64, uint64) { return 1 << 30, 10 << 30 })

	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/sub/т", nil))
	got := w.Header().Get("Subscription-Userinfo")
	if !strings.Contains(got, "total=10737418240") {
		t.Errorf("лимит не попал в заголовок: %q", got)
	}
}

func TestURL(t *testing.T) {
	if got := URL("vpn.example.com", 8880, "abc", true); got != "https://vpn.example.com:8880/sub/abc" {
		t.Errorf("с сертификатом = %q", got)
	}
	if got := URL("vpn.example.com", 8880, "abc", false); got != "http://vpn.example.com:8880/sub/abc" {
		t.Errorf("URL = %s", got)
	}
	if got := URL("2001:db8::1", 8880, "abc", false); !strings.HasPrefix(got, "http://[2001:db8::1]:8880/") {
		t.Errorf("IPv6 адрес не в скобках: %s", got)
	}
}

// fetch asks for a subscription as one device would.
func fetch(s *Server, token, device string) int {
	r := httptest.NewRequest(http.MethodGet, "/sub/"+token, nil)
	if device != "" {
		r.Header.Set("x-hwid", device)
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w.Code
}

func TestSubscriptionRefusesTooManyDevices(t *testing.T) {
	st, _ := store.Open(t.TempDir() + "/users.json")
	for _, p := range []string{store.ProtoHy2, store.ProtoReality} {
		u, err := st.Add(&store.User{Name: "телефон", Proto: p, IPLimit: 2})
		if err != nil {
			t.Fatal(err)
		}
		if err := st.SetSub(u.ID, "токен123"); err != nil {
			t.Fatal(err)
		}
	}
	d := OpenDevices(t.TempDir() + "/devices.json")
	s := New(st, testProxies, "сервер", nil).TrackDevices(d)

	if code := fetch(s, "токен123", "телефон"); code != http.StatusOK {
		t.Fatalf("первое устройство: код %d", code)
	}
	if code := fetch(s, "токен123", "ноут"); code != http.StatusOK {
		t.Fatalf("второе устройство: код %d", code)
	}
	if code := fetch(s, "токен123", "чужой"); code != http.StatusForbidden {
		t.Fatalf("третье устройство при лимите 2: код %d, ожидалось 403", code)
	}
	// The two that were let in must keep working, and the key having several
	// protocols must not count as several devices.
	if code := fetch(s, "токен123", "телефон"); code != http.StatusOK {
		t.Fatalf("своё же устройство: код %d", code)
	}
	if n := d.Count("токен123"); n != 2 {
		t.Fatalf("устройств насчитано %d, ожидалось 2", n)
	}
}

func TestSubscriptionWithoutLimitServesEverybody(t *testing.T) {
	s, _ := serverWith(t, []*store.User{{Name: "телефон", Proto: store.ProtoHy2}})
	s.TrackDevices(OpenDevices(t.TempDir() + "/devices.json"))

	for _, device := range []string{"один", "два", "три", ""} {
		if code := fetch(s, "ткh", device); code != http.StatusOK {
			t.Fatalf("устройство %q: код %d", device, code)
		}
	}
}

// selfSignedForTest is a throwaway certificate for 127.0.0.1 — the test is
// about the transport, not about who signed it.
func selfSignedForTest(t *testing.T) *tls.Config {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := x509.Certificate{
		SerialNumber: big.NewInt(1),
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return &tls.Config{Certificates: []tls.Certificate{{
		Certificate: [][]byte{der}, PrivateKey: key,
	}}, MinVersion: tls.VersionTLS12}
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// The subscription carries every key of one person. Over TLS it must arrive
// intact — and a link handed out before HTTPS existed must still land, or
// switching it on would silently cut off everybody already using one.
func TestSubscriptionOverTLS(t *testing.T) {
	st, _ := store.Open(t.TempDir() + "/users.json")
	u, err := st.Add(&store.User{Name: "телефон", Proto: store.ProtoHy2})
	if err != nil {
		t.Fatal(err)
	}
	// A real token is hex, and the test uses one: a token with letters outside
	// ASCII would come back percent-encoded in the redirect and turn this into
	// a test about escaping.
	const token = "a1b2c3d4"
	if err := st.SetSub(u.ID, token); err != nil {
		t.Fatal(err)
	}
	s := New(st, testProxies, "сервер", nil)
	port := freePort(t)

	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	go func() {
		if err := Serve(ctx, port, s, selfSignedForTest(t)); err != nil {
			t.Errorf("подписка не поднялась: %v", err)
		}
	}()

	url := fmt.Sprintf("https://127.0.0.1:%d/sub/%s", port, token)
	client := &http.Client{
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	var resp *http.Response
	for range 50 {
		if resp, err = client.Get(url); err == nil {
			break
		}
		time.Sleep(40 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("по https не ответила: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("код %d", resp.StatusCode)
	}
	raw, err := base64.StdEncoding.DecodeString(string(body))
	if err != nil || !strings.Contains(string(raw), "hysteria2://") {
		t.Fatalf("тело не похоже на подписку: %q", body)
	}

	// The same port, asked in plain text, points at the https address instead
	// of failing with a TLS error the app cannot explain.
	plain := fmt.Sprintf("http://127.0.0.1:%d/sub/%s", port, token)
	resp, err = client.Get(plain)
	if err != nil {
		t.Fatalf("по http оборвалось: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusPermanentRedirect {
		t.Fatalf("http дал код %d, ждали 308", resp.StatusCode)
	}
	if got := resp.Header.Get("Location"); got != url {
		t.Fatalf("Location %q, ждали %q", got, url)
	}
}
