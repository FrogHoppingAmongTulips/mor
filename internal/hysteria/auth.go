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

func authHandler(st *store.Store) http.Handler {
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
		json.NewEncoder(w).Encode(authResp{OK: true, ID: u.ID})
	})
	return mux
}

func StartAuthServer(ctx context.Context, st *store.Store) error {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", AuthPort))
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: authHandler(st)}
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
