package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"mor/internal/config"
	"mor/internal/hysteria"
	"mor/internal/store"
	"mor/internal/systemd"
	"mor/internal/webauth"
	"mor/internal/xray"
)

// onlineKey is one person's presence — that they are connected, and nothing
// about what they are doing. No address, no destination, no activity: the
// point of the VPN is that this is not collected, and a panel that collected
// it would be working against its own product.
type onlineKey struct {
	ID       string     `json:"id"`
	Name     string     `json:"name"`
	Sessions int        `json:"sessions"`
	Since    *time.Time `json:"since,omitempty"`
	Source   string     `json:"source"` // "live" or "derived"
}

// minPasswordRunes is the shortest panel password worth allowing. The panel
// answers from anywhere, so this is the only thing between a stranger and the
// server once they find the port.
const minPasswordRunes = 8

// presenceWindow is how recently a key must have moved traffic to be called
// present when the protocol cannot say so itself.
const presenceWindow = 3 * time.Minute

// handleOnline answers "who is connected right now".
//
// Only Hysteria2 knows: it reports live session counts per key. The Xray
// protocols report traffic counters and nothing else, so for those the answer
// is inferred from whether the counter moved recently — which is a guess, and
// is labelled as one rather than dressed up as fact.
func (ws *webServer) handleOnline(w http.ResponseWriter, r *http.Request) {
	sessions, err := hysteria.Online(ws.e.cfg.StatsSecret)
	liveOK := err == nil && ws.e.cfg.On(store.ProtoHy2)

	out := []onlineKey{}
	for _, g := range groupKeys(ws.e.st.List()) {
		if g[0].Banned {
			continue
		}
		live := 0
		hasXray := false
		for _, u := range g {
			if u.Proto == store.ProtoHy2 {
				live += sessions[u.ID]
				continue
			}
			hasXray = true
		}
		entry := ws.e.stats.Sum(ids(g))
		row := onlineKey{ID: groupID(g), Name: g[0].Name}
		switch {
		case liveOK && live > 0:
			row.Sessions, row.Source = live, "live"
		case hasXray && !entry.LastSeen.IsZero() && time.Since(entry.LastSeen) < presenceWindow:
			row.Source = "derived"
		default:
			continue
		}
		if !entry.LastSeen.IsZero() {
			seen := entry.LastSeen
			row.Since = &seen
		}
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	writeJSON(w, map[string]any{
		// live says whether real presence data was available at all. When it
		// is false every row is inferred, and the panel says so instead of
		// showing an empty list that could equally mean "nobody home".
		"live": liveOK,
		"keys": out,
	})
}

// handleRestart restarts the engines that are supposed to be running. Only
// the ones actually installed and switched on are touched — restarting a unit
// that was deliberately stopped would quietly turn a protocol back on.
func (ws *webServer) handleRestart(w http.ResponseWriter, r *http.Request) {
	var done, failed []string

	if ws.e.cfg.On(store.ProtoHy2) && protoInstalled(store.ProtoHy2) {
		if err := systemd.Restart(hysteria.Service); err != nil {
			failed = append(failed, "Hysteria2: "+err.Error())
		} else {
			done = append(done, "Hysteria2")
		}
	}
	xrayOn := ws.e.cfg.On(store.ProtoReality) || ws.e.cfg.On(store.ProtoEnc) || ws.e.cfg.On(store.ProtoSS)
	if xrayOn && xray.Installed() {
		if err := systemd.Restart(xray.Service); err != nil {
			failed = append(failed, "Xray: "+err.Error())
		} else {
			done = append(done, "Xray")
		}
	}

	ws.e.audit.Add("перезапуск движков", strings.Join(done, ", "), time.Now())
	writeJSON(w, map[string]any{"restarted": done, "failed": failed})
}

// handleUserEdit changes what a key means without changing the key itself:
// its label, its deadline and its traffic cap. None of these are baked into
// the links already handed out, so an edit never forces a re-share — which is
// the whole reason editing has to exist instead of delete-and-recreate.
func (ws *webServer) handleUserEdit(w http.ResponseWriter, r *http.Request) {
	g, ok := findGroup(ws.e, r.PathValue("id"))
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	var req struct {
		Name      *string `json:"name"`
		Time      *string `json:"time"`
		Traffic   *string `json:"traffic"`
		IPLimit   *int    `json:"ipLimit"`
		AutoReset *bool   `json:"autoReset"`
	}
	if err := readJSON(r, &req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" {
			http.Error(w, "имя не может быть пустым", http.StatusBadRequest)
			return
		}
		for _, u := range g {
			if err := ws.e.st.SetName(u.ID, name); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
	}

	// An empty string clears the limit; an absent field leaves it alone. The
	// two have to stay distinguishable or "remove the cap" becomes impossible.
	if req.Time != nil {
		until, err := parsePeriodField(*req.Time, time.Now())
		if err != nil {
			http.Error(w, "срок: "+err.Error(), http.StatusBadRequest)
			return
		}
		for _, u := range g {
			_ = ws.e.st.SetExpiry(u.ID, until)
		}
	}
	if req.Traffic != nil {
		bytes, err := parseTrafficField(*req.Traffic)
		if err != nil {
			http.Error(w, "лимит трафика: "+err.Error(), http.StatusBadRequest)
			return
		}
		for _, u := range g {
			_ = ws.e.st.SetLimit(u.ID, bytes)
		}
	}

	if req.IPLimit != nil && *req.IPLimit != g[0].IPLimit {
		for _, u := range g {
			if err := ws.e.st.SetIPLimit(u.ID, *req.IPLimit); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
		// Devices already counted were admitted under the old cap; forgetting
		// them lets the new one take effect from the next connection instead of
		// leaving somebody locked out by a number that no longer applies. Only
		// a changed number does this: clearing the table on every save would
		// hand out a fresh set of slots for renaming a key.
		for _, u := range g {
			ws.e.ipLimits.Forget(u.ID)
			if u.Sub != "" {
				ws.e.devices.Forget(u.Sub)
			}
		}
	}

	if req.AutoReset != nil {
		// Stamping the current month when switching on means the counter starts
		// renewing next month rather than being wiped the moment it is enabled.
		month := ""
		if *req.AutoReset {
			month = time.Now().Format("2006-01")
		}
		for _, u := range g {
			if err := ws.e.st.SetAutoReset(u.ID, *req.AutoReset); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			_ = ws.e.st.SetResetMonth(u.ID, month)
		}
	}

	// g was read before the edits, so it still holds the old values — re-read
	// it or the reply would report the state the caller just replaced.
	g, ok = findGroup(ws.e, r.PathValue("id"))
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	// A key that just got more time or more traffic has to be let back into
	// the engines, and one that just lost them has to be cut.
	if err := applyLive(ws.e, g); err != nil {
		log.Printf("предупреждение: применение правки: %v", err)
	}
	ws.e.audit.Add("изменён ключ", g[0].Name, time.Now())
	writeJSON(w, toWebUser(ws.e, g, true))
}

// configPatch is every setting the panel may change. Pointers so an absent
// field means "leave it alone" — a form that submits three fields must not
// blank out the rest.
type configPatch struct {
	DNS         *string `json:"dns"`
	SNI         *string `json:"sni"`
	Host        *string `json:"host"`
	HyObfs      *string `json:"hyObfs"`
	VPNPort     *int    `json:"vpnPort"`
	RealityPort *int    `json:"realityPort"`
	RealityDest *string `json:"realityDest"`
	EncPort     *int    `json:"encPort"`
	SSPort      *int    `json:"ssPort"`
	SubPort     *int    `json:"subPort"`
	SubOff      *bool   `json:"subOff"`
	WebPort     *int    `json:"webPort"`
}

func validPort(p int) error {
	if p < 1 || p > 65535 {
		return fmt.Errorf("порт — число от 1 до 65535")
	}
	return nil
}

// handleConfigSave applies a settings change and brings the engines in line
// with it. Whether a change rewrites already-issued links is decided in the
// panel, which warns before submitting — here the job is to apply exactly
// what was asked and report honestly if an engine did not come back.
func (ws *webServer) handleConfigSave(w http.ResponseWriter, r *http.Request) {
	var p configPatch
	if err := readJSON(r, &p); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	// Whatever the terminal changed since the last tick is picked up first:
	// saving on top of a stale copy would put the old values back and give no
	// sign it had happened.
	c := ws.e.cfg
	c.ReloadIfChanged()

	for _, p := range []*int{p.VPNPort, p.RealityPort, p.EncPort, p.SSPort, p.SubPort, p.WebPort} {
		if p == nil {
			continue
		}
		if err := validPort(*p); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}

	set := func(dst *string, v *string) {
		if v != nil {
			*dst = strings.TrimSpace(*v)
		}
	}
	setInt := func(dst *int, v *int) {
		if v != nil {
			*dst = *v
		}
	}
	set(&c.DNS, p.DNS)
	set(&c.SNI, p.SNI)
	set(&c.PublicHost, p.Host)
	set(&c.HyObfs, p.HyObfs)
	set(&c.Reality.Dest, p.RealityDest)
	setInt(&c.VPNPort, p.VPNPort)
	setInt(&c.Reality.Port, p.RealityPort)
	setInt(&c.Enc.Port, p.EncPort)
	setInt(&c.SS.Port, p.SSPort)
	setInt(&c.SubPort, p.SubPort)
	setInt(&c.WebPort, p.WebPort)
	if p.SubOff != nil {
		c.SubOff = *p.SubOff
	}
	if c.DNS == "" {
		c.DNS = config.DefaultDNS
	}
	if c.SNI == "" {
		c.SNI = config.DefaultSNI
	}

	if err := c.Save(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	ws.e.audit.Add("изменены настройки", "", time.Now())

	// A port changed here has to be opened in the firewall, or the protocol
	// moves to a port nothing can reach and every key on it stops working with
	// no sign of why. The terminal has always done this; the panel never did,
	// which went unnoticed while servers had no firewall at all.
	ensureFirewall(ws.e)

	// The panel's own port only takes effect on the next start: rebinding the
	// listener underneath the request that asked for it would drop the answer.
	if err := applyProtos(ws.e, store.ProtoHy2, store.ProtoReality, store.ProtoEnc, store.ProtoSS); err != nil {
		writeJSON(w, map[string]any{"ok": true, "warning": "сохранено, но движок не отозвался: " + err.Error()})
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

// handlePortPick finds a port that is free here and actually reachable from
// outside, for one protocol. The terminal has had this for a while; the panel
// only ever offered typing a number, which is guesswork — a port can be free
// on the box and still cut somewhere along the way.
//
// It does not apply the port: the reply is a suggestion the operator saves
// like any other setting, so nothing moves under a running server by surprise.
func (ws *webServer) handlePortPick(w http.ResponseWriter, r *http.Request) {
	proto := r.PathValue("proto")
	var udp bool
	found := false
	for _, row := range portRows(ws.e) {
		if row.proto == proto {
			udp, found = row.kind == "udp", true
		}
	}
	if !found {
		http.Error(w, "unknown protocol", http.StatusBadRequest)
		return
	}

	// Every configured port counts as busy, on or off: a switched-off protocol
	// still owns its port in the config, and so does the subscription server.
	busy := map[int]bool{}
	for _, row := range portRows(ws.e) {
		busy[row.port] = true
	}
	busy[ws.e.cfg.SubPort] = true
	busy[ws.e.cfg.WebPort] = true

	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	port, err := findPort(ctx, ws.e.cfg.PublicHost, busy, udp, nil)
	if err != nil {
		http.Error(w, "проверка снаружи не отвечает — попробуй позже", http.StatusServiceUnavailable)
		return
	}
	if port == 0 {
		http.Error(w, "свободного рабочего порта не нашлось", http.StatusNotFound)
		return
	}
	writeJSON(w, map[string]any{"port": port})
}

// handlePassword changes the panel password. The current one is required —
// a stolen open session should not be enough to lock the owner out.
func (ws *webServer) handlePassword(w http.ResponseWriter, r *http.Request) {
	var req struct{ Current, New string }
	if err := readJSON(r, &req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if !webauth.VerifyPassword(ws.e.cfg.WebPasswordHash, req.Current) {
		http.Error(w, "текущий пароль неверный", http.StatusUnauthorized)
		return
	}
	// Runes, not bytes: len() would count a Cyrillic password twice over and
	// let four characters pass a minimum of eight.
	if utf8.RuneCountInString(req.New) < minPasswordRunes {
		http.Error(w, "пароль — минимум 8 символов", http.StatusBadRequest)
		return
	}
	ws.e.cfg.SetWebPassword(req.New)
	if err := ws.e.cfg.Save(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	ws.e.audit.Add("изменён пароль панели", "", time.Now())
	writeJSON(w, map[string]bool{"ok": true})
}

// diskEntry is one directory and what it costs, for the "what is eating the
// disk" answer.
type diskEntry struct {
	Path  string `json:"path"`
	Bytes uint64 `json:"bytes"`
}

// handleDisk walks the top level of the filesystem on demand. This is
// deliberately not part of the regular refresh: du reads every inode under
// each directory, which on a full disk takes seconds and would otherwise be
// paid on every poll by everyone. -x keeps it on one filesystem so mounted
// volumes and /proc do not turn a directory listing into an expedition.
func (ws *webServer) handleDisk(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx, "du", "-x", "-k", "-d", "1", "/").Output()
	if err != nil && len(out) == 0 {
		http.Error(w, "не удалось посчитать: "+err.Error(), http.StatusInternalServerError)
		return
	}

	entries := []diskEntry{}
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 || f[1] == "/" {
			continue
		}
		kb, err := strconv.ParseUint(f[0], 10, 64)
		if err != nil {
			continue
		}
		entries = append(entries, diskEntry{Path: f[1], Bytes: kb * 1024})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Bytes > entries[j].Bytes })
	if len(entries) > 8 {
		entries = entries[:8]
	}
	writeJSON(w, map[string]any{"entries": entries, "mor": ws.morDataBytes()})
}

// morDataBytes sums mor's own files, so the breakdown can answer "and how
// much of this is the panel itself" without a second walk.
func (ws *webServer) morDataBytes() uint64 {
	var total uint64
	for _, p := range []string{
		ws.e.paths.ConfigFile, ws.e.paths.DataFile, ws.e.paths.StatsFile,
		ws.e.paths.HistoryFile, ws.e.paths.AuditLogFile, ws.e.paths.SysHistFile,
	} {
		if fi, err := os.Stat(p); err == nil {
			total += uint64(fi.Size())
		}
	}
	return total
}
