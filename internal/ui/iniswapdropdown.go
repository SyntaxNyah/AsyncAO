package ui

import "strings"

// The theme's iniswap_dropdown / iniswap_remove pair. AO2 builds the list with
// iniswaps.prepend(char_list.at(m_cid).name); iniswaps.removeDuplicates() (courtroom.cpp:5331-5332), so the player's
// own folder is always index 0. The remove button is offered only while something other than index 0 is worn
// (:5323-5328 hides the pair when m_cid is -1).

// buildIniswapChoices returns the own folder, then the worn folder when it differs. dst is truncated and reused, so the
// draw site does not allocate per frame.
//
// The worn entry matters: AO2's combobox is editable (InsertAtBottom, courtroom.cpp:938-939), so what you wear is always
// listed; without it the dropdown shows your own character and the remove button hides.
//
// Matching is case-insensitive, unlike AO2's case-sensitive removeDuplicates and compare, which would list "Phoenix"
// and "phoenix" separately.
func buildIniswapChoices(dst []string, own, worn string) []string {
	dst = dst[:0]
	if own == "" {
		return dst // no character claimed yet: AO2 hides the pair (courtroom.cpp:5323-5328)
	}
	dst = append(dst, own)
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

// iniswapCurrentIndex finds the worn folder's row, scanning from 0 as AO2 does (courtroom.cpp:5340-5347). 0 (the
// player's own folder) when nothing matches or nothing is worn.
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

// ensureIniswapChoices rebuilds the option list when the own or worn folder changes.
func (a *App) ensureIniswapChoices() {
	own := a.myCharName()
	worn := a.activeCharName()
	if a.iniswapChoicesForChar == own && a.iniswapChoicesWorn == worn {
		return
	}
	a.iniswapChoicesForChar, a.iniswapChoicesWorn = own, worn
	a.iniswapChoices = buildIniswapChoices(a.iniswapChoices, own, worn)
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
