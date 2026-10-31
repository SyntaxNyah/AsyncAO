package ui

// Live snapping of floating surfaces to each other, using the layout editor's alignRect math (classicalign.go).
// Additive to snapToEdges; Shift bypasses it.

import (
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

const (
	// panelMagnetCap bounds the panelMagnetRects candidate buffer (hard rule 4); candidates past it are ignored.
	// There are 24 fixed surfaces and torn-off tabs, plus one torn-off Extras widget per table entry.
	panelMagnetCap = 32
)

// snapRectToSiblings snaps r's top-left to a sibling's or the screen's edge or centre within alignSnapPx.
// guides, if non-nil, is appended with the matches and returned, so a long drag allocates nothing after warm-up.
func snapRectToSiblings(r sdl.Rect, others []sdl.Rect, w, h int32, guides []alignGuide) (int32, int32, []alignGuide) {
	snapped, guides := alignRect(r, others, w, h, true, 0, guides)
	return snapped.X, snapped.Y, guides
}

// rebuildPanelMagnetRects refreshes the candidate buffer from every open surface except the one being dragged.
// Only one surface drags at a time; a settled frame only truncates the buffer.
func (a *App) rebuildPanelMagnetRects(w, h int32) {
	a.panelMagnetRects = a.panelMagnetRects[:0]
	if !a.anyPanelDragging() {
		return
	}
	a.collectOpenPanelRects(w, h)
}

// collectOpenPanelRects fills panelMagnetRects (capped by panelMagnetCap) with every open floating surface.
// A mid-drag surface is skipped by its drag flag; rect equality would fail because the rects are last-frame positions.
func (a *App) collectOpenPanelRects(w, h int32) {
	a.panelMagnetRects = a.panelMagnetRects[:0]
	add := func(r sdl.Rect) {
		if len(a.panelMagnetRects) < panelMagnetCap {
			a.panelMagnetRects = append(a.panelMagnetRects, r)
		}
	}
	for i := range panelSlotTable {
		row := &panelSlotTable[i]
		fw := row.fw(a)
		if !row.open(a) || fw.dragging {
			continue
		}
		add(fw.rect(row.defW, row.defH, row.minW, row.minH, w, h))
	}
	// msgWin is not in panelSlotTable.
	if a.showMessages && !a.msgWin.dragging {
		add(a.msgPanelRect(w, h))
	}
	if a.showWidgets && !a.extrasDragging {
		add(a.extrasBoxRect(w, h))
	}
	for i := range a.extrasDetached {
		if a.extrasDetachDragging && a.extrasDetachIdx == i {
			continue // the torn box being dragged
		}
		add(a.detachedBoxRect(i, w, h))
	}
	if a.d.Prefs.Load().FavEmoteBox && !a.favBoxDragging {
		add(a.favBoxRect(w, h))
	}
	if a.showStyleBox && !a.styleBoxDragging {
		sr, _ := a.styleBoxRect(w, h)
		add(sr)
	}
	// Torn-off tabs are passive targets; mirror drawTornTabs' visibility so an undrawn tab is not an invisible target.
	if len(a.classicOv) > 0 {
		for i := range tornTabTable {
			if a.tabHidden(tornTabTable[i].id) {
				continue
			}
			if r, ok := a.tornTabRect(tornTabTable[i].key, w, h); ok && r.W >= classicMinPx && r.H >= classicMinPx {
				add(r)
			}
		}
	}
	// The compact toolbox strip is a passive target, never the surface dragged here.
	if !a.panelHidden(panelToolbox) {
		add(a.compactToolboxStripRect(w, h))
	}
}

const (
	// panelDeOverlapFracNum/Den: a panel is nudged only when the overlap covers this share of the smaller rect (82%).
	// High so an intentional partial stack is left alone; an integer ratio so the comparison is exact.
	panelDeOverlapFracNum = 82
	panelDeOverlapFracDen = 100
	// panelDeOverlapStep is the diagonal nudge per cascade step, so the moved panel's title bar clears the one beneath.
	panelDeOverlapStep = int32(28)
	// panelDeOverlapCap bounds the cascade (hard rule 4): after this many nudges the panel stays where it landed.
	panelDeOverlapCap = 6
)

// rectsNearlyCover reports whether a and b overlap by at least panelDeOverlapFrac of the smaller rect's area.
func rectsNearlyCover(a, b sdl.Rect) bool {
	ix := max(a.X, b.X)
	iy := max(a.Y, b.Y)
	ix2 := min(a.X+a.W, b.X+b.W)
	iy2 := min(a.Y+a.H, b.Y+b.H)
	if ix2 <= ix || iy2 <= iy {
		return false // disjoint
	}
	inter := int64(ix2-ix) * int64(iy2-iy)
	areaA := int64(a.W) * int64(a.H)
	areaB := int64(b.W) * int64(b.H)
	smaller := areaA
	if areaB < smaller {
		smaller = areaB
	}
	if smaller <= 0 {
		return false
	}
	return inter*panelDeOverlapFracDen >= smaller*panelDeOverlapFracNum
}

// deOverlapRect nudges r down and right while it near-totally overlaps a sibling, bounded by panelDeOverlapCap.
func deOverlapRect(r sdl.Rect, census []sdl.Rect, w, h int32) sdl.Rect {
	for iter := 0; iter < panelDeOverlapCap; iter++ {
		hit := false
		for _, s := range census {
			if rectsNearlyCover(r, s) {
				hit = true
				break
			}
		}
		if !hit {
			return r
		}
		r.X += panelDeOverlapStep
		r.Y += panelDeOverlapStep
		// If the nudge ran off the right or bottom, wrap near the top-left so the cascade stays visible.
		if r.X+r.W > w-floatWinMargin {
			r.X = floatWinMargin
		}
		if r.Y+r.H > h-floatWinMargin {
			r.Y = floatTitleH
		}
	}
	return r
}

// panelDeOverlapCensus returns the sibling rects for de-overlap in the shared buffer, excluding the rect at exclude.
func (a *App) panelDeOverlapCensus(exclude sdl.Rect, w, h int32) []sdl.Rect {
	a.rebuildPanelMagnetRects(w, h) // populates only while dragging (release case)
	if len(a.panelMagnetRects) == 0 {
		// Seed case: no drag is active, so build the full list directly.
		a.collectOpenPanelRects(w, h)
	}
	// Drop the surface being placed so it cannot match itself.
	out := a.panelMagnetRects[:0]
	for _, r := range a.panelMagnetRects {
		if r == exclude {
			continue
		}
		out = append(out, r)
	}
	a.panelMagnetRects = out
	return a.panelMagnetRects
}

// anyPanelDragging reports whether any magnet surface is mid drag or resize, or the client window is panning.
// Unlike drawFloatingPanels' click suppression it includes hkWin, whose drag runs through snapToSiblings.
func (a *App) anyPanelDragging() bool {
	return a.extrasDragging || a.extrasDetachDragging || a.extrasResizing || a.extrasDetachResizing ||
		a.favBoxDragging || a.styleBoxDragging || a.styleBoxResizing ||
		a.pairWin.dragging || a.pairWin.resizing || a.modWin.dragging || a.modWin.resizing ||
		a.cmWin.dragging || a.cmWin.resizing || a.evidWin.dragging || a.evidWin.resizing ||
		a.modcallWin.dragging || a.modcallWin.resizing || a.msgWin.dragging || a.msgWin.resizing ||
		a.voiceWin.dragging || a.voiceWin.resizing || a.banWin.dragging || a.banWin.resizing ||
		a.debugWin.dragging || a.debugWin.resizing || a.hkWin.dragging || a.hkWin.resizing ||
		a.timerWin.dragging || a.timerWin.resizing || a.loginWin.dragging || a.loginWin.resizing ||
		a.pairPopWin.dragging || a.pairPopWin.resizing ||
		a.clientWin.dragging || a.clientWin.resizing || a.clientPanning
}

// magnetBypassed reports whether Shift is held; snapToEdges stays on either way.
// The layoutedit.go grid check reads this form: the "Magnet: off" chip must not disable grid snapping.
func magnetBypassed() bool {
	return sdl.GetModState()&sdl.KMOD_SHIFT != 0
}

// siblingMagnetOff reports whether to skip sibling snapping: Shift is held or the "Magnet: off" chip is set.
// Screen-edge snapping and the de-overlap pass stay on.
func (a *App) siblingMagnetOff() bool {
	return magnetBypassed() || a.layoutMagnetOff
}

// snapToSiblings snaps a dragging floatWin to the panelMagnetRects candidates, appending matches to a.alignGuides.
// The caller runs snapToEdges first. No-op until rect() has stamped the size.
func (a *App) snapToSiblings(fw *floatWin) {
	if fw.lastWinW <= 0 || fw.lastWinH <= 0 || len(a.panelMagnetRects) == 0 {
		return
	}
	r := sdl.Rect{X: fw.x, Y: fw.y, W: fw.lastW, H: fw.lastH}
	fw.x, fw.y, a.alignGuides = snapRectToSiblings(r, a.panelMagnetRects, fw.lastWinW, fw.lastWinH, a.alignGuides)
}

// drawPanelAlignGuides paints the guide hairlines at whatever the dragged surface snapped to this frame.
// hkWin drags after drawFloatingPanels, so its drag snaps but shows no guide line.
func (a *App) drawPanelAlignGuides(w, h int32) {
	if !a.anyPanelDragging() {
		return
	}
	c := a.ctx
	for _, g := range a.alignGuides {
		if g.vertical {
			c.Fill(sdl.Rect{X: g.pos, Y: 0, W: 1, H: h}, ColTierGreen)
		} else {
			c.Fill(sdl.Rect{X: 0, Y: g.pos, W: w, H: 1}, ColTierGreen)
		}
	}
}
