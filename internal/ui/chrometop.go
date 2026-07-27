package ui

// The reserved TOP BAND of app-wide client chrome (#14).
//
// Two strips live above every screen: the menu bar (menubar.go) and, docked
// directly beneath it, the server-tab strip (tabs.go). Before this file existed the
// tab strip FLOATED over the stage — which is exactly what issue #14 reported: on a
// themed courtroom the strip sat left-of-centre ON TOP of the IC log. Three separate
// defects, all real:
//
//   - The strip could not be moved under a theme. openLayoutEditor routes to the
//     THEMED editor whenever the theme layout is valid and enabled (layoutedit.go);
//     the themed editor's editable key set is built from the theme layout cache
//     alone, and "tabbar" is not a theme key; slotRect only registers a draggable
//     slot while classicEdit is true (classiclayout.go); and drawCourtroom
//     force-stops the classic editor on a themed layout (screens.go). So on a themed
//     screen the strip was literally undraggable.
//   - It clashed with the theme: plain kit chrome painted over theme art.
//   - It did not centre itself — tabStripDefaultX used to centre the strip in the gap
//     LEFT of the docked log column, which parks it off to one side.
//
// The fix is structural: the strip DOCKS in a band that belongs to it, every screen
// starts its content BELOW that band, and the band is measured in exactly one place —
// here. No screen may hardcode menuBarH or tabBarH again.
//
// App.dockLeftX went with it. It cached the docked log column's left edge so
// tabStripDefaultX could centre the strip in the gap beside it — the whole reason the
// default sat off to one side. With the strip in a band of its own, no X can put it
// on top of the dock tabs, so the cache had nothing left to answer.
//
// PERF. topChromeH is called from every screen draw (all of them under an
// AllocsPerRun gate) and from the two floating chips, so it must stay pure integer
// arithmetic over already-latched state: a length check and one probe of the
// lock-free classicOv override map, both alloc-free.

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
//
// Named and shared because two separate rules need exactly this set and neither can
// afford to re-spell it: the menu bar neither paints nor reserves its band in these
// modes (menuBarShows), and the server-tab strip must not read the themed courtroom's
// layout cache in them (tabStripThemeRect). Both used to test a.screen == Courtroom,
// which is STILL TRUE during an export or a replay — a.screen is not cleared, the
// mode simply preempts the dispatch.
func (a *App) screenDispatchPreempted() bool {
	return a.gifExporting || (a.replaying && a.replayRoom != nil) || a.makerOpen
}

// tabStripPaintsUnderEditor reports whether the server-tab strip's paint belongs to
// the courtroom pass this frame — beneath a layout editor's overlay — instead of to
// App.Frame's over-everything site.
//
// There are exactly two draw sites and exactly one may run per frame. The strip is
// opaque and, once docked (#14), it sits in the top 22 px of the window — precisely
// where BOTH editors put their banner and their Done / Reset all / Snap / Magnet /
// Profile row. Measured at 1152x864 with one session, a chip at {545,22,28,22} covered
// 4 px of a Y=0..26 banner and the bottom 2 px of the button row, and a wide strip
// marches its chips straight through the Snap/Reset/Done cluster. Painting the strip
// FIRST, inside the pass, puts the editor's chrome back on top. (Input is unaffected:
// both editors hit-test with raw pointIn, which no fence covers.)
//
// The classic editor already worked this way; the themed one did not, because the
// over-everything site was gated on classicEdit alone. The predicate is deliberately
// NOT just layoutEditorArmed(): it must be true only on frames where an editor's draw
// site actually RUNS, or the strip would vanish. drawCourtroom bails to the lobby with
// no room, returns early in theater, and App.Frame's gif/replay/maker modes preempt the
// whole screen dispatch — an editor flag can outlive any of those. Evaluated after the
// dispatch (both editors are force-stopped by the layout they do not belong to), so it
// and the two in-pass gates always agree.
//
// That question — "does an armed editor's own draw reach the screen this frame" — is
// editorDrawSiteRuns (layoutnudge.go), where the arrow-key nudge needs the same answer
// before it may claim a key. ONE copy of the conjunction, so the strip's paint and the
// editor's keyboard can never disagree about who owns the frame.
func (a *App) tabStripPaintsUnderEditor() bool { return a.editorDrawSiteRuns() }

// tabStripPaintLatch is THIS FRAME's single answer to WHICH of the strip's two draw
// sites owns the paint — the compactToolboxLatch idea applied to the same class of
// problem, and for the same reason: the two sites run at different points of the
// frame, state changes between them, and re-deriving the predicate at each one lets
// them disagree.
//
// Both failure modes were live before the latch, because the in-pass sites tested the
// editor flags while App.Frame's site re-tested the predicate AFTER the whole screen
// dispatch:
//
//   - DOUBLE PAINT on the dismiss frame. Done, Esc or a lost theme all disarm the
//     editor from INSIDE the pass, after the in-pass site has already painted the
//     strip — so App.Frame's gate re-opened and painted the same strip a second time,
//     the second one over the editor's own banner on the frame it was still up.
//   - ONE-FRAME LOSS on the arm frame. The command palette draws inside the courtroom
//     case, AFTER the in-pass site, so the frame that arms the editor there had
//     already passed the in-pass site while App.Frame's re-test now said "the editor
//     owns it" — and nobody painted the strip at all.
//
// The latch is taken at the TOP of the courtroom pass, before anything can disarm or
// arm an editor mid-frame, and BOTH sites replay it verbatim: a mid-pass change moves
// the paint to the next frame instead of duplicating or dropping it. It is also what
// keeps the in-pass site honest when drawCourtroom force-stops the editor that does
// not belong to the layout it dispatched to (screens.go) — the site replays "in pass",
// so the strip is still painted there even though the flag it used to read is gone.
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
	a.tabStripPaint = tabStripPaintLatch{set: true, inPass: a.tabStripPaintsUnderEditor()}
}

// drawTabBarInPass is the courtroom pass's strip site, REPLAYING the latch rather than
// re-reading the editor flags. Called unconditionally from both passes.
func (a *App) drawTabBarInPass(w, h int32) {
	if a.tabStripPaint.inPass {
		a.drawTabBar(w, h)
	}
}

// tabStripPaintsOverEverything is App.Frame's site, replaying the SAME latch. The two
// are exact complements, so exactly one paints per frame.
func (a *App) tabStripPaintsOverEverything() bool { return !a.tabStripPaint.inPass }

// tabStripBandH is the vertical band the DOCKED server-tab strip owns, directly
// under the menu bar. Zero in the two cases where the strip is not in the band:
//
//   - No sessions at all: tabBarRects returns nil and nothing is painted, so
//     reserving space for it would leave a dead gap on a first-run lobby.
//   - The user dragged it somewhere else in the layout editor (a slotTabBar
//     override): it floats where they put it, and the band it left behind belongs to
//     the screen again.
//
// Deliberately NOT zeroed for a theme that parks the strip via "asyncao_tabbar".
// That check would have to read a.themeLay — which themeLayoutIn is in the middle of
// rebuilding from this very number — and a rect that survives the
// minThemedElementPx floor at one band height but not the other would flip the band
// every frame, rebuilding the layout cache (and allocating) forever. Reserving 22 px
// a theme did not need only costs space; getting it wrong the other way re-creates
// the overlap this whole band exists to remove.
func (a *App) tabStripBandH() int32 {
	if len(a.tabs) == 0 {
		return 0
	}
	if _, moved := a.classicOv[slotTabBar]; moved {
		return 0
	}
	return tabBarH
}
