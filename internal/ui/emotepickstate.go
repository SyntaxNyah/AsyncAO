package ui

import (
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
)

// emotePickState owns emotepick state.
type emotePickState struct {
	emotes   []courtroom.Emote
	emoteIdx int
	// Emote-grid scroll position (emotescroll.go owns every derived answer). The
	// stored value is the first visible ROW, not a page: the wheel scrolls one row
	// per notch while the arrows and the "page x/y" readout stay page-shaped.
	emoteRow         int
	emoteCols        int
	emoteRowsPerPage int
	emoteTotalRows   int
	emotePerPage     int // cols*rowsPerPage, kept as its own field: several sites ask "is a grid laid out yet?"
	// SFX picker (IC-bar dropdown): the chosen sound that OVERRIDES this character's
	// emote sound on every send until set back to "auto". sfxChoices[0]="SFX: auto"
	// (the emote's own sound), then the character's distinct emote sounds; idx 0 = auto.
	// Rebuilt by ensureSFXChoices when the character (or its emote count)
	sfxChoiceIdx       int
	sfxChoices         []string
	sfxChoicesForName  string
	sfxChoicesForCount int
	// effects_dropdown selection for the next message (AO2 ui_effects_dropdown).
	// defaults true, so courtroom.cpp:2348's reset never fires out of the box (see
	// clearEffectsAfterSend, which owns the whole rule).
	overlayChoiceIdx int
	// emote_dropdown's option list (#21 label 11), cached with the same
	// plain-field guard as sfxChoices above and for the same reason: it is
	// rebuilt from a draw site that runs every frame. Per-tab, like a.emotes
	// itself — the zero values are correct for a fresh session, so
	// resetSessionState needs no seeding.
	emoteChoices         []string
	emoteChoicesForName  string
	emoteChoicesForCount int
	// iniswap_dropdown's option list (#21 label 11), same guarded-cache shape.
	// The wardrobe guard is a generation counter because WardrobeList CLONES —
	// rebuilding unguarded would allocate every frame on the settled path.
	iniswapChoices        []string
	iniswapChoicesForChar string
	iniswapChoicesWorn    string
	iniswapChoicesGen     uint64
	// #12 SFX Browser: an opt-in modal that expands the dropdown with persisted favourites,
	// per-row preview, and a free-text "use any sound name" entry. It picks into the SAME
	// sfxChoiceIdx override (find-or-append), so the dropdown reflects whatever it chose.
	showSfxBrowser   bool
	sfxBrowserQuery  string // the search / free-text field (filters the list; usable as an exact name)
	sfxBrowserScroll int32
	// emotePageLabel memoizes the "page x/y · N emotes" counter so the per-frame
	// emote-grid draw allocates nothing while paging is stable; rebuilt only when
	// {page, pages, total} change (same idiom as the generation-cached pages).
	emotePageLabel    string
	emotePageLabelKey [3]int
	// emoteAsk[i] paces demand for emote i's button art (drawEmoteRow).
	emoteAsk []time.Time
}
