package ui

// The MERGED LOCAL ASSET SOURCE browse — the evidence editor's image picker
// (issue #6 follow-up; Crystalwarrior's design).
//
// THE PROBLEM IT SOLVES. The evidence browse used to be an ordinary filesystem
// browser seeded at the first mount's evidence/ folder. That fails three ways the
// playtest hit within a minute: it shows ONE mount instead of the set the client
// actually reads, it lets the user wander anywhere on the machine (and, with a
// RELATIVE mount path configured, back out far enough to freeze
// "../../UPDATES/minimal/base/evidence/empty.png" into the field), and it lists
// names the origin can never serve.
//
// THE MODEL. The listing is a synthetic filesystem over the user's local asset
// sources: for every virtual directory, the union of what each mount holds there,
// with an EARLIER mount shadowing a later one — the same first-hit-wins order
// BuildMountIndex resolves a fetch with. So "if you have multiple mounted folders,
// and one folder has an updated image over the other, it will only show the
// updated image" is true BY CONSTRUCTION rather than by effort.
//
// It cannot leave the mounts: ".." clamps at the root, there is no drives view and
// there are no quick jumps, so the value that reaches the field is always an asset
// path. That is also why the field's value is derived from the VIRTUAL path
// (evidenceRelFromMerged) and never from a filesystem path — which makes the
// "../../UPDATES/…" leak structurally impossible rather than merely fixed.
//
// There is deliberately NO notion of a "base folder": AsyncAO's mounts are uniform
// asset sources in the user's own order, so this file never singles one out as
// special (Crystalwarrior: AO2's "folder 1 is always the default base" is a design
// restriction AsyncAO does not need, and must not adopt).

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
//
//	"evidence/knife.png"           → "knife.png"
//	"evidence/cases/knife.png"     → "cases/knife.png"
//	"characters/foo/char_icon.png" → "../characters/foo/char_icon.png"
//
// The "../" arm is #127's own cross-folder reuse ("../characters/Ridelle/
// char_icon.png" — AO lets an evidence image take art from anywhere in the base),
// which Evidence resolves with cleanRel. It can never STACK: the browser's root
// clamp means at most one "../" is producible, so nothing this returns can escape
// the origin.
func evidenceRelFromMerged(path string) string {
	if rest, ok := strings.CutPrefix(path, evidenceDirPrefix); ok {
		return rest
	}
	return "../" + path
}

// mergedBrowseMounts returns the local asset sources this browse walks: the
// local-only mount set when that mode is on, else the layered set, else nil.
//
// THE ACCESSORS, NOT THE RAW FIELDS, so the ambiguous both-flags state keeps the
// single meaning LayeredAssets defines (local-only wins, layering inert).
//
// STREAM MODE IS DELIBERATELY NIL, rather than "the mounts the user configured but
// is not using". This browse is the LOCAL half of the source story: listing folders
// the pipeline never reads would offer filenames that mean nothing at the origin,
// and "don't use the server's evidence for now" (Crystalwarrior) means the stream
// has no listing surface here at all. A stream-mode user types the name instead.
func (a *App) mergedBrowseMounts() []string {
	if a.d.Prefs == nil {
		return nil
	}
	if on, mounts := a.d.Prefs.LocalAssets(); on && len(mounts) > 0 {
		return mounts
	}
	if on, mounts := a.d.Prefs.LayeredAssets(); on {
		return mounts
	}
	return nil
}

// startMergedBrowseLoad kicks the off-thread listing of one virtual directory.
//
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
//
// OFF THE RENDER THREAD (the caller is the browser's loader goroutine): one
// os.ReadDir per mount per NAVIGATION, never per frame. It reads nothing but the
// folders themselves, so it behaves identically in local-only and layered modes —
// the two states a local source set is live in.
//
// .zip PACK MOUNTS ARE NOT LISTED. The mount index holds every pack entry, but it
// stores no directory tree: enumerating one virtual directory out of it means a
// prefix scan over the whole index per navigation, and the other route — opening
// the archive's central directory per click — re-parses the entire pack every time
// the user moves. The evidence scan this replaces skipped packs for the same
// reason. Picking an image out of a pack is a KNOWN GAP; a pack-only user types the
// filename instead.
//
// THE MANIFEST SOLUTION that closes it, documented here for the later design pass
// (Crystalwarrior's proposal): an evidence/ folder could ship a manifest listing the
// evidence images it holds, the way the character list works, so a client can
// discover them without crawling anything — which is also the ONLY way a streaming
// client could ever list them. That is deliberately not attempted now; it is why
// the stream arm is "type it in" rather than a second listing source.
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
