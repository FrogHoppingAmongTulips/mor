package store

import (
	"os"
	"strings"
	"testing"
)

func TestFindByHyTokenIgnoresOtherProtos(t *testing.T) {
	s, err := Open(t.TempDir() + "/users.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Add(&User{Name: "reality", Proto: ProtoReality, UUID: "uuid", HyToken: "shared"}); err != nil {
		t.Fatal(err)
	}
	if s.FindByHyToken("shared") != nil {
		t.Fatal("a key of another protocol authorized over Hysteria2")
	}
	if s.FindByHyToken("") != nil {
		t.Fatal("an empty token must never match")
	}
}

// A key without a deadline must not claim to have expired in year one: the file
// is read by people and by scripts, and a zero timestamp misleads both.
func TestNoDeadlineWritesNothing(t *testing.T) {
	path := t.TempDir() + "/users.json"
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Add(&User{Name: "телефон", Proto: ProtoHy2, HyToken: "t"}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "0001-01-01") {
		t.Errorf("бессрочный ключ записан как истёкший:\n%s", raw)
	}
	if s.List()[0].Expired() {
		t.Error("бессрочный ключ считается истёкшим")
	}
}

// A name is whatever a person typed. It travels into the store, the engine
// configs and every profile format, so the store has to keep it byte for byte
// — a name quietly rewritten on save would break the link that was already
// handed out, and a name that breaks the file would take every other key with
// it.
func TestNamesSurviveASaveAndReload(t *testing.T) {
	path := t.TempDir() + "/users.json"
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}

	names := []string{
		`обычное`, `с "кавычками"`, "с\nпереводом", "с\tтабом",
		`{"json":true}`, `- yaml: список`, `../../etc/passwd`,
		`очень длинное ` + strings.Repeat("имя ", 60),
		`emoji 🙃`, `  пробелы по краям  `,
	}
	ids := make([]string, 0, len(names))
	for _, n := range names {
		u, err := s.Add(&User{Name: n, Proto: ProtoHy2})
		if err != nil {
			t.Fatalf("имя %q не принялось: %v", n, err)
		}
		ids = append(ids, u.ID)
	}

	again, err := Open(path)
	if err != nil {
		t.Fatalf("файл с такими именами не читается обратно: %v", err)
	}
	if len(again.List()) != len(names) {
		t.Fatalf("после перечитывания ключей %d, было %d", len(again.List()), len(names))
	}
	byID := map[string]*User{}
	for _, u := range again.List() {
		byID[u.ID] = u
	}
	for i, id := range ids {
		u := byID[id]
		if u == nil {
			t.Errorf("ключ %q пропал", names[i])
			continue
		}
		if u.Name != names[i] {
			t.Errorf("имя изменилось: было %q, стало %q", names[i], u.Name)
		}
	}
}

// Протокол убрали из mor, но ключи под ним остались в файле у тех, кто
// обновляется. Такой ключ виден в списке, считается в лимитах и не попадает ни
// в один конфиг движка — то есть выглядит рабочим и не подключает никого.
func TestRetiredProtocolIsNotKnown(t *testing.T) {
	for _, p := range []string{ProtoHy2, ProtoReality, ProtoSS} {
		if !Known(p) {
			t.Errorf("рабочий протокол %q объявлен неизвестным", p)
		}
	}
	for _, p := range []string{"enc", "", "vmess", "trojan"} {
		if Known(p) {
			t.Errorf("протокол %q не поддерживается, но считается известным", p)
		}
	}
}
