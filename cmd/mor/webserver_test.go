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
		WebCertFile:  dir + "/web.crt",
		WebKeyFile:   dir + "/web.key",
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
	for _, path := range []string{
		"/api/users", "/api/config", "/api/stats", "/api/audit", "/api/me",
		"/api/status", "/api/stats/history", "/api/protocols", "/api/online",
		"/api/disk", "/api/qr/whatever",
	} {
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

// The health route is deliberately the one thing outside a session: a check
// that needs credentials is a check nobody wires up. It must therefore say
// nothing beyond up or down.
func TestHealthNeedsNoSessionAndLeaksNothing(t *testing.T) {
	ws, _ := testPanel(t)
	t.Setenv("MOR_WEB_DEV_NOAUTH", "")

	w := httptest.NewRecorder()
	ws.mux().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if w.Code != http.StatusOK && w.Code != http.StatusServiceUnavailable {
		t.Fatalf("healthz без сессии = %d", w.Code)
	}

	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("не JSON: %s", w.Body.String())
	}
	if _, ok := got["ok"]; !ok {
		t.Fatalf("нет поля ok: %v", got)
	}
	if len(got) != 1 {
		t.Fatalf("отдаёт лишнее без авторизации: %v", got)
	}
}

// Banning is what cuts somebody off, and unbanning is what lets them back. A
// silent no-op here would look identical to success in the panel.
func TestBanAndUnbanRoundTrip(t *testing.T) {
	ws, _ := testPanel(t)
	id := createTestKey(t, ws, "телефон")

	if w := do(t, ws, http.MethodPost, "/api/users/"+id+"/ban", `{"banned":true}`); w.Code != http.StatusOK {
		t.Fatalf("бан = %d: %s", w.Code, w.Body.String())
	}
	if !userField(t, ws, id, "banned").(bool) {
		t.Fatal("ключ не забанен")
	}

	if w := do(t, ws, http.MethodPost, "/api/users/"+id+"/ban", `{"banned":false}`); w.Code != http.StatusOK {
		t.Fatalf("разбан = %d", w.Code)
	}
	if userField(t, ws, id, "banned").(bool) {
		t.Fatal("ключ остался забаненным")
	}
}

func TestResetTrafficZeroesTheCounter(t *testing.T) {
	ws, st := testPanel(t)
	id := createTestKey(t, ws, "телефон")

	for _, u := range st.List() {
		ws.e.stats.Add(u.ID, 5<<30, time.Now())
	}
	if got := userField(t, ws, id, "traffic").(float64); got == 0 {
		t.Fatal("тест не смог начислить трафик")
	}

	if w := do(t, ws, http.MethodPost, "/api/users/"+id+"/reset", ""); w.Code != http.StatusOK {
		t.Fatalf("сброс = %d: %s", w.Code, w.Body.String())
	}
	if got := userField(t, ws, id, "traffic").(float64); got != 0 {
		t.Fatalf("после сброса трафик %v", got)
	}
}

func TestDeleteRemovesTheKey(t *testing.T) {
	ws, _ := testPanel(t)
	id := createTestKey(t, ws, "телефон")

	if w := do(t, ws, http.MethodDelete, "/api/users/"+id, ""); w.Code != http.StatusOK {
		t.Fatalf("удаление = %d: %s", w.Code, w.Body.String())
	}
	if w := do(t, ws, http.MethodGet, "/api/users/"+id, ""); w.Code != http.StatusNotFound {
		t.Fatalf("удалённый ключ всё ещё отвечает: %d", w.Code)
	}
}

// A protocol toggled off must come back as off, and keys must survive it —
// switching a protocol is not a way to lose everybody's access.
func TestProtocolToggleSticksAndKeepsKeys(t *testing.T) {
	ws, _ := testPanel(t)
	id := createTestKey(t, ws, "телефон")

	if w := do(t, ws, http.MethodPost, "/api/protocols/ss/toggle", `{"on":false}`); w.Code != http.StatusOK {
		t.Fatalf("выключение = %d: %s", w.Code, w.Body.String())
	}
	if protoOn(t, ws, "ss") {
		t.Fatal("протокол остался включённым")
	}
	if w := do(t, ws, http.MethodGet, "/api/users/"+id, ""); w.Code != http.StatusOK {
		t.Fatal("ключ пропал после выключения протокола")
	}

	if w := do(t, ws, http.MethodPost, "/api/protocols/ss/toggle", `{"on":true}`); w.Code != http.StatusOK {
		t.Fatalf("включение = %d", w.Code)
	}
	if !protoOn(t, ws, "ss") {
		t.Fatal("протокол не включился обратно")
	}
}

func TestProtocolToggleRejectsUnknown(t *testing.T) {
	ws, _ := testPanel(t)
	if w := do(t, ws, http.MethodPost, "/api/protocols/выдумка/toggle", `{"on":false}`); w.Code != http.StatusBadRequest {
		t.Fatalf("несуществующий протокол = %d, ждали 400", w.Code)
	}
}

// Restart drives systemd, which is absent in a test. It must answer honestly
// rather than hang or panic.
func TestRestartAnswersWithoutSystemd(t *testing.T) {
	ws, _ := testPanel(t)
	w := do(t, ws, http.MethodPost, "/api/restart", "")
	if w.Code != http.StatusOK {
		t.Fatalf("перезапуск = %d: %s", w.Code, w.Body.String())
	}
	var got struct {
		Restarted []string `json:"restarted"`
		Failed    []string `json:"failed"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("не JSON: %s", w.Body.String())
	}
}

func TestPortPickRejectsUnknownProtocol(t *testing.T) {
	ws, _ := testPanel(t)
	if w := do(t, ws, http.MethodPost, "/api/ports/выдумка/pick", ""); w.Code != http.StatusBadRequest {
		t.Fatalf("несуществующий протокол = %d, ждали 400", w.Code)
	}
}

// createTestKey makes one key and hands back its group id.
func createTestKey(t *testing.T, ws *webServer, name string) string {
	t.Helper()
	w := do(t, ws, http.MethodPost, "/api/users", `{"name":"`+name+`","protocols":["hy2","ss"]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("создание = %d: %s", w.Code, w.Body.String())
	}
	var made map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &made); err != nil {
		t.Fatal(err)
	}
	id, _ := made["id"].(string)
	if id == "" {
		t.Fatalf("создание не вернуло id: %s", w.Body.String())
	}
	return id
}

func userField(t *testing.T, ws *webServer, id, field string) any {
	t.Helper()
	w := do(t, ws, http.MethodGet, "/api/users/"+id, "")
	if w.Code != http.StatusOK {
		t.Fatalf("чтение ключа = %d", w.Code)
	}
	var u map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &u); err != nil {
		t.Fatal(err)
	}
	return u[field]
}

func protoOn(t *testing.T, ws *webServer, id string) bool {
	t.Helper()
	w := do(t, ws, http.MethodGet, "/api/protocols", "")
	var rows []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &rows); err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r["id"] == id {
			return r["on"].(bool)
		}
	}
	t.Fatalf("протокол %s не найден", id)
	return false
}
