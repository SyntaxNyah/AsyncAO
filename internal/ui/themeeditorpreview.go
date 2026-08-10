package ui

// THE THEME EDITOR'S PREVIEW — the canned offline demo (v1.90.0, field batch 9).
//
// ONE CONCERN: the editor's PREVIEW VIEW STATE. Entering it, driving it, drawing
// it, leaving it. The document it previews lives in themeeditordoc.go, the screen
// around it in themeeditor.go; nothing here mutates a theme.
//
// WHY A VIEW STATE AND NOT A TOGGLE. The editor already has a Live/Frozen toggle
// (themeEditor.livePaused), and it is a FREEZE SWITCH for canvas re-baking — its
// whole value is that the off state does LESS work (editorLiveOn's own note). A
// demo player is the opposite thing: it does MORE work, it owns the whole screen,
// and it has to be leavable with Esc. Folding a player into a freeze switch would
// give one control two meanings and leave "frozen" ambiguous between "stop
// re-baking" and "stop the demo". So the toggle is untouched and Preview is a
// distinct state, the pointer IS the flag (the shape `gallery` and `report`
// already use), and a session that never presses it pays one nil pointer.
//
// WHAT IT PLAYS, AND WHY THAT IS THE REAL THING. The demo is not a mock renderer.
// It builds its OWN offline courtroom through App.newRoom — the one sanctioned
// construction site (newroom.go) — feeds it synthetic courtroom.Event values, and
// then draws the REAL drawCourtroom pass over it with the demo session temporarily
// installed in the App's embedded slots. That is renderFullClientTexture's own
// pattern (app.go), for the same reason: the kit is single-instance and the
// courtroom reads a.room / a.sess / a.icLog through promoted fields, so the only
// honest way to render a second session is to be that session for the duration and
// put everything back. What the author sees is therefore the real typewriter, the
// real chatbox painter, the real blip cadence, the real showname plate, the real
// music marquee and the real IC log — with their real theme applied.
//
// FULLY OFFLINE, BY TWO INDEPENDENT MEANS. First, the scene carries no fetchable
// content: no character, no background, no desk art, no music playback, and a nil
// audio sink (NewCourtroom substitutes NopAudio). Second — because
// courtroom.stageMessage's prefetch fan-out is unconditional and URLBuilder always
// mints a URL — the demo brackets the manager's own single structural egress point
// (assets.Manager.SetOffline, netFetch's gate, the same one rehearsal mode uses)
// for exactly the demo's lifetime, saving and restoring whatever it found. The
// live courtroom is not drawing while the demo is up, so nothing else is demanding
// assets, and TestEditorPreviewProbesNothing counts source-level probes at zero
// across a driven demo.
//
// THE CHARACTER IS PROCEDURAL. A demo that shipped bundled sprite art would be a
// licence problem and a byte problem both; a demo with an empty stage would not
// show an author where their chatbox sits relative to a body. So the stage draws a
// silhouette out of rectangles, beside drawStageEgg in the one place both the
// classic and the themed courtroom render the viewport (vpzoom.go) — which is how
// it gets the stage rect for free in both layouts.
//
// Render thread only, like every other App method here.

import (
	"time"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
)

// ---------------------------------------------------------------------------
// Named constants (hard rule 9): the button, the script, the silhouette
// ---------------------------------------------------------------------------

const (
	// editorPreviewBtnW is the Preview button's width: TWO header buttons and the
	// gap between them. The brief asks for a prominent control rather than a small
	// toggle, and "twice the others" is the widest this band can carry without
	// reaching the theme-name column (see editorPreviewBtnRect's arithmetic).
	editorPreviewBtnW = editorHeaderBtnW*2 + editorHeaderBtnGap

	// editorPreviewSlotFromRight is the Preview button's SLOT in the header row,
	// counting from the right edge: Back(0) Save(1) Redo(2) Undo(3) Fonts(4)
	// Gallery(5) Share(6) — so Preview is 7, the leftmost, immediately beside
	// Share. Adding it at the LEFT end is what keeps every existing slot index
	// (and every gate that presses one) exactly where it was.
	editorPreviewSlotFromRight = 7

	// editorPreviewEnterLabel / editorPreviewExitLabel are the button's two
	// captions. Package constants rather than a formatted string, for the reason
	// editorLiveLabel gives: the label cache keys on the string, so a value built
	// per frame would rasterise a fresh texture sixty times a second.
	editorPreviewEnterLabel = "▶ Preview theme"
	editorPreviewExitLabel  = "◼ Exit preview (Esc)"
)

const (
	// editorPreviewShowname is the demo speaker's showname — what fills the
	// showname plate. Short enough to fit AO2's stock name box at every theme
	// scale, so an author sees the plate FULL rather than clipped.
	editorPreviewShowname = "Preview"

	// editorPreviewChar is the demo speaker's character folder, and it is EMPTY on
	// purpose: an empty character is what keeps a real sprite off the stage (the
	// silhouette draws instead), and it is the first of the two independent
	// guarantees this demo is offline. The manager bracket is the second.
	editorPreviewChar = ""

	// editorPreviewEmote is the emote name the demo message carries. Present so
	// the message is a well-formed MS the room stages exactly like a live one —
	// with no character folder in front of it, it names nothing that can be
	// fetched.
	editorPreviewEmote = "normal"

	// editorPreviewLine is the sample message. Long enough that the typewriter
	// actually CRAWLS (a three-word line settles before an author's eye reaches
	// the chatbox) and that it wraps onto a second row on a stock-width chatbox,
	// which is where line pitch and the crawl-scroll are judged.
	editorPreviewLine = "This is how your theme looks while somebody is talking — " +
		"the chatbox, the name plate, the crawl and the blips, all at play speed."

	// editorPreviewTrack is the demo's now-playing title. DELIBERATELY LONG: the
	// music banner only scrolls when the title is wider than its plate
	// (marqueeScrolls, AO2's scrollEnabled), so a short title would show the
	// author a marquee that never moves. This one is wider than the stock 224 px
	// music_name at every sane face size.
	//
	// IT CARRIES ITS EXTENSION because AO music track names do: a name with no
	// audio extension is an AREA TRANSFER on the unified music/area list, not a
	// song (isAreaTransfer, courtroom.go:830), and the room would drop it. The
	// banner strips it again for display (MusicDisplayName).
	editorPreviewTrack = "Pursuit ~ Cornered (Theme Preview Extended Orchestral Arrangement).opus"

	// editorPreviewRest is how long a settled message rests before the demo plays
	// it again. Long enough to read the settled chatbox, short enough that an
	// author who looks up has not missed the crawl.
	editorPreviewRest = 2500 * time.Millisecond
)

// editorPreviewLogEntry is one canned IC scrollback row: the line as the log
// renders it, and the speaker the name column tints from.
type editorPreviewLogEntry struct {
	line    string
	speaker string
}

// editorPreviewLog is the canned scrollback. A FIXED array, not a slice built per
// entry (hard rule 4): the demo seeds it exactly once, through the same pushIC the
// live client appends with, so the rows wrap, tint and stamp identically.
var editorPreviewLog = [...]editorPreviewLogEntry{
	{"Judge: Court is now in session.", "Judge"},
	{"Preview: Your theme is what everybody else sees.", "Preview"},
	{"Witness: …and the log wraps exactly like this.", "Witness"},
}

const (
	// The procedural silhouette, in PERCENT of the stage rect. Percentages rather
	// than pixels because the stage is 4:3 at whatever the window and the theme's
	// fit make it, and a demo that only looked right at one size would be worse
	// than no demo. Chosen to sit where an AO sprite sits: feet on the stage floor,
	// head clear of the chatbox band.
	editorPreviewFigureHPct    = 62 // figure height as a percent of the stage height
	editorPreviewFigureWPct    = 26 // shoulder width as a percent of the stage width
	editorPreviewHeadPct       = 26 // head height as a percent of the figure height
	editorPreviewNeckPct       = 34 // neck width as a percent of the shoulder width
	editorPreviewShoulderYPct  = 34 // where the shoulders start, percent of figure height
	editorPreviewFigureBandCnt = 14 // horizontal bands the torso taper is drawn from (BOUNDED: rule 4)

	// editorPreviewFigureAlpha is the silhouette's opacity. A stand-in, not a
	// character: solid enough to read as a body behind the desk, faint enough that
	// nobody mistakes it for art the theme ships.
	editorPreviewFigureAlpha = 190
)

// ---------------------------------------------------------------------------
// The state
// ---------------------------------------------------------------------------

// editorPreview is the demo's whole world. Allocated by enterEditorPreview and
// released by exitEditorPreview — the ONE door in and the ONE door out.
type editorPreview struct {
	// state is the demo's OWN session: its rehearsal Session, its offline room, its
	// URL builder and its IC scrollback. A whole sessionState rather than three
	// fields because that is the unit the App swaps (renderFullClientTexture), and
	// half a swap is how a view-only pass leaks into the live one.
	state sessionState

	// sel / selN is the selection the editor had when Preview was pressed. Restored
	// on the way out, because a preview that silently deselected your work would
	// make the button expensive to press.
	sel  [themeSelCap]editTarget
	selN int

	// wasOffline is the manager's network gate as it was found, restored on exit —
	// so a preview taken inside a rehearsal session leaves rehearsal offline.
	wasOffline bool

	// rest counts DOWN the pause between plays of the sample line, in the demo's
	// own dt rather than on the wall clock. Deterministic by construction: a
	// slowed or paused frame lengthens the pause instead of skipping it, exactly
	// like every other timing in the room, and a fixture with a pinned frame clock
	// can still drive the loop.
	rest time.Duration
}

// editorPreviewOn reports the demo being up. The pointer IS the flag.
func (a *App) editorPreviewOn() bool { return a.te != nil && a.te.preview != nil }

// ---------------------------------------------------------------------------
// The doors
// ---------------------------------------------------------------------------

// enterEditorPreview stages the demo. It is the ONLY place editorPreview is
// constructed — TestEditorPreviewHasOneConstructionSite fails the build gate on a
// second one, for the reason newroom.go gives about rooms: a state that can be
// born two ways will one day be born half-wired.
//
// Everything editing owns goes down first (panels, the live drag, the selection),
// because the demo covers the whole window and a gesture that survived under it
// would resume against pixels the user can no longer see.
func (a *App) enterEditorPreview() {
	if a.te == nil || a.te.preview != nil || a.d.Manager == nil {
		return
	}
	a.closeEditorPanels()
	p := &editorPreview{sel: a.te.sel, selN: a.te.selN}
	a.te.selectClear()
	a.te.drag = editGesture{}
	a.te.peek = false

	// THE NETWORK GATE, BRACKETED. See the file header: the room's prefetch fan-out
	// is unconditional, so "offline" is a property this state has to assert rather
	// than one the scene can express. Saved, not assumed, so exit restores what it
	// found (a rehearsal session is already offline and must stay so).
	p.wasOffline = a.d.Manager.Offline()
	a.d.Manager.SetOffline(true)

	// The demo's own session. NewRehearsalSession is the client's sanctioned
	// OFFLINE session (session.go) and it is exactly the right shape here: phase
	// Ready, no transport, an empty character list, a send func that discards.
	// Origin "" so nothing it mints can name a real host.
	p.state.sess = courtroom.NewRehearsalSession("", nil)
	p.state.rehearsal = true
	p.state.urls = courtroom.NewURLBuilder("")
	p.state.serverName = editorPreviewShowname
	// Construction IS wiring (newroom.go): the demo speaker blips, skins and flashes
	// exactly like a live one, and the room starts under the AUTHOR'S OWN viewing
	// preferences — which is the whole point. This is a VIEWING room, not an
	// authoring one: a theme is judged as a player will see it, so reduce-motion and
	// the effect gates must reach it (requirement 6 is that ReduceMotion freezes the
	// demo, not that the demo overrides it).
	//
	// A NIL AUDIO SINK: NewCourtroom substitutes NopAudio, so the blip CADENCE
	// (Typewriter.tickBlip) runs exactly as it does live while nothing resolves a
	// sound over the wire. An offline demo cannot make noise; it can keep time.
	p.state.room = a.newRoom(p.state.urls, nil, nil)
	a.te.preview = p

	// The scrollback and the first beats go through the REAL append and the REAL
	// event path, with the demo session installed — see editorPreviewPass.
	saved := a.beginEditorPreviewPass()
	for _, e := range editorPreviewLog {
		a.pushIC(e.line, 0, false, -1, e.speaker)
	}
	a.endEditorPreviewPass(saved)
	a.startEditorPreviewBeat()

	a.bindViewportToPreview()
	a.uiDirty = true
}

// exitEditorPreview leaves the demo and puts editing back exactly as it was: the
// selection, the viewport's callbacks and the manager's network gate.
//
// THE ONE DOOR OUT, and both affordances (the button and the Esc rung) call it, so
// "leaving restores everything" is a property of the exit rather than a rule two
// call sites have to remember.
func (a *App) exitEditorPreview() {
	if a.te == nil || a.te.preview == nil {
		return
	}
	p := a.te.preview
	if a.d.Manager != nil {
		a.d.Manager.SetOffline(p.wasOffline)
	}
	// THE DEMO'S OWN CHAT TEXTURES ARE FREED HERE, and this is the half a swap does
	// not do for you. The pass builds a chat raster (and, for an effect line, a glyph
	// cache) INTO the demo's sessionState — endEditorPreviewPass parks them back into
	// p.state — so when the pointer goes the App holds no reference to them any more
	// and their sdl.Textures (MessageRaster.lines/.styled) would leak, once per press,
	// against a 256 MiB budget. clearSplit does exactly this for the pinned tab's
	// parked state and for exactly this reason; the swap was copied from there, and
	// the release belongs with it.
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
	// THE VIEWPORT GOES BACK TO THE LIVE ROOM. teardownMakerPreview's pattern and
	// its reason: a replay's stopReplay and the maker both rebind these, and a
	// viewport still pointed at a room nobody holds delivers a preanim-done to a
	// dead scene.
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

// toggleEditorPreview is what the button presses. One function so the caption and
// the act cannot disagree about which state they are in.
func (a *App) toggleEditorPreview() {
	if a.editorPreviewOn() {
		a.exitEditorPreview()
		return
	}
	a.enterEditorPreview()
}

// bindViewportToPreview points the shared viewport's one-shot callbacks at the
// demo room. Re-asserted every driven frame (driveEditorPreview) rather than bound
// once, because a replay's start and stopReplay both rebind them behind our back —
// the same re-assert driveMakerPreview makes, for the same reason.
func (a *App) bindViewportToPreview() {
	if a.d.Viewport == nil || !a.editorPreviewOn() {
		return
	}
	room := a.te.preview.state.room
	a.d.Viewport.OnPreanimDone = room.NotifyPreanimDone
	a.d.Viewport.OnFrameShown = room.NotifyFrameShown // #17: frame effects follow the demo's sprite
}

// ---------------------------------------------------------------------------
// The script
// ---------------------------------------------------------------------------

// startEditorPreviewBeat feeds the canned scene's opening beats into the demo
// room: the music change (which fills the banner and starts the marquee) and the
// sample message (which starts the typewriter, the blips and the showname plate).
//
// Through room.HandleEvent, the same door a packet comes in by, so the demo is
// staged by the production state machine rather than by writing Scene fields.
func (a *App) startEditorPreviewBeat() {
	if !a.editorPreviewOn() {
		return
	}
	room := a.te.preview.state.room
	room.HandleEvent(courtroom.Event{Kind: courtroom.EventMusic, Text: editorPreviewTrack, Int: -1})
	a.replayEditorPreviewLine()
}

// replayEditorPreviewLine stages the sample message. A FRESH message value each
// time (never a shared pointer the room could alias) — cloneEvent's discipline in
// the scene maker, for the same reason.
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

// driveEditorPreview advances the demo one frame. Called from App.Frame's scene
// switch, beside driveMakerPreview and for the same reason: the shared viewport
// must track the room that is on screen, not the live one behind it.
//
// REDUCE MOTION FREEZES IT. Not "plays it slower" — the whole point of the setting
// is that nothing moves on its own, and this is a self-driven animation the user
// did not ask for. The frozen frame still shows the staged message, the filled
// plate and the banner, so the theme is still previewable; it simply holds still.
// reduceMotionNow is the app's ONE spelling of that answer (themeclock.go), which
// is what keeps the demo frozen by the same switch the themed chrome freezes on.
//
// IT RIDES THE PACING CENSUS. While it animates it calls NoteAnimating every
// frame, exactly like the marquee and the theme clock, so FramePace holds the
// active cap and the crawl is smooth on a screen the pacer otherwise parks
// (expScreenSkippable returns true for the editor). The census is RETROSPECTIVE
// and self-clearing, so leaving the demo releases it with no second call — which
// is precisely what TestEditorPreviewReleasesThePacingCensusOnExit pins.
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
	// THE LOOP. A one-shot demo is a demo you miss, so the settled line rests and
	// plays again. The countdown runs only while the room is IDLE, so the pause is
	// measured from the moment the line settled rather than from when it started —
	// a slower Playback speed lengthens the crawl without shortening the rest.
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

// ---------------------------------------------------------------------------
// The pass — being the demo session for the duration of a draw
// ---------------------------------------------------------------------------

// editorPreviewSaved is everything the demo pass borrows and gives back. A VALUE
// carried between the two halves, so the pairing is visible at the call site and a
// missing restore does not compile.
type editorPreviewSaved struct {
	state     sessionState
	screen    Screen
	tip       string
	seqLen    int
	in        ctxInput
	prevPinne bool
}

// beginEditorPreviewPass installs the demo session in the App's embedded slots and
// neutralizes input. ALWAYS paired with endEditorPreviewPass, normally by a defer
// on the next line.
//
// It is renderFullClientTexture's swap (app.go), minus the render target, and the
// list of what it borrows is the same list for the same reasons: the session state
// (which promotes a.room / a.sess / a.icLog / the chat raster), the screen (a
// view-only pass must not navigate — drawCourtroom sends a session-less app to the
// lobby), the tooltip, the Tab-focus order, and every input signal.
//
// INPUT IS NEUTRALIZED, and that is a FEATURE and not only a safety rail: the brief
// asks for "buttons at rest", and a demo whose widgets lit up under the pointer
// would be inviting clicks that cannot mean anything. It is also what makes
// requirement 9 structural rather than careful — nothing inside this pass can see a
// key or a click, so nothing inside it can mutate the theme document.
//
// pinnedPass is raised for the same reason the pinned client raises it: drawCourtroom
// drains single-consumer App channels (pollCharINI), and a view-only pass must not
// eat a result the primary session asked for.
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

// endEditorPreviewPass captures the demo pass's own side effects back onto the
// preview (its scroll, its raster cache) and restores the live session untouched.
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

// ---------------------------------------------------------------------------
// Draw
// ---------------------------------------------------------------------------

// drawEditorPreviewScreen is the whole editor screen while the demo is up: the
// full-bleed courtroom and ONE way back. No rails, no handles, no selection
// outlines, no magnet guides, no panels — that is the requirement, and it is met
// by NOT CALLING them rather than by asking each of them to hide itself, which is
// the only version that stays true when the next overlay is added.
func (a *App) drawEditorPreviewScreen(w, h int32) {
	// The art plan is still reconciled: the demo has to show the theme as the
	// document holds it right now, and this is the settled path (one hash and a
	// compare — themeeditorart.go), not a re-bake.
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

// drawEditorPreviewExit draws the one door back, in the header band's own Preview
// slot so the control does not jump between the two states.
//
// The band is claimed (noteEditorBanner) exactly as drawEditorHeader claims it —
// without that the menu bar paints an opaque strip straight over this button, and
// then the only way out is the keyboard.
func (a *App) drawEditorPreviewExit(w int32) {
	c := a.ctx
	a.noteEditorBanner()
	r := editorPreviewBtnRect(w)
	// Its own plate, because there is no header band behind it any more: the button
	// floats over whatever the theme painted there and has to stay legible on a
	// white chatbox and a black stage alike.
	c.Fill(r, ColPanel)
	if c.Button(r, editorPreviewExitLabel) {
		a.exitEditorPreview()
	}
}

// editorPreviewBtnRect is the Preview button's box in the header band — ONE
// publisher, so the enter door, the exit door and every gate that presses either
// are the same pixels.
//
// The wide button is RIGHT-ALIGNED on the standard slot it occupies, so the seven
// buttons to its right keep their existing positions (and the gates that count
// slots from the right keep pressing what they always pressed).
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

// drawEditorPreviewFigure paints the procedural stand-in on the stage: a head, a
// neck and a tapering torso, built from rectangles.
//
// RECTANGLES AND NOTHING ELSE, deliberately. It draws inside the courtroom frame,
// which every whole-screen alloc gate covers at ZERO allocations per frame, so a
// texture, a surface or a per-frame slice here would be a regression in the one
// place this package measures hardest. A bounded band count (rule 4) taper is the
// most body-shaped thing a fixed number of Fill calls can be.
//
// Called from renderViewportZoomed beside drawStageEgg — the one place both the
// classic and the themed courtroom hand over the stage rect.
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
		// The head reads as a head because its top band is inset — two rects, no
		// circle rasteriser, no per-frame geometry.
		crown := headH / 4
		c.Fill(sdl.Rect{X: cx - headH/3, Y: top, W: headH * 2 / 3, H: crown}, ink)
		c.Fill(sdl.Rect{X: cx - headH/2, Y: top + crown, W: headH, H: headH - crown}, ink)
	}
	neckW := figW * editorPreviewNeckPct / 100
	shoulderY := top + figH*editorPreviewShoulderYPct/100
	if shoulderY > top+headH && neckW > 0 {
		c.Fill(sdl.Rect{X: cx - neckW/2, Y: top + headH, W: neckW, H: shoulderY - top - headH}, ink)
	}
	// The torso: a fixed number of bands widening from the neck to the shoulders and
	// then holding, which is a silhouette rather than a triangle.
	bandH := (bottom - shoulderY) / editorPreviewFigureBandCnt
	if bandH <= 0 {
		bandH = 1
	}
	for i := int32(0); i < editorPreviewFigureBandCnt; i++ {
		y := shoulderY + i*bandH
		if y >= bottom {
			break
		}
		w := neckW + (figW-neckW)*minInt32(i+1, editorPreviewFigureBandCnt/3)/(editorPreviewFigureBandCnt/3)
		h := bandH
		if y+h > bottom {
			h = bottom - y
		}
		c.Fill(sdl.Rect{X: cx - w/2, Y: y, W: w, H: h}, ink)
	}
}

// minInt32 is the taper's clamp. Named rather than inlined because the expression
// it sits inside is already an interpolation and a second `if` in there is where a
// polarity slip hides.
func minInt32(a, b int32) int32 {
	if a < b {
		return a
	}
	return b
}
