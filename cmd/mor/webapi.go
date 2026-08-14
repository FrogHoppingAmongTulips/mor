package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	qrcode "github.com/skip2/go-qrcode"

	"mor/internal/store"
	"mor/internal/sysinfo"
	"mor/internal/webauth"
)

//go:embed web/panel.html
var webFS embed.FS

const webSessionCookie = "mor_session"
const webSessionTTL = 7 * 24 * time.Hour

type webServer struct {
	e        *env
	sessions *webauth.Sessions
	tokens   *webauth.Tokens
	sysHist  *sysHistory
	throttle *loginThrottle

	updMu      sync.Mutex
	updChecked time.Time
	updTag     string
}

// checkUpdate asks GitHub for the latest tag at most once an hour — the home
// screen refreshes every 15s, and a version check does not need to run on
// every one of those.
func (ws *webServer) checkUpdate() string {
	ws.updMu.Lock()
	defer ws.updMu.Unlock()
	if time.Since(ws.updChecked) < time.Hour {
		return ws.updTag
	}
	ws.updChecked = time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tag, err := latest(ctx)
	if err == nil && newerThanRunning(tag) {
		ws.updTag = tag
	} else {
		ws.updTag = ""
	}
	return ws.updTag
}

// startWebPanel serves the JSON API and the panel HTML. It only comes up once
// a password is set — WebOn refuses otherwise, the same way sub.Serve refuses
// with no port.
func startWebPanel(ctx context.Context, e *env) {
	if !e.cfg.WebOn() {
		return
	}
	ws := &webServer{e: e, sessions: webauth.OpenSessions(webSessionTTL, e.paths.SessionsFile), tokens: webauth.OpenTokens(e.paths.TokensFile), sysHist: openSysHistory(e.paths.SysHistFile), throttle: newLoginThrottle()}
	go ws.sysHist.run(ctx)
	mux := http.NewServeMux()
	ws.routes(mux)
	// The policy names the hashes of the page's own two inline blocks, so it is
	// derived from the file that will be served rather than written by hand.
	page, err := webFS.ReadFile("web/panel.html")
	if err != nil {
		log.Printf("предупреждение: panel.html не читается: %v", err)
		return
	}
	csp := contentSecurityPolicy(page)
	srv := &http.Server{Addr: fmt.Sprintf(":%d", e.cfg.WebPort), Handler: withSecurityHeaders(mux, csp), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		// Flush the expiries that Valid slid forward without writing, so a
		// planned restart does not shorten anybody's session.
		ws.sessions.Save()
		shutdown, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()

	// The panel carries the one password to this server and every key it hands
	// out, so it is never served in the clear: a real certificate when there is
	// one, mor's own otherwise.
	tc, err := tlsConfig(e)
	if err != nil {
		log.Printf("предупреждение: TLS для панели не поднялся: %v", err)
		return
	}
	srv.TLSConfig = tc

	raw, err := net.Listen("tcp", srv.Addr)
	if err != nil {
		log.Printf("предупреждение: веб-панель на :%d не поднялась: %v", e.cfg.WebPort, err)
		return
	}
	// Same port answers both: TLS as normal, and plain HTTP with a redirect to
	// the https address instead of a protocol error nobody can act on.
	ln := newRedirectingListener(raw, e.cfg.WebPort)
	log.Printf("веб-панель на https://%s:%d (%s)", e.cfg.PublicHost, e.cfg.WebPort, certSummary(e.paths.WebCertFile))
	if err := srv.ServeTLS(ln, "", ""); err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
		log.Printf("предупреждение: веб-панель на :%d не поднялась: %v", e.cfg.WebPort, err)
	}
}

func (ws *webServer) routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /", ws.handleIndex)
	mux.HandleFunc("GET /healthz", ws.handleHealth)
	mux.HandleFunc("POST /api/login", ws.handleLogin)
	mux.HandleFunc("POST /api/logout", ws.handleLogout)
	mux.HandleFunc("GET /api/me", ws.auth(ws.handleMe))
	mux.HandleFunc("GET /api/stats", ws.auth(ws.handleStats))
	mux.HandleFunc("GET /api/status", ws.auth(ws.handleStatus))
	mux.HandleFunc("GET /api/stats/history", ws.auth(ws.handleStatsHistory))
	mux.HandleFunc("GET /api/users", ws.auth(ws.handleUsersList))
	mux.HandleFunc("POST /api/users", ws.auth(ws.handleUsersCreate))
	mux.HandleFunc("GET /api/users/{id}", ws.auth(ws.handleUserGet))
	mux.HandleFunc("DELETE /api/users/{id}", ws.auth(ws.handleUserDelete))
	mux.HandleFunc("PATCH /api/users/{id}", ws.auth(ws.handleUserEdit))
	mux.HandleFunc("POST /api/users/{id}/ban", ws.auth(ws.handleUserBan))
	mux.HandleFunc("POST /api/users/{id}/reset", ws.auth(ws.handleUserReset))
	mux.HandleFunc("POST /api/users/{id}/devices/reset", ws.auth(ws.handleUserDevicesReset))
	mux.HandleFunc("GET /api/protocols", ws.auth(ws.handleProtocolsList))
	mux.HandleFunc("POST /api/protocols/{id}/toggle", ws.auth(ws.handleProtocolToggle))
	mux.HandleFunc("GET /api/config", ws.auth(ws.handleConfigGet))
	mux.HandleFunc("GET /api/audit", ws.auth(ws.handleAudit))
	mux.HandleFunc("GET /api/disk", ws.auth(ws.handleDisk))
	mux.HandleFunc("GET /api/qr/{id}", ws.auth(ws.handleQR))
	mux.HandleFunc("GET /api/online", ws.auth(ws.handleOnline))
	mux.HandleFunc("POST /api/restart", ws.auth(ws.handleRestart))
	mux.HandleFunc("PUT /api/config", ws.auth(ws.handleConfigSave))
	mux.HandleFunc("POST /api/password", ws.auth(ws.handlePassword))
	mux.HandleFunc("POST /api/ports/{proto}/pick", ws.auth(ws.handlePortPick))
}

// handleHealth is for something outside to watch: a monitor, a uptime checker,
// a cron on another box. It is the only route without a session, because a
// health check that needs credentials is a health check nobody sets up — and
// it says nothing a stranger could use, only whether the engines are up.
func (ws *webServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	ok := true
	for _, row := range localCheck(ws.e) {
		if !row.engine || !row.held {
			ok = false
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if !ok {
		w.WriteHeader(http.StatusServiceUnavailable)
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": ok})
}

func (ws *webServer) handleIndex(w http.ResponseWriter, r *http.Request) {
	b, err := webFS.ReadFile("web/panel.html")
	if err != nil {
		http.Error(w, "panel.html missing", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(b)
}

// auth wraps a handler so it 401s without a live session, instead of every
// handler remembering to check.
func (ws *webServer) auth(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// MOR_WEB_DEV_NOAUTH is for a local preview build only — set by the
		// developer's own run script, never something to turn on facing the
		// internet. The real server always requires the session cookie.
		if os.Getenv("MOR_WEB_DEV_NOAUTH") == "1" {
			h(w, r)
			return
		}
		// A browser brings a session cookie; a script brings a token. Both are
		// the owner, so both open the same doors — there is one account here.
		if c, err := r.Cookie(webSessionCookie); err == nil && ws.sessions.Valid(c.Value) {
			h(w, r)
			return
		}
		if tok, ok := bearer(r); ok && ws.tokens.Valid(tok) {
			h(w, r)
			return
		}
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}
}

// bearer pulls the token out of the Authorization header.
func bearer(r *http.Request) (string, bool) {
	h := r.Header.Get("Authorization")
	const p = "Bearer "
	if len(h) <= len(p) || !strings.EqualFold(h[:len(p)], p) {
		return "", false
	}
	return strings.TrimSpace(h[len(p):]), true
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func readJSON(r *http.Request, v any) error {
	defer r.Body.Close()
	b, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

// auth handlers

func (ws *webServer) handleLogin(w http.ResponseWriter, r *http.Request) {
	who := clientIP(r)
	if wait := ws.throttle.retryAfter(who); wait > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
		http.Error(w, "too many attempts", http.StatusTooManyRequests)
		return
	}
	var req struct{ Password string }
	if err := readJSON(r, &req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if !webauth.VerifyPassword(ws.e.cfg.WebPasswordHash, req.Password) {
		ws.throttle.fail(who)
		ws.e.audit.Add("неудачный вход", who, time.Now())
		http.Error(w, "wrong password", http.StatusUnauthorized)
		return
	}
	ws.throttle.reset(who)
	tok := ws.sessions.Issue()
	http.SetCookie(w, &http.Cookie{
		Name: webSessionCookie, Value: tok, Path: "/", HttpOnly: true,
		SameSite: http.SameSiteLaxMode, MaxAge: int(webSessionTTL.Seconds()),
	})
	writeJSON(w, map[string]bool{"ok": true})
}

func (ws *webServer) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(webSessionCookie); err == nil {
		ws.sessions.Revoke(c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: webSessionCookie, Value: "", Path: "/", MaxAge: -1})
	writeJSON(w, map[string]bool{"ok": true})
}

func (ws *webServer) handleMe(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]bool{"ok": true})
}

// stats

func (ws *webServer) handleStats(w http.ResponseWriter, r *http.Request) {
	s := sysinfo.Read()
	writeJSON(w, map[string]any{
		"sys": map[string]any{
			"cpuPercent": s.CPUPercent,
			"memUsed":    s.MemUsed,
			"memTotal":   s.MemTotal,
			"diskUsed":   s.DiskUsed,
			"diskTotal":  s.DiskTotal,
		},
		"monthTraffic":    ws.monthTraffic(),
		"updateAvailable": ws.checkUpdate(),
		"version":         version,
		"certSelfSigned":  certIsSelfSigned(ws.e.paths.WebCertFile),
		"cert":            certSummary(ws.e.paths.WebCertFile),
	})
}

// monthTraffic sums this calendar month's usage across every key — one
// number for "how much has the server moved lately", instead of adding up
// each person's card by eye.
func (ws *webServer) monthTraffic() uint64 {
	month := time.Now().Format("2006-01")
	var total uint64
	for _, u := range ws.e.st.List() {
		total += ws.e.stats.Get(u.ID).Months[month]
	}
	return total
}

// handleStatus is the home screen's "is anything actually wrong" check — the
// same local-only, instant checks "mor check --fast" runs in the terminal.
// No external probing here: that takes up to a minute, and the panel polls
// this every 15s.
func (ws *webServer) handleStatus(w http.ResponseWriter, r *http.Request) {
	ok := true
	for _, row := range localCheck(ws.e) {
		if row.broken() {
			ok = false
		}
	}
	// Both forms go out: the code so the panel can say it in the reader's
	// language, and the Russian text as the fallback for anything the panel
	// has no wording for yet.
	problems := []map[string]string{}
	for _, p := range localProblems(ws.e) {
		problems = append(problems, map[string]string{"code": p.code, "arg": p.arg, "text": p.text})
		ok = false
	}
	writeJSON(w, map[string]any{"ok": ok, "problems": problems})
}

func (ws *webServer) handleStatsHistory(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, ws.sysHist.all())
}

// users

type webUser struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Protocols []string   `json:"protocols"`
	Created   time.Time  `json:"created"`
	LastSeen  *time.Time `json:"lastSeen,omitempty"`
	Traffic   uint64     `json:"traffic"`
	Limit     uint64     `json:"limit,omitempty"`
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
	Banned    bool       `json:"banned"`
	IPLimit   int        `json:"ipLimit,omitempty"`
	Devices   int        `json:"devices,omitempty"`
	AutoReset bool       `json:"autoReset"`
	Spark     []uint64   `json:"spark"`
	Months    []webMonth `json:"months,omitempty"`
	SubLink   string     `json:"subLink,omitempty"`
	Link      string     `json:"link,omitempty"`
	// Links is one direct link per protocol, for handing out a single
	// protocol instead of the subscription that carries all of them.
	Links map[string]string `json:"links,omitempty"`
}

// sparkHours is how far back a key's row-level traffic sparkline reaches. A
// day is long enough to show a usage rhythm and short enough that the hourly
// buckets History keeps are all still there.
const sparkHours = 24

// sparkline turns History's sparse buckets into the dense, fixed-length series
// a chart needs — missing hours are real zeros, not gaps to be skipped.
func sparkline(e *env, g []*store.User) []uint64 {
	// The window ends with the hour in progress rather than after it: that
	// bucket is the one the operator actually cares about, and starting a full
	// sparkHours back would push it one slot past the end of the series.
	now := time.Now().Truncate(time.Hour)
	from := now.Add(-(sparkHours - 1) * time.Hour)
	out := make([]uint64, sparkHours)
	for _, p := range e.hist.Series(ids(g), from, now.Add(time.Hour), time.Hour) {
		if i := int(p.At.Sub(from) / time.Hour); i >= 0 && i < sparkHours {
			out[i] = p.Bytes
		}
	}
	return out
}

type webMonth struct {
	Month string `json:"month"`
	Bytes uint64 `json:"bytes"`
}

// groupID is the stable handle a group is addressed by: the shared
// subscription token when one exists, otherwise the lone key's own ID.
func groupID(g []*store.User) string {
	if g[0].Sub != "" {
		return g[0].Sub
	}
	return g[0].ID
}

func findGroup(e *env, id string) ([]*store.User, bool) {
	for _, g := range groupKeys(e.st.List()) {
		if groupID(g) == id {
			return g, true
		}
	}
	return nil, false
}

func toWebUser(e *env, g []*store.User, detail bool) webUser {
	q := quotaOf(e, g)
	entry := e.stats.Sum(ids(g))
	protos := make([]string, 0, len(g))
	for _, u := range g {
		protos = append(protos, u.Proto)
	}
	u := webUser{
		ID: groupID(g), Name: g[0].Name, Protocols: protos,
		Created: g[0].CreatedAt, Traffic: q.used, Limit: q.limit,
		Banned: g[0].Banned, IPLimit: g[0].IPLimit, AutoReset: g[0].AutoReset,
		Spark: sparkline(e, g),
	}
	if g[0].IPLimit > 0 && g[0].Sub != "" {
		u.Devices = e.devices.Count(g[0].Sub)
	}
	if !entry.LastSeen.IsZero() {
		u.LastSeen = &entry.LastSeen
	}
	if !g[0].ExpiresAt.IsZero() {
		u.ExpiresAt = &g[0].ExpiresAt
	}
	if detail {
		months := entry.MonthsSorted()
		out := make([]webMonth, len(months))
		for i, m := range months {
			out[len(months)-1-i] = webMonth{Month: m.Month, Bytes: m.Bytes}
		}
		u.Months = out
		u.SubLink = subURL(e, g[0])
		u.Link = keyText(e.cfg, g[0])
		u.Links = make(map[string]string, len(g))
		for _, k := range g {
			if text := keyText(e.cfg, k); text != "" {
				u.Links[k.Proto] = text
			}
		}
	}
	return u
}

func (ws *webServer) handleUsersList(w http.ResponseWriter, r *http.Request) {
	groups := groupKeys(ws.e.st.List())
	out := make([]webUser, len(groups))
	for i, g := range groups {
		out[i] = toWebUser(ws.e, g, false)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.After(out[j].Created) })
	writeJSON(w, out)
}

func (ws *webServer) handleUserGet(w http.ResponseWriter, r *http.Request) {
	g, ok := findGroup(ws.e, r.PathValue("id"))
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	writeJSON(w, toWebUser(ws.e, g, true))
}

func (ws *webServer) handleUsersCreate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name      string
		Protocols []string
		Time      string
		Traffic   string
	}
	if err := readJSON(r, &req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	name := req.Name
	if name == "" {
		http.Error(w, "имя обязательно", http.StatusBadRequest)
		return
	}
	made, err := createAccessFor(ws.e, name, "", req.Protocols)
	if len(made) == 0 {
		http.Error(w, errText(err), http.StatusBadRequest)
		return
	}
	if req.Time != "" || req.Traffic != "" {
		until, err := parsePeriodField(req.Time, time.Now())
		if err != nil {
			http.Error(w, "срок: "+err.Error(), http.StatusBadRequest)
			return
		}
		bytes, err := parseTrafficField(req.Traffic)
		if err != nil {
			http.Error(w, "лимит трафика: "+err.Error(), http.StatusBadRequest)
			return
		}
		for _, u := range made {
			_ = ws.e.st.SetExpiry(u.ID, until)
			_ = ws.e.st.SetLimit(u.ID, bytes)
		}
		_ = applyLive(ws.e, made)
	}
	g, _ := findGroup(ws.e, groupIDFromMade(made))
	ws.e.audit.Add("создан ключ", name, time.Now())
	writeJSON(w, toWebUser(ws.e, g, true))
}

// groupIDFromMade re-derives the group ID for keys just created together —
// they all share the same Sub token, set by createAccessFor.
func groupIDFromMade(made []*store.User) string {
	if len(made) == 0 {
		return ""
	}
	return groupID(made)
}

func (ws *webServer) handleUserDelete(w http.ResponseWriter, r *http.Request) {
	g, ok := findGroup(ws.e, r.PathValue("id"))
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	name := g[0].Name
	// The key is gone from the store the moment removeKeys touches it; an
	// engine that did not answer is a warning, not a failure. Reporting 500
	// here told the panel the deletion failed while the key was already
	// deleted — and the collect loop reconciles the engines within the minute
	// anyway. Ban and edit have always behaved this way.
	if err := removeKeys(ws.e, g); err != nil {
		if _, still := findGroup(ws.e, r.PathValue("id")); still {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		log.Printf("предупреждение: «%s» удалён, движок не отозвался: %v", name, err)
	}
	ws.e.audit.Add("удалён ключ", name, time.Now())
	writeJSON(w, map[string]bool{"ok": true})
}

func (ws *webServer) handleUserBan(w http.ResponseWriter, r *http.Request) {
	g, ok := findGroup(ws.e, r.PathValue("id"))
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	var req struct{ Banned bool }
	if err := readJSON(r, &req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	for _, u := range g {
		if err := ws.e.st.SetBanned(u.ID, req.Banned); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	if err := applyLive(ws.e, g); err != nil {
		log.Printf("предупреждение: применение бана: %v", err)
	}
	action := "разбанен"
	if req.Banned {
		action = "забанен"
	}
	ws.e.audit.Add(action, g[0].Name, time.Now())
	writeJSON(w, map[string]bool{"ok": true})
}

func (ws *webServer) handleUserReset(w http.ResponseWriter, r *http.Request) {
	g, ok := findGroup(ws.e, r.PathValue("id"))
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	for _, u := range g {
		if err := ws.e.stats.Reset(u.ID); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	ws.e.audit.Add("сброшен трафик", g[0].Name, time.Now())
	writeJSON(w, map[string]bool{"ok": true})
}

// handleUserDevicesReset hands the device slots back — somebody changed their
// phone, and the old one would otherwise hold its place for a month.
func (ws *webServer) handleUserDevicesReset(w http.ResponseWriter, r *http.Request) {
	g, ok := findGroup(ws.e, r.PathValue("id"))
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	for _, u := range g {
		if u.Sub != "" {
			ws.e.devices.Forget(u.Sub)
		}
	}
	ws.e.audit.Add("сброшены устройства", g[0].Name, time.Now())
	writeJSON(w, map[string]bool{"ok": true})
}

// protocols

func (ws *webServer) handleProtocolsList(w http.ResponseWriter, r *http.Request) {
	out := make([]map[string]any, 0, len(portRows(ws.e)))
	for _, row := range portRows(ws.e) {
		out = append(out, map[string]any{
			"id": row.proto, "on": ws.e.cfg.On(row.proto), "port": row.port, "kind": row.kind,
		})
	}
	writeJSON(w, out)
}

func (ws *webServer) handleProtocolToggle(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	valid := false
	for _, p := range baseProtocols {
		valid = valid || p == id
	}
	if !valid {
		http.Error(w, "unknown protocol", http.StatusBadRequest)
		return
	}
	var req struct{ On bool }
	if err := readJSON(r, &req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	ws.e.cfg.SetOn(id, req.On)
	if err := ws.e.cfg.Save(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	action := "протокол выключен"
	if req.On {
		action = "протокол включён"
	}
	ws.e.audit.Add(action, protoDisplayNames[id], time.Now())

	// The setting is already on disk by this point, so a sulking engine is a
	// warning, not a failure: reporting it as an error would leave the panel
	// showing a switch position that contradicts the saved config.
	if err := applyProtos(ws.e, id); err != nil {
		writeJSON(w, map[string]any{"ok": true, "warning": "сохранено, но движок не отозвался"})
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

var protoDisplayNames = map[string]string{
	store.ProtoHy2:     "Hysteria2",
	store.ProtoReality: "VLESS+Reality",
	store.ProtoEnc:     "VLESS Encryption",
	store.ProtoSS:      "Shadowsocks",
}

// config

func (ws *webServer) handleConfigGet(w http.ResponseWriter, r *http.Request) {
	c := ws.e.cfg
	writeJSON(w, map[string]any{
		"dns": c.DNS, "sni": c.SNI, "host": c.PublicHost, "hyObfs": c.HyObfs,
		"vpnPort": c.VPNPort, "realityPort": c.Reality.Port, "realityDest": c.Reality.Dest,
		"encPort": c.Enc.Port, "ssPort": c.SS.Port,
		"subPort": c.SubPort, "subOff": c.SubOff, "webPort": c.WebPort,
	})
}

// audit — admin actions (ban, reset, create, delete, protocol toggle), as
// the only log the panel keeps: mor deliberately records nothing about who
// connected from where.
func (ws *webServer) handleAudit(w http.ResponseWriter, r *http.Request) {
	events := ws.e.audit.Recent(100)
	out := make([]map[string]any, len(events))
	for i, ev := range events {
		out[i] = map[string]any{"action": ev.Action, "target": ev.Target, "at": ev.At}
	}
	writeJSON(w, out)
}

// qr

func (ws *webServer) handleQR(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSuffix(r.PathValue("id"), ".png")
	g, ok := findGroup(ws.e, id)
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	text := ""
	if proto := r.URL.Query().Get("proto"); proto != "" {
		for _, k := range g {
			if k.Proto == proto {
				text = keyText(ws.e.cfg, k)
			}
		}
		if text == "" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
	} else {
		text = subURL(ws.e, g[0])
		if text == "" {
			text = keyText(ws.e.cfg, g[0])
		}
	}
	png, err := qrcode.Encode(text, qrcode.Medium, 296)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(png)
}
