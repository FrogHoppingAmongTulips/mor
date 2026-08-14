package hysteria

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"

	"mor/internal/store"
)

func maskToken(t string) string {
	switch {
	case t == "":
		return "(empty)"
	case len(t) <= 8:
		return "…"
	default:
		return t[:8] + "…"
	}
}

type authReq struct {
	Addr string `json:"addr"`
	Auth string `json:"auth"`
}

type authResp struct {
	OK bool   `json:"ok"`
	ID string `json:"id,omitempty"`
}

// Spent says whether a key has used up what it was given. It is asked on every
// connection, so it must be cheap; nil means nothing is capped.
type Spent func(id string) bool

// TooManyDevices says whether letting this address in would put the key over
// its own device limit. It is asked on every connection, so it must be cheap;
// nil means no key is capped.
type TooManyDevices func(u *store.User, addr string) bool

// Nothing here records who connected. Hysteria2 is the one protocol that
// could report a per-connection client address, and mor deliberately does not
// keep it: a panel that logged where its users connect from would work against
// the only thing the product exists to do. Refusals are logged because a
// refusal is the operator's own problem to debug.
func authHandler(st *store.Store, spent Spent, tooMany TooManyDevices) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /auth", func(w http.ResponseWriter, r *http.Request) {
		var req authReq
		_ = json.NewDecoder(r.Body).Decode(&req)
		u := st.FindByHyToken(req.Auth)
		if u == nil {

			log.Printf("отказ: неизвестный ключ %s, клиент %s", maskToken(req.Auth), req.Addr)
			json.NewEncoder(w).Encode(authResp{OK: false})
			return
		}
		if spent != nil && spent(u.ID) {
			log.Printf("отказ: «%s» исчерпал лимит трафика, клиент %s", u.Name, req.Addr)
			json.NewEncoder(w).Encode(authResp{OK: false})
			return
		}
		// Checked last: a key that is out of traffic or unknown should be
		// refused for that reason, and should not spend a device slot on the
		// way to being told so.
		if tooMany != nil && tooMany(u, req.Addr) {
			log.Printf("отказ: «%s» превысил лимит устройств (%d)", u.Name, u.IPLimit)
			json.NewEncoder(w).Encode(authResp{OK: false})
			return
		}
		json.NewEncoder(w).Encode(authResp{OK: true, ID: u.ID})
	})
	return mux
}

func StartAuthServer(ctx context.Context, st *store.Store, spent Spent, tooMany TooManyDevices) error {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", AuthPort))
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: authHandler(st, spent, tooMany)}
	go func() {
		<-ctx.Done()
		_ = srv.Close()
	}()
	err = srv.Serve(ln)
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
