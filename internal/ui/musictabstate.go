package ui

import "time"

// musicTabState owns musicTab state.
type musicTabState struct {
	// musicCount / areaCount memoize the "<shown> / <total>" header counters of
	// the Music and Areas lists (shownTotalMemo, screens.go) — same reason as
	// emotePageLabel: a bare Sprintf there is one allocation per frame for as long
	// as the tab is open. Separate slots because both panels can be on screen at
	// once. Per-SESSION (the counts come from a.sess.Music / a.sess.Areas), so
	// they park and restore with the tab exactly like the emote memo above.
	musicCount shownTotalMemo
	// musicNowLabel memoizes the Music header's "Now playing: <track>" readout for
	// exactly the same reason (nowPlayingMemo, screens.go).
	musicNowLabel nowPlayingMemo
	musicScroll   int32
	// Music-list search (AO2/webAO parity): the query plus a memoized filter so
	// a list of thousands isn't re-scanned (and re-lowercased — that allocates)
	// every frame. musicFiltered holds matching indices into a.sess.Music.
	musicSearch     string
	musicFiltered   []int
	musicFilterMemo musicFilterKey
	// Music-list category collapse (Issue #17 — AO2/KFO parity: categories
	// were flat with no fold triangle and no Expand/Collapse All). Collapsed
	// state is keyed by the category HEADER's index into a.sess.Music (not
	// its text — servers can reuse a category name, and the index is unique
	// per occurrence). Absent from the map = expanded (the default AO2 look).
	musicCollapsed   map[int]bool
	musicCollapseGen uint64
	musicRows        []musicRow
	musicGroupMemo   musicGroupKey
	volStripOn       bool // the log panel's toggleable on-screen volume strip
	musicVolMode     bool // Music tab shows the volume sliders instead of the track list
	// musicStartedAt stamps when the ACTIVE tab's current area track began playing
	// (frame clock). It is App-level and single-valued — it does NOT park per tab
	// (an earlier comment claimed it did; it never has). Re-stamped by the active-tab
	// EventMusic handler on every track change. Its ONLY reader is resumeActiveTabMusic's
	// FALLBACK seek estimate (now-minus-start, capped) for the case where no per-URL
	// SwappedOutSnap exists; SwappedOutSnap is the drift-free primary. Because it tracks
	// the active tab, the estimate is only rough for a tab whose song changed while
	// backgrounded — acceptable: that path also lacks a snapshot, and a backgrounded
	// change that IS followed audibly (MusicAcrossTabs ON, routeBackgroundEvent) makes
	// the return a same-track no-op that never seeks. Zero = unknown (resume from top).
	musicStartedAt time.Time
}
