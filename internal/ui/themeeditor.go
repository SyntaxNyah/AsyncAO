package ui

// The theme editor screen: the live courtroom at full window size with two translucent rails over it.
// Insetting the canvas would teach fitDesignCanvas, which char select shares, about reserved bands.
// The Space peek changes alpha, never clip: SDL clip timing is backend-specific (Metal evaluates at use time).
// A closed editor costs one nil pointer, a.te, which the courtroom's frame path does not read.

import (
	"strconv"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/helper/parse"
	"github.com/SyntaxNyah/AsyncAO/internal/helper/value"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

const (
	// themeSelCap bounds the selection and bulk ops; a fixed array because a slice would allocate on the per-frame
	// walk.
	themeSelCap = 64

	// editRailW is both rails' width: fits a label and value, and two still leave a 4:3 canvas readable at 1280 px.
	editRailW = 232

	// editBannerH matches layoutBannerH so the header lines up with the legacy overlay editors' banner.
	editBannerH = 26

	// editRailAlpha and editRailPeekAlpha are the rail plate alpha normally and while Space is held.
	editRailAlpha     = 235
	editRailPeekAlpha = 38

	// editRowH is one element-rail row: a glyph, a name and two toggles.
	editRowH = 20
	// editRowPad is the gutter inside a rail.
	editRowPad = 6
	// editToggleW is the eye / lock hit box on a rail row.
	editToggleW = 18

	// editFieldRowH is one inspector row's height; editFieldLabelW is the label's share of it.
	editFieldRowH   = 22
	editFieldLabelW = 84
	// editStepW is a ± stepper button.
	editStepW = 18

	// editRectSpinStep and editRectSpinCoarse are the stepper's amounts: one unit, or one grid line with Shift held.
	editRectSpinStep   = 1
	editRectSpinCoarse = layoutGridDesign

	// editorHeaderBtnW and editorHeaderBtnGap size the header button row, laid out from the right edge.
	editorHeaderBtnW   = 62
	editorHeaderBtnGap = 4
	// Placement of the label a disabled header button draws itself, since Ctx.Button is not called for it.
	editorHeaderBtnLabelInsetY = 3
	editorHeaderBtnLabelPadX   = 8

	// editRailAddBtnW and editRailLiveBtnW size the element rail's two header buttons, leaving room for "Elements".
	editRailAddBtnW  = 74
	editRailLiveBtnW = 56

	// editStatusMs is how long a rail chip ("cap reached", "saved") stays up.
	editStatusMs = 4000
)

// themeEditor is the screen's state; it exists only while open, so a closed editor costs one nil pointer.
type themeEditor struct {
	doc  *themeDoc
	ring themeEditRing

	// editorReturn is where Back goes.
	editorReturn Screen

	// sel is the selection (see themeSelCap).
	sel  [themeSelCap]editTarget
	selN int
	// drag is the live canvas gesture, alive only during the press.
	drag editGesture

	// railScroll is the element rail's scroll offset in pixels.
	railScroll int32
	// peek is Space-held: both rails drop to editRailPeekAlpha.
	peek bool
	// exitAt stamps a Back press with unsaved edits; a second press within the chip's window discards them.
	exitAt time.Time
	// copyAt stamps a Save press on a read-only theme; a second press within themeCopyConfirmWindow saves a copy.
	copyAt time.Time
	// save is the in-flight write; non-nil means one is running, so there is only ever one.
	save *editSaveJob
	// export is the in-flight .aotheme bundle; non-nil means running, and a second Share is refused with a chip.
	export *editExportJob

	// picks caches the inspector's two dynamic pick lists; it lives here so it dies with the screen.
	picks pickCache

	// gallery is the preset gallery panel; non-nil means open, and it holds a bounded miniature cache and one-deep
	// undo.
	gallery *themeGallery

	// report is the import report panel; non-nil means open.
	report *themeReportPanel
	// reportChip and reportChipN are the chip's baked label and note count, so the per-frame rail draw does not concat.
	reportChip  string
	reportChipN int

	// image is the in-flight image intake; non-nil means running, and a buffered channel never blocks its goroutine.
	image *editImageJob

	// preview is the offline demo played full-bleed with no editing chrome; non-nil means playing.
	// Distinct from livePaused, which freezes to do less work; this plays to do more.
	preview *editorPreview

	// livePaused is the live preview toggle, inverted so the zero value is live.
	livePaused bool

	// Font rail state: panel up, the in-flight face copy (non-nil means running), and the cycler's reusable name
	// buffer.
	fontsOpen bool
	font      *editFontJob
	fontNames []string

	// status and statusAt are the rail chip: what just happened, and when.
	status   string
	statusAt time.Time
}

// openThemeEditor arms the editor over the applied theme, after disarming the two legacy overlay editors.
// Those edit preferences and this edits a theme document, so they are never armed together.
// Returns false when the theme has no native tier to edit; the Settings row offers copy-for-editing instead.
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
	// Modals close too: an editor armed under a modal locks up the Esc handling.
	a.closeEditorBlockingOverlays()
	if !a.armThemeEditor(from) {
		return false
	}
	a.screen = ScreenThemeEditor
	a.uiDirty = true
	// Save needs the catalog's answer on whether this theme is writable; the scan is off-thread and rate limited.
	a.requestThemeCatalog(themeScanOnOpen)
	return true
}

// armThemeEditor builds the editor state over the applied theme, without SDL so headless callers use the same path.
func (a *App) armThemeEditor(from Screen) bool {
	name, _ := a.d.Prefs.Load().Theme()
	doc := newThemeDoc(name, a.themeSidecar)
	if doc == nil {
		return false
	}
	// Bound to the applied design map: an [overrides] edit must move a pixel as well as write a row.
	doc.bindLive(a.themeRects)
	a.te = &themeEditor{doc: doc}
	a.te.editorReturn = from
	return true
}

// requestEditorExit is the only exit: a clean document leaves at once, a dirty one needs a second press in the window.
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
		// Restores geometry; closeThemeEditor below reconciles the art for every exit.
		a.invalidateThemeCanvases()
		a.closeThemeEditor()
		return
	}
	a.te.exitAt = time.Now()
	a.te.note("unsaved edits — press Save, or Back again to discard them")
}

// closeThemeEditor releases the editor and returns to where it was opened from.
// The model stays live because a.themeSidecar and doc.side are the same *theme.Sidecar.
// Art is reconciled here: dropping a.te ends the per-frame editorSyncArt, and this is the only place that clears it.
// Skipping it after a discard leaves the courtroom drawing the edited plan, possibly with tiles already released.
func (a *App) closeThemeEditor() {
	if a.te == nil {
		return
	}
	// Lift the pause first: a paused exit that skipped the sync would leave the courtroom drawing a stale plan.
	a.te.livePaused = false
	a.editorSyncArt()
	back := a.te.editorReturn
	a.te = nil
	if back == ScreenThemeEditor { // never bounce back into ourselves
		back = ScreenSettings
	}
	a.screen = back
	a.uiDirty = true
}

// themeEditorOpen reports the editor armed and on screen; on frames preempting the dispatch it must not eat the
// keyboard.
func (a *App) themeEditorOpen() bool {
	return a.te != nil && a.screen == ScreenThemeEditor && !a.screenDispatchPreempted()
}

// editorApply performs and records one op; it is the only place the screen mutates the document.
// Apply, push and invalidate go together, or the edit could not be undone or could not be seen.
// Preview refuses here because every input path converges here.
func (a *App) editorApply(op themeEditOp) bool {
	if a.te == nil || op.kind == opNone || a.editorPreviewOn() {
		return false
	}
	done, res := a.te.doc.apply(op)
	if res != applyDone {
		// Only a real refusal chips: a drag holding still asks for the rect it already holds, which is a quiet no-op.
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

// editorLiveOn reports the live preview on; true with no editor so callers need not special-case the closed state.
// Off freezes the canvas only: edits still land, and the checks return before the signature hash and the invalidate.
func (te *themeEditor) editorLiveOn() bool { return te == nil || !te.livePaused }

// editorInvalidateLive is invalidateThemeCanvases, skipped while the preview is frozen.
func (a *App) editorInvalidateLive() {
	if !a.te.editorLiveOn() {
		return
	}
	a.invalidateThemeCanvases()
}

// setEditorLive flips the toggle; turning on always reconciles, since editorSyncArt's signature decides if anything
// changed.
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

// editorUndo steps back one op or one whole group; it refuses during preview, as does editorRedo.
// Neither goes through editorApply and Ctrl+Z is claimed pre-screen, so the demo could otherwise rewind unseen work.
func (a *App) editorUndo() bool {
	if a.te == nil || a.editorPreviewOn() {
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
	if a.te == nil || a.editorPreviewOn() {
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

func (e *themeEditor) selectOnly(t editTarget) {
	e.selN = 0
	e.selectAdd(t)
}

// selectAdd appends unless already selected or full; the array is the cap (hard rule 4).
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

// selectPrimary swaps a selected target to the front, keeping the rest so a multi-drag works; else it selects it alone.
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

// primary is the first selection, so a Shift-click adding a second does not re-point the inspector.
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

// drawThemeEditor draws the canvas, then the header, then the rails, so the rails float over the frame.
func (a *App) drawThemeEditor(w, h int32) {
	if a.te == nil {
		// Reachable only through a state restore that set a.screen directly: leave rather than draw an empty shell.
		a.screen = ScreenSettings
		return
	}
	c := a.ctx
	// Space peeks only while held.
	a.te.peek = c.keyHeld(sdl.K_SPACE) && c.focusID == ""
	// modalOn goes up before keys and canvas for the file browser: the canvas eats arrows and hit-tests with raw
	// pointIn.
	if demoBrowser.open {
		c.modalOn = true
	}
	a.pollEditorSave()
	a.pollEditorFont()
	a.pollEditorExport()
	a.pollEditorImage()
	// Preview returns above the key handler, canvas input and rails, so they are hidden by not running.
	// Below the polls so a running save or export still lands; the font and image polls hold their results themselves.
	if a.editorPreviewOn() {
		a.te.peek = false
		a.drawEditorPreviewScreen(w, h)
		return
	}
	// Keys are claimed before the canvas: it is the courtroom, whose arrow consumers would otherwise spend the key
	// first.
	a.handleEditorKeys()
	// Art is reconciled before the canvas bakes it, because element pages resolve from a.themeMediaPlan by index.
	// Here rather than in editorApply so undo, redo, grouped paste and discard are all covered.
	a.editorSyncArt()

	lay := a.themeWindowLayout(w, h)
	a.drawEditorCanvas(w, h)
	// drawCourtroom may have moved to the lobby on a dropped session: follow to that screen, since it is not a Back
	// press.
	if a.screen != ScreenThemeEditor {
		to := a.screen
		a.closeThemeEditor()
		a.screen = to
		return
	}
	a.editorCanvasInput(w, h, lay)
	// Fetched again after input: every geometry edit invalidates the layout, and stale outlines would lag the cursor.
	lay = a.themeWindowLayout(w, h)
	a.drawEditorCanvasOverlay(w, h, lay)
	a.drawEditorHeader(w)
	// Back may have closed the editor, and every rail below dereferences a.te.
	if a.te == nil {
		return
	}
	// modalOn over the rails while a panel is up: the centred panel can overlap a rail, and a click would reach both.
	// The header stays outside: the panel starts below editBannerH, and blocking it would kill the Fonts close button.
	savedModal := c.modalOn
	if a.editorPanelUp() {
		c.modalOn = true
	}
	a.drawEditorElementRail(w, h)
	a.drawEditorInspector(w, h)
	c.modalOn = savedModal
	// Drawn last with modalOn released, so panels float over the rails and their own buttons stay live.
	a.drawEditorFontPanel(w, h)
	a.drawThemeGallery(w, h)
	a.drawThemeReportPanel(w, h)
	// The file browser is the one true modal here, so it draws topmost with modalOn released for its own rows.
	if demoBrowser.open {
		c.modalOn = false
	}
	a.drawDemoBrowser(w, h)
}

// editorPanelUp reports a panel on screen; the draw and the raw-pointIn canvas probe both ask it.
// The two modals are not panels: they close the screen's input path rather than float over it.
func (a *App) editorPanelUp() bool {
	return a.te != nil && (a.te.fontsOpen || a.te.gallery != nil || a.te.report != nil)
}

// closeEditorPanels shuts all editorPanelUp reports, since panels never stack; one missing here is skipped by Esc.
func (a *App) closeEditorPanels() {
	if a.te == nil {
		return
	}
	a.te.fontsOpen = false
	a.te.gallery = nil
	a.te.report = nil
	a.uiDirty = true
}

// drawEditorCanvas paints the theme full bleed: the real courtroom with a live room, else the fitted geometry canvas.
func (a *App) drawEditorCanvas(w, h int32) {
	if a.room != nil && a.sess != nil && !a.theaterOn {
		// Courtroom widgets go inert while used as a canvas: the kit is single-pass, so a select press would also press
		// them.
		// Save and restore, not assign: a rail's open dropdown may hold modalOn legitimately.
		saved := a.ctx.modalOn
		a.ctx.modalOn = true
		a.drawCourtroom(w, h)
		a.ctx.modalOn = saved
		return
	}
	a.drawEditorGeometryCanvas(w, h)
}

// drawEditorGeometryCanvas is the session-free arm, drawn in design geometry at 1:1.
func (a *App) drawEditorGeometryCanvas(w, h int32) {
	c := a.ctx
	c.Fill(sdl.Rect{X: 0, Y: 0, W: w, H: h}, ColBackground)
	lay := a.themeWindowLayout(w, h)
	if lay == nil || !lay.valid {
		c.Label(editRailW+editRowPad*2, editBannerH+editRowPad*2,
			"This theme has no courtroom_design.ini canvas to lay out.", ColTextDim)
		return
	}
	// Only the canvas: drawEditorCanvasOverlay outlines widgets and elements over both arms, so they agree on hit
	// areas.
	if court, ok := lay.rect("courtroom"); ok {
		c.Border(court, ColPanelHi)
	}
}

// drawEditorHeader is the top band: theme name, dirty state, action buttons and Back.
func (a *App) drawEditorHeader(w int32) {
	c := a.ctx
	band := sdl.Rect{X: 0, Y: 0, W: w, H: editBannerH}
	// The band is ours this frame, so the menu bar stands down; otherwise its opaque strip paints over these buttons.
	a.noteEditorBanner()
	c.Fill(band, a.te.editorPlate(ColPanel))
	name := a.te.doc.name
	if name == "" {
		name = "(unnamed theme)"
	}
	// The label columns are clamped against the button row, which the wide Preview button moves left.
	edge := editorPreviewBtnRect(w).X - editorHeaderBtnGap
	nameW := clampLabelColumn(w/3, editRowPad, edge)
	if nameW > 0 {
		c.LabelClipped(editRowPad, 5, nameW, name, ColText)
	}
	stateX := w/3 + editRowPad
	if stateW := clampLabelColumn(w/4, stateX, edge); stateW > 0 {
		c.LabelClipped(stateX, 5, stateW, a.te.editorStateLabel(), ColTextDim)
	}

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
	// Disabled with an empty timeline, the ordinary Redo state because every fresh edit truncates it.
	x -= btnW + btnGap
	if a.editorHeaderBtn(sdl.Rect{X: x, Y: 2, W: btnW, H: editBannerH - 4}, "Redo", a.te.ring.canRedo()) {
		a.editorRedo()
	}
	x -= btnW + btnGap
	if a.editorHeaderBtn(sdl.Rect{X: x, Y: 2, W: btnW, H: editBannerH - 4}, "Undo", a.te.ring.canUndo()) {
		a.editorUndo()
	}
	x -= btnW + btnGap
	if c.Button(sdl.Rect{X: x, Y: 2, W: btnW, H: editBannerH - 4}, "Fonts") {
		a.te.fontsOpen = !a.te.fontsOpen
	}
	// Image and Live live on the element rail: at 1280 px two more buttons here would draw over the theme name.
	// Gallery sits beside Fonts: both are panels that change the whole theme at once.
	x -= btnW + btnGap
	if c.Button(sdl.Rect{X: x, Y: 2, W: btnW, H: editBannerH - 4}, "Gallery") {
		if a.te.gallery != nil {
			a.te.gallery = nil
		} else {
			a.openThemeGallery()
		}
	}
	x -= btnW + btnGap
	if c.Button(sdl.Rect{X: x, Y: 2, W: btnW, H: editBannerH - 4}, "Share") {
		a.editorExport()
	}
	// Preview grows leftward from the row instead of pushing it (editorPreviewBtnRect).
	if c.Button(editorPreviewBtnRect(w), editorPreviewEnterLabel) {
		a.toggleEditorPreview()
	}
}

// clampLabelColumn is a label column's clip width, cut at the button row; callers must draw nothing when non-positive,
// because LabelClipped reads a non-positive width as no clip.
func clampLabelColumn(want, x, edge int32) int32 {
	if lim := edge - x; want > lim {
		want = lim
	}
	return want
}

// editorHeaderBtn is a header button that dims when disabled instead of vanishing, which would re-flow the band.
// The click is swallowed here so the action never quietly no-ops.
func (a *App) editorHeaderBtn(r sdl.Rect, label string, enabled bool) bool {
	c := a.ctx
	if enabled {
		return c.Button(r, label)
	}
	// ColPanel as an enabled Ctx.Button fills, so it reads as the same button greyed.
	c.Fill(r, ColPanel)
	c.Border(r, ColPanelHi)
	w := c.TextWidth(label)
	// maxW > 0 as in Ctx.Button: LabelClipped reads a non-positive width as no clip rather than nothing fits.
	if maxW := r.W - editorHeaderBtnLabelPadX; w > maxW && maxW > 0 {
		w = maxW
	}
	c.LabelClipped(r.X+(r.W-w)/2, r.Y+editorHeaderBtnLabelInsetY, w, label, ColTextDim)
	return false
}

// editorLiveLabel is the Live button's caption; constants because the label cache keys on the string.
func editorLiveLabel(on bool) string {
	if on {
		return "Live"
	}
	return "Frozen"
}

// editorImageBtnLabel is the rail's add-image caption, constants for the same reason as editorLiveLabel.
func editorImageBtnLabel(busy bool) string {
	if busy {
		return "adding…"
	}
	return "+ Image"
}

// editorStateLabel is the saved state; one constant per state because the label cache keys on the string.
func (te *themeEditor) editorStateLabel() string {
	switch {
	case te.save != nil:
		return "Saving…"
	case te.doc.dirty:
		return "Unsaved edits"
	}
	return "Saved"
}

// editorPlate is a plate colour at the current peek alpha, shared so the peek applies to every surface.
func (te *themeEditor) editorPlate(base sdl.Color) sdl.Color {
	base.A = editRailAlpha
	if te != nil && te.peek {
		base.A = editRailPeekAlpha
	}
	return base
}

// drawEditorElementRail is the left rail: the z-order list.
// Use pushClip, never raw Ren.SetClipRect: the raw call clips drawing only, and hovering() honours only the kit's
// stack.
func (a *App) drawEditorElementRail(w, h int32) {
	c := a.ctx
	rail := sdl.Rect{X: 0, Y: editBannerH, W: editRailW, H: h - editBannerH}
	c.Fill(rail, a.te.editorPlate(ColPanel))
	c.Label(editRowPad, rail.Y+4, "Elements", ColText)
	a.drawEditorRailActions(rail)

	// The report chip takes a row from the body so it does not draw over the element count.
	chipH := int32(0)
	if len(a.themeReport) > 0 {
		chipH = editRowH
	}
	body := sdl.Rect{X: rail.X, Y: rail.Y + editRowH, W: rail.W, H: rail.H - editRowH*2 - chipH}
	n := a.te.doc.elementCount()
	// Through WheelIn before the clip, so the courtroom underneath cannot also scroll from the same notch.
	if d := c.WheelIn(body); d != 0 {
		a.te.railScroll -= d * editRowH
	}
	// Clamped after the take, so a theme that shrank does not strand the list past its end.
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

	// Footer: the count against the cap, with the import report chip above it when there are notes.
	foot := rail.Y + rail.H - editRowH
	a.drawEditorReportChip(rail, foot-chipH)
	c.LabelClipped(editRowPad, foot+3, rail.W-editRowPad*2,
		strconv.Itoa(n)+" / "+strconv.Itoa(theme.ElementCap)+" elements", ColTextDim)
	// Status stacks above the report chip, or a refusal would paint over the button explaining the notes.
	if a.te.status != "" && time.Since(a.te.statusAt) < editStatusMs*time.Millisecond {
		c.LabelClipped(editRowPad, foot-chipH-editRowH+3, rail.W-editRowPad*2, a.te.status, ColDanger)
	}
}

// drawEditorRailActions draws + Image and the live toggle; other element kinds come from re-typing in the inspector.
func (a *App) drawEditorRailActions(rail sdl.Rect) {
	c := a.ctx
	// Laid out from the right so the buttons cannot push each other off the rail as labels change.
	live := a.te.editorLiveOn()
	liveR := sdl.Rect{
		X: rail.X + rail.W - editRowPad - editRailLiveBtnW,
		Y: rail.Y + 2,
		W: editRailLiveBtnW,
		H: editRowH - 2,
	}
	imgR := sdl.Rect{X: liveR.X - editRailAddBtnW - editRowPad/2, Y: liveR.Y, W: editRailAddBtnW, H: liveR.H}
	if c.Button(imgR, editorImageBtnLabel(a.te.image != nil)) {
		// Browsing uses the same intake as a drag (editorTakeImage), so caps and destination cannot diverge.
		a.openDemoBrowserFor(purposeThemeImage)
	}
	// The label shows the state, not the verb; both strings are constants, so no per-frame allocation.
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

	// Draw the whole row before taking input, or it is half-painted on the frame it was clicked.
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

	// Toggles are probed first: they sit inside the row and ClickedIn consumes, so order is priority.
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

// eyeGlyph and lockGlyph are the row's state characters; ASCII because a per-frame colour-emoji raster is costly.
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

// drawEditorInspector is the right rail: the property rows for the primary selection.
func (a *App) drawEditorInspector(w, h int32) {
	c := a.ctx
	rail := sdl.Rect{X: w - editRailW, Y: editBannerH, W: editRailW, H: h - editBannerH}
	c.Fill(rail, a.te.editorPlate(ColPanel))
	t := a.te.primary()
	if !t.armed() {
		c.Label(rail.X+editRowPad, rail.Y+4, "Nothing selected", ColTextDim)
		return
	}
	rows := a.te.inspectorRowsFor(t)
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

// drawInspectorTail draws what sits below the rows: the widget parity note and Reset, and the text format-1 note.
// Not table rows because neither is a field: a row would need a mutator that mutates nothing.
func (a *App) drawInspectorTail(t editTarget, rail sdl.Rect, y int32) int32 {
	c := a.ctx
	switch t.space {
	case spaceDesign:
		// A theme may restate an AO2 widget's rect and nothing else, so this rail has one section.
		c.LabelClipped(rail.X+editRowPad, y+4, rail.W-editRowPad*2,
			"AO2 widget — geometry only", ColTextDim)
		y += editFieldRowH
		_, authored := a.te.doc.side.OverrideRect(t.designKey())
		btn := sdl.Rect{X: rail.X + editRowPad, Y: y, W: rail.W - editRowPad*2, H: editFieldRowH - 2}
		if !authored {
			// Nothing to reset: drawn disabled rather than hidden, so the control does not move during a drag.
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
		// font and size are carried but not drawn in format 1 (docs/THEME-FORMAT.md): a per-element point size would
		// rebuild the fontSet per frame. The [fontbind] rail is what renders, so this points there.
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

// drawInspectorRow draws one row and routes its edit through the row's mut op; it never writes a field itself.
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
		// Four spinners on one row: x, y, w, h.
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
		// CheckboxIn returns the new value, so a click on a muted row, which returns it untouched, writes nothing.
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
		// TextField's second result is Enter; comparing the string keeps a half-typed hex inert instead of writing
		// black.
		hex := editHex(cur)
		if s, _ := c.TextField(editFieldID(f.field), hexR, hex, "rrggbbaa"); s != hex {
			if c, good := parse.HexColor(s); good {
				v := editValueRGBA(c.R, c.G, c.B, c.A)
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

// drawInspectorPick is an fkPick row: two step buttons over the live list, then the value as free text.
// The text field must stay: the format reads these as free text, so an unknown generator name must still be editable.
func (a *App) drawInspectorPick(f *inspectorField, t editTarget, ctl sdl.Rect, old string) {
	c := a.ctx
	names := a.te.editorPickNames(f.pick)
	field := ctl
	if len(names) > 0 {
		// Both buttons always draw: Ctx.Button draws and answers in one call, so short-circuiting would blank the
		// second.
		back := c.Button(sdl.Rect{X: ctl.X, Y: ctl.Y, W: editStepW, H: ctl.H}, "<")
		fwd := c.Button(sdl.Rect{X: ctl.X + editStepW, Y: ctl.Y, W: editStepW, H: ctl.H}, ">")
		field.X, field.W = ctl.X+editStepW*2, ctl.W-editStepW*2
		if back || fwd {
			// Wrap from the current position; a value not on the list starts at "(none)" rather than a random entry.
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

// editorWriteStringField validates, interns and applies a typed or picked string.
// Validation is here because a setter cannot refuse: the op is already recorded by the time it runs.
func (a *App) editorWriteStringField(f *inspectorField, t editTarget, s string) {
	if f.field == editFieldGenParams {
		// The format's own parser, so the editor and the reader cannot disagree about what fits.
		if _, err := theme.ParseGenParams(s); err != nil {
			a.te.note("gen_params: at most " + strconv.Itoa(theme.GenParamCap) +
				" k=v pairs, each under " + strconv.Itoa(theme.GenParamRuneCap) + " characters")
			return
		}
	}
	if f.field == editFieldElemID {
		// Asked before recording: a refusal in the setter would leave an undo entry for a rename that never happened.
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

// editFieldID is one inspector row's widget id, from the field so focus survives re-selecting another element.
func editFieldID(f editField) string { return "themeedit.f." + f.String() }

// editorSpinner is a - value + control returning the possibly changed value; Shift takes the coarse step.
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
	return value.Clamp(v, lo, hi)
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
	// Wrap, never clamp: every one of these enums is a ring of equals.
	n := int32(len(names))
	v = ((v % n) + n) % n
	c.LabelClipped(r.X+editStepW+2, r.Y+3, r.W-editStepW*2-4, names[v], ColText)
	return v
}

func editHex(v editValue) string {
	const digits = "0123456789abcdef"
	out := make([]byte, 0, 8)
	for _, b := range v.b {
		out = append(out, digits[b>>4], digits[b&0x0f])
	}
	return string(out)
}

// handleEditorKeys is the editor's plain-key map; Ctrl chords are claimed before the screen (editorUndoChord).
// HandleEvent routes non-clipboard Ctrl combinations into c.hotkey, so an in-draw Ctrl+Z check would be dead code.
// It yields to a focused field and to a modal: modalOn only affects hovering(), and this zeroes c.keyPressed.
func (a *App) handleEditorKeys() {
	c := a.ctx
	if c.modalOn {
		return
	}
	if c.focusID != "" || a.te == nil {
		return
	}
	if a.te.drag.active() {
		// A drag keeps the mouse in charge: the next drag frame would overwrite a nudge from its press-time base.
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

// editorNudgeSelection moves the selection one step through nudgeCoord, the function the legacy editors share.
// No canvas clamp for elements: an anchored rect is a signed delta, and SpaceWindow elements have no canvas.
// Consecutive nudges hit the ring's coalescing window, so a burst is one undo step.
func (a *App) editorNudgeSelection(dx, dy int, coarse bool) {
	if a.te == nil || a.te.selN == 0 {
		return
	}
	// Group only when there are several: the group id is part of coalesceKey, so a fresh group per tap defeats the
	// merge.
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
			// A widget clamps where an element does not, as editorClampGeometry does for the mouse.
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

// editorToggleField flips a bool field across the selection as one grouped op.
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

// editorDeleteSelection removes every selected element as one grouped op, highest index first.
// Identity is the index, so removal renumbers everything after it.
func (a *App) editorDeleteSelection() {
	if a.te == nil || a.te.selN == 0 {
		return
	}
	// A copy of the indices, sorted descending.
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
	// Counted apart because the remedies differ: a locked element wants L, a full graveyard wants a save or reopen.
	// Past the lock and a missing element, the only refusal left for opElemDel is themeElemGraveCap.
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

// editorDeleteRefusal is the chip a partly refused delete posts, or "" when everything happened.
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

// editorPluralN is "1 thing" or "3 things".
func editorPluralN(n int, noun string) string {
	s := itoa(n) + " " + noun
	if n != 1 {
		s += "s"
	}
	return s
}

// itoa keeps the chip sentences readable; the chip path is cold, so the allocation does not matter.
func itoa(n int) string { return strconv.Itoa(n) }

// reclaimThemeDoc reinstalls the editor's model after a theme apply, so unsaved edits survive.
// Applies are not user-initiated: healTheme and the texture-filter row kick them too, each replacing a.themeSidecar.
// Guarded on the theme name, so switching themes loses the document rather than pasting it onto the new theme.
func (a *App) reclaimThemeDoc() {
	if a.te == nil || a.te.doc == nil || a.te.doc.side == nil {
		return
	}
	name, _ := a.d.Prefs.Load().Theme()
	if name != a.te.doc.name {
		return // a different theme is applied now; the document is not about it
	}
	a.themeSidecar = a.te.doc.side
	// pollThemeApply just rebuilt a.themeRects from disk, so bind and fold are both needed, in this order.
	// This runs after applyRectOverrides, so while the editor is open the document beats live Ctrl+F2 tweaks for its
	// keys.
	a.te.doc.bindLive(a.themeRects)
	foldSidecarOverrides(a.themeRects, a.te.doc.side)
}
