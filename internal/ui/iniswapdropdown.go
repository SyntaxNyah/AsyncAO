package ui

import "strings"

// The theme's iniswap_dropdown / iniswap_remove pair (#21 label 11 — the
// reporter's "iniswap missing").
// (../AO2-Client/src/courtroom.cpp:5331-5332):
// The list logic lives here as PURE functions rather than inline at the draw
// site, because the index it produces is destructive: it feeds RemoveWardrobe,
// which PERSISTS. An off-by-one deletes a wardrobe entry the user did not pick,

// buildIniswapChoices returns own-folder-first, then the saved wardrobe, then the
// worn folder if it is in neither. dst is truncated and reused (the draw site
// calls this behind a generation guard, so it must not allocate per frame).
// (courtroom.cpp:938-939) and it rewrites iniswaps.ini on every change, so
// Matching is case-insensitive, which is a deliberate divergence: AO2's
// removeDuplicates and its selection compare are both case-SENSITIVE, so it would
// keep "Phoenix" and "phoenix" as separate rows and select neither. Asset folder
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
	if a.emotepick.iniswapChoicesForChar == own && a.emotepick.iniswapChoicesGen == gen && a.emotepick.iniswapChoicesWorn == worn {
		return
	}
	a.emotepick.iniswapChoicesForChar, a.emotepick.iniswapChoicesGen, a.emotepick.iniswapChoicesWorn = own, gen, worn
	var wardrobe []string
	if own != "" {
		wardrobe = a.d.Prefs.Load().WardrobeList(a.srvMeta.serverKey)
	}
	a.emotepick.iniswapChoices = buildIniswapChoices(a.emotepick.iniswapChoices, own, wardrobe, worn)
}

// pos_remove: the little X beside the position dropdown that reverts to the
// character's own declared side.

// defaultSide is the active character's char.ini side, AO2's default_side
// (courtroom.cpp:4723-4726, which is get_char_side). An empty charDefaultSide
// means "not known yet, or none declared", and get_char_side answers "wit" in
// exactly that case (text_file_functions.cpp:480-483) — so the loading window
// resolves to the same value AsyncAO's mySide() already falls back to, which is
// what makes pos_remove correctly HIDDEN while a char.ini is in flight.
func (a *App) defaultSide() string {
	if a.compose.charDefaultSide != "" {
		return a.compose.charDefaultSide
	}
	return "wit"
}

// posRemoveVisible reports whether the revert-position button should be offered:
// only when the current side differs from the character's own default. AO2:
// `p_side.isEmpty() || p_side == default_side()` hides it (courtroom.cpp:5285-5292).
//
// The isEmpty arm is kept even though AsyncAO's mySide() never returns "" (it
// falls back to "wit"). It mirrors AO2's guard for a combobox that genuinely can
// be empty, and costs nothing; dropping it would make this a different predicate
// from the one being cited.
func posRemoveVisible(side, def string) bool {
	return side != "" && side != def
}
