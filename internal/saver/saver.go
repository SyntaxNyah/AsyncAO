// Package saver writes small files in the background: a change schedules one write a short while later, and a write
// replaces the file whole, so a reader never sees half of one.
package saver

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	// FilePerm is the mode of a saved file.
	FilePerm = 0o644
	// DirPerm is the mode of a folder made for one.
	DirPerm = 0o755
)

// Saver writes one file a delay after the first change, off the caller's thread, so a burst of changes costs one write.
// The zero path saves nothing, for stores that live only in memory.
type Saver struct {
	path   string
	delay  time.Duration
	encode func() ([]byte, error)

	// writing keeps writes in order, so an older snapshot never lands after a newer one.
	writing sync.Mutex

	mu      sync.Mutex
	pending *time.Timer
	closed  bool
}

// New saves to path what encode returns; encode runs on the saver's goroutine and must take its own snapshot.
func New(path string, delay time.Duration, encode func() ([]byte, error)) *Saver {
	return &Saver{path: path, delay: delay, encode: encode}
}

// Changed schedules a write, unless one is already waiting; it never blocks on the disk.
func (s *Saver) Changed() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.pending != nil || s.path == "" {
		return
	}
	s.pending = time.AfterFunc(s.delay, func() {
		s.mu.Lock()
		s.pending = nil
		s.mu.Unlock()
		_ = s.Flush()
	})
}

// Discard drops a waiting write, for a store whose file is about to be deleted.
func (s *Saver) Discard() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pending != nil {
		s.pending.Stop()
		s.pending = nil
	}
}

// Flush writes now, on the caller's thread.
func (s *Saver) Flush() error {
	if s.path == "" {
		return nil
	}
	s.writing.Lock()
	defer s.writing.Unlock()
	data, err := s.encode()
	if err != nil {
		return err
	}
	return WriteFile(s.path, data)
}

// Close drops the waiting write, writes once more and schedules nothing after.
func (s *Saver) Close() error {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	s.Discard()
	return s.Flush()
}

// WriteFile replaces path with data through a synced temporary file in the same folder.
func WriteFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, DirPerm); err != nil {
		return fmt.Errorf("saver: creating %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("saver: creating a temporary file: %w", err)
	}
	tmpName := tmp.Name()
	fail := func(err error) error {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		return fail(fmt.Errorf("saver: writing %s: %w", tmpName, err))
	}
	if err := tmp.Sync(); err != nil {
		return fail(fmt.Errorf("saver: syncing %s: %w", tmpName, err))
	}
	if err := tmp.Close(); err != nil {
		return fail(fmt.Errorf("saver: closing %s: %w", tmpName, err))
	}
	// CreateTemp makes the file 0600; widen it where the OS allows.
	_ = os.Chmod(tmpName, FilePerm)
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("saver: replacing %s: %w", path, err)
	}
	return nil
}
