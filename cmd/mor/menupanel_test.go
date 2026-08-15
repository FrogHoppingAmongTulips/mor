package main

import (
	"bufio"
	"io"
	"os"
	"strings"
	"testing"

	"mor/internal/config"
	"mor/internal/webauth"
)

// screen runs one menu screen with canned input and returns what it printed.
func screen(t *testing.T, e *env, input string, show func(*menu)) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w

	done := make(chan string)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()

	show(&menu{e: e, in: bufio.NewReader(strings.NewReader(input))})

	w.Close()
	os.Stdout = old
	return <-done
}

func panelEnv(t *testing.T, hash string) *env {
	t.Helper()
	dir := t.TempDir()
	cfg := config.NewDefault()
	// The real config always comes through Load, which fills the ports in.
	cfg.EnsureDefaults()
	cfg.SetPath(dir + "/config.json")
	cfg.PublicHost = "203.0.113.7"
	cfg.WebPasswordHash = hash
	return &env{cfg: cfg, paths: config.Paths{WebCertFile: dir + "/web.crt"}}
}

// The menu is what most people ever see. Until this screen existed it said
// nothing about the panel at all, and the only way to switch it on was a line
// in `help` — which is exactly the question this came from.
func TestPanelScreenOffersThePasswordWhenThereIsNone(t *testing.T) {
	out := screen(t, panelEnv(t, ""), "\n", func(m *menu) { m.panel() })

	for _, want := range []string{"Пароль", "пароль не задан", "Задать свой", "Сгенерировать новый", "Порт"} {
		if !strings.Contains(out, want) {
			t.Errorf("на экране нет %q:\n%s", want, out)
		}
	}
	// Nothing to switch off or re-issue a certificate for yet.
	for _, unwanted := range []string{"Выключить", "сертификат заново"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("до пароля предложено «%s»:\n%s", unwanted, out)
		}
	}
}

func TestPanelScreenShowsTheAddressOnceItRuns(t *testing.T) {
	e := panelEnv(t, "")
	e.cfg.SetWebPassword("мойпароль123")
	out := screen(t, e, "\n", func(m *menu) { m.panel() })

	if !strings.Contains(out, "https://203.0.113.7:9090") {
		t.Errorf("нет адреса панели:\n%s", out)
	}
	// The whole point: the password is readable without asking anyone.
	if !strings.Contains(out, "мойпароль123") {
		t.Errorf("пароль не показан:\n%s", out)
	}
	for _, want := range []string{"Выключить", "сертификат заново"} {
		if !strings.Contains(out, want) {
			t.Errorf("нет пункта %q:\n%s", want, out)
		}
	}
}

func TestPanelIsInTheMainMenu(t *testing.T) {
	found := false
	for _, it := range menuItems {
		if it.title == "Пароль" {
			found = true
		}
	}
	if !found {
		t.Fatal("пункта «Пароль» нет в главном меню")
	}
}

func TestShortPasswordIsRefused(t *testing.T) {
	e := panelEnv(t, "")
	m := &menu{e: e, in: bufio.NewReader(strings.NewReader("1234567\n"))}
	msg, ok := m.askPanelPassword()
	if ok {
		t.Fatal("пароль из семи знаков приняли")
	}
	if !strings.Contains(msg, "8") {
		t.Errorf("непонятная причина отказа: %q", msg)
	}
	if e.cfg.WebPasswordHash != "" {
		t.Error("пароль всё-таки записан")
	}
}

// A password set before mor kept a readable copy cannot be shown. Saying so is
// the point — printing nothing would look like the password had been lost.
func TestOldPasswordWithoutReadableCopySaysSo(t *testing.T) {
	out := screen(t, panelEnv(t, "соль:хеш"), "\n", func(m *menu) { m.panel() })

	if !strings.Contains(out, "не сохранён") {
		t.Errorf("не сказано, что старый пароль не показать:\n%s", out)
	}
}

// The installer generates one so the panel works out of the box.
func TestGeneratedPasswordIsUsable(t *testing.T) {
	seen := map[string]bool{}
	for range 50 {
		pw := webauth.NewPassword()
		if len(pw) != 16 {
			t.Fatalf("длина %d, ожидалось 16: %q", len(pw), pw)
		}
		if strings.ContainsAny(pw, "0O1lI5S") {
			t.Errorf("в пароле легко спутать знаки: %q", pw)
		}
		if seen[pw] {
			t.Fatalf("пароль повторился: %q", pw)
		}
		seen[pw] = true
	}
}

// The hash and the readable copy are written together, so a login can never
// disagree with what the menu shows.
func TestSetWebPasswordKeepsBothInStep(t *testing.T) {
	c := config.NewDefault()
	c.SetWebPassword("пароль-которым-входят")

	if c.WebPassword != "пароль-которым-входят" {
		t.Errorf("читаемая копия: %q", c.WebPassword)
	}
	if !webauth.VerifyPassword(c.WebPasswordHash, c.WebPassword) {
		t.Error("показанным паролем нельзя войти")
	}
}
