package main

import (
	"fmt"
	"strings"
	"time"

	"mor/internal/webauth"
)

// cmdToken manages the keys other programs use to talk to mor.
//
// They are separate from the panel password on purpose: a script that needs to
// list keys should not be handed the password to the whole server, and a token
// that leaks should be revocable on its own without logging the owner out.
func cmdToken(args []string) {
	e, err := load()
	if err != nil {
		fmt.Println(err)
		return
	}
	tokens := webauth.OpenTokens(e.paths.TokensFile)

	if len(args) == 0 {
		list := tokens.List()
		if len(list) == 0 {
			fmt.Println("  токенов нет")
			fmt.Println("  выпустить: token new <имя>")
			return
		}
		fmt.Println()
		for _, t := range list {
			used := "не использовался"
			if !t.LastUse.IsZero() {
				used = "последний раз " + t.LastUse.Format("02.01.2006 15:04")
			}
			fmt.Printf("  %-20s создан %s · %s\n", t.Name, t.Created.Format("02.01.2006"), used)
		}
		fmt.Println("\n  выпустить: token new <имя> · отозвать: token rm <имя>")
		return
	}

	switch args[0] {
	case "new", "add":
		name := strings.TrimSpace(strings.Join(args[1:], " "))
		if name == "" {
			fmt.Println("  укажи имя: token new бот")
			return
		}
		secret, err := tokens.Issue(name)
		if err != nil {
			fmt.Println(" ", err)
			return
		}
		// Shown once and never again: only the hash is stored, so there is
		// nowhere to look it up later.
		fmt.Printf("\n  %s\n\n", secret)
		fmt.Println("  запиши сейчас — второй раз он не покажется")
		fmt.Printf("  проверить: curl -H 'Authorization: Bearer %s' https://%s:%d/api/users\n",
			secret, e.cfg.PublicHost, e.cfg.WebPort)
		e.audit.Add("выпущен токен API", name, time.Now())
		_ = e.audit.Save()

	case "rm", "del", "revoke":
		name := strings.TrimSpace(strings.Join(args[1:], " "))
		if name == "" {
			fmt.Println("  укажи имя: token rm бот")
			return
		}
		if !tokens.Revoke(name) {
			fmt.Printf("  токена «%s» нет\n", name)
			return
		}
		fmt.Printf("  «%s» отозван\n", name)
		e.audit.Add("отозван токен API", name, time.Now())
		_ = e.audit.Save()

	default:
		fmt.Println("  token · token new <имя> · token rm <имя>")
	}
}
