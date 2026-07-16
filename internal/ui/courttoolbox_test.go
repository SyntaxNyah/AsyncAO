package ui

import (
	"strings"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/veandco/go-sdl2/sdl"
)

// TestHideablePanelsHaveShortLabels pins the editor-toolbox requirement (#27): every
// hideable chrome panel carries a SHORT chip label, so no toolbox chip renders blank.
func TestHideablePanelsHaveShortLabels(t *testing.T) {
	for _, p := range hideablePanels {
		if p.short == "" {
			t.Errorf("hideablePanel %q (%q) has no short chip label", p.id, p.label)
		}
		if p.label == "" {
			t.Errorf("hideablePanel %q has no dialog label", p.id)
		}
	}
}

// TestHideableForSlot pins the drag show/hide mapping (#27 slice 2): a slot key
// resolves to its hideable element id, and non-mapped slots return "".
func TestHideableForSlot(t *testing.T) {
	if got := hideableForSlot(slotEmotes); got != panelEmotes {
		t.Errorf("hideableForSlot(emotes) = %q, want %q", got, panelEmotes)
	}
	if got := hideableForSlot(slotRightCol); got != panelLog {
		t.Errorf("hideableForSlot(rightcol) = %q, want %q", got, panelLog)
	}
	if got := hideableForSlot("ctrl.mods"); got != "ctrl.mods" {
		t.Errorf("hideableForSlot(ctrl.mods) = %q, want ctrl.mods", got)
	}
	// The viewport and the IC bar are not hide targets; toggle-only pieces (hp) have
	// no slot. Both must resolve to "" so a drag-release there never hides anything.
	if got := hideableForSlot(slotViewport); got != "" {
		t.Errorf("hideableForSlot(viewport) = %q, want empty", got)
	}
	if got := hideableForSlot(panelHP); got != "" {
		t.Errorf("hideableForSlot(hp) = %q, want empty (no slot)", got)
	}
}

// TestHideableSlotKeysKnown guards the map's keys against drift: every mapped id must
// be a real hideable panel or button.
func TestHideableSlotKeysKnown(t *testing.T) {
	known := make(map[string]bool)
	for _, p := range hideablePanels {
		known[p.id] = true
	}
	for _, b := range hideableButtons {
		known[b.id] = true
	}
	for id := range hideableSlot {
		if !known[id] {
			t.Errorf("hideableSlot maps unknown id %q", id)
		}
	}
}

// TestToolboxIDsUnique guards against a duplicate id across the panel + button sets,
// which would make two toolbox chips toggle the same hidden-state key.
func TestToolboxIDsUnique(t *testing.T) {
	seen := make(map[string]string)
	for _, p := range hideablePanels {
		if prev, dup := seen[p.id]; dup {
			t.Errorf("duplicate hideable id %q (panel %q and %q)", p.id, p.short, prev)
		}
		seen[p.id] = p.short
	}
	for _, b := range hideableButtons {
		if prev, dup := seen[b.id]; dup {
			t.Errorf("duplicate hideable id %q (button %q and %q)", b.id, b.label, prev)
		}
		seen[b.id] = b.label
	}
	for _, b := range hideableRosterButtons {
		if prev, dup := seen[b.id]; dup {
			t.Errorf("duplicate hideable id %q (roster button %q and %q)", b.id, b.label, prev)
		}
		seen[b.id] = b.label
	}
}

// TestToolIconAllocFree pins the A1 vector-icon draw at zero allocations: the
// collapsed grip's discoverability ring is 0-alloc (gated by the whole-screen
// courtroom gate), but the icon chips draw on the EXPANDED/pinned path which the
// whole-screen gate does NOT cover (the headless cursor never hovers the grip),
// so this measures drawToolIcon directly. Constant geometry fed to c.Fill (which
// copies into c.cgoRect) must never heap-allocate.
func TestToolIconAllocFree(t *testing.T) {
	ren, cleanup := newCaptureHarness(t)
	defer cleanup()
	c, err := NewCtx(ren)
	if err != nil {
		t.Skipf("Ctx unavailable: %v", err)
	}
	r := sdl.Rect{X: 10, Y: 10, W: compactChipH, H: compactChipH}
	col := ColText
	// Every iconKind the chip set uses, drawn in a tight loop, must be 0-alloc.
	kinds := []iconKind{iconTheater, iconEdit, iconEyeOff, iconPin, iconGrid}
	draw := func() {
		for _, k := range kinds {
			drawToolIcon(c, k, r, col)
		}
	}
	draw() // warm any lazy renderer state
	if n := testing.AllocsPerRun(200, draw); n != 0 {
		t.Fatalf("drawToolIcon allocates %.1f/op, want 0 — an icon draw shipped a per-call alloc", n)
	}
}

// TestUICfgRetired pins the A1 retirement of the showUICfg dialog + the un-strand
// path: the hide-UI hotkey opens the pinned per-piece panel (toolboxPinned +
// toolboxPieces), and it does so EVEN when the toolbox grip itself is hidden via
// panelHidden(panelToolbox) — so a user who hid the toolbox can still reach the
// per-piece list. The old showUICfg field is gone; this is now the only trigger.
func TestUICfgRetired(t *testing.T) {
	a := testTabApp(t)
	a.sess = &courtroom.Session{}        // handleHotkeys early-returns without a session
	a.hidden = map[string]bool{}         // App init seeds this; bare test fixture must too
	a.setPanelHidden(panelToolbox, true) // hide the grip: the un-strand scenario
	a.ctx.hotkey = sdl.K_f               // default hotkeyUIChrome bind is "f"

	a.handleHotkeys()

	if !a.toolboxPinned || !a.toolboxPieces {
		t.Fatalf("hotkeyUIChrome must open the pinned per-piece panel (pinned=%v pieces=%v), even with the toolbox hidden", a.toolboxPinned, a.toolboxPieces)
	}
	// The panel draws gated ONLY on pinned+pieces, never on panelHidden(panelToolbox):
	// prove the grip stays hidden but the panel is armed.
	if !a.panelHidden(panelToolbox) {
		t.Fatal("test setup: the toolbox grip should still be hidden")
	}
}

// TestEditLayoutHotkey pins FIX 2b: the Edit-Layout hotkey (default Ctrl+F2)
// opens the live layout editor — the keyboard un-strand path for a user who can't
// reach the toolbox Edit chip. With no theme layout, openLayoutEditor arms the
// classic slot editor (classicEdit).
func TestEditLayoutHotkey(t *testing.T) {
	a := testTabApp(t)
	a.sess = &courtroom.Session{} // handleHotkeys early-returns without a session
	// Bind the action to whatever name SDL gives Ctrl+F2's keycode (headless
	// GetKeyName can differ from the "f2" default the dispatch compares against), so
	// the bind and the dispatch agree — this proves the WIRING, not the key-name
	// quirk. Skip only if key names aren't resolvable at all.
	key := strings.ToLower(sdl.GetKeyName(sdl.K_F2))
	if key == "" {
		t.Skip("SDL key names unavailable headless")
	}
	a.d.Prefs.SetHotkey(hotkeyEditLayout, key)
	a.ctx.hotkey = sdl.K_F2

	a.handleHotkeys()

	if !a.classicEdit {
		t.Fatal("hotkeyEditLayout (Ctrl+F2) must open the layout editor (classicEdit) on the default layout")
	}
}

// TestToolboxPiecesPanelClickable is the recon-demanded regression for the FIX 1
// BLOCKER: a click on a checkbox row INSIDE the pinned pieces panel must actually
// toggle the piece. The bug was that drawToolboxPieces ran inside drawCourtroom's
// fenced pass — boxFencesPointer blanks the pointer for the WHOLE pass while the
// cursor is over the panel, so every checkbox/scrollbar/Close was dead. Moving the
// draw post-courtroom (app.go, input restored) fixes it. This test drives
// drawToolboxPieces directly with a real (unfenced) pointer over the first row and
// asserts setPanelHidden fired; then it re-runs with the SAME pointer but the ctx
// fenced (the old in-pass condition) and asserts NOTHING toggles — pinning the
// blindness the move eliminated.
func TestToolboxPiecesPanelClickable(t *testing.T) {
	ren, cleanup := newCaptureHarness(t)
	defer cleanup()
	c, err := NewCtx(ren)
	if err != nil {
		t.Skipf("Ctx unavailable: %v", err)
	}
	a := testTabApp(t)
	// Real fonts + renderer so the panel actually draws and hit-tests; a live
	// surface (room + sess + no blocking popup) so extrasSurfaceLive is true; the
	// backing hidden map (it lives on App, not sessionState); and the panel's own
	// draw gate.
	a.ctx = c
	a.hidden = map[string]bool{}
	a.sess = courtroom.NewRehearsalSession("", nil)
	a.room = &courtroom.Courtroom{}
	a.toolboxPinned, a.toolboxPieces = true, true

	const w, h = int32(1280), int32(720)
	panel := a.toolboxPiecesRect(w, h)
	// The first checkbox row sits at (panel.X+pad, body.Y) with scroll 0, where
	// body.Y = panel.Y + toolboxPiecesHeaderH (mirrors drawToolboxPieces).
	firstID := hideablePanels[0].id
	rowX := panel.X + pad + 3
	rowY := panel.Y + toolboxPiecesHeaderH + 3
	if a.panelHidden(firstID) {
		t.Fatalf("test setup: %q should start shown", firstID)
	}

	// Case 1 (the fixed path): real pointer over the row, a committed click. The
	// checkbox must flip the piece to hidden — proof the panel gets real input.
	c.mouseX, c.mouseY = rowX, rowY
	c.clicked = true
	a.drawToolboxPieces(w, h)
	if !a.panelHidden(firstID) {
		t.Fatalf("a click on the first pieces-panel checkbox must hide %q — the panel is blind to input", firstID)
	}

	// Case 2 (the old bug): the SAME cursor + click, but with the ctx fenced exactly
	// as boxFencesPointer did inside drawCourtroom. Re-show the piece, fence, and
	// re-click: the fence blanks the pointer, so the checkbox must NOT toggle. This
	// pins WHY the draw had to move out of the fenced pass.
	a.setPanelHidden(firstID, false)
	if a.panelHidden(firstID) {
		t.Fatal("test setup: piece should be re-shown before the fenced case")
	}
	c.mouseX, c.mouseY = rowX, rowY
	c.clicked = true
	c.fencePointer() // what the courtroom pass did over the panel
	a.drawToolboxPieces(w, h)
	c.unfencePointer()
	if a.panelHidden(firstID) {
		t.Fatalf("a fenced pass must NOT toggle %q (this is the bug the move fixed)", firstID)
	}
}

// TestToolboxPiecesPanelModalFenced pins the A1 Phase 1 edit-mode contract: while a
// layout editor is armed its fence sets c.modalOn (classicEditFence/layoutEditFence),
// and modalOn — unlike ptrFenced — is NOT cleared by the deferred unfencePointer, so
// hovering() reads false and the pieces panel would draw CLICK-DEAD at the
// post-courtroom site. The post-court block in app.go therefore releases modalOn
// around the toolbox draw while editing, then restores it. This test proves both
// halves: modalOn blanks the checkbox, and clearing it (as app.go does) revives it.
func TestToolboxPiecesPanelModalFenced(t *testing.T) {
	ren, cleanup := newCaptureHarness(t)
	defer cleanup()
	c, err := NewCtx(ren)
	if err != nil {
		t.Skipf("Ctx unavailable: %v", err)
	}
	a := testTabApp(t)
	a.ctx = c
	a.hidden = map[string]bool{}
	a.sess = courtroom.NewRehearsalSession("", nil)
	a.room = &courtroom.Courtroom{}
	a.toolboxPinned, a.toolboxPieces = true, true

	const w, h = int32(1280), int32(720)
	panel := a.toolboxPiecesRect(w, h)
	firstID := hideablePanels[0].id
	rowX := panel.X + pad + 3
	rowY := panel.Y + toolboxPiecesHeaderH + 3
	if a.panelHidden(firstID) {
		t.Fatalf("test setup: %q should start shown", firstID)
	}

	// Case 1 (the edit-mode fence): modalOn set (as the editor's fence leaves it) —
	// the checkbox must NOT toggle, because hovering() is blanked. This is exactly
	// why a naive "just un-suppress the panel in edit" would ship a dead panel.
	c.mouseX, c.mouseY = rowX, rowY
	c.clicked = true
	c.modalOn = true
	a.drawToolboxPieces(w, h)
	c.modalOn = false
	if a.panelHidden(firstID) {
		t.Fatalf("with modalOn set (editor fence), the panel must be click-dead — got %q hidden", firstID)
	}

	// Case 2 (app.go's release path): clear modalOn around the draw, as the
	// post-courtroom edit block does — the same click must now toggle the piece.
	c.mouseX, c.mouseY = rowX, rowY
	c.clicked = true
	c.modalOn = false // app.go saves+clears modalOn while editing, then restores
	a.drawToolboxPieces(w, h)
	if !a.panelHidden(firstID) {
		t.Fatalf("with modalOn released (app.go edit path), a click must hide %q", firstID)
	}
}

// TestToolboxPieceSearchLowered pins the Phase-3 filter precompute: every hideable
// id has a LOWERED searchable entry (so per-frame matching never lowers a label),
// and the lowered text carries the label. Guards against a registry gaining an id
// the precompute misses.
func TestToolboxPieceSearchLowered(t *testing.T) {
	all := make([]struct{ id, label string }, 0)
	for _, p := range hideablePanels {
		all = append(all, struct{ id, label string }{p.id, p.label})
	}
	for _, b := range hideableButtons {
		all = append(all, struct{ id, label string }{b.id, b.label})
	}
	for _, b := range hideableRosterButtons {
		all = append(all, struct{ id, label string }{b.id, b.label})
	}
	for _, e := range all {
		s, ok := toolboxPieceSearch[e.id]
		if !ok {
			t.Errorf("toolboxPieceSearch missing id %q — precompute out of sync with a registry", e.id)
			continue
		}
		if s != strings.ToLower(s) {
			t.Errorf("toolboxPieceSearch[%q] = %q is not lowered — per-row match would allocate", e.id, s)
		}
		if e.label != "" && !strings.Contains(s, strings.ToLower(e.label)) {
			t.Errorf("toolboxPieceSearch[%q] = %q does not contain its lowered label %q", e.id, s, e.label)
		}
	}
}

// TestToolboxPieceMatches pins the filter's matching contract: an empty query
// matches everything (inert), a substring of a piece's label matches, and a
// non-matching query excludes it.
func TestToolboxPieceMatches(t *testing.T) {
	id := hideablePanels[0].id
	if !toolboxPieceMatches(id, "") {
		t.Errorf("empty query must match every piece (filter inert), %q excluded", id)
	}
	// A guaranteed-present token: the first panel's own lowered short/label text.
	tok := strings.ToLower(hideablePanels[0].short)
	if tok != "" && !toolboxPieceMatches(id, tok) {
		t.Errorf("query %q (from the piece's own label) must match %q", tok, id)
	}
	if toolboxPieceMatches(id, "zzzz-no-such-piece-token") {
		t.Errorf("a non-matching query must exclude %q", id)
	}
}

// TestToolboxFilteredContentHShrinks pins that a filter matching nothing collapses
// the scroll content to a single (empty) region — no dead scroll space — while an
// empty filter reports the full unfiltered height. Uses the guard-free counts, so
// no SDL is needed.
func TestToolboxFilteredContentHShrinks(t *testing.T) {
	a := testTabApp(t)
	// Derive showBtnGrid exactly as drawToolboxPieces does, so the empty-filter
	// height matches toolboxPiecesContentH regardless of the test theme.
	showBtnGrid := !a.d.Prefs.LegacyDevThemeOn()
	full := a.toolboxPiecesFilteredContentH("", showBtnGrid)
	if want := a.toolboxPiecesContentH(); full != want {
		t.Errorf("empty-filter content height = %d, want the unfiltered %d", full, want)
	}
	none := a.toolboxPiecesFilteredContentH("zzzz-no-such-piece-token", showBtnGrid)
	if none >= full {
		t.Errorf("a filter matching nothing must shrink the content height: got %d, full %d", none, full)
	}
}

// TestToolboxSectionVisibleCounts pins the section visible-count helpers the grid
// layout and content height rely on: empty query returns every entry, and a
// no-match query returns zero (so no stranded heading is drawn / counted).
func TestToolboxSectionVisibleCounts(t *testing.T) {
	if got, want := toolboxPanelsVisible(""), int32(len(hideablePanels)); got != want {
		t.Errorf("toolboxPanelsVisible(\"\") = %d, want %d", got, want)
	}
	if got := toolboxButtonsVisible("zzzz-no-such-piece-token"); got != 0 {
		t.Errorf("toolboxButtonsVisible(no-match) = %d, want 0", got)
	}
	if toolboxRosterHaveMatch("zzzz-no-such-piece-token") {
		t.Errorf("toolboxRosterHaveMatch(no-match) must be false so its heading is suppressed")
	}
	if !toolboxButtonsHaveMatch("") {
		t.Errorf("toolboxButtonsHaveMatch(\"\") must be true (filter inert)")
	}
}
