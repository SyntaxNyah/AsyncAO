package config

// Jukebox: a GLOBAL library of music links (the URLs DJs /play in OOC on AO
// servers) organized into named playlists. Stored in its OWN async-written
// file — not the prefs blob — because it can hold tens of thousands of links
// and must never re-serialize on an unrelated settings change (a volume-slider
// drag marks prefs dirty). One JSON under <prefsdir>/jukebox.json; loads
// happen off the render thread, writes go through a debounced async flush (the
// render thread never touches the disk, spec §17.2). Global by design — your
// music collection travels across every server.

import (
	"encoding/json"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	// JukeboxFileName is the global jukebox library beside the prefs file.
	JukeboxFileName = "jukebox.json"
	// jukeboxMaxPlaylists caps the number of playlists (rule §17.4).
	jukeboxMaxPlaylists = 200
	// jukeboxMaxEntries caps the TOTAL links across all playlists — high
	// enough for the "tens of thousands" use case, bounded so the file and
	// the keybind scan stay sane.
	jukeboxMaxEntries = 50000
	// Length caps for one playlist name / entry title / URL.
	jukeboxMaxNameLen  = 100
	jukeboxMaxTitleLen = 256
	jukeboxMaxURLLen   = 2048
	// jukeboxSaveDelay debounces flushes: an add spree costs one write.
	jukeboxSaveDelay = 1 * time.Second
)

// JukeboxEntry is one stored music link. Key, when set, is a bare-key bind
// (no modifier) that /plays this entry from the courtroom.
type JukeboxEntry struct {
	Title string `json:"title,omitempty"`
	URL   string `json:"url"`
	Key   string `json:"key,omitempty"`
}

// Playlist is a named folder of links. Key, when set, is a bare-key bind that
// /plays a RANDOM entry from this playlist (shuffle).
type Playlist struct {
	Name    string         `json:"name"`
	Key     string         `json:"key,omitempty"`
	Entries []JukeboxEntry `json:"entries,omitempty"`
}

// Jukebox is the global library. Safe for concurrent use; the flush goroutine
// is the only disk writer.
type Jukebox struct {
	mu        sync.Mutex
	path      string
	playlists []Playlist
	timer     *time.Timer
	// rev bumps on every mutation so the UI can cache its Playlists()
	// snapshot (and its filtered search index) instead of copying per frame.
	rev int64
}

// jukeboxJSON is the on-disk shape.
type jukeboxJSON struct {
	Playlists []Playlist `json:"playlists"`
}

// JukeboxPath returns the global jukebox file beside the preferences file.
func JukeboxPath() (string, error) {
	base, err := DefaultPath()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(base), JukeboxFileName), nil
}

// LoadJukebox reads (or initializes) the global jukebox. Does disk I/O — call
// it off the render thread.
func LoadJukebox() (*Jukebox, error) {
	path, err := JukeboxPath()
	if err != nil {
		return nil, err
	}
	return OpenJukebox(path), nil
}

// OpenJukebox loads (or initializes) a jukebox library at an explicit path;
// LoadJukebox is this at the default location. Disk I/O — off the render thread.
func OpenJukebox(path string) *Jukebox { return loadJukeboxFile(path) }

// loadJukeboxFile reads one jukebox file; absent/corrupt = fresh.
func loadJukeboxFile(path string) *Jukebox {
	j := &Jukebox{path: path}
	data, err := os.ReadFile(path)
	if err != nil {
		return j
	}
	var onDisk jukeboxJSON
	if json.Unmarshal(data, &onDisk) == nil {
		j.playlists = sanitizePlaylists(onDisk.Playlists)
	}
	return j
}

// sanitizePlaylists trims, length-caps, drops empty-URL entries, and enforces
// the playlist and total-entry caps. Returns a fresh deep copy.
func sanitizePlaylists(in []Playlist) []Playlist {
	if len(in) > jukeboxMaxPlaylists {
		in = in[:jukeboxMaxPlaylists]
	}
	out := make([]Playlist, 0, len(in))
	total := 0
	for _, pl := range in {
		p := Playlist{
			Name: clampStr(strings.TrimSpace(pl.Name), jukeboxMaxNameLen),
			Key:  normalizeJukeKey(pl.Key),
		}
		for _, e := range pl.Entries {
			if total >= jukeboxMaxEntries {
				break
			}
			url := clampStr(strings.TrimSpace(e.URL), jukeboxMaxURLLen)
			if url == "" {
				continue
			}
			p.Entries = append(p.Entries, JukeboxEntry{
				Title: clampStr(strings.TrimSpace(e.Title), jukeboxMaxTitleLen),
				URL:   url,
				Key:   normalizeJukeKey(e.Key),
			})
			total++
		}
		out = append(out, p)
	}
	return out
}

func clampStr(s string, max int) string {
	if len(s) > max {
		return s[:max]
	}
	return s
}

// normalizeJukeKey lowercases an SDL key name (binds are case-insensitive).
func normalizeJukeKey(k string) string { return strings.ToLower(strings.TrimSpace(k)) }

// Rev reports the mutation revision (UI snapshot/filter cache key).
func (j *Jukebox) Rev() int64 {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.rev
}

// Playlists snapshots the library for drawing (callers must not mutate the
// returned slices). Deep copy.
func (j *Jukebox) Playlists() []Playlist {
	j.mu.Lock()
	defer j.mu.Unlock()
	return clonePlaylists(j.playlists)
}

func clonePlaylists(in []Playlist) []Playlist {
	out := make([]Playlist, len(in))
	for i, pl := range in {
		out[i] = pl
		out[i].Entries = append([]JukeboxEntry(nil), pl.Entries...)
	}
	return out
}

// PlaylistCount and TotalEntries report sizes without copying.
func (j *Jukebox) PlaylistCount() int {
	j.mu.Lock()
	defer j.mu.Unlock()
	return len(j.playlists)
}

func (j *Jukebox) TotalEntries() int {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.totalLocked()
}

// AddPlaylist appends a new empty playlist; false at the cap or empty name.
func (j *Jukebox) AddPlaylist(name string) bool {
	name = clampStr(strings.TrimSpace(name), jukeboxMaxNameLen)
	if name == "" {
		return false
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if len(j.playlists) >= jukeboxMaxPlaylists {
		return false
	}
	j.playlists = append(j.playlists, Playlist{Name: name})
	j.touchLocked()
	return true
}

// RemovePlaylist deletes playlist i (no-op out of range).
func (j *Jukebox) RemovePlaylist(i int) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if i >= 0 && i < len(j.playlists) {
		j.playlists = append(j.playlists[:i], j.playlists[i+1:]...)
		j.touchLocked()
	}
}

// RenamePlaylist renames playlist i (ignored for an empty name / bad index).
func (j *Jukebox) RenamePlaylist(i int, name string) {
	name = clampStr(strings.TrimSpace(name), jukeboxMaxNameLen)
	if name == "" {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if i >= 0 && i < len(j.playlists) {
		j.playlists[i].Name = name
		j.touchLocked()
	}
}

// SetPlaylistKey binds (or clears, with "") playlist i's shuffle key.
func (j *Jukebox) SetPlaylistKey(i int, key string) {
	key = normalizeJukeKey(key)
	j.mu.Lock()
	defer j.mu.Unlock()
	if i >= 0 && i < len(j.playlists) {
		j.playlists[i].Key = key
		j.touchLocked()
	}
}

// AddEntry appends a link to playlist i; false at the total cap, a bad index,
// or an empty URL.
func (j *Jukebox) AddEntry(i int, title, url string) bool {
	url = clampStr(strings.TrimSpace(url), jukeboxMaxURLLen)
	if url == "" {
		return false
	}
	title = clampStr(strings.TrimSpace(title), jukeboxMaxTitleLen)
	j.mu.Lock()
	defer j.mu.Unlock()
	if i < 0 || i >= len(j.playlists) || j.totalLocked() >= jukeboxMaxEntries {
		return false
	}
	j.playlists[i].Entries = append(j.playlists[i].Entries, JukeboxEntry{Title: title, URL: url})
	j.touchLocked()
	return true
}

// RemoveEntry deletes entry e from playlist pl (no-op out of range).
func (j *Jukebox) RemoveEntry(pl, e int) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if pl >= 0 && pl < len(j.playlists) {
		es := j.playlists[pl].Entries
		if e >= 0 && e < len(es) {
			j.playlists[pl].Entries = append(es[:e], es[e+1:]...)
			j.touchLocked()
		}
	}
}

// SetEntryKey binds (or clears, with "") entry e's play key in playlist pl.
func (j *Jukebox) SetEntryKey(pl, e int, key string) {
	key = normalizeJukeKey(key)
	j.mu.Lock()
	defer j.mu.Unlock()
	if pl >= 0 && pl < len(j.playlists) {
		es := j.playlists[pl].Entries
		if e >= 0 && e < len(es) {
			es[e].Key = key
			j.touchLocked()
		}
	}
}

// ResolveKey maps a bare key to a URL to /play: an entry whose Key matches
// (entry binds win — scanned first), else a RANDOM entry from a playlist whose
// Key matches (shuffle). ok=false when nothing binds the key.
func (j *Jukebox) ResolveKey(key string) (string, bool) {
	key = normalizeJukeKey(key)
	if key == "" {
		return "", false
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	for _, pl := range j.playlists { // entry binds first (more specific)
		for _, e := range pl.Entries {
			if e.Key == key {
				return e.URL, true
			}
		}
	}
	for _, pl := range j.playlists { // then playlist shuffle binds
		if pl.Key == key && len(pl.Entries) > 0 {
			return pl.Entries[rand.IntN(len(pl.Entries))].URL, true
		}
	}
	return "", false
}

// Shuffle returns a random entry URL from playlist i (ok=false if empty/bad).
func (j *Jukebox) Shuffle(i int) (string, bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if i < 0 || i >= len(j.playlists) || len(j.playlists[i].Entries) == 0 {
		return "", false
	}
	return j.playlists[i].Entries[rand.IntN(len(j.playlists[i].Entries))].URL, true
}

// ShuffleAll returns a random entry URL from ANY playlist (uniform over links).
func (j *Jukebox) ShuffleAll() (string, bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	n := j.totalLocked()
	if n == 0 {
		return "", false
	}
	pick := rand.IntN(n)
	for _, pl := range j.playlists {
		if pick < len(pl.Entries) {
			return pl.Entries[pick].URL, true
		}
		pick -= len(pl.Entries)
	}
	return "", false
}

// Clear wipes the whole library (the Wipe-everything factory reset; Reset
// settings leaves the jukebox untouched).
func (j *Jukebox) Clear() {
	j.mu.Lock()
	j.playlists = nil
	j.touchLocked()
	j.mu.Unlock()
}

// ExportJSON returns the whole library as pretty JSON (the on-disk shape) so a
// playlist config can be shared with others — save it, send it, paste it.
func (j *Jukebox) ExportJSON() ([]byte, error) {
	j.mu.Lock()
	snapshot := jukeboxJSON{Playlists: clonePlaylists(j.playlists)}
	j.mu.Unlock()
	return json.MarshalIndent(snapshot, jsonMarshalPrefix, jsonMarshalIndent)
}

// MergeJSON folds a shared jukebox export into this library: a playlist whose
// name matches (case-insensitively) gains the imported links it doesn't already
// have (dedup by URL); unknown playlists are added whole — all within the
// playlist/entry caps. Imported key binds are kept only where they don't
// collide with a bind you already have (yours win), so a shared config can't
// silently hijack a courtroom key. Returns the number of links added.
func (j *Jukebox) MergeJSON(data []byte) (int, error) {
	var incoming jukeboxJSON
	if err := json.Unmarshal(data, &incoming); err != nil {
		return 0, err
	}
	add := sanitizePlaylists(incoming.Playlists)
	j.mu.Lock()
	defer j.mu.Unlock()
	added := mergePlaylists(&j.playlists, add)
	if added > 0 {
		j.touchLocked()
	}
	return added, nil
}

// QuickAdd files a link under a named playlist (created if absent), skipping a
// duplicate URL — the one-click "save this shared link" path (e.g. capturing a
// song a DJ just /played in OOC). Returns true only when a NEW link was stored.
func (j *Jukebox) QuickAdd(playlist, title, url string) bool {
	url = clampStr(strings.TrimSpace(url), jukeboxMaxURLLen)
	playlist = clampStr(strings.TrimSpace(playlist), jukeboxMaxNameLen)
	if url == "" || playlist == "" {
		return false
	}
	title = clampStr(strings.TrimSpace(title), jukeboxMaxTitleLen)
	j.mu.Lock()
	defer j.mu.Unlock()
	idx := -1
	for i, pl := range j.playlists {
		if strings.EqualFold(pl.Name, playlist) {
			idx = i
			break
		}
	}
	if idx < 0 {
		if len(j.playlists) >= jukeboxMaxPlaylists {
			return false
		}
		j.playlists = append(j.playlists, Playlist{Name: playlist})
		idx = len(j.playlists) - 1
	}
	for _, e := range j.playlists[idx].Entries {
		if strings.EqualFold(e.URL, url) {
			return false // already saved
		}
	}
	if j.totalLocked() >= jukeboxMaxEntries {
		return false
	}
	j.playlists[idx].Entries = append(j.playlists[idx].Entries, JukeboxEntry{Title: title, URL: url})
	j.touchLocked()
	return true
}

// mergePlaylists folds add into *dst (see MergeJSON) and returns links added.
// Bounded by jukeboxMaxPlaylists / jukeboxMaxEntries; dedups links by URL within
// a playlist; drops an imported bind that collides with one already in *dst.
func mergePlaylists(dst *[]Playlist, add []Playlist) int {
	used := map[string]bool{}            // every key already bound (yours win)
	index := map[string]int{}            // lower(name) -> playlist index in *dst
	have := map[string]map[string]bool{} // lower(name) -> set of lower(URL)
	for i, pl := range *dst {
		nameKey := strings.ToLower(pl.Name)
		index[nameKey] = i
		set := make(map[string]bool, len(pl.Entries))
		for _, e := range pl.Entries {
			set[strings.ToLower(e.URL)] = true
			if e.Key != "" {
				used[e.Key] = true
			}
		}
		have[nameKey] = set
		if pl.Key != "" {
			used[pl.Key] = true
		}
	}
	total := totalEntries(*dst)
	added := 0
	for _, pl := range add {
		nameKey := strings.ToLower(pl.Name)
		idx, exists := index[nameKey]
		if !exists {
			if len(*dst) >= jukeboxMaxPlaylists {
				continue // playlist cap reached
			}
			*dst = append(*dst, Playlist{Name: pl.Name, Key: freshKey(pl.Key, used)})
			idx = len(*dst) - 1
			index[nameKey] = idx
			have[nameKey] = map[string]bool{}
		}
		set := have[nameKey]
		for _, e := range pl.Entries {
			if total >= jukeboxMaxEntries {
				break // total-entry cap reached
			}
			urlKey := strings.ToLower(e.URL)
			if set[urlKey] {
				continue // already in this playlist
			}
			(*dst)[idx].Entries = append((*dst)[idx].Entries, JukeboxEntry{
				Title: e.Title, URL: e.URL, Key: freshKey(e.Key, used),
			})
			set[urlKey] = true
			total++
			added++
		}
	}
	return added
}

// freshKey returns key if set and not already used (recording it), else "" — so
// an import can't take over a bind you already rely on.
func freshKey(key string, used map[string]bool) string {
	if key == "" || used[key] {
		return ""
	}
	used[key] = true
	return key
}

// totalEntries counts links across playlists.
func totalEntries(pls []Playlist) int {
	n := 0
	for _, pl := range pls {
		n += len(pl.Entries)
	}
	return n
}

func (j *Jukebox) totalLocked() int { return totalEntries(j.playlists) }

// touchLocked bumps the revision and (re)arms the debounced flush. The timer
// goroutine is the single disk writer. Caller holds j.mu.
func (j *Jukebox) touchLocked() {
	j.rev++
	if j.timer != nil {
		j.timer.Stop()
	}
	j.timer = time.AfterFunc(jukeboxSaveDelay, func() { _ = j.Flush() })
}

// Flush writes the library atomically (tmp + rename). Also called on exit so
// nothing rides only on the debounce.
func (j *Jukebox) Flush() error {
	j.mu.Lock()
	snapshot := jukeboxJSON{Playlists: clonePlaylists(j.playlists)}
	path := j.path
	j.mu.Unlock()
	if path == "" {
		return nil
	}
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

// Close stops the debounce timer and writes any pending change. Safe to call
// when nil-guarded by the caller.
func (j *Jukebox) Close() error {
	j.mu.Lock()
	if j.timer != nil {
		j.timer.Stop()
		j.timer = nil
	}
	j.mu.Unlock()
	return j.Flush()
}
