package assets

// The player's folders and .zip packs, as the media chain's source between the disk cache and the web.

import (
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/media"
)

// folderBadCap bounds the folder files set aside after a decode failure (hard rule 4).
const folderBadCap = 256

// bridgeFolders is media.Folders with the files whose decode failed set aside, so the server's copy is fetched.
type bridgeFolders struct {
	*media.Folders
	m   *Manager
	bad quarantine
}

func newBridgeFolders(m *Manager) *bridgeFolders {
	return &bridgeFolders{Folders: media.NewFolders(time.Now), m: m}
}

// Data serves a file unless its decode failed; a hit counts as served from the folders.
func (f *bridgeFolders) Data(file media.File) ([]byte, error) {
	if f.bad.has(string(file)) {
		return nil, media.ErrAbsent
	}
	b, err := f.Folders.Data(file)
	if err == nil {
		f.m.mountFetches.Add(1)
	}
	return b, err
}

// read is Data for the synchronous fetch paths; workers only.
func (f *bridgeFolders) read(url string) ([]byte, bool) {
	b, err := f.Data(media.File(url))
	return b, err == nil
}

// SetFolders mounts the player's folders and .zip packs, in order, for their local:// origin and for each server
// asset origin given, when they layer over streaming. It reads nothing, so it is safe on the render thread.
func (m *Manager) SetFolders(mounts, serverOrigins []string) {
	f := m.bridge.folders
	before := f.Mounts()
	var origins []string
	if len(mounts) > 0 {
		origins = append([]string{LocalOriginFor(mounts)}, serverOrigins...)
	}
	f.Mount(mounts, origins)
	if slices.Equal(before, f.Mounts()) {
		return
	}
	f.bad.reset() // the files set aside belonged to the old folders
	// The folders may now hold what was missing; the web's 404 memory stays (hard rule 6).
	m.bridge.forgetMissingAssets()
}

// RecheckFolders makes the next request for each folder look at the disk again and asks again for assets
// remembered as missing, for when the player may have just changed the folders. The web's 404 memory stays.
func (m *Manager) RecheckFolders() {
	m.bridge.folders.Stale()
	m.bridge.forgetMissingAssets()
}

// FolderStatus reports the mounted folders, how many folders have been read, and the mounts that could not open.
func (m *Manager) FolderStatus() (mounts []string, folders int, errs []error) {
	f := m.bridge.folders
	return f.Mounts(), f.Size().Entries, f.Errors()
}

// ChangedFolderFiles reports the files read from the player's folders whose copy on disk changed or went away since,
// releasing any set aside after a failed decode so the new copy is tried. It stats every file read, so call it off
// the render thread.
func (m *Manager) ChangedFolderFiles() []string {
	f := m.bridge.folders
	changed := f.Changed()
	urls := make([]string, 0, len(changed))
	for _, file := range changed {
		f.bad.forget(string(file))
		urls = append(urls, string(file))
	}
	return urls
}

// PackCovers reports whether the player's folders hold any format of base; it may read the disk, so workers only.
func (m *Manager) PackCovers(base string) bool { return m.bridge.folders.FindStem(base) }

// PackHeld is PackCovers from the folders already read, with no I/O, so on any thread.
func (m *Manager) PackHeld(base string) bool { return m.bridge.folders.HeldStem(base) }

// QuarantinePack sets aside a folder file whose decode failed, so the server's copy is fetched; false means it is
// not a folder file or is already set aside, and the caller must then record nothing.
func (m *Manager) QuarantinePack(url string) bool {
	f := m.bridge.folders
	if _, ok := f.RelOf(url); !ok || !f.bad.mark(url) {
		return false
	}
	m.packQuarantined.Add(1)
	return true
}

// quarantine is the bounded set of folder files whose decode failed, written by render, read by workers.
type quarantine struct {
	n   atomic.Int64
	mu  sync.Mutex
	set map[string]struct{}
	// ring records insertion order for oldest-first eviction.
	ring [folderBadCap]string
	seq  int
}

// has is the hot path: an empty quarantine costs one atomic load, with no lock or allocation.
func (q *quarantine) has(url string) bool {
	if q.n.Load() == 0 {
		return false
	}
	q.mu.Lock()
	_, bad := q.set[url]
	q.mu.Unlock()
	return bad
}

// mark inserts, evicting the oldest at the cap; true only on a new insert, so callers log once per file.
func (q *quarantine) mark(url string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.set == nil {
		q.set = make(map[string]struct{})
	}
	if _, dup := q.set[url]; dup {
		return false
	}
	if len(q.set) >= folderBadCap {
		if old := q.ring[q.seq%folderBadCap]; old != "" {
			delete(q.set, old)
		}
	}
	q.set[url] = struct{}{}
	q.ring[q.seq%folderBadCap] = url
	q.seq++
	q.n.Store(int64(len(q.set)))
	return true
}

func (q *quarantine) reset() {
	q.mu.Lock()
	defer q.mu.Unlock()
	clear(q.set)
	q.ring = [folderBadCap]string{}
	q.seq = 0
	q.n.Store(0)
}

// forget takes one file out, so its next read is tried again; its ring slot empties when overwritten.
func (q *quarantine) forget(url string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	delete(q.set, url)
	q.n.Store(int64(len(q.set)))
}
