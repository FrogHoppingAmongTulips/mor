package main

import (
	"bufio"
	"context"
	"strings"
	"testing"
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
