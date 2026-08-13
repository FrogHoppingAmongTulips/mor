// Package fsutil holds the two file tricks every store in mor needs and used
// to reimplement on its own: writing so a crash mid-save can't corrupt what
// was there before, and noticing when a file changed under a process that
// holds no lock on it.
package fsutil

import (
	"os"
	"path/filepath"
	"time"
)

// WriteAtomic writes data to path via a temp file plus rename, so a reader
// never observes a half-written file and a crash mid-write leaves the old
// content intact. The containing directory is created private (0700): this is
// the shape every one of mor's own JSON stores wants.
func WriteAtomic(path string, data []byte, perm os.FileMode) error {
	return WriteAtomicDir(path, data, 0o700, perm)
}

// WriteAtomicDir is WriteAtomic with the directory mode spelled out, for the
// configs of engines mor does not own the readership of — Xray and Hysteria2
// run as their own users and need to be able to read what mor writes them.
func WriteAtomicDir(path string, data []byte, dirPerm, filePerm os.FileMode) error {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, dirPerm); err != nil {
			return err
		}
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, filePerm); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// FileState remembers a file's modtime and size so a second process's writes
// — mor's own systemd services edit these files too — can be picked up
// without re-reading and re-parsing on every call.
type FileState struct {
	modTime time.Time
	size    int64
}

// Changed reads path if it looks different from what was last remembered.
// The empty read on a stat failure or an unchanged file is not an error: the
// caller just keeps what it already has.
func (fs *FileState) Changed(path string) ([]byte, bool) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, false
	}
	if fi.ModTime().Equal(fs.modTime) && fi.Size() == fs.size {
		return nil, false
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	fs.modTime, fs.size = fi.ModTime(), fi.Size()
	return b, true
}

// Remember records path's current modtime and size without reading it, for
// right after this process itself wrote it.
func (fs *FileState) Remember(path string) {
	if fi, err := os.Stat(path); err == nil {
		fs.modTime, fs.size = fi.ModTime(), fi.Size()
	}
}
