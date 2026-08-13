package fsutil

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteAtomicWritesAndCleansUp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "file.json")

	if err := WriteAtomic(path, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hello" {
		t.Errorf("content = %q, want %q", got, "hello")
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Error("temp file was not cleaned up by the rename")
	}
}

func TestWriteAtomicDirMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "world-readable", "config.json")

	if err := WriteAtomicDir(path, []byte("x"), 0o755, 0o644); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o755 {
		t.Errorf("dir mode = %v, want 0755 — Xray/Hysteria2 run as their own user and must be able to read this", fi.Mode().Perm())
	}
}

// A second process — one of mor's own systemd services rewriting the same
// file — must be picked up on the next read without the caller re-parsing on
// every single call.
func TestFileStateChanged(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data.json")
	if err := os.WriteFile(path, []byte("v1"), 0o600); err != nil {
		t.Fatal(err)
	}

	var fs FileState
	b, changed := fs.Changed(path)
	if !changed || string(b) != "v1" {
		t.Fatalf("first read: changed=%v b=%q, want true/\"v1\"", changed, b)
	}

	if _, changed := fs.Changed(path); changed {
		t.Error("reported changed on an untouched file")
	}

	// A rewrite in the same second can share a modtime with the file that
	// already exists on some filesystems, so size is what still catches it.
	if err := os.WriteFile(path, []byte("v2-longer"), 0o600); err != nil {
		t.Fatal(err)
	}
	b, changed = fs.Changed(path)
	if !changed || string(b) != "v2-longer" {
		t.Fatalf("after rewrite: changed=%v b=%q, want true/\"v2-longer\"", changed, b)
	}
}

func TestFileStateRemember(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data.json")
	if err := os.WriteFile(path, []byte("v1"), 0o600); err != nil {
		t.Fatal(err)
	}

	var fs FileState
	fs.Remember(path)
	if _, changed := fs.Changed(path); changed {
		t.Error("Changed reported true right after Remember, on the same file")
	}

	// Remember on a file that does not exist yet must not panic, and must
	// leave the state such that the file, once it appears, reads as changed.
	var fs2 FileState
	missing := filepath.Join(dir, "missing.json")
	fs2.Remember(missing)
	if err := os.WriteFile(missing, []byte("v1"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, changed := fs2.Changed(missing); !changed {
		t.Error("file created after a no-op Remember was not seen as changed")
	}
}
