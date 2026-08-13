package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"mor/internal/auditlog"
	"mor/internal/config"
	"mor/internal/hysteria"
	"mor/internal/stats"
	"mor/internal/store"
	"mor/internal/webauth"
	"mor/internal/xray"
)

// testPanel wires a real router over throwaway files. Handlers are where the
// panel meets the outside world, and a route that is registered wrong or a
// handler that dereferences a nil compiles perfectly — only a request finds it.
func testPanel(t *testing.T) (*webServer, *store.Store) {
	t.Helper()
	dir := t.TempDir()
	paths := config.Paths{
		BaseDir:      dir,
		ConfigFile:   dir + "/config.json",
		DataFile:     dir + "/users.json",
		StatsFile:    dir + "/stats.json",
		HistoryFile:  dir + "/history.json",
		AuditLogFile: dir + "/audit.json",
		SysHistFile:  dir + "/syshist.json",
	}
	cfg := config.NewDefault()
	cfg.PublicHost = "203.0.113.7"
	cfg.SetPath(paths.ConfigFile)
	cfg.EnsureDefaults()
	cfg.WebPasswordHash = webauth.HashPassword("правильный-пароль")

	st, err := store.Open(paths.DataFile)
	if err != nil {
		t.Fatal(err)
	}
	st2, err := stats.Open(paths.StatsFile)
	if err != nil {
		t.Fatal(err)
	}
	hist, err := stats.OpenHistory(paths.HistoryFile)
	if err != nil {
		t.Fatal(err)
	}
	audit, err := auditlog.Open(paths.AuditLogFile)
	if err != nil {
		t.Fatal(err)
	}
	// The engines are part of a real env: handlers that apply a change reach
	// for them, and a half-built fixture would only prove the handler runs
	// when nothing asks it to do any work.
	paths.HyConfig = dir + "/hy.yaml"
	paths.HyCertFile = dir + "/hy.crt"
	paths.HyKeyFile = dir + "/hy.key"
	paths.XrayConfig = dir + "/xray.json"
	e := &env{
		cfg: cfg, st: st, stats: st2, hist: hist, audit: audit, paths: paths,
		hy: hysteria.New(cfg, paths), xr: xray.New(cfg, paths),
	}
	ws := &webServer{
		e:        e,
		sessions: webauth.NewSessions(time.Hour),
		sysHist:  openSysHistory(paths.SysHistFile),
		throttle: newLoginThrottle(),
	}
	return ws, st
}

func (ws *webServer) mux() *http.ServeMux {
	m := http.NewServeMux()
	ws.routes(m)
	return m
}

// do issues an authenticated request the way the browser does.
func do(t *testing.T, ws *webServer, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	}
	r.AddCookie(&http.Cookie{Name: webSessionCookie, Value: ws.sessions.Issue()})
	w := httptest.NewRecorder()
	ws.mux().ServeHTTP(w, r)
	return w
}

// Every route has to answer. A typo in a pattern is invisible until something
// asks for it, and by then it is in a release.
func TestEveryReadRouteAnswers(t *testing.T) {
	ws, _ := testPanel(t)
	for _, path := range []string{
		"/api/me", "/api/stats", "/api/status", "/api/stats/history",
		"/api/users", "/api/protocols", "/api/config", "/api/audit", "/api/online",
	} {
		if w := do(t, ws, http.MethodGet, path, ""); w.Code != http.StatusOK {
			t.Errorf("GET %s = %d, тело: %s", path, w.Code, w.Body.String())
		}
	}
}

// Without a session nothing but the login door may open.
func TestAuthGuardsEveryApiRoute(t *testing.T) {
	ws, _ := testPanel(t)
	t.Setenv("MOR_WEB_DEV_NOAUTH", "")
	for _, path := range []string{"/api/users", "/api/config", "/api/stats", "/api/audit"} {
		w := httptest.NewRecorder()
		ws.mux().ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusUnauthorized {
			t.Errorf("GET %s без сессии = %d, ждали 401", path, w.Code)
		}
	}
}

// Creating a key through the panel has to produce something usable: a link,
// and a record the next request can find by the id that was just returned.
func TestCreateKeyReturnsUsableKey(t *testing.T) {
	ws, _ := testPanel(t)
	w := do(t, ws, http.MethodPost, "/api/users", `{"name":"телефон","protocols":["hy2"]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("создание = %d: %s", w.Code, w.Body.String())
	}
	var created webUser
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.ID == "" || created.Name != "телефон" {
		t.Fatalf("вернулся пустой ключ: %+v", created)
	}
	if created.Link == "" && created.SubLink == "" {
		t.Error("ключ без ссылки — его нечего передать человеку")
	}
	if w := do(t, ws, http.MethodGet, "/api/users/"+created.ID, ""); w.Code != http.StatusOK {
		t.Errorf("ключ не находится по своему же id: %d", w.Code)
	}
}

// Editing must change the record and report the state that was just written,
// not the one it replaced.
func TestEditKeyReportsFreshState(t *testing.T) {
	ws, _ := testPanel(t)
	w := do(t, ws, http.MethodPost, "/api/users", `{"name":"старое","protocols":["hy2"]}`)
	var created webUser
	_ = json.Unmarshal(w.Body.Bytes(), &created)

	w = do(t, ws, http.MethodPatch, "/api/users/"+created.ID, `{"name":"новое","traffic":"50gb"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("правка = %d: %s", w.Code, w.Body.String())
	}
	var edited webUser
	_ = json.Unmarshal(w.Body.Bytes(), &edited)
	if edited.Name != "новое" {
		t.Errorf("ответ вернул имя %q вместо только что записанного", edited.Name)
	}
	if edited.Limit == 0 {
		t.Error("лимит не применился")
	}

	// An empty string is how the panel says "no limit" — it has to stay
	// distinguishable from "leave it alone".
	w = do(t, ws, http.MethodPatch, "/api/users/"+created.ID, `{"traffic":""}`)
	// A fresh value: the field is omitempty, so a zero limit is absent from
	// the JSON and would leave a reused struct holding the old number.
	var cleared webUser
	_ = json.Unmarshal(w.Body.Bytes(), &cleared)
	if cleared.Limit != 0 {
		t.Errorf("пустое поле не сняло лимит: %d", cleared.Limit)
	}
}

func TestEditRejectsEmptyName(t *testing.T) {
	ws, _ := testPanel(t)
	w := do(t, ws, http.MethodPost, "/api/users", `{"name":"имя","protocols":["hy2"]}`)
	var created webUser
	_ = json.Unmarshal(w.Body.Bytes(), &created)

	if w := do(t, ws, http.MethodPatch, "/api/users/"+created.ID, `{"name":"  "}`); w.Code != http.StatusBadRequest {
		t.Errorf("пустое имя принято: %d", w.Code)
	}
}

func TestUnknownKeyIs404(t *testing.T) {
	ws, _ := testPanel(t)
	for _, c := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/users/нет-такого", ""},
		{http.MethodPatch, "/api/users/нет-такого", `{"name":"x"}`},
		{http.MethodDelete, "/api/users/нет-такого", ""},
		{http.MethodGet, "/api/qr/нет-такого", ""},
	} {
		if w := do(t, ws, c.method, c.path, c.body); w.Code != http.StatusNotFound {
			t.Errorf("%s %s = %d, ждали 404", c.method, c.path, w.Code)
		}
	}
}

// A bad port must be refused before it reaches the config: saving it would
// leave an engine unable to start with nothing explaining why.
func TestConfigRejectsImpossiblePort(t *testing.T) {
	ws, _ := testPanel(t)
	if w := do(t, ws, http.MethodPut, "/api/config", `{"vpnPort":70000}`); w.Code != http.StatusBadRequest {
		t.Errorf("порт 70000 принят: %d", w.Code)
	}
	if ws.e.cfg.VPNPort == 70000 {
		t.Error("невалидный порт всё же записан в конфиг")
	}
}

// Changing the password must require the current one: a stolen open session
// should not be enough to lock the owner out of their own server.
func TestPasswordChangeNeedsCurrent(t *testing.T) {
	ws, _ := testPanel(t)
	if w := do(t, ws, http.MethodPost, "/api/password", `{"current":"не тот","new":"достаточно-длинный"}`); w.Code != http.StatusUnauthorized {
		t.Errorf("пароль сменили без текущего: %d", w.Code)
	}
	if w := do(t, ws, http.MethodPost, "/api/password", `{"current":"правильный-пароль","new":"корот"}`); w.Code != http.StatusBadRequest {
		t.Errorf("слишком короткий пароль принят: %d", w.Code)
	}
	w := do(t, ws, http.MethodPost, "/api/password", `{"current":"правильный-пароль","new":"новый-длинный-пароль"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("смена пароля = %d: %s", w.Code, w.Body.String())
	}
	if !webauth.VerifyPassword(ws.e.cfg.WebPasswordHash, "новый-длинный-пароль") {
		t.Error("новый пароль не сохранился")
	}
}

// The login door is the one thing exposed without a session, so its throttle
// has to hold at the HTTP layer and not only in the counter underneath.
func TestLoginThrottleReturns429(t *testing.T) {
	ws, _ := testPanel(t)
	t.Setenv("MOR_WEB_DEV_NOAUTH", "")
	post := func() int {
		r := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(`{"password":"мимо"}`))
		r.RemoteAddr = "198.51.100.4:5000"
		w := httptest.NewRecorder()
		ws.mux().ServeHTTP(w, r)
		return w.Code
	}
	for i := 0; i < throttleFree; i++ {
		if code := post(); code != http.StatusUnauthorized {
			t.Fatalf("попытка %d = %d, ждали 401", i+1, code)
		}
	}
	if code := post(); code != http.StatusTooManyRequests {
		t.Errorf("после %d неудач пускает дальше: %d", throttleFree, code)
	}
}

// Presence must never carry an address. This is the one guarantee the product
// exists for, so it is asserted on the wire, not only in the code that builds it.
func TestOnlineNeverLeaksAddresses(t *testing.T) {
	ws, st := testPanel(t)
	if _, err := st.Add(&store.User{Name: "телефон", Proto: store.ProtoHy2, HyToken: "t"}); err != nil {
		t.Fatal(err)
	}
	body := do(t, ws, http.MethodGet, "/api/online", "").Body.String()
	for _, forbidden := range []string{"addr", "ip", "remote", "198.", "10.", "192.168"} {
		if strings.Contains(strings.ToLower(body), forbidden) {
			t.Errorf("в ответе о присутствии есть %q — адреса не должны покидать сервер: %s", forbidden, body)
		}
	}
}
