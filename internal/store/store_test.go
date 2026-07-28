package store

import "testing"

func TestSetSNI(t *testing.T) {
	s, err := Open(t.TempDir() + "/users.json")
	if err != nil {
		t.Fatal(err)
	}
	u, err := s.Add(&User{Name: "phone"})
	if err != nil {
		t.Fatal(err)
	}

	if err := s.SetSNI(u.ID, "kernel.org"); err != nil {
		t.Fatal(err)
	}
	if got := s.List()[0]; got.SNI != "kernel.org" {
		t.Fatalf("SNI = %q", got.SNI)
	}

	if err := s.SetSNI(u.ID, ""); err != nil {
		t.Fatal(err)
	}
	if got := s.List()[0]; got.SNI != "" {
		t.Fatalf("SNI should have been cleared, got %q", got.SNI)
	}

	if err := s.SetSNI("nope", "kernel.org"); err == nil {
		t.Fatal("expected ErrNotFound")
	}

	if err := s.SetSNI(u.ID, "www.apple.com"); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(s.path)
	if err != nil {
		t.Fatal(err)
	}
	if got := s2.List()[0]; got.Name != "phone" || got.SNI != "www.apple.com" {
		t.Fatalf("after reload: %+v", got)
	}
}

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
