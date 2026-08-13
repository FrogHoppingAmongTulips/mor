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
