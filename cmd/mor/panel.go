package main

import (
	"fmt"
	"strings"
)

// cmdPanel is the web panel's CLI twin: set the one password it needs, or
// change its port, or take it down without losing that password.
func cmdPanel(args []string) {
	e, err := load()
	if err != nil {
		fmt.Println(err)
		return
	}
	if len(args) == 0 {
		state := "выключена"
		if e.cfg.WebOn() {
			state = "включена"
		}
		fmt.Printf("  панель %s, порт %d/tcp\n", state, e.cfg.WebPort)
		switch {
		case e.cfg.WebPasswordHash == "":
			fmt.Println("  пароль не задан — без него панель не запустится")
		case e.cfg.WebPassword != "":
			fmt.Printf("  https://%s:%d   %s\n", e.cfg.PublicHost, e.cfg.WebPort, e.cfg.WebPassword)
		}
		fmt.Printf("  сертификат: %s\n", certSummary(e.paths.WebCertFile))
		fmt.Println("  сменить: panel password <пароль> · panel on · panel off · panel port 9090 · panel cert [домен|ip]")
		return
	}
	switch args[0] {
	case "cert":
		// Issuing restarts mor, so it cannot share the save-and-report tail below.
		cmdPanelCert(args[1:])
		return
	case "password":
		if len(args) < 2 {
			fmt.Println("  укажи пароль: panel password секрет123")
			return
		}
		e.cfg.SetWebPassword(strings.Join(args[1:], " "))
	case "on", "off":
		e.cfg.WebOff = args[0] == "off"
	case "port":
		if len(args) < 2 {
			fmt.Println("  укажи порт: panel port 9090")
			return
		}
		p, err := parsePort(args[1])
		if err != nil {
			fmt.Println(" ", err)
			return
		}
		e.cfg.WebPort = p
	default:
		fmt.Println("  panel password <пароль> · panel on · panel off · panel port <номер> · panel cert [домен|ip]")
		return
	}
	if err := e.cfg.Save(); err != nil {
		fmt.Println(" ", err)
		return
	}
	ensureFirewall(e)
	// The daemon reads web settings at startup, same as sub — a restart is
	// what makes a password or port change take hold.
	restartSelf()
	fmt.Println("  сохранено и применено")
}
