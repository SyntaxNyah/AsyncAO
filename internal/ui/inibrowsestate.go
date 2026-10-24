package ui

import "time"

// iniBrowseState owns inibrowse state.
type iniBrowseState struct {
	// --- wardrobe / iniswap (client favourites + server iniswap.txt) ---
	iniChar      string   // active override folder ("" = picked character)
	pendingIni   string   // wear this once PV confirms (char-select joins)
	iniServer    []string // the server's iniswap.txt names (may be empty)
	iniList      []string // merged menu: wardrobe first, then server extras
	iniWardrobe  []bool   // parallel to iniList: wardrobe membership (star)
	iniServerMem []bool   // parallel to iniList: is in the server's iniswap.txt (Iniswaps tab filter)
	iniLower     []string // lowercased names for the search filter
	iniFolders   []string // parallel to iniList: each entry's folder ("" = unsorted)
	// iniHoverIDs caches each wardrobe cell's HoverPreview id ("iniswap:"+name),
	// parallel to iniList. It was a runtime concat inside the cell draw, i.e. one heap
	// allocation per VISIBLE CELL per frame on three grids — the exact twin of the
	// Characters grid's charHoverIDs, and it has to be the twin now that both tabs of
	// char select lay out on one plan. Rebuilt with the list (rebuildIniMenu).
	iniHoverIDs []string
	// A wardrobe ★ toggle is DEFERRED out of the cell to after the grid loop: it
	// rebuilds (and shrinks) the iniList/iniWardrobe/iniFolders slices the loop is
	// ranging, so toggling mid-loop panicked on a remove (the crash report).
	iniFavPending    string // char to toggle ("" = none)
	iniFavPendingAdd bool   // true = add to wardrobe, false = remove
	iniListErr       string
	iniBusy          bool
	showIni          bool
	showReset        bool // factory-reset confirmation pop-up (Settings)
	iniSearch        string
	iniAdd           string   // "add folder to wardrobe" input
	iniSwapMode      bool     // Characters tab: click iniswaps instead of switching char (off=switch; per session)
	iniFolder        string   // open wardrobe folder ("" = top level/root, else folder name)
	iniNewFold       string   // "new folder" text input
	iniMenuChar      string   // wardrobe char with an open "move to folder" menu ("" = none)
	iniMenuAt        [2]int32 // that menu's top-left (cursor at right-click)
	iniHoverChar     string   // wardrobe char under the cursor this frame (number-key quick-file)
	// Drag-to-file (app-drawer style): drag a character cell onto a folder
	// chip to file it. iniDragChar is armed on press; iniDragging flips once
	// the move passes iniDragThreshold (and then suppresses the wear-click).
	iniDragChar     string
	iniDragStart    [2]int32
	iniDragging     bool
	iniPrevDown     bool // mouse-held tracker for press detection in the modal
	iniPressed      bool // mouse went down this frame (computed per frame)
	iniScroll       int32
	iniBrowseScroll int32       // separate scroll for the Iniswaps (browse-all) tab
	iniAsk          []time.Time // demand pacing stamps, parallel to iniList
	// iniBase is the Iniswaps tab's "From your base" source: the server's character
	// ROSTER plus the character folders found under the user's local mounts. It is a
	// separate browser (iniswapbase.go) rather than more entries in iniList on
	// purpose — iniList is the wardrobe's index-keyed backing store, ranged whole by
	// char select's Wardrobe tab, so a 4000-name roster merged into it would flood
	// that grid and invalidate every cached icon page.
	iniBase iniBaseBrowser
}
