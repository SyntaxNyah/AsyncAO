package ui

import "strings"

// The theme's iniswap_dropdown / iniswap_remove pair. AO2 builds the list with
// iniswaps.prepend(char_list.at(m_cid).name); iniswaps.removeDuplicates() (courtroom.cpp:5331-5332), so the player's
// own folder is always index 0 and the saved swaps follow. The remove button is offered only while something other than
// index 0 is worn (:5323-5328 hides the pair when m_cid is -1).
//
// The list logic is pure functions, not inline at the draw site, because the index feeds RemoveWardrobe, which
// persists: an off-by-one deletes a wardrobe entry the user did not pick.

// buildIniswapChoices returns own-folder-first, then the saved wardrobe, then the worn folder if it is in neither. dst
// is truncated and reused (the draw site calls this behind a generation guard, so it must not allocate per frame).
//
// The trailing "worn if absent" entry matters: AO2's combobox is editable (InsertAtBottom, courtroom.cpp:938-939) and
// rewrites iniswaps.ini on every change, so what you wear is always listed. AsyncAO can wear a folder that was never
// saved (wearFromMenu uses the server's iniswap.txt, not AddWardrobe); without it the dropdown shows your own character
// and the remove button hides.
//
// Matching is case-insensitive, unlike AO2's case-sensitive removeDuplicates and compare, which would list "Phoenix"
// and "phoenix" separately.
// case-folded for a streaming client.
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

// listHasFold reports case-insensitive membership in a list. Not scenemaker.go's containsFold, which is a substring
// search over one string.
func listHasFold(list []string, want string) bool {
	for _, s := range list {
		if strings.EqualFold(s, want) {
			return true
		}
	}
	return false
}

// iniswapCurrentIndex finds the worn folder's row. It scans the whole slice from 0 and returns the loop index, as AO2
// does (courtroom.cpp:5340-5347); a scan over choices[1:] with a re-offset is the off-by-one that would make
// iniswap_remove delete the wrong wardrobe entry. 0 (the player's own folder) when nothing matches or nothing is worn.
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

// ensureIniswapChoices rebuilds the option list when the character or saved wardrobe changes. The guard is two plain
// comparisons and an atomic load because WardrobeList clones the slice on every call, which would allocate every frame.
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
		wardrobe = a.d.Prefs.Load().WardrobeList(a.serverKey)
	}
	a.iniswapChoices = buildIniswapChoices(a.iniswapChoices, own, wardrobe, worn)
}

// pos_remove: the X beside the position dropdown that reverts to the character's own declared side.

// defaultSide is the active character's char.ini side, AO2's default_side (courtroom.cpp:4723-4726, get_char_side). An
// empty charDefaultSide means not known yet or none declared, and get_char_side answers "wit" then
// (text_file_functions.cpp:480-483), matching mySide()'s fallback, so pos_remove stays hidden while a char.ini is in
// flight.
func (a *App) defaultSide() string {
	if a.charDefaultSide != "" {
		return a.charDefaultSide
	}
	return "wit"
}

// posRemoveVisible reports whether the revert-position button is offered: only when the current side differs from the
// character's default. AO2: `p_side.isEmpty() || p_side == default_side()` hides it (courtroom.cpp:5285-5292). The
// isEmpty arm mirrors AO2's guard even though mySide() never returns "".
func posRemoveVisible(side, def string) bool {
	return side != "" && side != def
}
