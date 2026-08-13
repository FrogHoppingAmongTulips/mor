package main

import (
	"fmt"
	"os/exec"
	"strings"

	"mor/internal/store"
	"mor/internal/xray"
)

func yes(answer string) bool {
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "y", "yes", "д", "да":
		return true
	}
	return false
}

// plural writes "3 ключа" the way Russian wants it.
func plural(n int) string {
	word := "ключей"
	switch {
	case n%10 == 1 && n%100 != 11:
		word = "ключ"
	case n%10 >= 2 && n%10 <= 4 && (n%100 < 12 || n%100 > 14):
		word = "ключа"
	}
	if n == 1 {
		return word
	}
	return fmt.Sprintf("%d %s", n, word)
}

func protoInstalled(proto string) bool {
	switch proto {
	case store.ProtoHy2:
		_, err := exec.LookPath("hysteria")
		return err == nil
	default:
		return xray.Installed()
	}
}
