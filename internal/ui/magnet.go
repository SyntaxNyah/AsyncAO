package ui

// Live piece-to-piece magnetism (M3 / A2). The classic layout editor already
// snaps a dragged slot to its neighbours' edges/centres with the pure
// Inkscape-style math in classicalign.go (alignRect/bestAlign). This file
// generalises that magnet to the LIVE (normal-play) floating surfaces — every
// floatWin panel, the Extras box + its torn-off widgets, the favourite-emotes
// box and the Sprite-Style box — and to the THEMED layout editor's design-space
// drags, WITHOUT rewriting any of the four bespoke drag models.
//
// One shared primitive (snapRectToSiblings) wraps alignRect in move-mode and is
// SDL-type-light so it unit-tests without SDL, exactly like classicalign_test.go.
// The candidate set is a per-frame-rebuilt, capped App buffer (panelMagnetRects)
// assembled once at the top of drawFloatingPanels from every OTHER currently-open
// floating surface; screen edges + centre come free from alignRect's window
// targets (they overlap snapToEdges' floatSnapPx=12 branches, which stay exactly
// as they were — the sibling pass is purely additive). Reuses alignSnapPx=6: 6px
// is already tuned in the classic editor, so a distinct live threshold would be
// unjustified magic. Always-on, Shift-bypass (matching the editor's KMOD_SHIFT
// check) — no new pref.

import "github.com/veandco/go-sdl2/sdl"

const (
	// panelMagnetCap bounds the reused panelMagnetRects candidate buffer (hard
	// rule §17.4). Headroom over the 11 floatWin panels + Extras/detached/fav/
	// style/torn-tab surfaces the census can collect in one frame; excess
	// candidates past the cap are simply not considered as magnet targets.
	panelMagnetCap = 24
)

// snapRectToSiblings snaps r's top-left so an edge/centre lands flush with a
// sibling surface's edge/centre (or a screen edge/centre) when within
// alignSnapPx, returning the translated X,Y (W/H unchanged — this is the
// MOVE-mode magnet). guides, if non-nil, is appended in place with whatever
// matched (reused by the live-drag caller so a long drag allocates nothing after
// warm-up) and returned; handlers that don't render guides pass nil and ignore
// the second result. Pure — wraps the SDL-free alignRect, unit-tested without a
// renderer.
func snapRectToSiblings(r sdl.Rect, others []sdl.Rect, w, h int32, guides []alignGuide) (int32, int32, []alignGuide) {
	snapped, guides := alignRect(r, others, w, h, true, 0, guides)
	return snapped.X, snapped.Y, guides
}

// rebuildPanelMagnetRects refreshes the shared candidate buffer for THIS frame
// from every OPEN floating surface EXCEPT the one currently being dragged (its
// own drag flag is set, so it is skipped by identity — a rect-equality filter
// would fail because the census carries last-frame positions while the snap tests
// this-frame mouse-grab positions). The buffer is always truncated to length 0
// first, so a settled frame (nothing dragging) leaves it empty and this does no
// work beyond the reslice — the alloc gate never sees a populate or a guide draw.
//
// Only ONE surface drags at a time (they share one press edge), so skipping the
// active-drag surface cleanly yields "all other open surfaces". Bounded by
// panelMagnetCap. Runs only while a live drag is active (anyPanelDragging), off
// every settled frame.
func (a *App) rebuildPanelMagnetRects(w, h int32) {
	a.panelMagnetRects = a.panelMagnetRects[:0]
	if !a.anyPanelDragging() {
		return
	}
	add := func(r sdl.Rect) {
		if len(a.panelMagnetRects) < panelMagnetCap {
			a.panelMagnetRects = append(a.panelMagnetRects, r)
		}
	}
	// The 10 table-driven floatWin panels: open per their own flag, skipped while
	// this panel is the one being dragged.
	for i := range panelSlotTable {
		row := &panelSlotTable[i]
		fw := row.fw(a)
		if !row.open(a) || fw.dragging {
			continue
		}
		add(fw.rect(row.defW, row.defH, row.minW, row.minH, w, h))
	}
	// msgWin keeps its historical slot + wrappers, so it isn't in the table.
	if a.showMessages && !a.msgWin.dragging {
		add(a.msgPanelRect(w, h))
	}
	// The bespoke (non-floatWin) boxes share the same shared helper.
	if a.showWidgets && !a.extrasDragging {
		add(a.extrasBoxRect(w, h))
	}
	for i := range a.extrasDetached {
		if a.extrasDetachDragging && a.extrasDetachIdx == i {
			continue // the torn box being dragged
		}
		add(a.detachedBoxRect(i, w, h))
	}
	if a.d.Prefs.FavEmoteBoxOn() && !a.favBoxDragging {
		add(a.favBoxRect(w, h))
	}
	if a.showStyleBox && !a.styleBoxDragging {
		sr, _ := a.styleBoxRect(w, h)
		add(sr)
	}
	// Torn-off TAB panels (torntabs.go) persist via classic slots; include them as
	// passive magnet targets (they are never the surface dragged here). Mirror
	// drawTornTabs' visibility rules: a torn tab that the user ALSO fully hid
	// (tabHidden) or whose rect degenerated below classicMinPx is not drawn, so it
	// must not be an invisible magnet target either.
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
}

// anyPanelDragging reports whether any live magnet-participating floating surface
// is mid drag/resize (or the client window is panning). The census rebuild and the
// guide draw both key off THIS one helper, so they can never diverge from each
// other. It is close to drawFloatingPanels' finished-drag-click suppression list
// but deliberately ALSO includes hkWin (the hotkey sheet): hkWin is a floatWin in
// panelSlotTable whose title-bar drag runs through floatWinDrag → snapToSiblings,
// so it must gate the census/guides even though the suppression block omits it
// (hkWin draws + fences via a separate path, qol.go, not drawFloatingPanels). It is
// a superset of the move-only drags the magnet actually acts on; a resize frame
// leaves the census populated but fires no snapToSiblings call, which is harmless.
func (a *App) anyPanelDragging() bool {
	return a.extrasDragging || a.extrasDetachDragging || a.extrasResizing || a.extrasDetachResizing ||
		a.favBoxDragging || a.styleBoxDragging || a.styleBoxResizing ||
		a.pairWin.dragging || a.pairWin.resizing || a.modWin.dragging || a.modWin.resizing ||
		a.cmWin.dragging || a.cmWin.resizing || a.evidWin.dragging || a.evidWin.resizing ||
		a.modcallWin.dragging || a.modcallWin.resizing || a.msgWin.dragging || a.msgWin.resizing ||
		a.voiceWin.dragging || a.voiceWin.resizing || a.banWin.dragging || a.banWin.resizing ||
		a.debugWin.dragging || a.debugWin.resizing || a.hkWin.dragging || a.hkWin.resizing ||
		a.clientWin.dragging || a.clientWin.resizing || a.clientPanning
}

// magnetBypassed reports whether the live piece-to-piece magnet is suppressed
// this frame: Shift held (the editor's precise-placement modifier). Screen-edge
// snapping (snapToEdges) is NOT bypassed — that is the pre-existing #21 behaviour
// and stays independent, so Shift only turns off the new sibling magnet.
func magnetBypassed() bool {
	return sdl.GetModState()&sdl.KMOD_SHIFT != 0
}

// snapToSiblings snaps a dragging floatWin's top-left flush to the sibling
// candidates in a.panelMagnetRects (move-mode), appending any match into
// a.alignGuides for the live guide overlay. Additive to snapToEdges — the caller
// runs snapToEdges first (screen edges), then this (piece-to-piece). No-op until
// rect() has stamped the size (lastWinW/H), mirroring snapToEdges' guard, and
// no-op with an empty census (a settled frame / the only open panel).
func (a *App) snapToSiblings(fw *floatWin) {
	if fw.lastWinW <= 0 || fw.lastWinH <= 0 || len(a.panelMagnetRects) == 0 {
		return
	}
	r := sdl.Rect{X: fw.x, Y: fw.y, W: fw.lastW, H: fw.lastH}
	fw.x, fw.y, a.alignGuides = snapRectToSiblings(r, a.panelMagnetRects, fw.lastWinW, fw.lastWinH, a.alignGuides)
}

// drawPanelAlignGuides paints the live-snap guide hairlines (the Inkscape-style
// "you are aligned" feedback) at whatever the dragged surface snapped flush to
// this frame, reusing the classic editor's alignGuide rendering pattern. Drawn at
// the tail of drawFloatingPanels while a drag is active; alloc-free (constant
// geometry Fill via c.cgoRect). Guides are populated by the live snap calls
// (floatWinDrag / the bespoke drag handlers) and reset at the head of the frame's
// snap census. Note: the hotkey sheet (hkWin) draws + drags AFTER drawFloatingPanels
// (app.go), so its own drag SNAPS correctly but shows no guide line — every other
// panel, drawn within drawFloatingPanels, populates a.alignGuides before this tail.
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
