package main

import (
	"bufio"
	"context"
	"strings"
	"testing"

	"mor/internal/store"
)

// Открытие экрана «Обновление» не должно ничего скачивать и ставить: человек
// заходит посмотреть версию, а не перезапустить себе сервис. Ставится только
// по выбору пункта.
func TestOpeningUpdateScreenInstallsNothing(t *testing.T) {
	installed := 0
	// В тестовой сборке версия — "dev", и сравнение её не понимает: без этого
	// экран всегда считал бы, что новее нет.
	origVersion := version
	version = "v0.0.1"
	defer func() { version = origVersion }()

	origInstall, origLatest := installUpdate, latestFn
	installUpdate = func(string) { installed++ }
	latestFn = func(context.Context) (string, error) { return "v9.9.9", nil }
	defer func() { installUpdate, latestFn = origInstall, origLatest }()

	e := panelEnv(t, "")
	out := screen(t, e, "\n", func(m *menu) {
		m.in = bufio.NewReader(strings.NewReader("\n"))
		m.update()
	})

	if installed != 0 {
		t.Fatalf("экран поставил обновление сам (%d раз)", installed)
	}
	if !strings.Contains(out, "доступно v9.9.9") {
		t.Errorf("не показана доступная версия:\n%s", out)
	}
	if !strings.Contains(out, "Установить v9.9.9") {
		t.Errorf("нет пункта для установки:\n%s", out)
	}
}

// А по выбору пункта — ставится.
func TestPickingTheRowInstalls(t *testing.T) {
	installed := ""
	origVersion := version
	version = "v0.0.1"
	defer func() { version = origVersion }()

	origInstall, origLatest := installUpdate, latestFn
	installUpdate = func(tag string) { installed = tag }
	latestFn = func(context.Context) (string, error) { return "v9.9.9", nil }
	defer func() { installUpdate, latestFn = origInstall, origLatest }()

	e := panelEnv(t, "")
	// "1" выбирает единственный пункт, пустая строка выходит.
	screen(t, e, "1\n\n", func(m *menu) {
		m.in = bufio.NewReader(strings.NewReader("1\n\n"))
		m.update()
	})

	if installed != "v9.9.9" {
		t.Fatalf("выбор пункта не поставил обновление: %q", installed)
	}
}

// Когда новее нет, ставить нечего и предлагать нечего.
func TestNothingToInstallWhenUpToDate(t *testing.T) {
	origInstall, origLatest := installUpdate, latestFn
	installUpdate = func(string) { t.Fatal("поставил, хотя новее нет") }
	latestFn = func(context.Context) (string, error) { return version, nil }
	defer func() { installUpdate, latestFn = origInstall, origLatest }()

	e := panelEnv(t, "")
	out := screen(t, e, "\n", func(m *menu) {
		m.in = bufio.NewReader(strings.NewReader("\n"))
		m.update()
	})
	if !strings.Contains(out, "новее нет") {
		t.Errorf("не сказано, что обновление не нужно:\n%s", out)
	}
	if strings.Contains(out, "Установить") {
		t.Errorf("предложена установка без новой версии:\n%s", out)
	}
}

// Нумерацию релизов начали заново — на машине с прежней версией «обновление не
// требуется» было бы неправдой: она навсегда осталась бы на старой сборке.
func TestRenumberedReleaseIsOfferedNotHidden(t *testing.T) {
	installed := ""
	origVersion, origInstall, origLatest := version, installUpdate, latestFn
	version = "v0.4.0"
	installUpdate = func(tag string) { installed = tag }
	latestFn = func(context.Context) (string, error) { return "v0.0.1", nil }
	defer func() { version, installUpdate, latestFn = origVersion, origInstall, origLatest }()

	e := panelEnv(t, "")
	out := screen(t, e, "\n", func(m *menu) {
		m.in = bufio.NewReader(strings.NewReader("\n"))
		m.update()
	})

	if installed != "" {
		t.Fatal("экран поставил сам, хотя должен спросить")
	}
	if strings.Contains(out, "новее нет") {
		t.Errorf("сказано «новее нет», хотя версия просто ниже:\n%s", out)
	}
	if !strings.Contains(out, "Поставить v0.0.1") {
		t.Errorf("нет пункта, чтобы поставить выложенную версию:\n%s", out)
	}

	// И по выбору — ставится.
	screen(t, e, "1\n\n", func(m *menu) {
		m.in = bufio.NewReader(strings.NewReader("1\n\n"))
		m.update()
	})
	if installed != "v0.0.1" {
		t.Fatalf("выбор пункта не поставил: %q", installed)
	}
}

// mor update --force ставит выложенную версию, даже когда её номер ниже.
func TestForceInstallsLowerVersion(t *testing.T) {
	installed := ""
	origVersion, origInstall, origLatest := version, installUpdate, latestFn
	version = "v0.4.0"
	installUpdate = func(tag string) { installed = tag }
	latestFn = func(context.Context) (string, error) { return "v0.0.1", nil }
	defer func() { version, installUpdate, latestFn = origVersion, origInstall, origLatest }()

	cmdUpdate()
	if installed != "" {
		t.Fatal("без --force поставил сам")
	}
	cmdUpdate("--force")
	if installed != "v0.0.1" {
		t.Fatalf("--force не поставил: %q", installed)
	}
}

// Ключи убранного протокола удаляются при запуске, а остальные остаются.
func TestRetiredKeysAreDroppedOnLoad(t *testing.T) {
	st, err := store.Open(t.TempDir() + "/users.json")
	if err != nil {
		t.Fatal(err)
	}
	live, err := st.Add(&store.User{Name: "живой", Proto: store.ProtoHy2})
	if err != nil {
		t.Fatal(err)
	}
	dead, err := st.Add(&store.User{Name: "старый", Proto: "enc"})
	if err != nil {
		t.Fatal(err)
	}

	dropRetiredKeys(st)

	names := map[string]bool{}
	for _, u := range st.List() {
		names[u.ID] = true
	}
	if !names[live.ID] {
		t.Error("удалён рабочий ключ")
	}
	if names[dead.ID] {
		t.Error("ключ убранного протокола остался")
	}
}
