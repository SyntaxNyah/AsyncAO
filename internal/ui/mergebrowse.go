package ui

// The evidence editor's image picker: a synthetic filesystem over the local asset sources, each directory the union of
// the mounts, an earlier mount shadowing a later one, the same first-hit-wins order BuildMountIndex resolves a fetch
// with. It cannot leave the mounts: ".." clamps at the root and there are no drives or quick jumps, and the field's
// value comes from the virtual path (evidenceRelFromMerged), so it is always an asset path. No mount is special; there
// is no "base folder".

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const (
	// evidenceBrowseSeed is where both evidence entries open.
	evidenceBrowseSeed = "evidence"
	// evidenceDirPrefix is the merged tree's evidence folder with its separator, which evidenceRelFromMerged strips.
	evidenceDirPrefix = "evidence/"
	// mergedBrowseTip fills the row the filesystem browser spends on quick jumps; short enough for the modal's inner
	// width.
	mergedBrowseTip = "Local asset sources only — merged, an earlier source wins. Nothing is streamed."
	// mergedBrowseNoSources is the empty state: no local source is switched on.
	mergedBrowseNoSources = "No local asset sources — add one in Settings ▸ Assets, or type the name."
)

// mergedEntry adapts one merged row to browseDirEntry so filterBrowseEntries and overflowCount apply unchanged.
type mergedEntry struct {
	name  string
	isDir bool
}

func (e mergedEntry) Name() string { return e.name }
func (e mergedEntry) IsDir() bool  { return e.isDir }

// isZipMount reports a .zip pack mount, the one kind this listing cannot enumerate.
func isZipMount(m string) bool { return strings.EqualFold(filepath.Ext(m), ".zip") }

// mergedParentDir climbs one level and clamps at the root, which is what keeps the browse inside the mounts.
// Contrast parentBrowseDir, whose "" is the drives sentinel.
func mergedParentDir(dir string) string {
	i := strings.LastIndex(dir, "/")
	if i < 0 {
		return ""
	}
	return dir[:i]
}

// evidenceRelFromMerged re-roots a merged path at the mounts' evidence/ folder, which URLBuilder.Evidence prepends:
// "evidence/cases/knife.png" gives "cases/knife.png"; "characters/foo/char_icon.png" gives
// "../characters/foo/char_icon.png". AO lets an evidence image take art from anywhere in the base. The root clamp
// allows at most one "../", so it can't escape the origin.
func evidenceRelFromMerged(path string) string {
	if rest, ok := strings.CutPrefix(path, evidenceDirPrefix); ok {
		return rest
	}
	return "../" + path
}

// mergedBrowseMounts returns the sources this browse walks: local-only if on, else layered, else nil.
// Accessors keep LayeredAssets' meaning of the both-flags state. Stream mode is nil: listing unread folders
// would offer names that mean nothing at the origin, so a stream-mode user types the name.
func (a *App) mergedBrowseMounts() []string {
	if a.d.Prefs == nil {
		return nil
	}
	if on, mounts := a.localAssets(); on && len(mounts) > 0 {
		return mounts
	}
	if on, mounts := a.layeredAssets(); on {
		return mounts
	}
	return nil
}

// startMergedBrowseLoad starts the off-thread listing of one virtual directory.
// Mounts are resolved here because prefs are render-thread state. navBrowseTo's loading flag limits this to one
// at a time, so the capacity-1 result channel never blocks the sender.
func (a *App) startMergedBrowseLoad(dir string, keep func(string) bool) {
	mounts := a.mergedBrowseMounts()
	if len(mounts) == 0 {
		// Answer on the spot, saying why: an empty listing would look like an empty folder.
		s := &demoBrowser
		s.loading = false
		s.entries, s.more = nil, 0
		s.loadErr = mergedBrowseNoSources
		return
	}
	go func(target string, mounts []string, keep func(string) bool) {
		ents, more, errStr := listMergedMountDir(mounts, target, keep)
		demoBrowser.res <- browseResult{dir: target, entries: ents, more: more, err: errStr}
	}(dir, mounts, keep)
}

// listMergedMountDir lists one virtual directory across the mount folders, first spelling of a name winning.
// Off the render thread: one os.ReadDir per mount per navigation. .zip packs are not listed: the index has no tree, so
// this would mean a whole-index scan or re-parsing the archive per click. A pack-only user types the filename.
func listMergedMountDir(mounts []string, relDir string, keep func(string) bool) (entries []browseEntry, more int, errStr string) {
	var all []mergedEntry
	seen := make(map[string]bool, 64)
	for _, m := range mounts {
		if isZipMount(m) {
			continue
		}
		// FromSlash on the mount side only: the virtual tree is always slash-joined.
		des, err := os.ReadDir(filepath.Join(m, filepath.FromSlash(relDir)))
		if err != nil {
			// A missing folder is normal for a layered set, so only keep a real error.
			if !errors.Is(err, fs.ErrNotExist) && errStr == "" {
				errStr = err.Error()
			}
			continue
		}
		for _, de := range des {
			name := de.Name()
			key := strings.ToLower(name)
			if seen[key] {
				continue // an earlier mount already owns this name (first hit wins)
			}
			seen[key] = true
			all = append(all, mergedEntry{name: name, isDir: de.IsDir()})
		}
	}
	entries = filterBrowseEntries(all, keep)
	more = overflowCount(all, keep)
	if len(all) > 0 {
		// One unreadable sibling mount must not shout over a listing that worked.
		errStr = ""
	}
	return entries, more, errStr
}
