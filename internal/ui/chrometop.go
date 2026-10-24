package ui

// The reserved TOP BAND of app-wide client chrome (#14).
// tab strip FLOATED over the stage — which is exactly what issue #14 reported: on a
// PERF. topChromeH is called from every screen draw (all of them under an
// AllocsPerRun gate) and from the two floating chips, so it must stay pure integer
// arithmetic over already-latched state: a length check and one probe of the

// topChromeH is the height of the app-wide client-chrome band at the top of the
// window — THE reserved top offset every screen offsets its content by, and the only
// place the two strip heights are added together.
//
// It is safe to apply unconditionally: menuBarHeight() already reports 0 for the
// modes that take the whole window instead of a screen (gif export, replay, scene
// maker, theater), and tabStripBandH() reports 0 whenever the strip is not docked in
// the band. A screen therefore never has to ask WHICH chrome is showing.
func (a *App) topChromeH() int32 { return a.menuBarHeight() + a.tabStripBandH() }

// screenDispatchPreempted reports whether a FULL-WINDOW mode has taken this frame
// instead of a screen — App.Frame's gif > replay > maker precedence, checked ahead
// of the `switch a.screen` that draws the lobby / char select / courtroom / menus.
// Named and shared because two separate rules need exactly this set and neither can
// afford to re-spell it: the menu bar neither paints nor reserves its band in these
// modes (menuBarShows), and the server-tab strip must not read the themed courtroom's
func (a *App) screenDispatchPreempted() bool {
	return a.sel.gifExporting || (a.sel.replaying && a.sel.replayRoom != nil) || a.sel.makerOpen
}

// tabStripPaintsUnderEditor reports whether the server-tab strip's paint belongs to
// the courtroom pass this frame — beneath a layout editor's overlay — instead of to
// App.Frame's over-everything site.
// opaque and, once docked (#14), it sits in the top 22 px of the window — precisely
// The classic editor already worked this way; the themed one did not, because the
// over-everything site was gated on classicEdit alone. The predicate is deliberately
// NOT just layoutEditorArmed(): it must be true only on frames where an editor's draw
// That question — "does an armed editor's own draw reach the screen this frame" — is
// editorDrawSiteRuns (layoutnudge.go), where the arrow-key nudge needs the same answer
// before it may claim a key. ONE copy of the conjunction, so the strip's paint and the
func (a *App) tabStripPaintsUnderEditor() bool { return a.editorDrawSiteRuns() }

// tabStripPaintLatch is THIS FRAME's single answer to WHICH of the strip's two draw
// sites owns the paint — the compactToolboxLatch idea applied to the same class of
// problem, and for the same reason: the two sites run at different points of the
// frame, state changes between them, and re-deriving the predicate at each one lets
// them disagree.
// Both failure modes were live before the latch, because the in-pass sites tested the
// editor flags while App.Frame's site re-tested the predicate AFTER the whole screen
// dispatch:
// The latch is taken at the TOP of the courtroom pass, before anything can disarm or
// arm an editor mid-frame, and BOTH sites replay it verbatim: a mid-pass change moves
// the paint to the next frame instead of duplicating or dropping it. It is also what
type tabStripPaintLatch struct {
	// set marks that a courtroom pass took the decision this frame. Frames with no
	// courtroom pass (lobby, char select, gif export, replay, scene maker) leave it
	// false, which reads as "App.Frame's over-everything site owns the paint" — the
	// right answer for exactly those frames.
	set bool
	// inPass is "the courtroom pass paints the strip, under a layout editor's overlay".
	inPass bool
}

// latchTabStripPaint takes that decision. Called once, at the top of the courtroom
// pass.
func (a *App) latchTabStripPaint() {
	a.toolbox.tabStripPaint = tabStripPaintLatch{set: true, inPass: a.tabStripPaintsUnderEditor()}
}

// drawTabBarInPass is the courtroom pass's strip site, REPLAYING the latch rather than
// re-reading the editor flags. Called unconditionally from both passes.
func (a *App) drawTabBarInPass(w, h int32) {
	if a.toolbox.tabStripPaint.inPass {
		a.drawTabBar(w, h)
	}
}

// tabStripPaintsOverEverything is App.Frame's site, replaying the SAME latch. The two
// are exact complements, so exactly one paints per frame.
func (toolbox1 *toolboxState) tabStripPaintsOverEverything() bool {
	return !toolbox1.tabStripPaint.inPass
}

// tabStripBandH is the vertical band the DOCKED server-tab strip owns, directly
// under the menu bar. Zero in the two cases where the strip is not in the band:
// That check would have to read a.themeLay — which themeLayoutIn is in the middle of
// rebuilding from this very number — and a rect that survives the
// minThemedElementPx floor at one band height but not the other would flip the band
func (a *App) tabStripBandH() int32 {
	if len(a.tabs) == 0 {
		return 0
	}
	if _, moved := a.classic.classicOv[slotTabBar]; moved {
		return 0
	}
	return tabBarH
}
