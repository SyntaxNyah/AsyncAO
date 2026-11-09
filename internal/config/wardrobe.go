package config

// The Wardrobe is global and in its own file: favourites are not tied to a server, and the list must not
// re-serialize the prefs file on every star, drag or nickname.

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/helper/text"
)

const (
	// WardrobeFileName sits beside the prefs file.
	WardrobeFileName = "wardrobe.json"
	// WardrobeImageDirName holds folders' custom icon images, beside the prefs file.
	WardrobeImageDirName = "wardrobe-icons"
	// WardrobeNameMaxRunes caps a folder name and a nickname.
	WardrobeNameMaxRunes = 40

	wardrobeMaxEntries = 4096
	wardrobeMaxFolders = 200
	// wardrobeMaxCharBytes caps one character folder path.
	wardrobeMaxCharBytes = 256
	// wardrobeMaxLookBytes caps an icon, colour or image name.
	wardrobeMaxLookBytes = 64
	// wardrobeSaveDelay debounces flushes: a drag or an import costs one write.
	wardrobeSaveDelay = 1 * time.Second
	// wardrobeFolderIDPrefix starts every folder id; the rest is a number unique within the file.
	wardrobeFolderIDPrefix = "f"
)

var (
	ErrWardrobeNameEmpty = errors.New("wardrobe: the folder name is empty")
	ErrWardrobeNameTaken = errors.New("wardrobe: a folder already has that name")
	ErrWardrobeFull      = errors.New("wardrobe: the folder limit is reached")
)

// WardrobeEntry is one saved character; Char, the character folder, is its identity.
type WardrobeEntry struct {
	Char     string `json:"char"`
	Nickname string `json:"nickname,omitempty"`
	// Folders are the WardrobeFolder.IDs the entry is filed in, in the order it was filed; none is unfiled.
	Folders []string `json:"folders,omitempty"`
	// LastWorn is Unix seconds; 0 is never.
	LastWorn int64 `json:"lastWorn,omitempty"`
}

// WardrobeFolder is a named group of entries; Server is the server key it was made on, "" for none.
type WardrobeFolder struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Server string `json:"server,omitempty"`
	Icon   string `json:"icon,omitempty"`
	Color  string `json:"color,omitempty"`
	// Image is a file name under WardrobeImageDirName; it wins over Icon.
	Image string `json:"image,omitempty"`
}

// Wardrobe is safe for concurrent use; the flush goroutine is the only disk writer. Entry and folder order is the
// player's own order.
type Wardrobe struct {
	mu      sync.Mutex
	path    string
	entries []WardrobeEntry
	folders []WardrobeFolder
	timer   *time.Timer
	// rev bumps on every change so the UI rebuilds its views only then.
	rev int64
}

type wardrobeJSON struct {
	Entries []WardrobeEntry  `json:"entries"`
	Folders []WardrobeFolder `json:"folders"`
}

// WardrobePath is wardrobe.json beside the prefs file.
func WardrobePath() (string, error) {
	base, err := DefaultPath()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(base), WardrobeFileName), nil
}

// WardrobeImageDir is the folder that holds folders' custom icon images.
func WardrobeImageDir() (string, error) {
	base, err := DefaultPath()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(base), WardrobeImageDirName), nil
}

// LoadWardrobe reads wardrobe.json, or builds it once from the old per-server lists in old; disk I/O, so call it off
// the render thread.
func LoadWardrobe(old *Settings) (*Wardrobe, error) {
	path, err := WardrobePath()
	if err != nil {
		return nil, err
	}
	return OpenWardrobe(path, old), nil
}

// OpenWardrobe is LoadWardrobe at an explicit path. A corrupt file opens empty and is not migrated again.
func OpenWardrobe(path string, old *Settings) *Wardrobe {
	w := &Wardrobe{path: path}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		w.entries, w.folders = migrateWardrobe(old)
		if len(w.entries) > 0 || len(w.folders) > 0 {
			w.mu.Lock()
			w.touchLocked()
			w.mu.Unlock()
		}
		return w
	}
	if err != nil {
		return w
	}
	var onDisk wardrobeJSON
	if json.Unmarshal(data, &onDisk) == nil {
		w.entries, w.folders = sanitizeWardrobe(onDisk.Entries, onDisk.Folders)
	}
	return w
}

// migrateWardrobe merges the flat pre-split list and every server's list into one, in server-key order, first
// occurrence wins. Old folders with the same name merge, keeping the first server they came from.
func migrateWardrobe(old *Settings) ([]WardrobeEntry, []WardrobeFolder) {
	if old == nil {
		return nil, nil
	}
	var entries []WardrobeEntry
	var folders []WardrobeFolder
	seen := map[string]bool{}
	folderID := map[string]string{} // lowercased name → id
	add := func(char, folderName, server string) {
		char = strings.TrimSpace(char)
		key := strings.ToLower(char)
		if char == "" || seen[key] || len(entries) >= wardrobeMaxEntries {
			return
		}
		seen[key] = true
		e := WardrobeEntry{Char: text.TruncateBytes(char, wardrobeMaxCharBytes)}
		if name := strings.TrimSpace(folderName); name != "" {
			fk := strings.ToLower(name)
			id, ok := folderID[fk]
			if !ok && len(folders) < wardrobeMaxFolders {
				id = nextFolderID(folders)
				folders = append(folders, WardrobeFolder{ID: id, Name: text.Truncate(name, WardrobeNameMaxRunes), Server: server})
				folderID[fk] = id
				ok = true
			}
			if ok {
				e.Folders = []string{id}
			}
		}
		entries = append(entries, e)
	}
	for _, c := range old.Wardrobe {
		add(c, "", "")
	}
	servers := make([]string, 0, len(old.ServerWarm))
	for k := range old.ServerWarm {
		servers = append(servers, k)
	}
	slices.Sort(servers)
	for _, server := range servers {
		info := old.ServerWarm[server]
		for _, c := range info.Wardrobe {
			add(c, info.WardrobeFolder[strings.ToLower(c)], server)
		}
	}
	return entries, folders
}

// sanitizeWardrobe trims, caps and deduplicates a loaded file; an entry in an unknown folder becomes unfiled.
func sanitizeWardrobe(entries []WardrobeEntry, folders []WardrobeFolder) ([]WardrobeEntry, []WardrobeFolder) {
	outF := make([]WardrobeFolder, 0, min(len(folders), wardrobeMaxFolders))
	ids := map[string]bool{}
	names := map[string]bool{}
	for _, f := range folders {
		f.Name = text.Truncate(strings.TrimSpace(f.Name), WardrobeNameMaxRunes)
		if f.ID == "" || f.Name == "" || ids[f.ID] || names[strings.ToLower(f.Name)] || len(outF) >= wardrobeMaxFolders {
			continue
		}
		f.Icon = text.TruncateBytes(f.Icon, wardrobeMaxLookBytes)
		f.Color = text.TruncateBytes(f.Color, wardrobeMaxLookBytes)
		f.Image = text.TruncateBytes(filepath.Base(f.Image), wardrobeMaxLookBytes)
		if f.Image == "." {
			f.Image = ""
		}
		ids[f.ID] = true
		names[strings.ToLower(f.Name)] = true
		outF = append(outF, f)
	}
	outE := make([]WardrobeEntry, 0, min(len(entries), wardrobeMaxEntries))
	seen := map[string]bool{}
	for _, e := range entries {
		e.Char = text.TruncateBytes(strings.TrimSpace(e.Char), wardrobeMaxCharBytes)
		key := strings.ToLower(e.Char)
		if e.Char == "" || seen[key] || len(outE) >= wardrobeMaxEntries {
			continue
		}
		seen[key] = true
		e.Nickname = cleanNickname(e.Nickname, e.Char)
		e.Folders = uniqueFolders(e.Folders, ids)
		if len(e.Folders) == 0 {
			e.Folders = nil
		}
		outE = append(outE, e)
	}
	return outE, outF
}

// uniqueFolders keeps the known folder ids of in, each once, in order.
func uniqueFolders(in []string, known map[string]bool) []string {
	var out []string
	for _, id := range in {
		if known[id] && !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out
}

// cleanNickname trims and caps a nickname; one equal to the character's own name is no nickname.
func cleanNickname(nick, char string) string {
	nick = text.Truncate(strings.TrimSpace(nick), WardrobeNameMaxRunes)
	if strings.EqualFold(nick, char) {
		return ""
	}
	return nick
}

// nextFolderID is one past the highest numbered id in folders.
func nextFolderID(folders []WardrobeFolder) string {
	highest := 0
	for _, f := range folders {
		if n, err := strconv.Atoi(strings.TrimPrefix(f.ID, wardrobeFolderIDPrefix)); err == nil && n > highest {
			highest = n
		}
	}
	return wardrobeFolderIDPrefix + strconv.Itoa(highest+1)
}

// Rev reports the change revision, the UI's cache key.
func (w *Wardrobe) Rev() int64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.rev
}

// Snapshot returns copies of the entries and folders in the player's order.
func (w *Wardrobe) Snapshot() ([]WardrobeEntry, []WardrobeFolder) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return cloneWardrobeEntries(w.entries), slices.Clone(w.folders)
}

func cloneWardrobeEntries(in []WardrobeEntry) []WardrobeEntry {
	out := slices.Clone(in)
	for i := range out {
		out[i].Folders = slices.Clone(out[i].Folders)
	}
	return out
}

// Has reports whether char is saved, ignoring case.
func (w *Wardrobe) Has(char string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.entryLocked(char) >= 0
}

func (w *Wardrobe) entryLocked(char string) int {
	return slices.IndexFunc(w.entries, func(e WardrobeEntry) bool { return strings.EqualFold(e.Char, char) })
}

func (w *Wardrobe) folderLocked(id string) int {
	return slices.IndexFunc(w.folders, func(f WardrobeFolder) bool { return f.ID == id })
}

// Add appends the characters not saved yet to folder ("" unfiled, an unknown id also unfiled) and returns how many
// were added; it stops at the entry cap.
func (w *Wardrobe) Add(chars []string, folder string) int {
	w.mu.Lock()
	defer w.mu.Unlock()
	if folder != "" && w.folderLocked(folder) < 0 {
		folder = ""
	}
	added := 0
	for _, c := range chars {
		c = text.TruncateBytes(strings.TrimSpace(c), wardrobeMaxCharBytes)
		if c == "" || w.entryLocked(c) >= 0 || len(w.entries) >= wardrobeMaxEntries {
			continue
		}
		e := WardrobeEntry{Char: c}
		if folder != "" {
			e.Folders = []string{folder}
		}
		w.entries = append(w.entries, e)
		added++
	}
	if added > 0 {
		w.touchLocked()
	}
	return added
}

// Remove deletes char's entry.
func (w *Wardrobe) Remove(char string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if i := w.entryLocked(char); i >= 0 {
		w.entries = slices.Delete(w.entries, i, i+1)
		w.touchLocked()
	}
}

// Move puts char at target's place in the player's order: after it when moving down, before it when moving up.
func (w *Wardrobe) Move(char, target string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	from, to := w.entryLocked(char), w.entryLocked(target)
	if from < 0 || to < 0 || from == to {
		return
	}
	e := w.entries[from]
	w.entries = slices.Insert(slices.Delete(w.entries, from, from+1), to, e)
	w.touchLocked()
}

// AddToFolder files char under folder too, keeping its other folders.
func (w *Wardrobe) AddToFolder(char, folder string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	i := w.entryLocked(char)
	if i < 0 || w.folderLocked(folder) < 0 || slices.Contains(w.entries[i].Folders, folder) {
		return
	}
	w.entries[i].Folders = append(slices.Clip(w.entries[i].Folders), folder)
	w.touchLocked()
}

// RemoveFromFolder takes char out of folder, keeping its other folders.
func (w *Wardrobe) RemoveFromFolder(char, folder string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	i := w.entryLocked(char)
	if i < 0 || !slices.Contains(w.entries[i].Folders, folder) {
		return
	}
	w.entries[i].Folders = slices.DeleteFunc(slices.Clone(w.entries[i].Folders), func(id string) bool { return id == folder })
	if len(w.entries[i].Folders) == 0 {
		w.entries[i].Folders = nil
	}
	w.touchLocked()
}

// MoveToFolder takes char out of from and files it under to; "" for to leaves it only out of from.
func (w *Wardrobe) MoveToFolder(char, from, to string) {
	if from == to {
		return
	}
	w.RemoveFromFolder(char, from)
	if to != "" {
		w.AddToFolder(char, to)
	}
}

// ClearFolders takes char out of every folder, so it is unfiled.
func (w *Wardrobe) ClearFolders(char string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if i := w.entryLocked(char); i >= 0 && len(w.entries[i].Folders) > 0 {
		w.entries[i].Folders = nil
		w.touchLocked()
	}
}

// SetNickname sets the name shown instead of char's own; "" or the character's own name clears it.
func (w *Wardrobe) SetNickname(char, nick string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	i := w.entryLocked(char)
	if i < 0 {
		return
	}
	nick = cleanNickname(nick, w.entries[i].Char)
	if w.entries[i].Nickname != nick {
		w.entries[i].Nickname = nick
		w.touchLocked()
	}
}

// MarkWorn records that char was worn at; unsaved characters are ignored.
func (w *Wardrobe) MarkWorn(char string, at time.Time) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if i := w.entryLocked(char); i >= 0 {
		w.entries[i].LastWorn = at.Unix()
		w.touchLocked()
	}
}

// checkFolderNameLocked returns the trimmed, capped name, or why it can't be used.
func (w *Wardrobe) checkFolderNameLocked(name, exceptID string) (string, error) {
	name = text.Truncate(strings.TrimSpace(name), WardrobeNameMaxRunes)
	if name == "" {
		return "", ErrWardrobeNameEmpty
	}
	for _, f := range w.folders {
		if f.ID != exceptID && strings.EqualFold(f.Name, name) {
			return "", ErrWardrobeNameTaken
		}
	}
	return name, nil
}

// CheckFolderName reports why name can't name a folder other than exceptID, or nil.
func (w *Wardrobe) CheckFolderName(name, exceptID string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	_, err := w.checkFolderNameLocked(name, exceptID)
	return err
}

// AddFolder appends a folder made on server, with the given look, and returns its id.
func (w *Wardrobe) AddFolder(name, server, icon, color string) (string, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	name, err := w.checkFolderNameLocked(name, "")
	if err != nil {
		return "", err
	}
	if len(w.folders) >= wardrobeMaxFolders {
		return "", ErrWardrobeFull
	}
	id := nextFolderID(w.folders)
	w.folders = append(w.folders, WardrobeFolder{
		ID: id, Name: name, Server: server,
		Icon:  text.TruncateBytes(icon, wardrobeMaxLookBytes),
		Color: text.TruncateBytes(color, wardrobeMaxLookBytes),
	})
	w.touchLocked()
	return id, nil
}

// RenameFolder renames folder id.
func (w *Wardrobe) RenameFolder(id, name string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	i := w.folderLocked(id)
	if i < 0 {
		return nil
	}
	name, err := w.checkFolderNameLocked(name, id)
	if err != nil {
		return err
	}
	if w.folders[i].Name != name {
		w.folders[i].Name = name
		w.touchLocked()
	}
	return nil
}

// DeleteFolder removes folder id and takes every entry out of it. When keepMembers is false, an entry that was in no
// other folder is removed from the Wardrobe too; one still in another folder stays there.
func (w *Wardrobe) DeleteFolder(id string, keepMembers bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	i := w.folderLocked(id)
	if i < 0 {
		return
	}
	w.folders = slices.Delete(w.folders, i, i+1)
	if !keepMembers {
		w.entries = slices.DeleteFunc(w.entries, func(e WardrobeEntry) bool {
			return len(e.Folders) == 1 && e.Folders[0] == id
		})
	}
	for j := range w.entries {
		if slices.Contains(w.entries[j].Folders, id) {
			w.entries[j].Folders = slices.DeleteFunc(slices.Clone(w.entries[j].Folders), func(f string) bool { return f == id })
			if len(w.entries[j].Folders) == 0 {
				w.entries[j].Folders = nil
			}
		}
	}
	w.touchLocked()
}

// MoveFolder puts folder id at target's place, as Move does for entries.
func (w *Wardrobe) MoveFolder(id, target string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	from, to := w.folderLocked(id), w.folderLocked(target)
	if from < 0 || to < 0 || from == to {
		return
	}
	f := w.folders[from]
	w.folders = slices.Insert(slices.Delete(w.folders, from, from+1), to, f)
	w.touchLocked()
}

// SetFolderLook sets folder id's built-in icon and colour, and drops its custom image.
func (w *Wardrobe) SetFolderLook(id, icon, color string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	i := w.folderLocked(id)
	if i < 0 {
		return
	}
	w.folders[i].Icon = text.TruncateBytes(icon, wardrobeMaxLookBytes)
	w.folders[i].Color = text.TruncateBytes(color, wardrobeMaxLookBytes)
	w.folders[i].Image = ""
	w.touchLocked()
}

// SetFolderColor sets folder id's colour and keeps its icon or image.
func (w *Wardrobe) SetFolderColor(id, color string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	i := w.folderLocked(id)
	if i < 0 {
		return
	}
	w.folders[i].Color = text.TruncateBytes(color, wardrobeMaxLookBytes)
	w.touchLocked()
}

// SetFolderImage sets folder id's custom image, a file name under WardrobeImageDir.
func (w *Wardrobe) SetFolderImage(id, image string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	i := w.folderLocked(id)
	if i < 0 {
		return
	}
	w.folders[i].Image = text.TruncateBytes(filepath.Base(image), wardrobeMaxLookBytes)
	w.touchLocked()
}

// Clear empties the Wardrobe for Wipe everything; Reset settings leaves it alone.
func (w *Wardrobe) Clear() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.entries, w.folders = nil, nil
	w.touchLocked()
}

// touchLocked bumps the revision and rearms the debounced flush; the caller holds w.mu.
func (w *Wardrobe) touchLocked() {
	w.rev++
	if w.timer != nil {
		w.timer.Stop()
	}
	w.timer = time.AfterFunc(wardrobeSaveDelay, func() { _ = w.Flush() })
}

// Flush writes the file atomically; Close calls it so nothing waits only on the debounce.
func (w *Wardrobe) Flush() error {
	w.mu.Lock()
	snapshot := wardrobeJSON{Entries: cloneWardrobeEntries(w.entries), Folders: slices.Clone(w.folders)}
	path := w.path
	w.mu.Unlock()
	if path == "" {
		return nil
	}
	data, err := json.MarshalIndent(snapshot, jsonMarshalPrefix, jsonMarshalIndent)
	if err != nil {
		return err
	}
	return atomicWriteFile(path, data, prefsFilePerm)
}

// Close stops the debounce timer and writes any pending change; the caller guards against nil.
func (w *Wardrobe) Close() error {
	w.mu.Lock()
	if w.timer != nil {
		w.timer.Stop()
		w.timer = nil
	}
	w.mu.Unlock()
	return w.Flush()
}

// WipeWardrobeImages deletes the folder images for Wipe everything.
func WipeWardrobeImages() error {
	dir, err := WardrobeImageDir()
	if err != nil {
		return err
	}
	return os.RemoveAll(dir)
}
