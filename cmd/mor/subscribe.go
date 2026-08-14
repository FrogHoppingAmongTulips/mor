package main

import (
	"fmt"
	"strings"

	"mor/internal/keys"
	"mor/internal/qr"
	"mor/internal/store"
	"mor/internal/sub"
)

// createAccess makes one key per live protocol and ties them with a single
// subscription token. The app on the phone re-reads that link, tests what is
// inside and picks whatever works — the owner never has to say which protocol.
func createAccess(e *env, name, sni string) ([]*store.User, error) {
	return createAccessFor(e, name, sni, protoList)
}

// createAccessFor is createAccess narrowed to a caller-chosen subset of
// protocols — the web panel's create form lets a person uncheck ones they
// don't want, the CLI's "all protocols" path just passes every one of them.
func createAccessFor(e *env, name, sni string, protos []string) ([]*store.User, error) {
	token := keys.Token()
	made := []*store.User{}
	var last error
	for _, p := range protos {
		// A protocol whose engine is missing would only mint a dead key.
		if !e.cfg.On(p) || !protoInstalled(p) {
			continue
		}
		u, err := createKey(e, name, p, sni)
		if u == nil {
			last = err
			continue
		}
		if err := e.st.SetSub(u.ID, token); err != nil {
			return made, err
		}
		u.Sub = token
		made = append(made, u)
		if err != nil {
			last = err
		}
	}
	if len(made) == 0 {
		if last == nil {
			last = fmt.Errorf("нет включённых протоколов")
		}
		return nil, last
	}
	return made, last
}

// subURL is the link handed to a person, or empty when subscriptions are off.
func subURL(e *env, u *store.User) string {
	if u == nil || u.Sub == "" || !e.cfg.SubOn() {
		return ""
	}
	return sub.URL(e.cfg.PublicHost, e.cfg.SubPort, u.Sub, subSecure(e))
}

// subSecure reports whether the subscription can be served over TLS.
//
// Only with a certificate from an authority. A self-signed one is refused by
// every client app, and a subscription that cannot be fetched at all is worse
// than one fetched in the clear — the link carries every key of one person, but
// a link that does not work carries nothing. Until `mor panel cert` succeeds
// the link stays http; the port answers both either way, so nothing has to be
// handed out twice when it does.
func subSecure(e *env) bool {
	return fileExists(e.paths.WebCertFile) && fileExists(e.paths.WebKeyFile) &&
		!certIsSelfSigned(e.paths.WebCertFile)
}

// groupKeys folds a person's keys into one entry, keeping creation order. Keys
// made before subscriptions existed stand alone and are shown as before.
func groupKeys(users []*store.User) [][]*store.User {
	out := [][]*store.User{}
	at := map[string]int{}
	for _, u := range users {
		if u.Sub == "" {
			out = append(out, []*store.User{u})
			continue
		}
		if i, ok := at[u.Sub]; ok {
			out[i] = append(out[i], u)
			continue
		}
		at[u.Sub] = len(out)
		out = append(out, []*store.User{u})
	}
	return out
}

func ids(users []*store.User) []string {
	out := make([]string, 0, len(users))
	for _, u := range users {
		out = append(out, u.ID)
	}
	return out
}

func protoNames(users []*store.User) string {
	names := make([]string, 0, len(users))
	for _, u := range users {
		names = append(names, store.ProtoName(u.Proto))
	}
	return strings.Join(names, ", ")
}

// menu

func (m *menu) showSub(users []*store.User) {
	if len(users) == 0 {
		return
	}
	link := subURL(m.e, users[0])
	if link == "" {
		return
	}
	fmt.Println()
	if a, err := qr.ASCII(link); err == nil {
		fmt.Print(indent(a))
	}
	fmt.Printf("  %s\n", link)
	fmt.Printf("\n  %sОтдай ссылку целиком или дай отсканировать QR. Приложение само%s\n", dim, reset)
	fmt.Printf("  %sвыберет протокол, который работает в его сети.%s\n", dim, reset)
}

func cmdSub(args []string) {
	e, err := load()
	if err != nil {
		fmt.Println(err)
		return
	}
	if len(args) == 0 {
		state := "включена"
		if !e.cfg.SubOn() {
			state = "выключена"
		}
		fmt.Printf("  автовыбор %s, порт %d/tcp\n", state, e.cfg.SubPort)
		fmt.Println("  сменить: sub off · sub on · sub port 8880")
		return
	}
	switch args[0] {
	case "on", "off":
		e.cfg.SubOff = args[0] == "off"
	case "port":
		if len(args) < 2 {
			fmt.Println("  укажи порт: sub port 8880")
			return
		}
		p, err := parsePort(args[1])
		if err != nil {
			fmt.Println(" ", err)
			return
		}
		e.cfg.SubPort = p
	default:
		fmt.Println("  sub on · sub off · sub port <номер>")
		return
	}
	if err := e.cfg.Save(); err != nil {
		fmt.Println(" ", err)
		return
	}
	ensureFirewall(e)
	// The daemon reads the subscription port at startup, so the change lands
	// with a restart — done here, not left as homework.
	restartSelf()
	fmt.Println("  сохранено и применено")
}
