package config

// Jukebox is global, in its own file, because tens of thousands of links must not re-serialize on settings changes.

import (
	"encoding/json"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/helper/text"
)

const (
	// JukeboxFileName sits beside the prefs file.
	JukeboxFileName = "jukebox.json"

	jukeboxMaxPlaylists = 200
	// jukeboxMaxEntries caps links across all playlists, bounding the file and the keybind scan.
	jukeboxMaxEntries = 50000
	// Length caps for one playlist name, entry title and URL.
	jukeboxMaxNameLen  = 100
	jukeboxMaxTitleLen = 256
	jukeboxMaxURLLen   = 2048
	// jukeboxSaveDelay debounces flushes: an add spree costs one write.
	jukeboxSaveDelay = 1 * time.Second
)

// JukeboxEntry is one stored music link; Key is an optional bare-key bind that /plays it.
type JukeboxEntry struct {
	Title string `json:"title,omitempty"`
	URL   string `json:"url"`
	Key   string `json:"key,omitempty"`
	Fav   bool   `json:"fav,omitempty"` // starred for the Favorites view (M12)
}

// Playlist is a named folder of links; Key is an optional bare-key bind that /plays a random entry.
type Playlist struct {
	Name    string         `json:"name"`
	Key     string         `json:"key,omitempty"`
	Entries []JukeboxEntry `json:"entries,omitempty"`
}

// Jukebox is safe for concurrent use; the flush goroutine is the only disk writer.
type Jukebox struct {
	mu        sync.Mutex
	path      string
	playlists []Playlist
	timer     *time.Timer
	// rev bumps on every mutation so the UI can cache its snapshot and search index instead of copying per frame.
	rev int64
}

// jukeboxJSON is the on-disk shape.
type jukeboxJSON struct {
	Playlists []Playlist `json:"playlists"`
}

func JukeboxPath() (string, error) {
	base, err := DefaultPath()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(base), JukeboxFileName), nil
}

// LoadJukebox reads or initializes the global jukebox; disk I/O, so call it off the render thread.
func LoadJukebox() (*Jukebox, error) {
	path, err := JukeboxPath()
	if err != nil {
		return nil, err
	}
	return OpenJukebox(path), nil
}

// OpenJukebox is LoadJukebox at an explicit path; disk I/O, so call it off the render thread.
func OpenJukebox(path string) *Jukebox { return loadJukeboxFile(path) }

// loadJukeboxFile treats an absent or corrupt file as a fresh library.
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

// sanitizePlaylists returns a trimmed, capped deep copy without empty-URL entries.
func sanitizePlaylists(in []Playlist) []Playlist {
	if len(in) > jukeboxMaxPlaylists {
		in = in[:jukeboxMaxPlaylists]
	}
	out := make([]Playlist, 0, len(in))
	total := 0
	for _, pl := range in {
		p := Playlist{
			Name: text.TruncateBytes(strings.TrimSpace(pl.Name), jukeboxMaxNameLen),
			Key:  normalizeJukeKey(pl.Key),
		}
		for _, e := range pl.Entries {
			if total >= jukeboxMaxEntries {
				break
			}
			url := text.TruncateBytes(strings.TrimSpace(e.URL), jukeboxMaxURLLen)
			if url == "" {
				continue
			}
			p.Entries = append(p.Entries, JukeboxEntry{
				Title: text.TruncateBytes(strings.TrimSpace(e.Title), jukeboxMaxTitleLen),
				URL:   url,
				Key:   normalizeJukeKey(e.Key),
				Fav:   e.Fav, // preserve the star across save→load
			})
			total++
		}
		out = append(out, p)
	}
	return out
}

// normalizeJukeKey lowercases an SDL key name; binds are case-insensitive.
func normalizeJukeKey(k string) string { return strings.ToLower(strings.TrimSpace(k)) }

// Rev reports the mutation revision, the UI's cache key.
func (j *Jukebox) Rev() int64 {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.rev
}

// Playlists returns a deep copy for drawing; callers must not mutate it.
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

// AddPlaylist appends an empty playlist; false at the cap or for an empty name.
func (j *Jukebox) AddPlaylist(name string) bool {
	name = text.TruncateBytes(strings.TrimSpace(name), jukeboxMaxNameLen)
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

// RemovePlaylist deletes playlist i; no-op out of range.
func (j *Jukebox) RemovePlaylist(i int) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if i >= 0 && i < len(j.playlists) {
		j.playlists = append(j.playlists[:i], j.playlists[i+1:]...)
		j.touchLocked()
	}
}

// SetPlaylistKey binds or, with "", clears playlist i's shuffle key.
func (j *Jukebox) SetPlaylistKey(i int, key string) {
	key = normalizeJukeKey(key)
	j.mu.Lock()
	defer j.mu.Unlock()
	if i >= 0 && i < len(j.playlists) {
		j.playlists[i].Key = key
		j.touchLocked()
	}
}

// AddEntry appends a link to playlist i; false at the cap, on a bad index or for an empty URL.
func (j *Jukebox) AddEntry(i int, title, url string) bool {
	url = text.TruncateBytes(strings.TrimSpace(url), jukeboxMaxURLLen)
	if url == "" {
		return false
	}
	title = text.TruncateBytes(strings.TrimSpace(title), jukeboxMaxTitleLen)
	j.mu.Lock()
	defer j.mu.Unlock()
	if i < 0 || i >= len(j.playlists) || j.totalLocked() >= jukeboxMaxEntries {
		return false
	}
	j.playlists[i].Entries = append(j.playlists[i].Entries, JukeboxEntry{Title: title, URL: url})
	j.touchLocked()
	return true
}

// RemoveEntry deletes entry e from playlist pl; no-op out of range.
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

// SetEntryKey binds or, with "", clears an entry's play key.
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

// SetEntryFav stars or unstars an entry for the Favorites view; no-op out of range.
func (j *Jukebox) SetEntryFav(pl, e int, fav bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if pl >= 0 && pl < len(j.playlists) {
		es := j.playlists[pl].Entries
		if e >= 0 && e < len(es) {
			es[e].Fav = fav
			j.touchLocked()
		}
	}
}

// ResolveKey maps a bare key to a URL: entry binds win, then a random entry of a playlist bound to the key.
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

// Shuffle returns a random entry URL from playlist i; false if empty or out of range.
func (j *Jukebox) Shuffle(i int) (string, bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if i < 0 || i >= len(j.playlists) || len(j.playlists[i].Entries) == 0 {
		return "", false
	}
	return j.playlists[i].Entries[rand.IntN(len(j.playlists[i].Entries))].URL, true
}

// ShuffleAll returns a random entry URL, uniform over all links.
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

// Clear wipes the library for the factory reset; Reset settings leaves the jukebox untouched.
func (j *Jukebox) Clear() {
	j.mu.Lock()
	j.playlists = nil
	j.touchLocked()
	j.mu.Unlock()
}

// ExportJSON returns the library as pretty JSON in the on-disk shape, for sharing.
func (j *Jukebox) ExportJSON() ([]byte, error) {
	j.mu.Lock()
	snapshot := jukeboxJSON{Playlists: clonePlaylists(j.playlists)}
	j.mu.Unlock()
	return json.MarshalIndent(snapshot, jsonMarshalPrefix, jsonMarshalIndent)
}

// MergeJSON merges an export by playlist name, dedups by URL and returns the links added.
// Imported binds that collide are dropped so a shared config can't hijack a courtroom key.
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

// QuickAdd files a link under a named playlist, created if absent; true only when a new link was stored.
func (j *Jukebox) QuickAdd(playlist, title, url string) bool {
	url = text.TruncateBytes(strings.TrimSpace(url), jukeboxMaxURLLen)
	playlist = text.TruncateBytes(strings.TrimSpace(playlist), jukeboxMaxNameLen)
	if url == "" || playlist == "" {
		return false
	}
	title = text.TruncateBytes(strings.TrimSpace(title), jukeboxMaxTitleLen)
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

// mergePlaylists implements MergeJSON and returns the links added.
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

// freshKey returns key if set and unused, recording it, else ""; an import can't take over an existing bind.
func freshKey(key string, used map[string]bool) string {
	if key == "" || used[key] {
		return ""
	}
	used[key] = true
	return key
}

func totalEntries(pls []Playlist) int {
	n := 0
	for _, pl := range pls {
		n += len(pl.Entries)
	}
	return n
}

func (j *Jukebox) totalLocked() int { return totalEntries(j.playlists) }

// touchLocked bumps the revision and rearms the debounced flush; the caller holds j.mu.
func (j *Jukebox) touchLocked() {
	j.rev++
	if j.timer != nil {
		j.timer.Stop()
	}
	j.timer = time.AfterFunc(jukeboxSaveDelay, func() { _ = j.Flush() })
}

// Flush writes atomically (tmp + rename); also called on exit so nothing rides only on the debounce.
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

// Close stops the debounce timer and writes any pending change; the caller guards against nil.
func (j *Jukebox) Close() error {
	j.mu.Lock()
	if j.timer != nil {
		j.timer.Stop()
		j.timer = nil
	}
	j.mu.Unlock()
	return j.Flush()
}
