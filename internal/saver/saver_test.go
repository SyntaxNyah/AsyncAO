package saver

import (
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

const (
	testDelay   = 20 * time.Millisecond
	testTimeout = 2 * time.Second
	burst       = 50
)

// counter is a store whose encode counts its writes and saves its current value.
type counter struct {
	value  atomic.Int64
	writes atomic.Int64
}

func (c *counter) encode() ([]byte, error) {
	c.writes.Add(1)
	return []byte(strconv.FormatInt(c.value.Load(), 10)), nil
}

func readBack(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(testTimeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(testDelay)
	}
}

func TestBurstOfChangesIsOneWriteOfTheLatestValue(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.json")
	var c counter
	s := New(path, testDelay, c.encode)
	for i := range burst {
		c.value.Store(int64(i))
		s.Changed()
	}
	waitFor(t, func() bool { return c.writes.Load() > 0 })
	time.Sleep(testDelay * 2)
	if n := c.writes.Load(); n != 1 {
		t.Errorf("%d writes, want 1", n)
	}
	if got := readBack(t, path); got != strconv.Itoa(burst-1) {
		t.Errorf("file holds %q, want %d", got, burst-1)
	}
}

func TestCloseWritesAndSchedulesNothingAfter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "store.json")
	var c counter
	s := New(path, time.Hour, c.encode)
	c.value.Store(burst)
	s.Changed()
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if got := readBack(t, path); got != strconv.Itoa(burst) {
		t.Errorf("file holds %q", got)
	}
	s.Changed()
	time.Sleep(testDelay)
	if n := c.writes.Load(); n != 1 {
		t.Errorf("%d writes after Close, want 1", n)
	}
}

func TestCloseWithoutAChangeWritesNothing(t *testing.T) {
	var c counter
	s := New(filepath.Join(t.TempDir(), "store.json"), testDelay, c.encode)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if n := c.writes.Load(); n != 0 {
		t.Errorf("%d writes with nothing changed", n)
	}
}

func TestDiscardDropsTheWaitingWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.json")
	var c counter
	s := New(path, testDelay, c.encode)
	s.Changed()
	s.Discard()
	time.Sleep(testDelay * 3)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("file written after Discard: %v", err)
	}
}

func TestNoPathSavesNothing(t *testing.T) {
	var c counter
	s := New("", testDelay, c.encode)
	s.Changed()
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if n := c.writes.Load(); n != 0 {
		t.Errorf("%d writes without a path", n)
	}
}

func TestWriteFileReplacesAndLeavesNoTemporaryFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "store.json")
	for _, body := range []string{"first", "second"} {
		if err := WriteFile(path, []byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if got := readBack(t, path); got != "second" {
		t.Errorf("file holds %q", got)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("folder holds %d files, want 1", len(entries))
	}
}
