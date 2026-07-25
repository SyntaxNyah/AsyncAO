package ui

// Tab focus order for the CHAT fields (playtest: "focus on ic, press tab, it
// goes to the showname instead of the OOC box").
//
// AO2-Client never calls setTabOrder (0 hits across ../AO2-Client/src), so its
// Tab chain is Qt's implicit one: widget CONSTRUCTION order. courtroom.cpp
// builds ui_ic_chat_name (187) -> ui_ic_chat_message (193) ->
// ui_ooc_chat_message (205) -> ui_ooc_chat_name (210) -> ui_music_search (219),
// i.e. Tab out of the IC box lands on the OOC box. AO2-Client wins, so that is
// the chain we reproduce.
//
// Our fieldSeq is pure DRAW order (Ctx.textField, ui.go) and NO layout draws in
// that order: the default classic layout draws the whole right column — OOC box
// included — BEFORE the IC bar (drawCourtroom, screens.go), and the themed
// layout draws the OOC input FIRST and the showname LAST (drawCourtroomThemed,
// theme_layout.go). Result: Tab from "ic" wrapped to the log search (classic)
// or landed on the showname (themed).
//
// Ranking is by ROLE, never by a fixed id list, because the same role has
// different ids per layout: the OOC input is "oocmsg" in the default box
// (drawOOCPanel), "ooc" on the Legacy bottom bar (drawICControls) and "ooc"
// again in the themed layout — and each box may or may not draw on a given
// frame. A role with nothing drawn simply owns no slot, so a layout that hides
// the OOC box keeps Tab inside what is actually on screen (focusing an undrawn
// field would swallow keystrokes with no caret and no visible box).
const (
	tabRankShowname = iota // AO2 ui_ic_chat_name
	tabRankIC              // AO2 ui_ic_chat_message
	tabRankOOC             // AO2 ui_ooc_chat_message
	tabRankOOCName         // AO2 ui_ooc_chat_name
	tabChainRanks          // count of roles — a loop bound, never a rank
)

// tabRankNone marks every field that is NOT part of the chat chain (search
// boxes, panel fields, the pinned pane's "ic-split"): they keep pure draw order.
const tabRankNone = -1

// tabChainSpanCap bounds the window of fieldSeq this may rewrite — the distance
// from the first chat field to the last, inclusive. The widest real frame is the
// themed merged-log layout (notes tab + music list interleaved between the OOC
// and IC inputs): noteadd, ooc, oocname, musicsearch, ic, icshownameov = 6. The
// cap leaves generous slack; past it the reorder bails and draw order stands, so
// a pathological frame degrades to the old behaviour instead of shuffling half
// the screen. It also sizes the fixed stack scratch below, which is what keeps
// the reorder allocation-free.
const tabChainSpanCap = 16

// tabRank maps a field id to its AO2 chat-chain role, or tabRankNone.
// A switch, not a map: no allocation, no init order, and the id list is the one
// reviewable artifact for "what counts as a chat field".
func tabRank(id string) int {
	switch id {
	case "icshownameov": // classic drawICInputRow / themed drawCourtroomThemed
		return tabRankShowname
	case icFieldID: // "ic" — the IC message box in both layouts
		return tabRankIC
	case "oocmsg", "ooc": // default OOC box / Legacy bottom bar / themed OOC input
		return tabRankOOC
	case "oocname2", "oocname": // default OOC box / themed OOC name
		return tabRankOOCName
	}
	return tabRankNone
}

// orderTabChain rewrites seq IN PLACE so the chat fields it contains form one
// contiguous block in AO2 role order, starting at the slot the FIRST chat field
// already occupied; any non-chat field that happened to be interleaved between
// them slides to just after the block, keeping its own relative order.
// Everything outside that window is untouched, so a search box or a floating
// panel's field never jumps around.
//
// Stable within a role (draw order breaks ties), which is what the Legacy hybrid
// needs: with "OOC in the log tab" selected AND the OOC tab open, the tab's own
// input and the always-visible bottom bar are both tabRankOOC and both edit
// a.oocInput — the one drawn first wins the slot.
//
// Idempotent (a second pass sees an already-contiguous, already-sorted window),
// which matters because BeginFrame may cycle several times against the same
// fieldSeq across draw-less frames.
//
// Pure and allocation-free: the scratch is a fixed [tabChainSpanCap]string on
// the stack and the write-back is one copy. Called ONLY on a Tab press, never on
// a render frame, so the whole-screen 0-alloc gates never even reach it.
func orderTabChain(seq []string) {
	first, last, n := -1, -1, 0
	for i, id := range seq {
		if tabRank(id) == tabRankNone {
			continue
		}
		if first < 0 {
			first = i
		}
		last, n = i, n+1
	}
	if n < 2 {
		return // 0 or 1 chat field on screen: there is no order to impose
	}
	span := last - first + 1
	if span > tabChainSpanCap {
		return // past the named cap: leave draw order exactly as it was
	}
	var win [tabChainSpanCap]string
	w := 0
	for r := 0; r < tabChainRanks; r++ { // role-major; draw order within a role
		for i := first; i <= last; i++ {
			if tabRank(seq[i]) == r {
				win[w] = seq[i]
				w++
			}
		}
	}
	for i := first; i <= last; i++ { // then the interleaved non-chat fields
		if tabRank(seq[i]) == tabRankNone {
			win[w] = seq[i]
			w++
		}
	}
	copy(seq[first:last+1], win[:w])
}
