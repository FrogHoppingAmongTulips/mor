package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

const releaseAPI = "https://api.github.com/repos/FrogHoppingAmongTulips/mor/releases/latest"

const installURL = "https://github.com/FrogHoppingAmongTulips/mor/releases/latest/download/install.sh"

// latest asks GitHub for the newest published version. Failure is not an error
// worth shouting about: a server without outbound access still works fine.
func latest(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, releaseAPI, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("github ответил %d", resp.StatusCode)
	}
	var doc struct {
		Tag string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return "", err
	}
	if doc.Tag == "" {
		return "", fmt.Errorf("в ответе нет версии")
	}
	return doc.Tag, nil
}

// newerThanRunning compares tags as plain strings after stripping the v. Version
// numbers here are always vX.Y.Z from the same tool, so this is enough.
// olderThanRunning: выложенная версия ниже установленной. Так бывает, когда
// нумерацию релизов начали заново — машина, где стоит прежняя, иначе молча
// осталась бы на ней навсегда, считая, что обновляться не нужно.
func olderThanRunning(tag string) bool {
	have, want := norm(version), norm(tag)
	if have == "" || have == "dev" || want == "" {
		return false
	}
	return compareVersions(want, have) < 0
}

func newerThanRunning(tag string) bool {
	have, want := norm(version), norm(tag)
	if have == "" || have == "dev" || want == "" {
		return false
	}
	return compareVersions(want, have) > 0
}

func norm(v string) string { return strings.TrimPrefix(strings.TrimSpace(v), "v") }

func compareVersions(a, b string) int {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) || i < len(bs); i++ {
		x, y := part(as, i), part(bs, i)
		if x != y {
			if x > y {
				return 1
			}
			return -1
		}
	}
	return 0
}

func part(list []string, i int) int {
	if i >= len(list) {
		return 0
	}
	n := 0
	for _, r := range list[i] {
		if r < '0' || r > '9' {
			break
		}
		n = n*10 + int(r-'0')
	}
	return n
}

// cmdUpdate — явная команда «поставь свежую версию»: её набирают, когда
// именно этого и хотят, поэтому она ставит.
func cmdUpdate(args ...string) {
	force := false
	for _, a := range args {
		if a == "--force" || a == "-f" {
			force = true
		}
	}
	tag, newer, ok := checkUpdate()
	if !ok {
		return
	}
	if !newer && !force {
		return
	}
	installUpdate(tag)
}

// checkUpdate только смотрит и печатает. Ничего не качает и не ставит.
func checkUpdate() (tag string, newer, ok bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	tag, err := latest(ctx)
	cancel()
	if err != nil {
		fmt.Printf("  не удалось узнать последнюю версию: %v\n", err)
		return "", false, false
	}
	fmt.Printf("  установлено: %s\n  доступно:    %s\n", version, tag)
	if olderThanRunning(tag) {
		fmt.Println("\n  выложенная версия ниже установленной — нумерацию начали заново")
		fmt.Println("  поставить её: mor update --force")
		return tag, false, true
	}
	if !newerThanRunning(tag) {
		fmt.Println("\n  обновление не требуется")
		return tag, false, true
	}
	return tag, true, true
}

// installUpdate — переменная, чтобы проверка могла убедиться, что открытие
// экрана его не вызывает, не запуская установку на самом деле.
var installUpdate = func(tag string) {
	fmt.Printf("\n  качаю и ставлю %s…\n", tag)
	// The installer already knows how to fetch the binary, restart the service
	// and leave settings alone, so updating is just running it again.
	cmd := exec.Command("bash", "-c", "curl -fsSL "+installURL+" | bash")
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Printf("  установка не удалась: %v\n", err)
		return
	}
	fmt.Println("  готово")
}

// update показывает, что установлено и что доступно, и на этом
// останавливается. Ставить или нет — решает человек: открыть экран, чтобы
// посмотреть версию, и обнаружить, что сервис уже перезапустился, — не то,
// чего от этого пункта ждут.
func (m *menu) update() {
	sec := &section{title: "Обновление"}
	m.walk(sec, func(s *section) {
		tag, newer, ok := checkUpdateQuiet()
		s.state = nil
		s.rows = nil
		switch {
		case !ok:
			s.state = append(s.state, "не удалось узнать последнюю версию")
		case olderThanRunning(tag):
			s.state = append(s.state,
				"установлено "+version, "выложено "+tag,
				"нумерацию релизов начали заново")
			s.rows = []row{{label: "Поставить " + tag, do: func() (string, bool) {
				installUpdate(tag)
				return "установлено — сервис перезапущен", true
			}}}
		case !newer:
			s.state = append(s.state, "установлено "+version, "новее нет")
		default:
			s.state = append(s.state, "установлено "+version, "доступно "+tag)
			s.rows = []row{{label: "Установить " + tag, do: func() (string, bool) {
				installUpdate(tag)
				return "установлено — сервис перезапущен", true
			}}}
		}
	})
	m.msg = ""
}

// checkUpdateQuiet — то же, что checkUpdate, но без печати: экран рисует сам.
// latestFn — переменная, чтобы проверка обходилась без сети.
var latestFn = latest

func checkUpdateQuiet() (tag string, newer, ok bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	tag, err := latestFn(ctx)
	cancel()
	if err != nil {
		return "", false, false
	}
	return tag, newerThanRunning(tag), true
}
