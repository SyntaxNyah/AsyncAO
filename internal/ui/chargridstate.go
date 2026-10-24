package ui

// charGridState owns charsel state.
type charGridState struct {
	// --- char select ---
	charSearch string
	charScroll int32
	charTab    int // charTabServer | charTabWardrobe (grid contents swap)
	// charShowTaken is AO2's char_taken FILTER checkbox (charselect.cpp:99 seeds it
	// CHECKED; :360-364 hides taken characters when it is cleared). It is not an
	// indicator and it is not persisted: AO2 resets it per join, and so does a fresh
	// session here. Only the themed canvas draws the checkbox, so the classic screen
	// passes `true` explicitly rather than reading this — a filter with no control
	// would be a trap.
	charShowTaken bool
	// charListSel is the roster index highlighted in the themed name column
	// (char_list), -1 for none. It also outlines the matching grid cell, which is the
	// whole point of drawing the column: the two views are one selection. Reset on a
	// tab switch, because the two tabs index different slices.
	charListSel int
	// charListScroll is the name column's own scroll offset. Independent of the
	// grid's: the column is one name per row and scrolls far faster.
	charListScroll int32
	// wardrobeMembers is the lowercased wardrobe set for the current server,
	// powering the ★ star state in the Characters grid. Rebuilt only when the
	// server or the wardrobe generation changes (ensureWardrobeMembers), so the
	// per-cell star lookup stays a lock-free, alloc-free map read.
	wardrobeMembers    map[string]bool
	wardrobeMembersFor string // serverKey the set was built for
	wardrobeMembersGen uint64 // config.WardrobeGeneration() it was built at
	// lowered-query memos: search filters run per frame; re-lowering the
	// query allocated on every one of them.
	charQ loweredCache
	pairQ loweredCache
	iniQ  loweredCache
	// charLower caches lowercased char names for the search filter —
	// without it a 4000-char grid pays two ToLower allocations per char
	// per frame while a query is active. Invalidated on EventCharsUpdated.
	charLower []string
	// charHoverIDs caches the char-select grid's per-cell HoverPreview id
	// ("char:"+name). It was a runtime concat at the call site, i.e. one heap
	// allocation per VISIBLE CELL per frame — 63 of them on a themed 7x9 page and
	// more on a big classic window, for a string that only changes when the roster
	// does. Built and invalidated in lockstep with charLower.
	charHoverIDs []string
	// charSelTitle caches the char-select heading ("Choose a character — <server>"),
	// which was two allocations per frame for a string that changes once per connect.
	// charSelTitleFor is the serverName it was built for.
	charSelTitle    string
	charSelTitleFor string
}
