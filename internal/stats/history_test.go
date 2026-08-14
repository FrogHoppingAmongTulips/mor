package stats

import (
	"os"
	"testing"
	"time"
)

func TestHistoryBucketsByHour(t *testing.T) {
	h, err := OpenHistory(t.TempDir() + "/history.json")
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 8, 2, 10, 0, 0, 0, time.UTC)
	h.Add("k", 100, base)
	h.Add("k", 50, base.Add(30*time.Minute)) // same hour
	h.Add("k", 200, base.Add(time.Hour))     // next hour

	got := h.Series([]string{"k"}, base.Add(-time.Hour), base.Add(2*time.Hour), time.Hour)
	if len(got) != 2 {
		t.Fatalf("точек %d, ждали 2: %+v", len(got), got)
	}
	if got[0].Bytes != 150 {
		t.Errorf("первый час = %d, ждали 150", got[0].Bytes)
	}
	if got[1].Bytes != 200 {
		t.Errorf("второй час = %d, ждали 200", got[1].Bytes)
	}
	if !got[0].At.Before(got[1].At) {
		t.Error("точки идут не по порядку")
	}
}

// One person holds a key per protocol; a chart of that person is the sum.
func TestHistorySumsKeysOfOnePerson(t *testing.T) {
	h, _ := OpenHistory(t.TempDir() + "/history.json")
	at := time.Date(2026, 8, 2, 10, 0, 0, 0, time.UTC)
	h.Add("hy2", 100, at)
	h.Add("reality", 300, at)

	got := h.Series([]string{"hy2", "reality"}, at.Add(-time.Hour), at.Add(time.Hour), time.Hour)
	if len(got) != 1 || got[0].Bytes != 400 {
		t.Errorf("сумма по протоколам = %+v, ждали 400 одной точкой", got)
	}
}

// A month is drawn by day, not by hour.
func TestHistoryDailyStep(t *testing.T) {
	h, _ := OpenHistory(t.TempDir() + "/history.json")
	day := time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC)
	h.Add("k", 10, day.Add(2*time.Hour))
	h.Add("k", 20, day.Add(20*time.Hour))
	h.Add("k", 5, day.Add(26*time.Hour))

	got := h.Series([]string{"k"}, day, day.Add(48*time.Hour), 24*time.Hour)
	if len(got) != 2 {
		t.Fatalf("дней %d, ждали 2: %+v", len(got), got)
	}
	if got[0].Bytes != 30 || got[1].Bytes != 5 {
		t.Errorf("по дням = %d и %d, ждали 30 и 5", got[0].Bytes, got[1].Bytes)
	}
}

func TestHistoryWindowExcludesOutside(t *testing.T) {
	h, _ := OpenHistory(t.TempDir() + "/history.json")
	at := time.Date(2026, 8, 2, 10, 0, 0, 0, time.UTC)
	h.Add("k", 100, at.Add(-5*time.Hour))
	h.Add("k", 100, at)

	got := h.Series([]string{"k"}, at.Add(-time.Hour), at.Add(time.Hour), time.Hour)
	if len(got) != 1 {
		t.Errorf("в окно попало %d точек, ждали 1: %+v", len(got), got)
	}
}

func TestHistorySurvivesReload(t *testing.T) {
	path := t.TempDir() + "/history.json"
	h, _ := OpenHistory(path)
	at := time.Now().Add(-2 * time.Hour)
	h.Add("k", 4096, at)
	if err := h.Save(); err != nil {
		t.Fatal(err)
	}

	fresh, err := OpenHistory(path)
	if err != nil {
		t.Fatal(err)
	}
	got := fresh.Series([]string{"k"}, at.Add(-time.Hour), time.Now(), time.Hour)
	if len(got) != 1 || got[0].Bytes != 4096 {
		t.Errorf("после перезагрузки = %+v", got)
	}
}

// Old points go away on their own, or the file grows until the disk is full.
func TestHistoryPrunesOldPoints(t *testing.T) {
	path := t.TempDir() + "/history.json"
	h, _ := OpenHistory(path)
	h.Add("k", 100, time.Now().Add(-40*24*time.Hour)) // старше месяца
	h.Add("k", 200, time.Now().Add(-2*time.Hour))
	if err := h.Save(); err != nil {
		t.Fatal(err)
	}

	fresh, _ := OpenHistory(path)
	got := fresh.Series([]string{"k"}, time.Now().Add(-60*24*time.Hour), time.Now(), time.Hour)
	if len(got) != 1 || got[0].Bytes != 200 {
		t.Errorf("старая точка не убрана: %+v", got)
	}
}

func TestHistoryDeleteRemovesKey(t *testing.T) {
	h, _ := OpenHistory(t.TempDir() + "/history.json")
	at := time.Now()
	h.Add("k", 100, at)
	h.Delete("k")

	if got := h.Series([]string{"k"}, at.Add(-time.Hour), at.Add(time.Hour), time.Hour); len(got) != 0 {
		t.Errorf("удалённый ключ остался в истории: %+v", got)
	}
}

// A damaged file must not stop the VPN: it holds pictures, not keys.
func TestHistoryIgnoresGarbageFile(t *testing.T) {
	path := t.TempDir() + "/history.json"
	if err := os.WriteFile(path, []byte("{ битый файл"), 0o600); err != nil {
		t.Fatal(err)
	}
	h, err := OpenHistory(path)
	if err != nil {
		t.Fatalf("битый файл истории не должен быть ошибкой: %v", err)
	}
	h.Add("k", 1, time.Now())
}
