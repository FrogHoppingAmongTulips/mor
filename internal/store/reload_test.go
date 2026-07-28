package store

import (
	"os"
	"testing"
)

func TestSeesExternalAdd(t *testing.T) {
	path := t.TempDir() + "/users.json"
	daemon, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(daemon.List()) != 0 {
		t.Fatal("expected an empty store")
	}

	cli, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cli.Add(&User{Name: "console key", HyToken: "tok-from-cli"}); err != nil {
		t.Fatal(err)
	}

	got := daemon.FindByHyToken("tok-from-cli")
	if got == nil {
		t.Fatal("daemon missed a key added by another process: clients get 403 until restart")
	}
	if got.Name != "console key" {
		t.Fatalf("name = %q", got.Name)
	}
}

func TestSeesExternalDelete(t *testing.T) {
	path := t.TempDir() + "/users.json"
	cli, _ := Open(path)
	u, _ := cli.Add(&User{Name: "key", HyToken: "tok"})

	daemon, _ := Open(path)
	if daemon.FindByHyToken("tok") == nil {
		t.Fatal("key should work before deletion")
	}

	if err := cli.Delete(u.ID); err != nil {
		t.Fatal(err)
	}
	if got := daemon.FindByHyToken("tok"); got != nil {
		t.Fatal("daemon still admits a deleted key")
	}
}

func TestBrokenFileKeepsMemory(t *testing.T) {
	path := t.TempDir() + "/users.json"
	cli, _ := Open(path)
	_, _ = cli.Add(&User{Name: "key", HyToken: "tok"})

	daemon, _ := Open(path)
	if daemon.FindByHyToken("tok") == nil {
		t.Fatal("setup: key should work")
	}

	if err := os.WriteFile(path, []byte("{ not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if daemon.FindByHyToken("tok") == nil {
		t.Fatal("a broken file wiped the list and revoked every key")
	}
}

func TestProtoDefaults(t *testing.T) {
	path := t.TempDir() + "/users.json"
	old := `[{"id":"x","name":"old","hy_token":"tok","created_at":"2026-01-01T00:00:00Z"}]`
	if err := os.WriteFile(path, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.List()[0]; got.Proto != ProtoHy2 {
		t.Fatalf("proto = %q, want %q", got.Proto, ProtoHy2)
	}
	if s.FindByHyToken("tok") == nil {
		t.Fatal("a key from an older version stopped working")
	}
}
