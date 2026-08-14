package sub

import (
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

func TestDeviceLimitLetsTheAllowedOnesIn(t *testing.T) {
	d := OpenDevices(t.TempDir() + "/devices.json")

	if !d.Allow("токен", "телефон", 2) {
		t.Fatal("первое устройство не пустили")
	}
	if !d.Allow("токен", "ноут", 2) {
		t.Fatal("второе устройство не пустили")
	}
	if d.Allow("токен", "чужой", 2) {
		t.Fatal("третье устройство пустили при лимите 2")
	}
	// Re-reading the subscription must never cost a device its own slot.
	if !d.Allow("токен", "телефон", 2) {
		t.Fatal("уже учтённое устройство перестали пускать")
	}
	if n := d.Count("токен"); n != 2 {
		t.Fatalf("устройств насчитано %d, ожидалось 2", n)
	}
}

func TestDeviceLimitIsPerSubscription(t *testing.T) {
	d := OpenDevices(t.TempDir() + "/devices.json")

	d.Allow("первый", "телефон", 1)
	if !d.Allow("второй", "телефон", 1) {
		t.Fatal("устройство одного ключа заняло слот другого")
	}
}

func TestNoLimitAndNoDeviceIDAreLetThrough(t *testing.T) {
	d := OpenDevices(t.TempDir() + "/devices.json")

	for i := range 10 {
		if !d.Allow("токен", string(rune('а'+i)), 0) {
			t.Fatal("без лимита кого-то не пустили")
		}
	}
	if n := d.Count("токен"); n != 0 {
		t.Fatalf("без лимита ничего не должно записываться, записано %d", n)
	}
	// A client that says nothing about itself cannot be counted, and must not
	// be refused for it.
	d.Allow("другой", "телефон", 1)
	if !d.Allow("другой", "", 1) {
		t.Fatal("клиента без идентификатора не пустили")
	}
}

func TestDeviceSlotIsFreedAfterTheWindow(t *testing.T) {
	d := OpenDevices(t.TempDir() + "/devices.json")
	d.Allow("токен", "старый", 1)

	d.seen[d.fingerprint("токен")][d.fingerprint("старый")] = time.Now().Add(-DeviceWindow - time.Hour)

	if !d.Allow("токен", "новый", 1) {
		t.Fatal("слот не освободился по истечении срока")
	}
	if n := d.Count("токен"); n != 1 {
		t.Fatalf("после замены устройств %d, ожидалось 1", n)
	}
}

func TestForgetClearsOneSubscription(t *testing.T) {
	d := OpenDevices(t.TempDir() + "/devices.json")
	d.Allow("первый", "телефон", 1)
	d.Allow("второй", "телефон", 1)

	d.Forget("первый")

	if n := d.Count("первый"); n != 0 {
		t.Fatalf("после сброса осталось %d устройств", n)
	}
	if n := d.Count("второй"); n != 1 {
		t.Fatal("сброс задел чужой ключ")
	}
}

func TestDevicesSurviveRestart(t *testing.T) {
	path := t.TempDir() + "/devices.json"
	first := OpenDevices(path)
	first.Allow("токен", "телефон", 2)
	first.Allow("токен", "ноут", 2)

	second := OpenDevices(path)
	if n := second.Count("токен"); n != 2 {
		t.Fatalf("после перезапуска устройств %d, ожидалось 2", n)
	}
	if second.Allow("токен", "чужой", 2) {
		t.Fatal("после перезапуска лимит перестал действовать")
	}
	// Already known devices must still be recognised: a new salt would have
	// turned each of them into a stranger.
	if !second.Allow("токен", "телефон", 2) {
		t.Fatal("после перезапуска своё же устройство не узнали")
	}
}

func TestRawDeviceIDIsNeverWritten(t *testing.T) {
	path := t.TempDir() + "/devices.json"
	d := OpenDevices(path)
	d.Allow("секретный-токен", "мой-телефон-12345", 1)

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"секретный-токен", "мой-телефон-12345"} {
		if strings.Contains(string(b), secret) {
			t.Fatalf("в файле оказалось «%s» в открытом виде", secret)
		}
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Fatalf("права на файле %v, ожидалось 0600", fi.Mode().Perm())
	}
}

func TestDeviceIDComesFromTheUsualHeaders(t *testing.T) {
	for _, h := range []string{"x-hwid", "X-Device-Id", "hwid"} {
		r, _ := http.NewRequest(http.MethodGet, "/sub/токен", nil)
		r.Header.Set(h, "устройство")
		if got := deviceID(r); got != "устройство" {
			t.Fatalf("заголовок %s: получено %q", h, got)
		}
	}
	r, _ := http.NewRequest(http.MethodGet, "/sub/токен", nil)
	if deviceID(r) != "" {
		t.Fatal("идентификатор взялся из ниоткуда")
	}
}
