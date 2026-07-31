package ui

import "strings"

// The theme's iniswap_dropdown / iniswap_remove pair (#21 label 11 — the
// reporter's "iniswap missing").
//
// AO2 builds the list in on_iniswap_dropdown_changed's neighbourhood
// (../AO2-Client/src/courtroom.cpp:5331-5332):
//
//	iniswaps.prepend(char_list.at(m_cid).name);
//	iniswaps.removeDuplicates();
//
// so the player's OWN folder is always index 0 and the saved swaps follow. The
// remove button is offered only while something other than index 0 is worn
// (:5323-5328 hides the pair outright when m_cid is -1).
//
// The list logic lives here as PURE functions rather than inline at the draw
// site, because the index it produces is destructive: it feeds RemoveWardrobe,
// which PERSISTS. An off-by-one deletes a wardrobe entry the user did not pick,
// and that is not something to leave to a loop written inside a draw call.

// buildIniswapChoices returns own-folder-first, then the saved wardrobe, then the
// worn folder if it is in neither. dst is truncated and reused (the draw site
// calls this behind a generation guard, so it must not allocate per frame).
//
// The trailing "worn if absent" entry is the load-bearing part, and it is AO2's
// behaviour by a different route: AO2's combobox is EDITABLE with InsertAtBottom
// (courtroom.cpp:938-939) and it rewrites iniswaps.ini on every change, so
// whatever you are wearing is always in the list. AsyncAO can wear a folder that
// was never saved — wearFromMenu takes entries from the SERVER's iniswap.txt and
// never calls AddWardrobe — so without this the current value would not be found,
// the dropdown would display the player's own character while they were wearing
// something else, and the remove button would hide itself exactly when it is
// needed.
//
// Matching is case-insensitive, which is a deliberate divergence: AO2's
// removeDuplicates and its selection compare are both case-SENSITIVE, so it would
// keep "Phoenix" and "phoenix" as separate rows and select neither. Asset folder
// lookups are effectively case-folded for a streaming client, so treating them as
// one entry is right here even though it is not literal parity.
func buildIniswapChoices(dst []string, own string, wardrobe []string, worn string) []string {
	dst = dst[:0]
	if own == "" {
		return dst // no character claimed yet: AO2 hides the pair (courtroom.cpp:5323-5328)
	}
	dst = append(dst, own)
	for _, w := range wardrobe {
		if w == "" || listHasFold(dst, w) {
			continue // empty entry, or the own folder / an earlier duplicate
		}
		dst = append(dst, w)
	}
	if worn != "" && !listHasFold(dst, worn) {
		dst = append(dst, worn)
	}
	return dst
}

// listHasFold reports case-insensitive membership in a list. Named apart from
// scenemaker.go's containsFold, which is a SUBSTRING search over one string —
// two different questions that read almost identically at a call site.
func listHasFold(list []string, want string) bool {
	for _, s := range list {
		if strings.EqualFold(s, want) {
			return true
		}
	}
	return false
}

// iniswapCurrentIndex finds the worn folder's row. Scans the WHOLE slice from 0
// and returns the loop index directly, exactly as AO2 does (courtroom.cpp:5340-5347)
// — deliberately not a scan over choices[1:] with a re-offset, because that
// re-offset is the off-by-one that would make iniswap_remove delete the wrong
// wardrobe entry.
//
// 0 (the player's own folder) when nothing matches, which is also the
// "not wearing anything" answer.
func iniswapCurrentIndex(choices []string, active string) int {
	if active == "" {
		return 0
	}
	for i, c := range choices {
		if strings.EqualFold(c, active) {
			return i
		}
	}
	return 0
}

// ensureIniswapChoices rebuilds the option list when the character or the saved
// wardrobe changes.
//
// The guard is two plain comparisons — a string and an atomic counter load —
// because WardrobeList CLONES the slice on every call, so an unguarded rebuild
// would allocate on the settled render path once per frame.
func (a *App) ensureIniswapChoices() {
	own := a.myCharName()
	gen := a.d.Prefs.WardrobeGeneration()
	worn := a.activeCharName()
	if a.iniswapChoicesForChar == own && a.iniswapChoicesGen == gen && a.iniswapChoicesWorn == worn {
		return
	}
	a.iniswapChoicesForChar, a.iniswapChoicesGen, a.iniswapChoicesWorn = own, gen, worn
	var wardrobe []string
	if own != "" {
		wardrobe = a.d.Prefs.WardrobeList(a.serverKey)
	}
	a.iniswapChoices = buildIniswapChoices(a.iniswapChoices, own, wardrobe, worn)
}
