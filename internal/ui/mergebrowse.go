package ui

// The MERGED LOCAL ASSET SOURCE browse — the evidence editor's image picker
// (issue #6 follow-up; Crystalwarrior's design).
// "../../UPDATES/minimal/base/evidence/empty.png" into the field), and it lists
// "../../UPDATES/…" leak structurally impossible rather than merely fixed.

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const (
	// evidenceBrowseSeed is where both evidence entries open, per Crystalwarrior:
	// "Browse/Choose both start in the <merged_base>/evidence/".
	evidenceBrowseSeed = "evidence"
	// evidenceDirPrefix is the merged tree's evidence folder with its separator — the
	// one spelling evidenceRelFromMerged strips.
	evidenceDirPrefix = "evidence/"
	// mergedBrowseTip fills the row the filesystem browser spends on quick jumps. It
	// says what this listing IS, because that is the one thing a user cannot tell by
	// looking: the same rows, from somewhere else, and nothing else. Kept short
	// enough for the modal's inner width — a clipped tip would say nothing.
	mergedBrowseTip = "Local asset sources only — merged, an earlier source wins. Nothing is streamed."
	// mergedBrowseNoSources is the empty state: no local source is switched on, so
	// there is nothing to list and nothing to explain it but this.
	mergedBrowseNoSources = "No local asset sources — add one in Settings ▸ Assets, or type the name."
)

// mergedEntry is one row of a merged listing, adapted to browseDirEntry so
// filterBrowseEntries/overflowCount apply unchanged: the hidden-dotfile skip, the
// purpose's keep rule, directories-first sorting and the maxBrowseEntries cap are
// all already built and tested there, and a merged row is the same thing as a
// filesystem row as far as the list is concerned.
type mergedEntry struct {
	name  string
	isDir bool
}

func (e mergedEntry) Name() string { return e.name }
func (e mergedEntry) IsDir() bool  { return e.isDir }

// isZipMount reports a .zip pack mount — the one mount kind this listing cannot
// enumerate. See listMergedMountDir for why, and for the manifest design that is
// the planned way out.
func isZipMount(m string) bool { return strings.EqualFold(filepath.Ext(m), ".zip") }

// mergedParentDir climbs one level in the merged tree and CLAMPS at the root: the
// merged root has no parent, and that clamp is the sandbox. Contrast
// parentBrowseDir, whose "" is the drives sentinel and whose roots walk into it.
func mergedParentDir(dir string) string {
	i := strings.LastIndex(dir, "/")
	if i < 0 {
		return ""
	}
	return dir[:i]
}

// evidenceRelFromMerged re-roots a merged browse path at the mounts' evidence/
// folder. The editor's field holds an ORIGIN-relative asset base ("base/<image>"),
// and evidence/ is the folder courtroom.URLBuilder.Evidence prepends to it:
// The "../" arm is #127's own cross-folder reuse ("../characters/Ridelle/
// char_icon.png" — AO lets an evidence image take art from anywhere in the base),
// which Evidence resolves with cleanRel. It can never STACK: the browser's root
// clamp means at most one "../" is producible, so nothing this returns can escape
func evidenceRelFromMerged(path string) string {
	if rest, ok := strings.CutPrefix(path, evidenceDirPrefix); ok {
		return rest
	}
	return "../" + path
}

// mergedBrowseMounts returns the local asset sources this browse walks: the
// local-only mount set when that mode is on, else the layered set, else nil.
// STREAM MODE IS DELIBERATELY NIL, rather than "the mounts the user configured but
// is not using". This browse is the LOCAL half of the source story: listing folders
// the pipeline never reads would offer filenames that mean nothing at the origin,
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

// startMergedBrowseLoad kicks the off-thread listing of one virtual directory.
// The mount set is resolved HERE, on the render thread, and captured by the
// goroutine — prefs are render-thread state and the loader may not touch them
// (§17.4: off-thread work captures its inputs before it starts). Single-flight is
// the browser's own loading latch (navBrowseTo sets it, the draw clears it when the
// result lands), so the cap-1 result channel can never block the sender.
func (a *App) startMergedBrowseLoad(dir string, keep func(string) bool) {
	mounts := a.mergedBrowseMounts()
	if len(mounts) == 0 {
		// Nothing to walk: answer on the spot rather than parking a goroutine on an
		// empty mount list — and say WHY, since an empty listing looks exactly like an
		// empty folder otherwise.
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

// listMergedMountDir lists ONE virtual directory across the local mount FOLDERS and
// merges them the way the asset layer resolves a fetch: mounts in order, the FIRST
// spelling of a name winning, every later mount's copy hiding behind it.
func listMergedMountDir(mounts []string, relDir string, keep func(string) bool) (entries []browseEntry, more int, errStr string) {
	var all []mergedEntry
	seen := make(map[string]bool, 64)
	for _, m := range mounts {
		if isZipMount(m) {
			continue
		}
		// FromSlash on the MOUNT side only: the virtual tree is always slash-joined
		// (the field's spelling and evidenceRelFromMerged both depend on it), while
		// the mount path is whatever the OS uses.
		des, err := os.ReadDir(filepath.Join(m, filepath.FromSlash(relDir)))
		if err != nil {
			// A mount with no such folder is the NORMAL case for a layered set — no
			// mount has to mirror the whole base — so only a real error is worth
			// keeping, and only if nothing else manages to produce rows.
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
		// Rows are on screen: one unreadable sibling mount must not shout over a
		// listing that worked.
		errStr = ""
	}
	return entries, more, errStr
}
