package ui

// The theme editor's preview: a canned offline demo that owns the screen and mutates no theme.
// It is a distinct state from the Live/Frozen toggle, which does less work when off; this does more.
// The demo builds its own room through App.newRoom and draws the real drawCourtroom with the demo session installed,
// as renderFullClientTexture does, because the courtroom reads a.room, a.sess and a.icLog through promoted fields.
// Offline two ways: the scene has no fetchable content and a nil audio sink, and the manager is held offline for the
// demo's lifetime, because courtroom.stageMessage's prefetch fan-out is unconditional and URLBuilder always mints a
// URL.
// The character is a procedural silhouette, avoiding the licence and size costs of bundled art. Render thread only.

import (
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

const (
	// editorPreviewBtnW is the Preview button's width: two header buttons and the gap, the widest the band can carry.
	editorPreviewBtnW = editorHeaderBtnW*2 + editorHeaderBtnGap

	// editorPreviewSlotFromRight is the button's position from the right: Back 0, Save, Redo, Undo, Fonts, Gallery,
	// Share 6.
	editorPreviewSlotFromRight = 7

	// editorPreviewEnterLabel and editorPreviewExitLabel are the captions; constants as the label cache keys on the
	// string.
	editorPreviewEnterLabel = "▶ Preview theme"
	editorPreviewExitLabel  = "◼ Exit preview (Esc)"
)

const (
	// editorPreviewShowname is the demo speaker's showname, short enough to fit AO2's stock name box at every scale.
	editorPreviewShowname = "Preview"

	// editorPreviewChar is empty on purpose: it keeps a real sprite off the stage, one of the demo's two offline
	// guarantees.
	editorPreviewChar = ""

	// editorPreviewEmote makes the message a well-formed MS; with no character it names nothing fetchable.
	editorPreviewEmote = "normal"

	// editorPreviewLine is long enough for the typewriter to crawl and to wrap to a second row on a stock-width
	// chatbox.
	editorPreviewLine = "This is how your theme looks while somebody is talking — " +
		"the chatbox, the name plate, the crawl and the blips, all at play speed."

	// editorPreviewTrack is wider than the stock 224 px music_name, because the banner only scrolls when the title
	// overflows.
	// It carries an extension: without one the name is an area transfer, not a song (isAreaTransfer, courtroom.go:830).
	editorPreviewTrack = "Pursuit ~ Cornered (Theme Preview Extended Orchestral Arrangement).opus"

	// editorPreviewRest is how long a settled message rests before the demo plays it again.
	editorPreviewRest = 2500 * time.Millisecond
)

// editorPreviewLogEntry is one canned IC row: the line as the log renders it, and the speaker that tints the name.
type editorPreviewLogEntry struct {
	line    string
	speaker string
}

// editorPreviewLog is the canned scrollback, seeded once through the same pushIC the live client uses.
var editorPreviewLog = [...]editorPreviewLogEntry{
	{"Judge: Court is now in session.", "Judge"},
	{"Preview: Your theme is what everybody else sees.", "Preview"},
	{"Witness: …and the log wraps exactly like this.", "Witness"},
}

const (
	// The procedural silhouette, in percent of the stage rect, since the stage size follows window and theme fit.
	editorPreviewFigureHPct    = 62 // figure height as a percent of the stage height
	editorPreviewFigureWPct    = 26 // shoulder width as a percent of the stage width
	editorPreviewHeadPct       = 26 // head height as a percent of the figure height
	editorPreviewNeckPct       = 34 // neck width as a percent of the shoulder width
	editorPreviewShoulderYPct  = 34 // where the shoulders start, percent of figure height
	editorPreviewFigureBandCnt = 14 // horizontal bands the torso taper is drawn from (BOUNDED: rule 4)

	// editorPreviewFigureAlpha is the silhouette's opacity: reads as a body, not mistaken for theme art.
	editorPreviewFigureAlpha = 190
)

// editorPreview is the demo's whole state, allocated by enterEditorPreview and released by exitEditorPreview.
type editorPreview struct {
	// state is the demo's own session: whole, because half a swap leaks a view-only pass into the live one.
	state sessionState

	// sel and selN are the selection when Preview was pressed, restored on the way out.
	sel  [themeSelCap]editTarget
	selN int

	// wasOffline is the manager's offline flag as found, so a preview inside rehearsal leaves rehearsal offline.
	wasOffline bool

	// rest counts down between plays in the demo's own dt, so a slowed frame lengthens the pause instead of skipping
	// it.
	rest time.Duration
}

// editorPreviewOn reports the demo being up.
func (a *App) editorPreviewOn() bool { return a.te != nil && a.te.preview != nil }

// enterEditorPreview stages the demo; the only place editorPreview is constructed.
// Panels, the drag and the selection go first: a gesture surviving under the demo would resume against unseen pixels.
func (a *App) enterEditorPreview() {
	if a.te == nil || a.te.preview != nil || a.d.Manager == nil {
		return
	}
	a.closeEditorPanels()
	p := &editorPreview{sel: a.te.sel, selN: a.te.selN}
	a.te.selectClear()
	a.te.drag = editGesture{}
	a.te.peek = false

	// Prefetch fan-out is unconditional, so offline is asserted here, saved so exit restores what it found.
	p.wasOffline = a.d.Manager.Offline()
	a.d.Manager.SetOffline(true)

	// NewRehearsalSession is the offline session: Ready, no transport, no characters, discarded sends.
	// Origin "" names no host.
	p.state.sess = courtroom.NewRehearsalSession("", nil)
	p.state.rehearsal = true
	p.state.urls = courtroom.NewURLBuilder("")
	p.state.serverName = editorPreviewShowname
	// A viewing room under the author's own preferences, so ReduceMotion still reaches it.
	// A nil audio sink becomes NopAudio: blip cadence runs as live, while nothing fetches a sound.
	p.state.room = a.newRoom(p.state.urls, nil, nil)
	a.te.preview = p

	// The scrollback and first beats use the real append and event path, with the demo session installed.
	saved := a.beginEditorPreviewPass()
	for _, e := range editorPreviewLog {
		a.pushIC(e.line, 0, false, -1, e.speaker)
	}
	a.endEditorPreviewPass(saved)
	a.startEditorPreviewBeat()

	a.bindViewportToPreview()
	a.uiDirty = true
}

// exitEditorPreview leaves the demo, restoring the selection, viewport callbacks and the manager's offline flag.
func (a *App) exitEditorPreview() {
	if a.te == nil || a.te.preview == nil {
		return
	}
	p := a.te.preview
	if a.d.Manager != nil {
		a.d.Manager.SetOffline(p.wasOffline)
	}
	// Free the demo's own chat textures: the pass parks them in p.state, so dropping the pointer would leak them per
	// press.
	// clearSplit releases the pinned tab's parked state for the same reason.
	if p.state.msRaster != nil {
		p.state.msRaster.Destroy()
		p.state.msRaster = nil
	}
	if p.state.glyphCache != nil {
		p.state.glyphCache.Purge() // the white-glyph textures the effects path uploads
		p.state.glyphCache = nil
	}
	a.te.sel, a.te.selN = p.sel, p.selN
	a.te.preview = nil
	// Back to the live room: a viewport pointed at a room nobody holds delivers preanim-done to a dead scene.
	if a.d.Viewport != nil {
		if a.room != nil {
			a.d.Viewport.OnPreanimDone = a.room.NotifyPreanimDone
			a.d.Viewport.OnFrameShown = a.room.NotifyFrameShown
		} else {
			a.d.Viewport.OnPreanimDone = nil
			a.d.Viewport.OnFrameShown = nil
		}
	}
	a.uiDirty = true
}

// toggleEditorPreview is what the button presses, so caption and act cannot disagree.
func (a *App) toggleEditorPreview() {
	if a.editorPreviewOn() {
		a.exitEditorPreview()
		return
	}
	a.enterEditorPreview()
}

// bindViewportToPreview points the viewport's one-shot callbacks at the demo room.
// Reasserted every driven frame, because a replay's start and stopReplay rebind them.
func (a *App) bindViewportToPreview() {
	if a.d.Viewport == nil || !a.editorPreviewOn() {
		return
	}
	room := a.te.preview.state.room
	a.d.Viewport.OnPreanimDone = room.NotifyPreanimDone
	a.d.Viewport.OnFrameShown = room.NotifyFrameShown // #17: frame effects follow the demo's sprite
}

// startEditorPreviewBeat feeds the opening beats through room.HandleEvent, so the real state machine stages the demo.
func (a *App) startEditorPreviewBeat() {
	if !a.editorPreviewOn() {
		return
	}
	room := a.te.preview.state.room
	room.HandleEvent(courtroom.Event{Kind: courtroom.EventMusic, Text: editorPreviewTrack, Int: -1})
	a.replayEditorPreviewLine()
}

// replayEditorPreviewLine stages the sample message, a fresh value each time so the room cannot alias it.
func (a *App) replayEditorPreviewLine() {
	if !a.editorPreviewOn() {
		return
	}
	a.te.preview.rest = editorPreviewRest
	a.te.preview.state.room.HandleEvent(courtroom.Event{
		Kind: courtroom.EventMessage,
		Message: &protocol.ChatMessage{
			CharName: editorPreviewChar,
			Showname: editorPreviewShowname,
			Emote:    editorPreviewEmote,
			Message:  editorPreviewLine,
			EmoteMod: protocol.EmoteModIdle,
			DeskMod:  protocol.DeskShow, // stand behind the desk: the framing a theme is judged in
		},
	})
}

// driveEditorPreview advances the demo one frame, so the shared viewport tracks the room on screen.
// Reduce motion freezes it entirely; the frame still shows the staged message, plate and banner.
// NoteAnimating holds the active frame cap on a screen the pacer otherwise parks, and clears itself on leaving.
func (a *App) driveEditorPreview(dt time.Duration) {
	if !a.editorPreviewOn() {
		return
	}
	p := a.te.preview
	room := p.state.room
	if a.reduceMotionNow() {
		return // frozen: no clock, no loop, no census
	}
	a.bindViewportToPreview()
	if a.d.Viewport != nil {
		a.d.Viewport.SetSpriteFX(a.spriteFX())
	}
	room.Update(dt)
	if a.d.Viewport != nil {
		a.d.Viewport.Update(&room.Scene, dt)
	}
	// Loop: the rest counts only while idle, so it is measured from when the line settled.
	if room.Phase() == courtroom.PhaseIdle && room.QueueLen() == 0 {
		p.rest -= dt
		if p.rest <= 0 {
			a.replayEditorPreviewLine()
		}
	} else {
		p.rest = editorPreviewRest
	}
	a.NoteAnimating()
}

// editorPreviewSaved is what the demo pass borrows and gives back, carried by value between the two halves.
type editorPreviewSaved struct {
	state     sessionState
	screen    Screen
	tip       string
	seqLen    int
	in        ctxInput
	prevPinne bool
}

// beginEditorPreviewPass installs the demo session and neutralizes input; always pair it with endEditorPreviewPass.
// Same swap as renderFullClientTexture minus the render target.
// The screen is saved so a view-only pass cannot navigate.
// No key or click reaches the pass, so nothing in it can mutate the document.
// pinnedPass is raised because drawCourtroom drains single-consumer channels (pollCharINI) the primary session needs.
func (a *App) beginEditorPreviewPass() editorPreviewSaved {
	c := a.ctx
	saved := editorPreviewSaved{
		state:     a.sessionState,
		screen:    a.screen,
		tip:       c.tipText,
		seqLen:    len(c.fieldSeq),
		prevPinne: a.pinnedPass,
	}
	saved.in = a.snapshotInput()
	a.sessionState = a.te.preview.state
	a.pinnedPass = true
	return saved
}

// endEditorPreviewPass keeps the pass's side effects on the preview and restores the live session.
func (a *App) endEditorPreviewPass(saved editorPreviewSaved) {
	c := a.ctx
	if a.te != nil && a.te.preview != nil {
		a.te.preview.state = a.sessionState
	}
	a.pinnedPass = saved.prevPinne
	a.sessionState = saved.state
	a.screen = saved.screen
	c.tipText = saved.tip
	if len(c.fieldSeq) > saved.seqLen {
		c.fieldSeq = c.fieldSeq[:saved.seqLen] // drop the demo's field ids from Tab-cycle
	}
	a.restoreInput(saved.in)
}

// drawEditorPreviewScreen is the whole screen while the demo is up; editing chrome is hidden by not being called.
func (a *App) drawEditorPreviewScreen(w, h int32) {
	// The art plan is still reconciled so the demo shows the current document; settled, this is one hash and a compare.
	a.editorSyncArt()
	a.drawEditorPreviewCanvas(w, h)
	a.drawEditorPreviewExit(w)
}

// drawEditorPreviewCanvas draws the demo through the REAL courtroom pass.
func (a *App) drawEditorPreviewCanvas(w, h int32) {
	saved := a.beginEditorPreviewPass()
	defer a.endEditorPreviewPass(saved)
	a.drawCourtroom(w, h)
}

// drawEditorPreviewExit draws the way back at the header's Preview position, claiming the band so the menu bar stands
// down.
func (a *App) drawEditorPreviewExit(w int32) {
	c := a.ctx
	a.noteEditorBanner()
	r := editorPreviewBtnRect(w)
	// Its own plate: there is no band behind it, and it must stay legible over any theme.
	c.Fill(r, ColPanel)
	if c.Button(r, editorPreviewExitLabel) {
		a.exitEditorPreview()
	}
}

// editorPreviewBtnRect is the Preview button's box, right-aligned on its standard position so other buttons stay put.
func editorPreviewBtnRect(w int32) sdl.Rect {
	slotX := w - editRowPad - editorHeaderBtnW -
		editorPreviewSlotFromRight*(editorHeaderBtnW+editorHeaderBtnGap)
	return sdl.Rect{
		X: slotX + editorHeaderBtnW - editorPreviewBtnW,
		Y: 2,
		W: editorPreviewBtnW,
		H: editBannerH - 4,
	}
}

// drawEditorPreviewFigure paints the procedural stand-in from rectangles, because the courtroom frame must not
// allocate.
// Called from renderViewportZoomed beside drawStageEgg, where both courtroom layouts hand over the stage rect.
func (a *App) drawEditorPreviewFigure(vp sdl.Rect) {
	if !a.editorPreviewOn() || vp.W <= 0 || vp.H <= 0 {
		return
	}
	c := a.ctx
	ink := ColPanelHi
	ink.A = editorPreviewFigureAlpha

	figH := vp.H * editorPreviewFigureHPct / 100
	figW := vp.W * editorPreviewFigureWPct / 100
	if figH <= 0 || figW <= 0 {
		return
	}
	cx := vp.X + vp.W/2
	bottom := vp.Y + vp.H
	top := bottom - figH

	headH := figH * editorPreviewHeadPct / 100
	if headH > 0 {
		// The inset top band makes the head read as a head, with no circle rasteriser.
		crown := headH / 4
		c.Fill(sdl.Rect{X: cx - headH/3, Y: top, W: headH * 2 / 3, H: crown}, ink)
		c.Fill(sdl.Rect{X: cx - headH/2, Y: top + crown, W: headH, H: headH - crown}, ink)
	}
	neckW := figW * editorPreviewNeckPct / 100
	shoulderY := top + figH*editorPreviewShoulderYPct/100
	if shoulderY > top+headH && neckW > 0 {
		c.Fill(sdl.Rect{X: cx - neckW/2, Y: top + headH, W: neckW, H: shoulderY - top - headH}, ink)
	}
	// The torso: a fixed number of bands widening from neck to shoulders, then holding.
	bandH := (bottom - shoulderY) / editorPreviewFigureBandCnt
	if bandH <= 0 {
		bandH = 1
	}
	for i := int32(0); i < editorPreviewFigureBandCnt; i++ {
		y := shoulderY + i*bandH
		if y >= bottom {
			break
		}
		w := neckW + (figW-neckW)*min(i+1, editorPreviewFigureBandCnt/3)/(editorPreviewFigureBandCnt/3)
		h := bandH
		if y+h > bottom {
			h = bottom - y
		}
		c.Fill(sdl.Rect{X: cx - w/2, Y: y, W: w, H: h}, ink)
	}
}
