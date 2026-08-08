package ui

// THE THEME EDITOR (v1.90.0 W7a) — docs/wip/THEME-EDITOR-DESIGN.md §Q7 and §3.
//
// One concern: the SCREEN. The document it edits lives in themeeditordoc.go, the
// timeline in themeeditorop.go, the property rows in themeeditorinspector.go; this
// file owns the pixels, the input and the screen's own lifecycle.
//
// A REAL SCREEN WHOSE CONTENT IS THE COURTROOM, NOT AN INSET PREVIEW. The editor
// draws the live courtroom at FULL WINDOW SIZE with two translucent rails floating
// over it, so you edit at exactly the size the theme will play — 1:1, no mode hop
// for pixel work (the brief's requirement 1). Insetting the canvas would mean
// teaching fitDesignCanvas about reserved bands, and that function is shared with
// char select by product decision and is pinned by TestCharSelectCanvasHonoursThemeFit
// — the constraint produced the better product.
//
// Hold SPACE and both rails drop to editRailPeekAlpha so you see the pure frame.
// AN ALPHA CHANGE, NEVER A CLIP CHANGE, deliberately: the macOS blank-chatbox hunt
// established that SDL clip timing is backend-specific (Metal evaluates at USE
// time), so a peek that collapsed a clip is a class of bug this surface simply does
// not open.
//
// ZERO COST WHEN CLOSED. App carries ONE pointer, a.te, nil while the editor is
// shut. Nothing on the courtroom's frame path reads it, no goroutine, no timer, no
// allocation — the v1.89.0 "0 base = 0 cost" rule, benchmarked rather than asserted
// by TestClosedThemeEditorCostsNothing.

import (
	"strconv"
	"time"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// ---------------------------------------------------------------------------
// Named caps and metrics (hard rule 9)
// ---------------------------------------------------------------------------

const (
	// themeSelCap bounds the selection AND any bulk op's children. A FIXED ARRAY on
	// the editor, never a slice: the selection is walked every frame to outline what
	// is picked, and a []selRef would allocate on a gated path.
	themeSelCap = 64

	// editRailW is both rails' width. Wide enough for a label plus a value control at
	// the chrome font, narrow enough that two of them still leave a 4:3 canvas
	// readable at 1280 px.
	editRailW = 232

	// editBannerH matches layoutBannerH so the editor's header band lines up with the
	// legacy overlay editors' banner — the same band, in the same place, whichever
	// tool you came from.
	editBannerH = 26

	// editRailAlpha / editRailPeekAlpha are the rails' plate alpha normally and while
	// Space is held. 38 is faint enough to read the frame through and solid enough
	// that the rails do not appear to have been deleted.
	editRailAlpha     = 235
	editRailPeekAlpha = 38

	// editRowH is one element-rail row: a glyph, a name and two toggles.
	editRowH = 20
	// editRowPad is the gutter inside a rail.
	editRowPad = 6
	// editToggleW is the eye / lock hit box on a rail row.
	editToggleW = 18

	// editFieldRowH is one inspector row's height, and editFieldLabelW how much of it
	// the label takes.
	editFieldRowH   = 22
	editFieldLabelW = 84
	// editStepW is a ± stepper button.
	editStepW = 18

	// editRectSpinStep / editRectSpinCoarse are the ± amounts a numeric stepper moves
	// per click: one unit, and one grid line when Shift is held. LayoutGridPx is the
	// editors' shared grid, so a typed nudge lands on the same lattice a snapped drag
	// does.
	editRectSpinStep   = 1
	editRectSpinCoarse = layoutGridDesign

	// editorHeaderBtnW / editorHeaderBtnGap size the header band's button row (Fonts,
	// Undo, Redo, Save, Back), laid out from the right edge.
	//
	// PACKAGE-LEVEL rather than local to the draw, because the band's geometry is what a
	// gate has to press to prove the header behaves — and a gate that hard-coded 62
	// would stop pressing the button the day the row was re-spaced, silently.
	editorHeaderBtnW   = 62
	editorHeaderBtnGap = 4

	// editRailAddBtnW / editRailLiveBtnW size the element rail's two header buttons
	// (v1.90.0 W10): "+ Image" and the Live/Frozen toggle. Measured against the
	// rail's own width — editRowPad + "Elements" is about 66 px at the chrome face,
	// and 74 + 56 + the two gaps leaves that intact at editRailW.
	editRailAddBtnW  = 74
	editRailLiveBtnW = 56

	// editStatusMs is how long a rail chip ("cap reached", "saved") stays up.
	editStatusMs = 4000

	// editorChromeAllocBudget bounds what ONE editor frame's rails may allocate.
	//
	// NAMED AND NON-ZERO, deliberately, and the honesty is the point: the rails format
	// numbers, hand strings to text fields and build hex readouts, and pretending an
	// inspector allocates nothing would be a gate that had to be weakened the first
	// time somebody read it. The CANVAS is gated at zero (it is the courtroom frame,
	// which every existing alloc gate already covers); the chrome is gated at a budget.
	//
	// 512 is roughly one allocation per drawn control at the widest rail — enough that
	// a normal frame never trips it, tight enough that a per-frame string built in a
	// LOOP (the classic regression: formatting every element rail row's index) blows
	// straight through it.
	editorChromeAllocBudget = 512
)

// ---------------------------------------------------------------------------
// themeEditor — the screen's own state, alive only while the screen is open
// ---------------------------------------------------------------------------

// themeEditor is everything the editor screen needs. It is allocated by
// openThemeEditor and released by closeThemeEditor, which is what makes the closed
// editor cost exactly one nil pointer.
type themeEditor struct {
	doc  *themeDoc
	ring themeEditRing

	// sel is the selection: a FIXED array (see themeSelCap).
	sel  [themeSelCap]editTarget
	selN int
	// drag is the live canvas gesture (themeeditorcanvas.go). A VALUE: it is born
	// and dies with the press, and its base rects ride inside it.
	drag editGesture

	// railScroll is the element rail's scroll offset in pixels.
	railScroll int32
	// peek is Space-held: both rails drop to editRailPeekAlpha.
	peek bool
	// exitAt stamps a Back press that found unsaved edits. The SECOND press inside
	// the chip's own window discards them — a confirmation that costs no modal, in a
	// screen the design allows exactly two of (and W7a spends neither).
	exitAt time.Time
	// copyAt stamps a Save press that found a theme this client may not write. The
	// SECOND press inside themeCopyConfirmWindow copies it into the user's own
	// themes folder and saves there (themecopy.go) — the same no-modal confirmation
	// exitAt is, for the other refusal a Save can produce.
	copyAt time.Time
	// save is the in-flight write (themeeditorsave.go). One at a time, by
	// construction: the channel is the flag.
	save *editSaveJob
	// export is the in-flight .aotheme bundle (themeexport.go). Same shape and
	// same reason as save: the pointer IS the single-flight flag, and a second
	// Share while one runs is refused with a chip rather than racing it.
	export *editExportJob

	// picks caches the inspector's two DYNAMIC pick lists (themeeditorpick.go). On the
	// editor, not on App, so it dies with the screen — the closed-editor cost stays one
	// nil pointer.
	picks pickCache

	// gallery is the PRESET GALLERY panel (themegallery.go), the surface design §3.1
	// has owed since W7 and editorPanelUp's own comment names. A POINTER, and the
	// pointer IS the open flag: it carries a bounded miniature cache and a one-deep
	// preset undo, and neither should exist for a session that never opens it.
	gallery *themeGallery

	// report is the IMPORT REPORT panel (themereportpanel.go), design §3.1's other
	// owed surface and the reader App.themeReport did not have until W9. A POINTER
	// for the same reason gallery is: it carries a 224-entry baked line array, and a
	// session that never opens it should pay one nil pointer.
	report *themeReportPanel
	// reportChip / reportChipN are the rail chip's BAKED label and the note count it
	// was baked for. On the editor rather than inside the panel because the chip is
	// drawn whether or not the panel is open, and the rail draws every frame: built
	// at the draw it would be a per-frame concat inside editorChromeAllocBudget.
	reportChip  string
	reportChipN int

	// image is the one in-flight IMAGE INTAKE (thememediaintake.go). Same shape and
	// same reason as `font`: the pointer IS the single-flight flag, and the channel
	// is buffered so the goroutine never blocks on an editor that closed.
	image *editImageJob

	// livePaused is the LIVE PREVIEW toggle, stored INVERTED so the zero value is
	// the live behaviour every session before this one had (the rule 4 shape a
	// theme.Element field already follows: absent means version 1). See
	// editorLiveOn / drawEditorHeader for what it gates and why the OFF state is
	// the cheap one.
	livePaused bool

	// fontsOpen / font / fontNames are the FONT RAIL (themeeditorfonts.go): whether the
	// panel is up, the one in-flight face copy (the pointer is the flag, exactly as
	// `save` is), and the cycler's reusable name buffer.
	fontsOpen bool
	font      *editFontJob
	fontNames []string

	// status / statusAt are the rail chip: what just happened, and when. The chip is
	// how a refused cap or a graveyard-full delete says so out loud (design §3.3:
	// name the offender, name the limit, offer the fix — never a bare "failed").
	status   string
	statusAt time.Time
}

// ---------------------------------------------------------------------------
// Lifecycle
// ---------------------------------------------------------------------------

// openThemeEditor arms the editor over the applied theme.
//
// IT DISARMS THE TWO LEGACY OVERLAY EDITORS FIRST. They edit PREFERENCES
// (ThemeRectOverrides / ClassicLayout); this edits a theme DOCUMENT. Two tools, two
// scopes, never armed simultaneously — layouteditlock_test.go enforces the
// exclusion, and arming without this would leave the courtroom fenced by an editor
// nobody can see.
//
// ok=false when the applied theme has no native tier to edit yet. That is not an
// error and it is not a dead end: copy-for-editing is the answer, and the Settings
// row draws THAT button instead of this one when it applies (themecopy.go's
// themeEditorRow). Opening an editor over nothing is the one thing this must not do.
func (a *App) openThemeEditor(from Screen) bool {
	if a.themeSidecar == nil {
		return false
	}
	if a.classicEdit {
		a.stopClassicEdit()
	}
	if a.layoutEdit {
		a.stopLayoutEdit()
	}
	// Every return-to-top modal goes down with them: an editor armed under a modal is
	// the hard lock the Esc ladder's own comment describes.
	a.closeEditorBlockingOverlays()
	if !a.armThemeEditor(from) {
		return false
	}
	a.screen = ScreenThemeEditor
	a.uiDirty = true
	// The catalog is what says WHERE this theme lives and whether it is ours to write
	// (themeCatalogEntry.Origin), which is the question Save asks. Kicked here rather
	// than at the save so the answer is already on hand when the button is pressed;
	// the scan is off-thread and floored, so an editor opened twice costs one walk.
	a.requestThemeCatalog(themeScanOnOpen)
	return true
}

// armThemeEditor builds the editor state over the applied theme. SDL-FREE, and that
// is why it is split out: openThemeEditor's overlay teardown needs a renderer, so a
// headless gate that had to re-implement this half would be a second construction
// path — the exact drift the encapsulation rule forbids.
func (a *App) armThemeEditor(from Screen) bool {
	name, _ := a.d.Prefs.Theme()
	doc := newThemeDoc(name, a.themeSidecar)
	if doc == nil {
		return false
	}
	// The document is bound to the APPLIED design map, because an [overrides] edit
	// has to move a pixel as well as write a row (themeDoc.live).
	doc.bindLive(a.themeRects)
	a.te = &themeEditor{doc: doc}
	a.editorReturn = from
	return true
}

// requestEditorExit is what Back and the Esc ladder call. It is the ONE place the
// editor can be left, so "unsaved edits cannot vanish silently" is a property of the
// exit rather than a rule every caller has to remember.
//
// A CLEAN DOCUMENT LEAVES IMMEDIATELY. A dirty one arms: the first press posts the
// chip, a second press inside its window discards. That is a confirmation without a
// modal — the design allows exactly two in the whole editor and spends both
// elsewhere (copy-to-edit, delete-theme), so a third one here would be the modal
// misery requirement 10 forbids. Saving first clears `dirty`, so Save-then-Back is
// one press each.
func (a *App) requestEditorExit() {
	if a.te == nil {
		return
	}
	if !a.te.doc.dirty {
		a.closeThemeEditor()
		return
	}
	if !a.te.exitAt.IsZero() && time.Since(a.te.exitAt) < editStatusMs*time.Millisecond {
		a.te.doc.restoreBaseline()
		// The GEOMETRY half of putting the baseline back on screen. The ART half — the
		// media plan the bake resolves each element's page out of — is reconciled by
		// closeThemeEditor below, for every exit at once.
		a.invalidateThemeCanvases()
		a.closeThemeEditor()
		return
	}
	a.te.exitAt = time.Now()
	a.te.note("unsaved edits — press Save, or Back again to discard them")
}

// closeThemeEditor releases the editor and returns to wherever it was opened from.
//
// The document is dropped, NOT the model: a.themeSidecar and doc.side are the same
// *theme.Sidecar, so whatever the document holds at this moment stays live in the
// applied theme. That is what makes "Save, then Back" leave the saved theme on
// screen and "Back, Back" leave the theme it opened over — requestEditorExit has
// already put the baseline back in the second case.
//
// AND THAT IS WHY THE ART IS RECONCILED HERE, on the way out. The model the app keeps
// drawing is whatever the document holds RIGHT NOW, so the media plan has to agree with
// it right now — and this is the last moment anything can make it, because dropping a.te
// is what stops drawThemeEditor (and with it the per-frame editorSyncArt) from ever
// running again. The discard-on-exit restore is the case that proved it: restoreBaseline
// moves the model back, invalidateThemeCanvases only drops the geometry caches, and the
// courtroom went on drawing the EDITED plan — a generator element whose orphaned tile
// pruneEditorGenTiles had already released could draw nothing at all until the user
// switched themes and back. Here rather than in requestEditorExit's discard arm because
// a.te = nil lives in exactly one function (deliberately — themeeditorop.go's note on the
// ring), so the one function is the only place that covers the clean exit, the
// canvas-left exit and whatever W8 adds by construction rather than by remembering.
//
// A CLEAN EXIT PAYS ONE HASH AND A COMPARE (themeeditorart.go's settled path), which is
// what makes covering all of them affordable.
func (a *App) closeThemeEditor() {
	if a.te == nil {
		return
	}
	// THE PAUSE IS LIFTED FIRST. Live preview off is a VIEW state of the open editor
	// (editorLiveOn), and this is the last instant anything can reconcile the plan
	// with the model — dropping a.te is what stops the frame from ever running
	// again. A paused exit that skipped the sync would leave the courtroom drawing a
	// plan the document has moved past, which is precisely the class the second call
	// site in themeeditorart.go exists to close.
	a.te.livePaused = false
	a.editorSyncArt()
	back := a.editorReturn
	a.te = nil
	if back == ScreenThemeEditor { // never bounce back into ourselves
		back = ScreenSettings
	}
	a.screen = back
	a.uiDirty = true
}

// themeEditorOpen reports the editor being armed AND on screen. Two questions
// rather than one, for the same reason editorDrawSiteRuns exists: a.te outlives any
// frame where App.Frame preempts the screen dispatch (gif export, replay, the scene
// maker), and on those frames the editor must not be eating the keyboard.
func (a *App) themeEditorOpen() bool {
	return a.te != nil && a.screen == ScreenThemeEditor && !a.screenDispatchPreempted()
}

// ---------------------------------------------------------------------------
// The mutation funnel — every edit in the UI goes through exactly this
// ---------------------------------------------------------------------------

// editorApply performs one op and records it. It is the ONLY place the screen
// mutates the document.
//
// The three steps are inseparable and that is the point: apply (which fills in the
// inverse), push (which makes it undoable), invalidate (which re-bakes the geometry
// the edit is expressed in). A call site that did two of the three would produce an
// edit that either cannot be undone or cannot be seen.
func (a *App) editorApply(op themeEditOp) bool {
	if a.te == nil || op.kind == opNone {
		return false
	}
	done, res := a.te.doc.apply(op)
	if res != applyDone {
		// THE CHIP FIRES ONLY FOR applyRefused, and the reason is a defect this arm
		// used to have: it exempted opElemField by KIND, which silenced element drags
		// and left every other kind chipping on its no-ops too. A widget drag holding
		// still — i.e. every real human drag, on the frames the hand is not moving —
		// asks applySlotRect for the rect it already holds, and so does every frame a
		// drag sits against clampDesignRectToCanvas: those posted "a cap or a lock is
		// in the way" for four seconds, naming a limit that was not there.
		//
		// The kind is no longer the question; the ANSWER is (editApplyResult). A real
		// theme.OverrideCap / theme.ElementCap / graveyard-full refusal still says so
		// out loud — design §3.3: name the offender, name the limit, never a bare
		// "failed" — and a no-op stays as quiet as it always should have been.
		if res == applyRefused {
			a.te.note("this " + op.kind.String() + " was refused — a cap or a lock is in the way")
		}
		return false
	}
	a.te.ring.push(done, time.Now())
	a.editorInvalidateLive()
	a.uiDirty = true
	return true
}

// ---------------------------------------------------------------------------
// LIVE PREVIEW — the toggle, and the one predicate that gates it
// ---------------------------------------------------------------------------

// editorLiveOn reports the live preview being on. TRUE when there is no editor at
// all, so a caller outside the screen never has to special-case the closed state.
//
// WHAT "LIVE" ALREADY MEANT, AND WHAT THIS ADDS. The canvas has been a real
// courtroom render since W7a and the art has re-derived per edit since W7b
// (themeeditorart.go), so the preview was never the missing half — what was missing
// was a way to STOP it. Every keystroke in a numeric field re-bakes the layout, and
// every keystroke in a generator's params rasterises a fresh 256 KiB tile and
// releases the old one (pruneEditorGenTiles' own note). On a big theme, on a slow
// machine, that is the whole editing experience: type a digit, wait for the frame.
//
// So the toggle FREEZES the canvas rather than enabling it. Off, the document goes
// on taking edits — the rails, the inspector, undo and Save are untouched — and the
// picture simply stops re-baking until the toggle goes back on, which reconciles
// once (setEditorLive). The frozen state is the CHEAP one by construction: the
// gates below return before the signature hash and before the invalidate, so a
// paused editor does exactly the work an idle one does. That is the zero-cost-closed
// discipline, and it is the direction that makes the toggle worth having — a toggle
// whose off-state still rebaked in the background would be a label.
func (a *App) editorLiveOn() bool { return a.te == nil || !a.te.livePaused }

// editorInvalidateLive is invalidateThemeCanvases, gated by the toggle. One
// spelling, so a future edit path cannot forget the gate and re-bake anyway.
func (a *App) editorInvalidateLive() {
	if !a.editorLiveOn() {
		return
	}
	a.invalidateThemeCanvases()
}

// setEditorLive flips the toggle and, on the way back ON, pays the one reconcile
// the frozen frames skipped.
//
// THE RESUME IS UNCONDITIONAL, not "if anything changed": the plan's signature is
// exactly what says whether anything changed (editorSyncArt compares it), and this
// is the one moment the answer has to be re-asked rather than trusted.
func (a *App) setEditorLive(on bool) {
	if a.te == nil {
		return
	}
	a.te.livePaused = !on
	if !on {
		a.te.note("live preview off — the canvas is frozen; edits still land and Save still works")
		return
	}
	a.invalidateThemeCanvases()
	a.editorSyncArt()
	a.uiDirty = true
}

// editorUndo steps the timeline back by one op — or by one whole GROUP, which is
// what makes "try a preset, hate it, Ctrl+Z" one keypress.
func (a *App) editorUndo() bool {
	if a.te == nil {
		return false
	}
	g, ok := a.te.ring.peekUndoGroup()
	if !ok {
		return false
	}
	moved := false
	for {
		op, ok := a.te.ring.takeUndo()
		if !ok {
			break
		}
		a.te.doc.revert(op)
		moved = true
		if g == 0 {
			break // an ungrouped op is one step on its own
		}
		next, ok := a.te.ring.peekUndoGroup()
		if !ok || next != g {
			break
		}
	}
	if moved {
		a.editorInvalidateLive()
		a.uiDirty = true
	}
	return moved
}

// editorRedo is editorUndo's mirror.
func (a *App) editorRedo() bool {
	if a.te == nil {
		return false
	}
	g, ok := a.te.ring.peekRedoGroup()
	if !ok {
		return false
	}
	moved := false
	for {
		op, ok := a.te.ring.takeRedo()
		if !ok {
			break
		}
		a.te.doc.redo(op)
		moved = true
		if g == 0 {
			break
		}
		next, ok := a.te.ring.peekRedoGroup()
		if !ok || next != g {
			break
		}
	}
	if moved {
		a.editorInvalidateLive()
		a.uiDirty = true
	}
	return moved
}

// ---------------------------------------------------------------------------
// Selection — a fixed array, never a slice
// ---------------------------------------------------------------------------

func (e *themeEditor) selectOnly(t editTarget) {
	e.selN = 0
	e.selectAdd(t)
}

// selectAdd appends unless the target is already selected or the array is full. A
// FULL SELECTION REFUSES rather than growing: the array is the cap (hard rule 4).
func (e *themeEditor) selectAdd(t editTarget) {
	if !t.armed() {
		return
	}
	for i := 0; i < e.selN; i++ {
		if e.sel[i] == t {
			return
		}
	}
	if e.selN >= len(e.sel) {
		return
	}
	e.sel[e.selN] = t
	e.selN++
}

// selectToggle is Ctrl+click.
func (e *themeEditor) selectToggle(t editTarget) {
	for i := 0; i < e.selN; i++ {
		if e.sel[i] != t {
			continue
		}
		copy(e.sel[i:], e.sel[i+1:e.selN])
		e.selN--
		return
	}
	e.selectAdd(t)
}

func (e *themeEditor) selectClear() { e.selN = 0 }

// selectPrimary makes an ALREADY-SELECTED target the one the inspector follows and
// the one a multi-drag is led by, by swapping it to the front.
//
// A swap rather than a re-select, because a press on one member of a multi-selection
// must not throw the rest away — that is precisely the gesture "grab this box and
// move all five" is made of. A target that is not selected at all is selected alone,
// which is what a plain press on a fresh box means.
func (e *themeEditor) selectPrimary(t editTarget) {
	for i := 0; i < e.selN; i++ {
		if e.sel[i] != t {
			continue
		}
		e.sel[i] = e.sel[0]
		e.sel[0] = t
		return
	}
	e.selectOnly(t)
}

func (e *themeEditor) selectedIs(t editTarget) bool {
	for i := 0; i < e.selN; i++ {
		if e.sel[i] == t {
			return true
		}
	}
	return false
}

// primary is the selection the inspector follows: the FIRST, so a Shift-click that
// adds a second element does not re-point the rail the user is reading.
func (e *themeEditor) primary() editTarget {
	if e.selN == 0 {
		return noTarget()
	}
	return e.sel[0]
}

// note posts a rail chip.
func (e *themeEditor) note(msg string) {
	e.status = msg
	e.statusAt = time.Now()
}

// ---------------------------------------------------------------------------
// Draw
// ---------------------------------------------------------------------------

// drawThemeEditor is the screen. Canvas first, then the header band, then the two
// rails — painter's order, so the rails float over the frame rather than reserving
// space from it.
func (a *App) drawThemeEditor(w, h int32) {
	if a.te == nil {
		// The screen is armed with no editor: leave rather than draw an empty shell.
		// Reachable only through a state restore that set a.screen directly.
		a.screen = ScreenSettings
		return
	}
	c := a.ctx
	// Space PEEKS. Held, not toggled: a peek you have to switch off is a mode, and
	// the whole point is to glance.
	a.te.peek = c.keyHeld(sdl.K_SPACE) && c.focusID == ""
	// THE FILE BROWSER'S FENCE IS RAISED HERE and released at the very end, right
	// before the browser draws — the same release-and-restore drawSettings uses, and
	// for the same reason: everything behind a modal must be click-dead while its own
	// rows must not be. It is raised BEFORE the key handler and the canvas, which is
	// the half a fence raised later would miss: the canvas is a real courtroom and
	// eats arrows, and editorCanvasInput hit-tests with raw pointIn.
	if demoBrowser.open {
		c.modalOn = true
	}
	a.pollEditorSave()
	a.pollEditorFont()
	a.pollEditorExport()
	a.pollEditorImage()
	// THE KEYS ARE CLAIMED FIRST, before the canvas draws, and that ordering is the
	// same one layoutnudge.go hoisted the legacy nudge out of a draw body for. This
	// screen's canvas IS the courtroom, and the courtroom's own plain-arrow consumers
	// — the IC/OOC recall rings, the pair-ghost offset nudge, the emote-preview
	// cycler — run inside it. An editor key handler that ran after the canvas would be
	// handed a key that was already spent.
	a.handleEditorKeys()
	// THE ART IS RECONCILED BEFORE THE CANVAS DRAWS, because the canvas is what bakes
	// it: an element's page comes out of a.themeMediaPlan BY INDEX, and every fill row
	// in the inspector (and every add and delete) changes what that index should
	// resolve to. Here rather than in editorApply so undo, redo, a grouped paste and
	// the discard-on-exit restore are covered by the same line — see themeeditorart.go.
	a.editorSyncArt()

	lay := a.themeWindowLayout(w, h)
	a.drawEditorCanvas(w, h)
	// THE CANVAS MAY HAVE LEFT. drawCourtroom sends the app to the lobby when the
	// session drops mid-frame, and the editor has to follow it out rather than paint
	// two rails over a lobby — with the screen it chose, not the one the editor was
	// opened from, because a dropped connection is not a Back press.
	if a.screen != ScreenThemeEditor {
		to := a.screen
		a.closeThemeEditor()
		a.screen = to
		return
	}
	a.editorCanvasInput(w, h, lay)
	// THE CACHE IS RE-ASKED FOR between the input and the overlay. Every geometry
	// edit invalidates it (the layout is derived from the very rects the drag moves),
	// so the outlines below would otherwise be drawn from the pre-edit numbers and
	// the box would visibly lag the cursor by one frame. This is the ~40-rect rebuild
	// per moved frame the legacy overlay editor already pays for the same reason.
	lay = a.themeWindowLayout(w, h)
	a.drawEditorCanvasOverlay(w, h, lay)
	a.drawEditorHeader(w)
	// BACK MAY HAVE LEFT. drawEditorHeader's own Back button closes the editor, and
	// every rail below dereferences a.te — so without this a press on Back with a clean
	// document is a nil dereference on the render thread rather than an exit.
	if a.te == nil {
		return
	}
	// THE PANEL FENCE, RAISED HERE AND RELEASED BELOW (design §3.1: every editor
	// surface but the two modals is a panel under the demoBrowser release-and-restore
	// fence, so what is behind it goes click-dead without stranding modalOn).
	//
	// The rails are what needs it: editorFontPanelRect centres the panel over the
	// canvas, and below ~928 px it overlaps a rail outright, so a press on a font row's
	// `<` also reached drawEditorElementRow's ClickedIn underneath. modalOn blanks
	// hovering(), which is what ClickedIn, Button and WheelIn are all built on.
	//
	// The HEADER is deliberately outside the fence and drew above: the panel's own rect
	// starts below editBannerH by construction, so nothing behind it is at risk there —
	// and fencing it would kill the Fonts button that closes the panel.
	savedModal := c.modalOn
	if a.editorPanelUp() {
		c.modalOn = true
	}
	a.drawEditorElementRail(w, h)
	a.drawEditorInspector(w, h)
	c.modalOn = savedModal
	// LAST, so the panel floats over both rails — it is a panel and not a modal
	// (design §3.1), so the surface behind it keeps drawing and the theme keeps
	// animating while a face is picked. It draws with the fence RELEASED, which is what
	// makes its own buttons live while everything behind them is not.
	a.drawEditorFontPanel(w, h)
	a.drawThemeGallery(w, h)
	a.drawThemeReportPanel(w, h)
	// THE FILE BROWSER DRAWS LAST AND TOPMOST (v1.90.0 W10), with the fence released
	// exactly as drawSettings releases it: it is the browse half of the image
	// intake, and it is the one surface here that IS a modal rather than a panel —
	// everything behind it must be click-dead while it is up, and its own rows must
	// not be. It no-ops when closed, so the editor pays one bool for having it.
	if demoBrowser.open {
		c.modalOn = false
	}
	a.drawDemoBrowser(w, h)
}

// editorPanelUp reports that an editor PANEL is on screen.
//
// ONE PREDICATE FOR BOTH HALVES OF THE FENCE, so the two can never disagree about when
// it is up. The draw raises c.modalOn over the rails (hovering() honours it); the
// canvas probe hit-tests with RAW pointIn and therefore sees through every fence, so
// editorCanvasInput has to ask the same question in its own words — exactly as it
// already does for the compact toolbox.
//
// OPEN to the panels design §3.1 still owes (budget details, export sheet): each joins
// the OR here and is fenced on both sides for free — the preset gallery and the import
// report took exactly that route in W9.
// CLOSED against the two MODALS, which are not panels — they close the screen's input
// path rather than floating over it.
func (a *App) editorPanelUp() bool {
	return a.te != nil && (a.te.fontsOpen || a.te.gallery != nil || a.te.report != nil)
}

// closeEditorPanels shuts whatever editorPanelUp reports. THE PREDICATE'S INVERSE, and
// it lives beside it so the two are read together: a panel added to the OR above and
// not closed here would be a panel Esc walks past — which is exactly the hole the Fonts
// panel had (app.go's ScreenThemeEditor arm).
//
// It closes ALL of them rather than "the top one", because the editor has no panel
// stack: design §3.1's panels are alternatives, never layers, and a "topmost" that is
// really a one-element set is a z-order nobody maintains.
func (a *App) closeEditorPanels() {
	if a.te == nil {
		return
	}
	a.te.fontsOpen = false
	a.te.gallery = nil
	a.te.report = nil
	a.uiDirty = true
}

// drawEditorCanvas paints what the theme looks like RIGHT NOW, full bleed.
//
// TWO ARMS, because the editor is reachable from Settings with no server attached
// and "you must join a courtroom before you may edit your theme" is not an
// acceptable sentence. With a live room it is the real courtroom pass, 1:1. Without
// one it is the theme's fitted design canvas with every editable slot and every
// free element outlined in place — the geometry, at the exact size it will play,
// which is what the geometry work needs. The live-art offline preview room is
// W7b's (it must be built through newRoom, which is the one construction site).
func (a *App) drawEditorCanvas(w, h int32) {
	if a.room != nil && a.sess != nil && !a.theaterOn {
		// THE COURTROOM'S OWN WIDGETS GO INERT while it is being used as a canvas.
		// This is layoutEditFence's trick — modalOn blanks hovering() everywhere and
		// the editor hit-tests with raw pointIn — and it is needed for the same
		// reason: the kit is single-pass, so a press that selects a themed widget
		// would ALSO press whatever real button is painted there.
		//
		// A BRACKET, not an assignment: an open dropdown belonging to the RAILS holds
		// modalOn legitimately, and forcing it false on the way out would drop its
		// fence and let the click behind it through. (app.go's own toolbox-under-an-
		// editor arm saves and restores for exactly this reason.)
		saved := a.ctx.modalOn
		a.ctx.modalOn = true
		a.drawCourtroom(w, h)
		a.ctx.modalOn = saved
		return
	}
	a.drawEditorGeometryCanvas(w, h)
}

// drawEditorGeometryCanvas is the session-free arm: the canvas outline, every
// editable AO2 slot, and every free element, all in design geometry at 1:1.
func (a *App) drawEditorGeometryCanvas(w, h int32) {
	c := a.ctx
	c.Fill(sdl.Rect{X: 0, Y: 0, W: w, H: h}, ColBackground)
	lay := a.themeWindowLayout(w, h)
	if lay == nil || !lay.valid {
		c.Label(editRailW+editRowPad*2, editBannerH+editRowPad*2,
			"This theme has no courtroom_design.ini canvas to lay out.", ColTextDim)
		return
	}
	// The canvas itself, and nothing else: every editable widget and every element is
	// outlined by drawEditorCanvasOverlay, which runs over BOTH arms so the two can
	// never disagree about what is selectable or where its handles are.
	if court, ok := lay.rect("courtroom"); ok {
		c.Border(court, ColPanelHi)
	}
}

// drawEditorHeader is the top band: the theme's name, the dirty state, undo/redo
// and Back.
func (a *App) drawEditorHeader(w int32) {
	c := a.ctx
	band := sdl.Rect{X: 0, Y: 0, W: w, H: editBannerH}
	// The band is OURS this frame, so the menu bar stands down (menubar.go's
	// menuBarPaintsNow) — the same publication both legacy editors make from the
	// statement that fills their banner. Without it an opaque 22 px strip paints over
	// Undo / Redo / Save / Back after the screen dispatch.
	a.noteEditorBanner()
	c.Fill(band, a.editorPlate(ColPanel))
	name := a.te.doc.name
	if name == "" {
		name = "(unnamed theme)"
	}
	c.LabelClipped(editRowPad, 5, w/3, name, ColText)
	c.LabelClipped(w/3+editRowPad, 5, w/4, a.editorStateLabel(), ColTextDim)

	const btnW, btnGap = editorHeaderBtnW, editorHeaderBtnGap
	x := w - editRowPad - btnW
	if c.Button(sdl.Rect{X: x, Y: 2, W: btnW, H: editBannerH - 4}, "Back") {
		a.requestEditorExit()
		return
	}
	x -= btnW + btnGap
	if c.Button(sdl.Rect{X: x, Y: 2, W: btnW, H: editBannerH - 4}, "Save") {
		a.editorSave()
	}
	x -= btnW + btnGap
	if c.Button(sdl.Rect{X: x, Y: 2, W: btnW, H: editBannerH - 4}, "Redo") {
		a.editorRedo()
	}
	x -= btnW + btnGap
	if c.Button(sdl.Rect{X: x, Y: 2, W: btnW, H: editBannerH - 4}, "Undo") {
		a.editorUndo()
	}
	x -= btnW + btnGap
	if c.Button(sdl.Rect{X: x, Y: 2, W: btnW, H: editBannerH - 4}, "Fonts") {
		a.te.fontsOpen = !a.te.fontsOpen
	}
	// THE IMAGE AND LIVE BUTTONS ARE NOT HERE, and that is a measurement rather than
	// a preference. This band already carries seven buttons at editorHeaderBtnW plus
	// the theme name (w/3) and the saved-state label (w/4); at 1280 px the two
	// columns meet at 752 and the button row starts at 818, so a ninth and tenth
	// button would draw straight over the name. They live on the ELEMENT RAIL
	// instead (drawEditorElementRail), which is where they belong anyway: both act
	// on the element list — one adds to it, the other decides whether it re-bakes.
	// GALLERY (v1.90.0 W9) — the 21 x 14 preset grid. Beside Fonts because they are
	// the same kind of thing: a panel that changes the whole theme at once, as
	// against the rails, which change one row.
	x -= btnW + btnGap
	if c.Button(sdl.Rect{X: x, Y: 2, W: btnW, H: editBannerH - 4}, "Gallery") {
		if a.te.gallery != nil {
			a.te.gallery = nil
		} else {
			a.openThemeGallery()
		}
	}
	// SHARE (v1.90.0 W8). It sits in the header rather than behind a menu because
	// "hand this to a friend" is the whole point of having authored a theme, and a
	// feature reachable only by knowing it exists is one nobody uses. The two
	// modes and the whole zip live in themeexport.go.
	x -= btnW + btnGap
	if c.Button(sdl.Rect{X: x, Y: 2, W: btnW, H: editBannerH - 4}, "Share") {
		a.editorExport()
	}
}

// editorLiveLabel is the Live button's caption. Two package constants rather than a
// formatted string, for the reason editorStateLabel gives below: the label cache
// keys on the string, so a value built per frame would rasterise a fresh texture
// sixty times a second.
func editorLiveLabel(on bool) string {
	if on {
		return "Live"
	}
	return "Frozen"
}

// editorImageBtnLabel is the rail's add-an-image caption, busy or idle. Two
// constants, same reason as editorLiveLabel.
func editorImageBtnLabel(busy bool) string {
	if busy {
		return "adding…"
	}
	return "+ Image"
}

// editorStateLabel is the one-word answer to "is my work safe". A CONSTANT string
// per state rather than a formatted one: it sits in the header of every frame, and
// the label cache keys on the string, so a value that changed per frame would
// rasterise a fresh texture sixty times a second.
func (a *App) editorStateLabel() string {
	switch {
	case a.te.save != nil:
		return "Saving…"
	case a.te.doc.dirty:
		return "Unsaved edits"
	}
	return "Saved"
}

// editorPlate is a rail/band plate colour at the current peek alpha. ONE function,
// so the peek can never apply to one surface and not another.
func (a *App) editorPlate(base sdl.Color) sdl.Color {
	base.A = editRailAlpha
	if a.te != nil && a.te.peek {
		base.A = editRailPeekAlpha
	}
	return base
}

// drawEditorElementRail is the LEFT rail: the z-order list.
//
// Clipped with pushClip/popClip and NEVER with a raw Ren.SetClipRect — the raw call
// clips DRAW only and lets clicks and hovers leak past the edge, which is the class
// of bug the char-select grid shipped in v1.55.8. hovering() honours the kit's clip
// stack; the renderer's does not.
func (a *App) drawEditorElementRail(w, h int32) {
	c := a.ctx
	rail := sdl.Rect{X: 0, Y: editBannerH, W: editRailW, H: h - editBannerH}
	c.Fill(rail, a.editorPlate(ColPanel))
	c.Label(editRowPad, rail.Y+4, "Elements", ColText)
	a.drawEditorRailActions(rail)

	// The report chip claims its row out of the BODY, not out of the footer, so a
	// theme with notes shows one row fewer of elements rather than drawing the chip
	// over the element count (design §3.1: the chip lives in the rail footer).
	chipH := int32(0)
	if len(a.themeReport) > 0 {
		chipH = editRowH
	}
	body := sdl.Rect{X: rail.X, Y: rail.Y + editRowH, W: rail.W, H: rail.H - editRowH*2 - chipH}
	n := a.te.doc.elementCount()
	// The wheel BEFORE the clip, and through WheelIn — the kit's single-consumer
	// wheel: taking it here means the courtroom underneath cannot also scroll from the
	// same notch, which is what "one wheel, one consumer" is for.
	if d := c.WheelIn(body); d != 0 {
		a.te.railScroll -= d * editRowH
	}
	// Clamped after the take, so a theme that shrank does not strand the list scrolled
	// past its own end with nothing to scroll back to.
	if maxScroll := int32(n)*editRowH - body.H; a.te.railScroll > maxScroll {
		a.te.railScroll = maxScroll
	}
	if a.te.railScroll < 0 {
		a.te.railScroll = 0
	}
	prev, had := c.pushClip(body)
	y := body.Y - a.te.railScroll
	for i := 0; i < n; i++ {
		if y+editRowH >= body.Y && y < body.Y+body.H {
			a.drawEditorElementRow(i, sdl.Rect{X: body.X, Y: y, W: body.W, H: editRowH})
		}
		y += editRowH
	}
	c.popClip(prev, had)

	// Footer: the count against the cap, so "why can I not add another" is answered
	// before it is asked, and above it the import-report chip (design §3.1) when
	// this client noticed anything about the theme.
	foot := rail.Y + rail.H - editRowH
	a.drawEditorReportChip(rail, foot-chipH)
	c.LabelClipped(editRowPad, foot+3, rail.W-editRowPad*2,
		strconv.Itoa(n)+" / "+strconv.Itoa(theme.ElementCap)+" elements", ColTextDim)
	// The status chip stacks ABOVE the report chip rather than at a fixed offset from
	// the footer: with both drawn at foot-editRowH a theme with notes would paint a
	// red refusal straight over the button that explains them.
	if a.te.status != "" && time.Since(a.te.statusAt) < editStatusMs*time.Millisecond {
		c.LabelClipped(editRowPad, foot-chipH-editRowH+3, rail.W-editRowPad*2, a.te.status, ColDanger)
	}
}

// drawEditorRailActions draws the element rail's header buttons: ADD AN IMAGE, and
// the LIVE PREVIEW toggle.
//
// ON THE RAIL RATHER THAN THE HEADER BAND. The band is full (drawEditorHeader's own
// note measures it), and this is the better home regardless: the rail is the
// element list, "+ Image" is the one act that adds to it, and the Live toggle
// decides whether the list re-bakes as you edit. Both are one press from the list
// they act on instead of across the window from it.
//
// "+ IMAGE" IS THE FIRST ADD-AN-ELEMENT AFFORDANCE IN THE EDITOR. Until this wave
// opElemAdd had no UI caller at all — it existed only as the inverse of a delete —
// so an element could be deleted, hidden, locked and re-typed but never created. It
// is a real gap and this closes it for the kind users asked for; the other five
// kinds are still reached by re-typing an element in the inspector.
func (a *App) drawEditorRailActions(rail sdl.Rect) {
	c := a.ctx
	// Laid out from the RIGHT edge so the "Elements" label keeps the left and the
	// two buttons cannot push each other off the rail as their labels change.
	live := a.editorLiveOn()
	liveR := sdl.Rect{
		X: rail.X + rail.W - editRowPad - editRailLiveBtnW,
		Y: rail.Y + 2,
		W: editRailLiveBtnW,
		H: editRowH - 2,
	}
	imgR := sdl.Rect{X: liveR.X - editRailAddBtnW - editRowPad/2, Y: liveR.Y, W: editRailAddBtnW, H: liveR.H}
	if c.Button(imgR, editorImageBtnLabel(a.te.image != nil)) {
		// THE SAME FUNNEL A DRAG ENTERS BY (thememediaintake.go): this opens the in-app
		// browser bound to purposeThemeImage, whose pick calls editorTakeImage. There is
		// no second intake path, so browsing and dropping cannot diverge about the
		// caps, the destination or the budget verdict.
		a.openDemoBrowserFor(purposeThemeImage)
	}
	// The label carries the STATE, not the verb: a toggle labelled with what it
	// would do reads as the opposite of its own state half the time. Both strings
	// are package constants, so the rail still costs no allocation per frame.
	if c.Button(liveR, editorLiveLabel(live)) {
		a.setEditorLive(!live)
	}
}

// drawEditorElementRow is ONE rail row: kind glyph, id, eye, lock.
func (a *App) drawEditorElementRow(idx int, r sdl.Rect) {
	c := a.ctx
	el := a.te.doc.element(elementTarget(idx))
	if el == nil {
		return
	}
	t := elementTarget(idx)
	if a.te.selectedIs(t) {
		c.Fill(r, ColPanelHi)
	}
	lockR := sdl.Rect{X: r.X + r.W - editToggleW - editRowPad, Y: r.Y + 1, W: editToggleW, H: r.H - 2}
	eyeR := sdl.Rect{X: lockR.X - editToggleW - 2, Y: lockR.Y, W: editToggleW, H: lockR.H}

	// DRAW THE WHOLE ROW FIRST, then take the input. The obvious order (handle the
	// toggle, return early) leaves the row half-painted for exactly the frame the user
	// clicked it on — the one frame they are looking at it.
	label := el.ID
	if label == "" {
		label = el.Kind.String()
	}
	col := ColText
	if el.Hidden {
		col = ColTextDim
	}
	c.LabelClipped(r.X+editRowPad, r.Y+3, eyeR.X-r.X-editRowPad*2, label, col)
	c.Label(eyeR.X+4, r.Y+3, eyeGlyph(el.Hidden), ColTextDim)
	c.Label(lockR.X+4, r.Y+3, lockGlyph(el.Locked), ColTextDim)

	// The two toggles are probed BEFORE the row, so their hit boxes win: they sit
	// inside the row's own rect, and ClickedIn consumes, so the order IS the priority.
	switch {
	case c.ClickedIn(eyeR):
		a.editorApply(mutateElemField(t, editFieldHidden, editValueBool(!el.Hidden)))
	case c.ClickedIn(lockR):
		a.editorApply(mutateElemField(t, editFieldLocked, editValueBool(!el.Locked)))
	case c.ClickedIn(r):
		switch {
		case c.ctrlHeld:
			a.te.selectToggle(t)
		case c.shiftHeld:
			a.te.selectAdd(t)
		default:
			a.te.selectOnly(t)
		}
	}
}

// eyeGlyph / lockGlyph are the row's two state characters. ASCII rather than emoji:
// the rail runs at the chrome face, and a per-frame colour-emoji raster on a list
// that redraws every frame is exactly the cost the font work spent v1.87.0 removing.
func eyeGlyph(hidden bool) string {
	if hidden {
		return "-"
	}
	return "o"
}

func lockGlyph(locked bool) string {
	if locked {
		return "L"
	}
	return "."
}

// drawEditorInspector is the RIGHT rail: the declarative property rows for the
// primary selection.
func (a *App) drawEditorInspector(w, h int32) {
	c := a.ctx
	rail := sdl.Rect{X: w - editRailW, Y: editBannerH, W: editRailW, H: h - editBannerH}
	c.Fill(rail, a.editorPlate(ColPanel))
	t := a.te.primary()
	if !t.armed() {
		c.Label(rail.X+editRowPad, rail.Y+4, "Nothing selected", ColTextDim)
		return
	}
	rows := a.inspectorRowsFor(t)
	if len(rows) == 0 {
		c.Label(rail.X+editRowPad, rail.Y+4, "Nothing editable here", ColTextDim)
		return
	}
	prev, had := c.pushClip(rail)
	y := rail.Y + 2
	sec := inspectorSectionCount
	for i := range rows {
		f := &rows[i]
		if f.section != sec {
			sec = f.section
			c.Label(rail.X+editRowPad, y+3, inspectorSectionNames[sec], ColAccent)
			y += editFieldRowH
		}
		a.drawInspectorRow(f, t, sdl.Rect{X: rail.X, Y: y, W: rail.W, H: editFieldRowH})
		y += editFieldRowH
		if y > rail.Y+rail.H {
			break
		}
	}
	a.drawInspectorTail(t, rail, y)
	c.popClip(prev, had)
}

// drawInspectorTail is what a rail carries BELOW its rows: the widget arm's parity
// note and its Reset, and the text arm's format-1 note.
//
// SEPARATE FROM THE TABLE because neither is a FIELD. Reset is an action (it removes a
// row rather than setting a value), and the notes are statements about the format. A
// row invented for either would have to carry a mutator that mutates nothing, which is
// precisely the shape TestEveryInspectorFieldIsUndoable would then have to be widened
// to excuse.
func (a *App) drawInspectorTail(t editTarget, rail sdl.Rect, y int32) int32 {
	c := a.ctx
	switch t.space {
	case spaceDesign:
		// THE PARITY RULE, STATED WHERE IT BITES (the AO2 full-parity ruling, design §Q1):
		// a theme may restate an AO2 widget's rect and nothing else about it, so this rail
		// has one section and always will.
		c.LabelClipped(rail.X+editRowPad, y+4, rail.W-editRowPad*2,
			"AO2 widget — geometry only", ColTextDim)
		y += editFieldRowH
		_, authored := a.te.doc.side.OverrideRect(t.designKey())
		btn := sdl.Rect{X: rail.X + editRowPad, Y: y, W: rail.W - editRowPad*2, H: editFieldRowH - 2}
		if !authored {
			// Nothing to reset. Drawn DISABLED rather than hidden, so the control does not
			// appear and disappear as the user drags — a button that moves is a button that
			// gets mis-clicked.
			c.Fill(btn, ColPanelHi)
			c.LabelClipped(btn.X+4, btn.Y+3, btn.W-8, "Reset to theme (unchanged)", ColTextDim)
			return y + editFieldRowH
		}
		if c.Button(btn, "Reset to theme") {
			a.editorApply(mutateSlotReset(t))
		}
		return y + editFieldRowH
	case spaceElement:
		el := a.te.doc.element(t)
		if el == nil || el.Kind != theme.ElemText {
			return y
		}
		// THE DOCUMENTED DEFERRAL, SAID OUT LOUD. `font` and `size` are carried and not
		// drawn in format 1 (docs/THEME-FORMAT.md §3, paintElementText's measurement:
		// giving each element its own point size means a fontSet rebuild per frame for
		// every element that shares a panel's face). W4 wrote that down in the format doc
		// and in the painter; nothing said it where the author sets the value, so setting
		// `size = 27` and seeing nothing was a discovery rather than an answer. The
		// [fontbind] rail below the element list is the tier that DOES render, and this
		// line points at it.
		if el.Font != "" || el.Size != 0 {
			c.LabelClipped(rail.X+editRowPad, y+4, rail.W-editRowPad*2,
				"font/size are saved, not drawn yet —", ColTierYellow)
			c.LabelClipped(rail.X+editRowPad, y+4+editFieldRowH-6, rail.W-editRowPad*2,
				"bind a face in Fonts to see it", ColTierYellow)
			y += editFieldRowH * 2
		}
		return y
	}
	return y
}

// drawInspectorRow draws ONE declarative row and routes its edit through the op the
// row's own `mut` returns. It never writes a field itself — that is the whole
// contract the table exists to enforce.
func (a *App) drawInspectorRow(f *inspectorField, t editTarget, r sdl.Rect) {
	c := a.ctx
	if int(f.kind) >= int(fieldKindCount) {
		return // defence in depth: a widget kind with no arm below would draw a bare label
	}
	cur := f.get(a, t, f.field)
	c.LabelClipped(r.X+editRowPad, r.Y+4, editFieldLabelW, f.label, ColTextDim)
	ctl := sdl.Rect{X: r.X + editRowPad + editFieldLabelW, Y: r.Y + 1,
		W: r.W - editRowPad*2 - editFieldLabelW, H: r.H - 2}
	switch f.kind {
	case fkRect:
		// Four spinners on one row: x, y, w, h. Narrow, and the value is the readout.
		cw := ctl.W / 4
		for lane := 0; lane < 4; lane++ {
			cell := sdl.Rect{X: ctl.X + int32(lane)*cw, Y: ctl.Y, W: cw - 1, H: ctl.H}
			if d := a.editorSpinner(cell, cur.i[lane], f.lo, f.hi); d != cur.i[lane] {
				next := cur
				next.i[lane] = d
				a.editorApply(f.mut(t, f.field, next))
				return
			}
		}
	case fkInt, fkPercent, fkAngle:
		if d := a.editorSpinner(ctl, cur.i[0], f.lo, f.hi); d != cur.i[0] {
			a.editorApply(f.mut(t, f.field, editValueInt(d)))
		}
	case fkBool:
		// CheckboxIn returns the NEW value, not "was clicked" — comparing against the
		// old one is what turns it into an edit, and it is why a click that lands on a
		// muted row (which returns the value untouched) writes nothing.
		if now := c.CheckboxIn(ctl, "", cur.i[0] != 0); now != (cur.i[0] != 0) {
			a.editorApply(f.mut(t, f.field, editValueBool(now)))
		}
	case fkEnum:
		if d := a.editorEnumCycler(ctl, cur.i[0], f.enum); d != cur.i[0] {
			a.editorApply(f.mut(t, f.field, editValueInt(d)))
		}
	case fkColor:
		sw := sdl.Rect{X: ctl.X, Y: ctl.Y, W: ctl.H, H: ctl.H}
		c.Fill(sw, sdl.Color{R: cur.b[0], G: cur.b[1], B: cur.b[2], A: 255})
		c.Border(sw, ColPanelHi)
		hexR := sdl.Rect{X: sw.X + sw.W + 2, Y: ctl.Y, W: ctl.W - sw.W - 2, H: ctl.H}
		// TextField's second result is ENTER, not "changed" — the edit is the string
		// coming back different, which is also what makes a half-typed hex inert
		// instead of writing black.
		hex := editHex(cur)
		if s, _ := c.TextField(editFieldID(f.field), hexR, hex, "rrggbbaa"); s != hex {
			if v, good := parseEditHex(s); good {
				a.editorApply(f.mut(t, f.field, v))
			}
		}
	case fkText, fkMedia, fkFont:
		old := a.te.doc.str(cur.s)
		if s, _ := c.TextField(editFieldID(f.field), ctl, old, ""); s != old {
			a.editorWriteStringField(f, t, s)
		}
	case fkPick:
		a.drawInspectorPick(f, t, ctl, a.te.doc.str(cur.s))
	}
}

// drawInspectorPick is an fkPick row: two step buttons that walk the LIVE list, then
// the value itself as free text.
//
// THE TEXT FIELD IS NOT A FALLBACK, it is the row's contract. Every fkPick field is
// read as free text by the format (see fieldKind's own comment), so a build that has
// never heard of `generator = ripples` must still be able to hold and re-save that
// line — and an author must be able to type it. The list is an affordance over the
// same value, never a gate on it.
func (a *App) drawInspectorPick(f *inspectorField, t editTarget, ctl sdl.Rect, old string) {
	c := a.ctx
	names := a.editorPickNames(f.pick)
	field := ctl
	if len(names) > 0 {
		// BOTH BUTTONS ARE ALWAYS DRAWN — never `if back { } else if fwd { }`. Ctx.Button
		// draws AND answers in one call, so short-circuiting the second one would blank it
		// for exactly the frame the first was pressed on, which is the frame the user is
		// looking at it (the element rail's own draw-then-probe rule).
		back := c.Button(sdl.Rect{X: ctl.X, Y: ctl.Y, W: editStepW, H: ctl.H}, "<")
		fwd := c.Button(sdl.Rect{X: ctl.X + editStepW, Y: ctl.Y, W: editStepW, H: ctl.H}, ">")
		field.X, field.W = ctl.X+editStepW*2, ctl.W-editStepW*2
		if back || fwd {
			// WRAP from the CURRENT value's position, and start at "(none)" when the value
			// is not offered at all — the ordinary case for a name this build has never
			// heard of, and the one that must not send the picker to a random entry.
			step := 1
			if back {
				step = -1
			}
			i := pickIndexOf(names, old)
			if i < 0 {
				i, step = 0, 0
			}
			i = ((i+step)%len(names) + len(names)) % len(names)
			a.editorWriteStringField(f, t, pickValueAt(names, i))
			return
		}
	}
	if s, _ := c.TextField(editFieldID(f.field), field, old, ""); s != old {
		a.editorWriteStringField(f, t, s)
	}
}

// editorWriteStringField is the ONE path a typed or picked string takes to the
// document: validate what the FORMAT validates, intern, apply.
//
// The validation is here rather than in the setter because a setter cannot say no —
// it has no return value and the op has already been recorded by the time it runs, so
// a refusal there is either a silent no-op (an undo step that does nothing) or a
// truncation nobody was told about. Design §3.3: name the offender, name the limit.
func (a *App) editorWriteStringField(f *inspectorField, t editTarget, s string) {
	if f.field == editFieldGenParams {
		// The FORMAT'S OWN parser, so the editor and the reader cannot disagree about
		// what fits (theme.ParseGenParams == the reader's genParamsOf).
		if _, err := theme.ParseGenParams(s); err != nil {
			a.te.note("gen_params: at most " + strconv.Itoa(theme.GenParamCap) +
				" k=v pairs, each under " + strconv.Itoa(theme.GenParamRuneCap) + " characters")
			return
		}
	}
	if f.field == editFieldElemID {
		// ASKED BEFORE IT IS RECORDED. The rename itself happens in the setter, on the
		// document's own funnel; what happens here is the FORMAT deciding whether it
		// would be allowed — a taken id, a `[element.<id>]` this build preserved and
		// ignored, an id outside the reader's grammar — because a setter cannot say no
		// and a refusal discovered there would leave an undo entry claiming a rename
		// that never happened.
		//
		// The message is theme's own, so the editor cannot quote a grammar or a cap the
		// format does not actually have.
		el := a.te.doc.element(t)
		if el == nil {
			return
		}
		if err := a.te.doc.side.CanRenameElement(el.ID, s); err != nil {
			a.te.note(err.Error())
			return
		}
	}
	id := a.te.doc.intern(s)
	if id == editStrNone && s != "" {
		a.te.note("the editor's string table is full (" + strconv.Itoa(editStrCap) + ") — reopen the editor to clear it")
		return
	}
	a.editorApply(f.mut(t, f.field, editValueStr(id)))
}

// editFieldID is a widget id for one inspector row. Derived from the FIELD, not
// from the element, so re-selecting a different element keeps focus on the same row
// — which is what makes "click through five elements adjusting the same number"
// bearable.
func editFieldID(f editField) string { return "themeedit.f." + f.String() }

// editorSpinner is a − value + control. Returns the (possibly changed) value.
//
// Shift takes the coarse step, so the keyboard rule (arrow = 1 px, Shift = a grid
// line) and the mouse rule are the same rule.
func (a *App) editorSpinner(r sdl.Rect, v, lo, hi int32) int32 {
	c := a.ctx
	step := int32(editRectSpinStep)
	if c.shiftHeld {
		step = editRectSpinCoarse
	}
	if c.Button(sdl.Rect{X: r.X, Y: r.Y, W: editStepW, H: r.H}, "-") {
		v -= step
	}
	if c.Button(sdl.Rect{X: r.X + r.W - editStepW, Y: r.Y, W: editStepW, H: r.H}, "+") {
		v += step
	}
	c.LabelClipped(r.X+editStepW+2, r.Y+3, r.W-editStepW*2-4, strconv.Itoa(int(v)), ColText)
	return clampInt32(v, lo, hi)
}

// editorEnumCycler is a < name > control over the format's own wire names.
func (a *App) editorEnumCycler(r sdl.Rect, v int32, names []string) int32 {
	c := a.ctx
	if len(names) == 0 {
		return v
	}
	if c.Button(sdl.Rect{X: r.X, Y: r.Y, W: editStepW, H: r.H}, "<") {
		v--
	}
	if c.Button(sdl.Rect{X: r.X + r.W - editStepW, Y: r.Y, W: editStepW, H: r.H}, ">") {
		v++
	}
	// WRAP, never clamp: a cycler that stops at the ends makes the last value feel
	// broken, and every one of these enums is a ring of equals.
	n := int32(len(names))
	v = ((v % n) + n) % n
	c.LabelClipped(r.X+editStepW+2, r.Y+3, r.W-editStepW*2-4, names[v], ColText)
	return v
}

// editHex renders a colour payload as rrggbbaa.
func editHex(v editValue) string {
	const digits = "0123456789abcdef"
	out := make([]byte, 0, 8)
	for _, b := range v.b {
		out = append(out, digits[b>>4], digits[b&0x0f])
	}
	return string(out)
}

// parseEditHex reads rrggbb or rrggbbaa. A six-digit value is OPAQUE, which is what
// every hex a user pastes from anywhere else means.
func parseEditHex(s string) (editValue, bool) {
	if len(s) != 6 && len(s) != 8 {
		return editValue{}, false
	}
	var out [4]uint8
	out[3] = 255
	for i := 0; i+1 < len(s); i += 2 {
		hi, ok1 := hexNibble(s[i])
		lo, ok2 := hexNibble(s[i+1])
		if !ok1 || !ok2 {
			return editValue{}, false
		}
		out[i/2] = hi<<4 | lo
	}
	return editValueRGBA(out[0], out[1], out[2], out[3]), true
}

// (The nibble decoder is floatbox.go's hexNibble, reused verbatim rather than
// restated: two hex parsers in one package are two chances to disagree about
// upper-case digits.)

// ---------------------------------------------------------------------------
// Keyboard
// ---------------------------------------------------------------------------

// handleEditorKeys is the editor's own plain-key map. The Ctrl chords are claimed
// PRE-SCREEN, by App.Frame's `if a.themeEditorOpen() { a.editorUndoChord() }`, for
// the reason that function documents: HandleEvent routes every non-clipboard Ctrl
// combination into c.hotkey, so an in-draw c.keyPressed check for Ctrl+Z is dead
// code — and this screen has no courtroom pass of its own to ride when it is opened
// from Settings with no server attached.
//
// It yields to a focused field, which is the house rule every other plain-key claim
// already follows — typing "hex" into a colour row must not delete the selection.
//
// AND IT YIELDS TO A MODAL, which the focus check does NOT cover. c.modalOn only
// gates hovering(), so the fence drawThemeEditor raises for the embedded file
// browser buys the POINTER half its protection and the keyboard half none: with the
// browser up over the editor, DELETE deleted the selected element, H/L toggled
// hidden/locked and the arrows nudged it, under a panel that owns the screen. Worse
// than a stray edit, this function ZEROES c.keyPressed on every arm, so the key is
// spent — a modal that later wants Escape or a text field could not see it either.
// The browser reads no keys today and has no field, so c.focusID stays empty and the
// check above never fires. One line, and it covers the design's two planned modals
// in advance.
func (a *App) handleEditorKeys() {
	c := a.ctx
	if c.modalOn {
		return
	}
	if c.focusID != "" || a.te == nil {
		return
	}
	if a.te.drag.active() {
		// A gesture in flight keeps the mouse in charge, exactly as on both legacy
		// editors' nudge arms (`a.editDrag != 0`). An arrow tap mid-drag would write a
		// value the next drag frame overwrites from its own press-time base, so the
		// nudge would appear to do nothing and would still cost an undo entry.
		return
	}
	if dx, dy, ok := nudgeKeyDir(c.keyPressed); ok {
		a.editorNudgeSelection(dx, dy, c.ctrlHeld)
		c.keyPressed = 0
		return
	}
	switch c.keyPressed {
	case sdl.K_DELETE:
		a.editorDeleteSelection()
		c.keyPressed = 0
	case sdl.K_h:
		a.editorToggleField(editFieldHidden)
		c.keyPressed = 0
	case sdl.K_l:
		a.editorToggleField(editFieldLocked)
		c.keyPressed = 0
	}
}

// editorNudgeSelection moves the whole selection by one step, as ONE grouped op.
//
// IT REUSES nudgeCoord AND layoutGridDesign — the very functions the two legacy
// editors nudge through (layoutnudge.go) — so "arrow = 1 px, Ctrl = the next grid
// line" is one implementation rather than three that agree today. An element's rect
// is design px in its own declared Space, which is the space its authored numbers,
// its anchor delta and the editors' shared grid all live in.
//
// NO CANVAS CLAMP, deliberately, and the argument is layoutnudge.go's: clamping is
// a SLOT rule. Under design §6.6 R1 an anchored element's rect is a signed DELTA on
// its anchor — negative values are the normal case for decoration that overhangs
// its widget — and a SpaceWindow element is measured against no canvas at all.
// Clamping here would forbid values the format defines.
//
// A BURST IS ONE UNDO STEP, and it costs nothing to arrange: consecutive nudges hit
// the ring's coalescing window on the same (kind, target, field), so twenty taps
// merge into one op whose `before` is where the element started.
func (a *App) editorNudgeSelection(dx, dy int, coarse bool) {
	if a.te == nil || a.te.selN == 0 {
		return
	}
	// A GROUP ONLY WHEN THERE IS SOMETHING TO GROUP, and this is not a micro-
	// optimisation — it is what makes a nudge burst one undo step. The group id is
	// part of the coalescing identity (coalesceKey), so opening a FRESH group on every
	// tap would give consecutive taps different keys and defeat the merge entirely.
	// One element = no group = the taps coalesce; many elements = one group per tap,
	// which undoes together and is the correct step for a multi-select nudge.
	if a.te.selN > 1 {
		a.te.ring.begin()
		defer a.te.ring.end()
	}
	for i := 0; i < a.te.selN; i++ {
		t := a.te.sel[i]
		switch t.space {
		case spaceElement:
			el := a.te.doc.element(t)
			if el == nil || el.Locked {
				continue // the editor-only lock (theme.Element.Locked): no drag, no nudge
			}
			v, ok := a.te.doc.read(t, editFieldRect)
			if !ok {
				continue
			}
			next := v
			next.i[0] = int32(nudgeCoord(int(v.i[0]), dx, coarse, 0, layoutGridDesign))
			next.i[1] = int32(nudgeCoord(int(v.i[1]), dy, coarse, 0, layoutGridDesign))
			a.editorApply(mutateElemField(t, editFieldRect, next))
		case spaceDesign:
			// A WIDGET NUDGES TOO, and it clamps where an element does not — the same
			// asymmetry editorClampGeometry carries for the mouse, so the keyboard and
			// the drag can never disagree about where the stage ends.
			r, ok := a.te.doc.slotRect(t.designKey())
			if !ok {
				continue
			}
			next := r
			next.X = nudgeCoord(r.X, dx, coarse, 0, layoutGridDesign)
			next.Y = nudgeCoord(r.Y, dy, coarse, 0, layoutGridDesign)
			a.editorApply(mutateSlotRect(t, a.editorClampGeometry(t, next)))
		}
	}
}

// editorToggleField flips a bool field across the whole selection, as ONE grouped
// op — so H on twelve selected elements is one Ctrl+Z.
func (a *App) editorToggleField(f editField) {
	if a.te == nil || a.te.selN == 0 {
		return
	}
	a.te.ring.begin()
	defer a.te.ring.end()
	for i := 0; i < a.te.selN; i++ {
		t := a.te.sel[i]
		v, ok := a.te.doc.read(t, f)
		if !ok {
			continue
		}
		a.editorApply(mutateElemField(t, f, editValueBool(v.i[0] == 0)))
	}
}

// editorDeleteSelection removes every selected element as one grouped op.
//
// HIGHEST INDEX FIRST. Identity is the INDEX (theme.Element), so removing element 3
// renumbers everything after it — deleting in ascending order would delete the
// wrong rows from the second one on. Descending is the only order in which the
// remaining targets stay valid.
func (a *App) editorDeleteSelection() {
	if a.te == nil || a.te.selN == 0 {
		return
	}
	// A copy of the indices, sorted descending, on the stack-resident selection array.
	var idxs [themeSelCap]int
	n := 0
	for i := 0; i < a.te.selN; i++ {
		if idx, ok := a.te.sel[i].elemIdx(); ok {
			idxs[n] = idx
			n++
		}
	}
	for i := 1; i < n; i++ { // insertion sort, descending; n <= themeSelCap
		v := idxs[i]
		j := i - 1
		for j >= 0 && idxs[j] < v {
			idxs[j+1] = idxs[j]
			j--
		}
		idxs[j+1] = v
	}
	a.te.ring.begin()
	defer a.te.ring.end()
	// TWO REFUSALS, COUNTED APART. "some elements were locked or the undo graveyard is
	// full" named neither offender and offered no fix, which is precisely what design
	// §3.3's error-copy rule forbids — and the two have OPPOSITE remedies: a locked
	// element wants L, a full graveyard wants a save or a reopen. One sentence covering
	// both taught the user nothing about either.
	//
	// The counting needs no second predicate and no new state. The lock is tested here,
	// before the funnel, so it is known without asking the document twice; a target the
	// selection outlived is skipped as neither (nothing was prevented — see
	// applyNoChange); and past those two the ONLY answer opElemDel has left is
	// themeElemGraveCap, so a false from the funnel means the graveyard and nothing else.
	locked, full := 0, 0
	for i := 0; i < n; i++ {
		t := elementTarget(idxs[i])
		el := a.te.doc.element(t)
		if el == nil {
			continue // the selection outlived the element: nothing was prevented, nothing to say
		}
		if el.Locked {
			locked++
			continue // the editor-only lock: no drag, no nudge, no delete
		}
		if !a.editorApply(themeEditOp{kind: opElemDel, target: t}) {
			full++
		}
	}
	if msg := editorDeleteRefusal(locked, full); msg != "" {
		a.te.note(msg)
	}
	a.te.selectClear()
}

// editorDeleteRefusal is the chip a partly-refused delete posts, or "" when
// everything the user asked for happened.
//
// A PURE FUNCTION so the wording is gate-able without a renderer: the sentence is the
// feature here, not the arithmetic that reaches it.
func editorDeleteRefusal(locked, full int) string {
	switch {
	case locked > 0 && full > 0:
		return editorPluralN(locked, "element") + " locked (press L to unlock), and the undo " +
			"graveyard is full at " + itoa(themeElemGraveCap) + " — save or reopen the editor to clear it"
	case locked > 0:
		return editorPluralN(locked, "locked element") + " kept — press L to unlock, then Delete"
	case full > 0:
		return "the undo graveyard is full at " + itoa(themeElemGraveCap) +
			" deleted elements — save or reopen the editor to clear it"
	}
	return ""
}

// editorPluralN is "1 thing" / "3 things". Named because three chips build the same
// phrase and an inline copy in each is three chances to forget the plural.
func editorPluralN(n int, noun string) string {
	s := itoa(n) + " " + noun
	if n != 1 {
		s += "s"
	}
	return s
}

// itoa is strconv.Itoa under a name short enough to keep the sentences above
// readable; the chip path is cold (a refusal, once per gesture), so the allocation
// it costs is not on any gated path.
func itoa(n int) string { return strconv.Itoa(n) }

// ---------------------------------------------------------------------------
// The reclaim seam — what makes an edit durable
// ---------------------------------------------------------------------------

// reclaimThemeDoc re-installs the editor's model after a theme apply.
//
// THE DEFECT IT CLOSES, recorded at its own site by W6 (layoutnudge.go): applies
// are NOT user-initiated. healTheme re-kicks one on any T1 eviction of a theme
// texture, the texture-filter row kicks one, boot kicks two — and each one does
// `a.themeSidecar = res.sidecar`, discarding every unsaved edit with nothing on
// screen to say why. A user could nudge a decoration into place, take their hand
// off the keyboard, and watch it jump back.
//
// ONE NAMED CALL SITE: pollThemeApply, immediately after that assignment. It is
// deliberately not a general "the editor owns the sidecar" rule — it is a single
// re-install, guarded on the theme NAME, so switching themes mid-session correctly
// loses the document rather than pasting the old theme's elements onto the new one.
func (a *App) reclaimThemeDoc() {
	if a.te == nil || a.te.doc == nil || a.te.doc.side == nil {
		return
	}
	name, _ := a.d.Prefs.Theme()
	if name != a.te.doc.name {
		return // a different theme is applied now; the document is not about it
	}
	a.themeSidecar = a.te.doc.side
	// THE GEOMETRY TIER NEEDS THE SAME TREATMENT, and it needs it in this order.
	// pollThemeApply has just replaced a.themeRects wholesale with a map built from
	// the sidecar ON DISK, so every unsaved [overrides] edit is missing from it and
	// the document is pointed at a map nobody draws from any more. Re-binding alone
	// would leave the pixels wrong; re-folding alone would leave the document holding
	// a dead map.
	//
	// The re-fold lands AFTER applyRectOverrides has already run (app.go), so while
	// the editor is open the document beats the player's own live Ctrl+F2 tweaks for
	// the keys it authors. That is the one place the documented precedence is
	// inverted, and it is inverted deliberately and temporarily: the editor is the
	// authoring surface, and a stale preference silently overwriting the widget the
	// user is dragging is not a precedence rule anybody would defend. The next apply
	// after the editor closes restores the ordinary order.
	a.te.doc.bindLive(a.themeRects)
	foldSidecarOverrides(a.themeRects, a.te.doc.side)
}
