package assets

import (
	"strings"
	"sync"
)

const (
	// sceneryPinFolders is how many rooms keep their scenery pinned at once: the active one and a split view's.
	sceneryPinFolders = 2
	// sceneryPinMaxFiles bounds the pinned files: about 8 positions with a background and desk each, per room.
	sceneryPinMaxFiles = 64
	// sceneryPinMaxBytes bounds the pinned bytes; compressed backgrounds are rarely over a few MB.
	sceneryPinMaxBytes = 64 << 20
)

// sceneryPin keeps the file bytes of a room's background folder in memory until the room's background changes, as its
// positions come back every few messages and the shared byte cache would push them out.
type sceneryPin struct {
	mu      sync.Mutex
	folders [sceneryPinFolders]string
	next    int
	files   map[string][]byte
	bytes   int64
}

func newSceneryPin() *sceneryPin {
	return &sceneryPin{files: map[string][]byte{}}
}

// pin starts keeping folder's files, dropping those of the folder pinned longest ago.
func (p *sceneryPin) pin(folder string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, f := range p.folders {
		if f == folder {
			return
		}
	}
	if old := p.folders[p.next]; old != "" {
		for file, data := range p.files {
			if strings.HasPrefix(file, old) {
				p.bytes -= int64(len(data))
				delete(p.files, file)
			}
		}
	}
	p.folders[p.next] = folder
	p.next = (p.next + 1) % sceneryPinFolders
}

// pinned reports whether file is under a pinned folder; the caller holds mu.
func (p *sceneryPin) pinned(file string) bool {
	for _, f := range p.folders {
		if f != "" && strings.HasPrefix(file, f) {
			return true
		}
	}
	return false
}

// keep stores file's bytes if it is under a pinned folder and both caps allow.
func (p *sceneryPin) keep(file string, data []byte) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.pinned(file) {
		return
	}
	if _, ok := p.files[file]; ok {
		return
	}
	if len(p.files) >= sceneryPinMaxFiles || p.bytes+int64(len(data)) > sceneryPinMaxBytes {
		return
	}
	p.files[file] = data
	p.bytes += int64(len(data))
}

func (p *sceneryPin) has(file string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	_, ok := p.files[file]
	return ok
}

func (p *sceneryPin) get(file string) ([]byte, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	data, ok := p.files[file]
	return data, ok
}

// PinScenery keeps the files under folder, a room's background folder, in memory until another room's replaces it.
func (m *Manager) PinScenery(folder string) {
	if m == nil || m.bridge == nil || folder == "" {
		return
	}
	m.bridge.pin.pin(folder)
}
