package hysteria

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"mor/internal/store"
)

func authTestServer(t *testing.T) (*httptest.Server, *store.Store, *store.User) {
	t.Helper()
	st, err := store.Open(t.TempDir() + "/users.json")
	if err != nil {
		t.Fatal(err)
	}
	u, err := st.Add(&store.User{Name: "phone", Proto: store.ProtoHy2, HyToken: "valid-token"})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(authHandler(st, nil, nil))
	t.Cleanup(srv.Close)
	return srv, st, u
}

func postAuth(t *testing.T, url, body string) string {
	t.Helper()
	resp, err := http.Post(url+"/auth", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	buf := make([]byte, 512)
	n, _ := resp.Body.Read(buf)
	return string(buf[:n])
}

func TestAuthAccepts(t *testing.T) {
	srv, _, u := authTestServer(t)

	got := postAuth(t, srv.URL, `{"addr":"198.51.100.1:5000","auth":"valid-token"}`)
	if !strings.Contains(got, `"ok":true`) || !strings.Contains(got, u.ID) {
		t.Fatalf("a valid token must be admitted: %s", got)
	}
}

func TestAuthRejectsUnknown(t *testing.T) {
	srv, _, _ := authTestServer(t)

	got := postAuth(t, srv.URL, `{"addr":"198.51.100.1:5000","auth":"wrong"}`)
	if !strings.Contains(got, `"ok":false`) {
		t.Fatalf("a foreign token must be rejected: %s", got)
	}
}

func TestAuthRejectsGarbage(t *testing.T) {
	srv, _, _ := authTestServer(t)

	got := postAuth(t, srv.URL, `{ not json`)
	if !strings.Contains(got, `"ok":false`) {
		t.Fatalf("garbage input must be rejected: %s", got)
	}
}

// A person who spent their traffic cap must be turned away even though the key
// itself is still valid.
func TestAuthRejectsSpent(t *testing.T) {
	st, err := store.Open(t.TempDir() + "/users.json")
	if err != nil {
		t.Fatal(err)
	}
	u, err := st.Add(&store.User{Name: "phone", Proto: store.ProtoHy2, HyToken: "valid-token"})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(authHandler(st, func(id string) bool { return id == u.ID }, nil))
	defer srv.Close()

	got := postAuth(t, srv.URL, `{"addr":"198.51.100.1:5000","auth":"valid-token"}`)
	if !strings.Contains(got, `"ok":false`) {
		t.Fatalf("исчерпавший лимит должен получить отказ: %s", got)
	}
}

func TestAuthRejectsDeleted(t *testing.T) {
	srv, st, u := authTestServer(t)

	if err := st.Delete(u.ID); err != nil {
		t.Fatal(err)
	}
	got := postAuth(t, srv.URL, `{"addr":"198.51.100.1:5000","auth":"valid-token"}`)
	if !strings.Contains(got, `"ok":false`) {
		t.Fatalf("a deleted key must be rejected: %s", got)
	}
}
