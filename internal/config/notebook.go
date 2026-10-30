package config

// Case notebook: one JSON file per server, loaded off the render thread, flushed async (hard rule 2).

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	// NotebookDirName holds the per-server files inside the config directory.
	NotebookDirName = "notebooks"
	// notebookLineCap bounds one notebook; past it the oldest pins fall off.
	notebookLineCap = 256
	// notebookLineMax truncates one pinned line.
	notebookLineMax = 512
	// notebookSaveDelay debounces flushes: a pin spree costs one write.
	notebookSaveDelay = 1 * time.Second
	// notebookKeyMax bounds the readable part of the file name; a hash suffix keeps servers distinct after truncation.
	notebookKeyMax = 48
)

// Notebook is safe for concurrent use; the flush goroutine is the only disk writer.
type Notebook struct {
	mu    sync.Mutex
	path  string
	lines []string
	timer *time.Timer
	// rev bumps on every mutation so the UI can cache its Lines() snapshot instead of copying per frame.
	rev int64
}

// Rev reports the mutation revision, the UI's cache key.
func (n *Notebook) Rev() int64 {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.rev
}

// Clear also stops the pending flush, which would otherwise recreate the file WipeNotebooks deletes.
func (n *Notebook) Clear() {
	n.mu.Lock()
	n.lines = nil
	if n.timer != nil {
		n.timer.Stop()
		n.timer = nil
	}
	n.rev++
	n.mu.Unlock()
}

// notebookJSON is the on-disk shape.
type notebookJSON struct {
	Lines []string `json:"lines"`
}

// NotebookPath maps a server key to its file: readable prefix + FNV hash so truncation can't collide two servers.
func NotebookPath(serverKey string) (string, error) {
	base, err := DefaultPath()
	if err != nil {
		return "", err
	}
	clean := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		default:
			return '-'
		}
	}, serverKey)
	if len(clean) > notebookKeyMax {
		clean = clean[:notebookKeyMax]
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(serverKey))
	name := fmt.Sprintf("%s-%08x.json", clean, h.Sum32())
	return filepath.Join(filepath.Dir(base), NotebookDirName, name), nil
}

func NotebookDir() (string, error) {
	base, err := DefaultPath()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(base), NotebookDirName), nil
}

// WipeNotebooks deletes every server's notebook for the factory reset; Reset settings leaves them.
func WipeNotebooks() error {
	dir, err := NotebookDir()
	if err != nil {
		return err
	}
	return os.RemoveAll(dir)
}

// LoadNotebook reads or initializes a server's notebook; disk I/O, so call it off the render thread.
func LoadNotebook(serverKey string) (*Notebook, error) {
	path, err := NotebookPath(serverKey)
	if err != nil {
		return nil, err
	}
	return loadNotebookFile(path), nil
}

// loadNotebookFile treats an absent or corrupt file as a fresh notebook.
func loadNotebookFile(path string) *Notebook {
	n := &Notebook{path: path}
	data, err := os.ReadFile(path)
	if err != nil {
		return n
	}
	var onDisk notebookJSON
	if json.Unmarshal(data, &onDisk) == nil {
		if len(onDisk.Lines) > notebookLineCap {
			onDisk.Lines = onDisk.Lines[len(onDisk.Lines)-notebookLineCap:]
		}
		n.lines = onDisk.Lines
	}
	return n
}

// Add pins one line, truncated, dropping the oldest past the cap, and schedules a flush.
func (n *Notebook) Add(line string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}
	if len(line) > notebookLineMax {
		line = line[:notebookLineMax]
	}
	n.mu.Lock()
	n.lines = append(n.lines, line)
	if len(n.lines) > notebookLineCap {
		n.lines = n.lines[len(n.lines)-notebookLineCap:]
	}
	n.rev++
	n.flushSoonLocked()
	n.mu.Unlock()
}

// Remove deletes the i-th line, no-op out of range, and schedules a flush.
func (n *Notebook) Remove(i int) {
	n.mu.Lock()
	if i >= 0 && i < len(n.lines) {
		n.lines = append(n.lines[:i], n.lines[i+1:]...)
		n.rev++
		n.flushSoonLocked()
	}
	n.mu.Unlock()
}

// Lines returns a snapshot for drawing; callers must not mutate it.
func (n *Notebook) Lines() []string {
	n.mu.Lock()
	out := make([]string, len(n.lines))
	copy(out, n.lines)
	n.mu.Unlock()
	return out
}

// Len reports the pin count without copying.
func (n *Notebook) Len() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.lines)
}

// flushSoonLocked rearms the debounce timer; the caller holds n.mu.
func (n *Notebook) flushSoonLocked() {
	if n.timer != nil {
		n.timer.Stop()
	}
	n.timer = time.AfterFunc(notebookSaveDelay, func() { _ = n.Flush() })
}

// Flush writes atomically (tmp + rename); also called on disconnect so nothing rides only on the debounce.
func (n *Notebook) Flush() error {
	n.mu.Lock()
	snapshot := notebookJSON{Lines: append([]string(nil), n.lines...)}
	path := n.path
	n.mu.Unlock()

	data, err := json.MarshalIndent(snapshot, jsonMarshalPrefix, jsonMarshalIndent)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), prefsDirPerm); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, path)
}
