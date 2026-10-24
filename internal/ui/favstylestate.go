package ui

import "github.com/SyntaxNyah/AsyncAO/internal/render"

// favStyleState owns favstyle state.
type favStyleState struct {
	// Emote favourites view (#77): emoteFavSet holds the active character's
	// favourited emote indices for lock-free O(1) star lookups, and emoteVisible
	// is the list of emote indices the grid shows (all, or favs-only) — a reused
	// buffer. Both are rebuilt by refreshEmoteView ONLY when the guard key
	// (char/favs-only/emote count/edit-rev) changes, so a steady-state frame does
	// one cheap compare and nothing else.
	emoteFavSet      map[int]struct{}
	emoteVisible     []int
	favBoxList       []int  // current character's favourite emote indices (always; for the floating box)
	emoteFavRev      int    // bumped on every favourite toggle to invalidate the view
	emoteViewChar    string // guard: character the view was built for
	emoteViewFavOnly bool   // guard: favs-only state the view was built for
	emoteViewLen     int    // guard: len(emotes) the view was built for
	emoteViewRev     int    // guard: emoteFavRev the view was built for
	// Floating favourite-emotes box (#85): movable geometry (session, like the
	// Extras box) + the drag grab offset. Open state is the FavEmoteBox pref.
	favBoxX, favBoxY int32
	favBoxPlaced     bool
	favBoxDragging   bool
	favBoxGrabDX     int32
	favBoxGrabDY     int32
	// Floating Sprite Style box (#104): the non-modal, draggable picker for the
	// transmitted sprite style — open state + movable geometry are session-only.
	showStyleBox     bool
	styleBoxX        int32
	styleBoxY        int32
	styleBoxPlaced   bool
	styleBoxDragging bool
	styleBoxResizing bool  // the right-edge width grip is being dragged
	styleBoxUserW    int32 // user-set width (0 = the default styleBoxW); height stays content-driven
	styleBoxScroll   int32 // vertical scroll of the box body (the box clamps to the window; the body scrolls inside — #C); clamped to [0, maxScroll] every frame
	styleBoxGrabDX   int32
	styleBoxGrabDY   int32
	// Custom glitch fringe-colour hex buffers (the style box's A/B fields): they
	// hold in-progress typing and mirror the pref's effective colour when unfocused.
	glitchHexA string
	glitchHexB string
	// Style-box live preview: collapse state, the background page cache (the
	// pair-ghost pattern; keys are full URLs so per-server separation is
	// structural), and the sprite prefetch dedupe (one warm per emote base).
	stylePrevOff     bool
	stylePrevBgPages []*render.TexturePage
	stylePrevBgGen   uint64
	stylePrevBgKey   string
	stylePrevWarm    string
}
