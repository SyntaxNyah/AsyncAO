package ui

// The reserved top band of client chrome: the menu bar and, docked beneath it, the server-tab strip.
// Every screen starts below the band, measured only here; no screen may hardcode menuBarH or tabBarH.

// topChromeH is the band's height, called from every screen draw, so it must stay allocation-free integer arithmetic.
// Safe unconditionally: both parts report 0 when their strip isn't taking the band.
func (a *App) topChromeH() int32 { return a.menuBarHeight() + a.tabStripBandH() }

// screenDispatchPreempted reports whether a full-window mode (gif, replay, maker) takes this frame instead of a screen.
// a.screen still reads Courtroom during these modes, so testing a.screen is not enough.
func (a *App) screenDispatchPreempted() bool {
	return a.gifExporting || (a.replaying && a.replayRoom != nil) || a.makerOpen
}

// tabStripPaintsUnderEditor reports whether the strip paints in the courtroom pass, beneath a layout editor's overlay,
// rather than at App.Frame's over-everything site: it is opaque and sits where the editors put their banner and
// buttons.
// True only on frames where an editor's draw site actually runs, or the strip would vanish.
func (a *App) tabStripPaintsUnderEditor() bool { return a.editorDrawSiteRuns() }

// tabStripPaintLatch records once per frame which of the strip's two draw sites paints it. Re-deriving it at each site
// could paint twice on the frame an editor is dismissed, or not at all on the frame one is armed.
// It is taken at the top of the courtroom pass, before anything can change an editor mid-frame.
type tabStripPaintLatch struct {
	// set marks that a courtroom pass decided this frame; false means App.Frame's site paints.
	set bool
	// inPass means the courtroom pass paints the strip, under a layout editor's overlay.
	inPass bool
}

// latchTabStripPaint records the decision; call once at the top of the courtroom pass.
func (a *App) latchTabStripPaint() {
	a.tabStripPaint = tabStripPaintLatch{set: true, inPass: a.tabStripPaintsUnderEditor()}
}

// drawTabBarInPass is the courtroom pass's strip site, using the recorded decision; both passes call it.
func (a *App) drawTabBarInPass(w, h int32) {
	if a.tabStripPaint.inPass {
		a.drawTabBar(w, h)
	}
}

// tabStripPaintsOverEverything is App.Frame's site, the exact complement, so exactly one paints per frame.
func (a *App) tabStripPaintsOverEverything() bool { return !a.tabStripPaint.inPass }

// tabStripBandH is the band the docked strip owns: zero with no sessions, or once dragged elsewhere (a slotTabBar
// override). Deliberately not zeroed for a theme that parks the strip via "asyncao_tabbar": reading a.themeLay while
// themeLayoutIn rebuilds from this number could flip the band every frame and rebuild the layout cache forever.
func (a *App) tabStripBandH() int32 {
	if len(a.tabs) == 0 {
		return 0
	}
	if _, moved := a.classicOv[slotTabBar]; moved {
		return 0
	}
	return tabBarH
}
