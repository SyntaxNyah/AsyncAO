package ui

import (
	"fmt"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/helper/value"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

// Scene-maker timeline: a horizontal strip of the scene's events whose widths are proportional to how long each was on
// screen (its OffsetMs gap to the next event). Click a segment to select it; drag the handles to crop In/Out.
// Segments are by array order, idle gaps are clamped [min,max] ms so an AFK pause can't swallow the strip, and a
// minimum pixel width keeps every event clickable. A scene with all-zero offsets gets equal widths.
// Only drawn while the maker is open.

const (
	makerTimelineH = int32(72) // total strip height (label + track + scrub lane + overhang)
	makerTrackH    = int32(30) // coloured segment track height
	makerScrubH    = int32(12) // #68 scrub lane under the track (drag = continuous preview)
	makerSegMinPx  = int32(9)  // min clickable segment width
	makerSegGap    = int32(1)  // 1px seam between segments
	makerHandleW   = int32(9)  // crop-handle grab width
	makerTLWheelPx = int32(48) // horizontal scroll per wheel notch

	makerDragThreshPx = int32(6)  // px the press must travel before a click becomes a reorder drag
	makerTLEdgePx     = int32(28) // a drag within this of a strip edge auto-scrolls toward it
	makerSegDragID    = "maker_tl_seg"
	makerScrubDragID  = "maker_tl_scrub"

	makerTLMinMs  = 150.0  // floor so a near-instant bg/music change still shows
	makerTLMaxMs  = 6000.0 // ceiling so dead air can't dominate the axis
	makerTLTailMs = 2000.0 // the last event has no successor — assume this on-screen
)

// abs32 is |v| for the drag-threshold check.
func abs32(v int32) int32 {
	if v < 0 {
		return -v
	}
	return v
}

// makerTLDurations fills durs (reused) with each event's display duration in ms: the OffsetMs gap to the next event,
// clamped [min,max]. When every gap is <= 0 (no recorded pacing) it falls back to equal widths.
func makerTLDurations(evs []recEvent, durs []float64) []float64 {
	durs = durs[:0]
	if len(evs) == 0 {
		return durs
	}
	signal := false
	for i := 0; i+1 < len(evs); i++ {
		if evs[i+1].OffsetMs > evs[i].OffsetMs {
			signal = true
			break
		}
	}
	if !signal { // no recorded timing → uniform, clickable, no-signal fallback
		for range evs {
			durs = append(durs, 1)
		}
		return durs
	}
	for i := range evs {
		d := makerTLTailMs
		if i+1 < len(evs) {
			d = float64(evs[i+1].OffsetMs - evs[i].OffsetMs)
		}
		switch {
		case d < makerTLMinMs:
			d = makerTLMinMs
		case d > makerTLMaxMs:
			d = makerTLMaxMs
		}
		durs = append(durs, d)
	}
	return durs
}

// makerTLLayout fills a.makerSegX/W (px, pre-scroll) from the clamped durations with a minimum pixel width.
// It returns the total content width including gaps, for scroll clamping.
func (a *App) makerTLLayout(evs []recEvent, usableW int32) int32 {
	a.makerTLDur = makerTLDurations(evs, a.makerTLDur)
	a.makerSegX = a.makerSegX[:0]
	a.makerSegW = a.makerSegW[:0]
	var total float64
	for _, d := range a.makerTLDur {
		total += d
	}
	if total <= 0 {
		total = 1
	}
	cx := int32(0)
	for _, d := range a.makerTLDur {
		w := int32(d / total * float64(usableW))
		if w < makerSegMinPx {
			w = makerSegMinPx
		}
		a.makerSegX = append(a.makerSegX, cx)
		a.makerSegW = append(a.makerSegW, w)
		cx += w + makerSegGap
	}
	return cx
}

// makerTLSegAt returns the segment index under screen-x mx (track left edge at stripX, honouring scroll), or -1.
func (a *App) makerTLSegAt(mx, stripX int32) int {
	rel := mx - stripX + a.makerTLScroll
	for i := range a.makerSegX {
		if rel >= a.makerSegX[i] && rel < a.makerSegX[i]+a.makerSegW[i] {
			return i
		}
	}
	return -1
}

// makerTLGapAt returns the insertion gap (0..n) nearest screen-x mx, where a dragged segment would drop.
// Gap g is between segment g-1 and g, so the scan stops at the first midpoint past the cursor.
func (a *App) makerTLGapAt(mx, stripX int32) int {
	rel := mx - stripX + a.makerTLScroll
	g := 0
	for i := range a.makerSegX {
		if rel >= a.makerSegX[i]+a.makerSegW[i]/2 {
			g = i + 1
		} else {
			break
		}
	}
	return g
}

// drawMakerTimeline paints the strip and handles its clicks; press is this frame's press edge.
func (a *App) drawMakerTimeline(x, y, w int32, press bool) {
	c := a.ctx
	evs := a.makerScene.Events
	c.Label(x, y, "Timeline — width ∝ pacing · click selects · drag a block reorders · ⟦ ⟧ crop · drag the lane below to SCRUB", ColTextDim)
	ty := y + 18
	track := sdl.Rect{X: x, Y: ty, W: w, H: makerTrackH}
	c.Fill(track, ColPanel)
	c.Border(track, ColPanelHi)
	if len(evs) == 0 {
		return
	}

	contentW := a.makerTLLayout(evs, w)
	scrollMax := contentW - w
	if scrollMax < 0 {
		scrollMax = 0
	}
	if c.hovering(track) && c.wheelY != 0 {
		a.makerTLScroll -= int32(c.wheelY) * makerTLWheelPx
	}
	a.makerTLScroll = value.Clamp(a.makerTLScroll, 0, scrollMax)

	s, e := a.trimRange()
	cropOn := a.trimActive()

	for i := range a.makerSegX {
		sx := x + a.makerSegX[i] - a.makerTLScroll
		sw := a.makerSegW[i] - makerSegGap
		if sw < 1 {
			sw = 1
		}
		if sx+sw < x || sx > x+w { // cull off-strip
			continue
		}
		seg := sdl.Rect{X: sx, Y: ty, W: sw, H: makerTrackH}
		col := makerKindColor(evs[i].Kind)
		if cropOn && (i < s || i > e) { // excluded from the crop → dim
			col = sdl.Color{R: col.R / 3, G: col.G / 3, B: col.B / 3, A: 255}
		}
		c.Fill(seg, col)
		if i == a.makerSel { // playhead = the selected event
			c.Border(seg, ColText)
		}
		if a.makerDragMoved && i == a.makerDragSeg { // the block being moved dims in place; the caret shows its target slot
			c.Fill(seg, sdl.Color{R: 0, G: 0, B: 0, A: 110})
		}
		if c.hovering(seg) && !a.makerDragMoved { // suppress tooltips mid-drag
			tag, text := eventSummary(evs[i])
			if len(text) > 60 {
				text = text[:60] + "…"
			}
			c.Tooltip(seg, fmt.Sprintf("#%d  %s  %s", i+1, tag, text))
		}
	}

	// Scrub lane: pressing and dragging along it moves the selection to the segment under the cursor, and the preview
	// re-plays as ensureMakerPreview rebuilds on any makerSel change. Its own lane, so it can't fight the reorder drag.
	lane := sdl.Rect{X: x, Y: ty + makerTrackH + 2, W: w, H: makerScrubH}
	c.Fill(lane, ColBackground)
	c.Border(lane, ColPanelHi)
	if a.makerSel >= 0 && a.makerSel < len(a.makerSegX) {
		px := x + a.makerSegX[a.makerSel] + (a.makerSegW[a.makerSel]-makerSegGap)/2 - a.makerTLScroll
		if px >= x && px <= x+w {
			c.Fill(sdl.Rect{X: px - 1, Y: lane.Y + 1, W: 2, H: lane.H - 2}, ColAccent)
			c.Fill(sdl.Rect{X: px - 3, Y: lane.Y + lane.H/2 - 3, W: 6, H: 6}, ColAccent)
		}
	}
	if press && pointIn(c.mouseX, c.mouseY, lane) {
		a.makerScrub = true
	}
	if a.makerScrub {
		if !c.mouseDown {
			a.makerScrub = false
			c.clicked = false // a finished scrub isn't a click
		} else {
			c.dragID = makerScrubDragID // own the pointer while sweeping
			seg := a.makerTLSegAt(c.mouseX, x)
			if seg < 0 { // past the ends → clamp
				if c.mouseX <= x {
					seg = 0
				} else {
					seg = len(evs) - 1
				}
			}
			a.makerSel = seg
			switch { // edge auto-scroll, same feel as the reorder drag
			case c.mouseX < x+makerTLEdgePx:
				a.makerTLScroll -= makerTLWheelPx
			case c.mouseX > x+w-makerTLEdgePx:
				a.makerTLScroll += makerTLWheelPx
			}
			a.makerTLScroll = value.Clamp(a.makerTLScroll, 0, scrollMax)
		}
	}

	// Crop handles: In at the left edge of segment s, Out at the right edge of e.
	inX := x + a.makerSegX[s] - a.makerTLScroll
	outX := x + a.makerSegX[e] + a.makerSegW[e] - makerSegGap - a.makerTLScroll
	inH := sdl.Rect{X: inX - makerHandleW/2, Y: ty - 3, W: makerHandleW, H: makerTrackH + 6}
	outH := sdl.Rect{X: outX - makerHandleW/2, Y: ty - 3, W: makerHandleW, H: makerTrackH + 6}
	c.Fill(inH, ColAccent)
	c.Fill(outH, ColAccent)

	// Interaction: grab a crop handle on the press edge, else select the clicked block and arm it for a reorder drag.
	if press {
		switch {
		case pointIn(c.mouseX, c.mouseY, inH):
			a.makerDragHandle = 1
		case pointIn(c.mouseX, c.mouseY, outH):
			a.makerDragHandle = 2
		case pointIn(c.mouseX, c.mouseY, track):
			if seg := a.makerTLSegAt(c.mouseX, x); seg >= 0 {
				a.makerSel = seg     // select immediately (feedback)
				a.makerDragSeg = seg // arm a potential reorder drag
				a.makerDragX = c.mouseX
				a.makerDragMoved = false
			}
		}
	}
	if !c.mouseDown {
		a.makerDragHandle = 0
	}
	if a.makerDragHandle != 0 && c.mouseDown {
		seg := a.makerTLSegAt(c.mouseX, x)
		if seg < 0 { // dragged past an end → clamp to it
			if c.mouseX <= x {
				seg = 0
			} else {
				seg = len(evs) - 1
			}
		}
		if a.makerDragHandle == 1 { // In
			a.makerTrimStart = seg
			if a.makerTrimEnd >= 0 && a.makerTrimEnd < seg {
				a.makerTrimEnd = -1
			}
		} else { // Out
			a.makerTrimEnd = seg
			if a.makerTrimStart > seg {
				a.makerTrimStart = -1
			}
		}
	}

	// Reorder drag: once the armed press travels past a small threshold it is a drag, not a click. A caret marks the
	// drop gap, an edge nudge auto-scrolls, and release commits the move.
	if a.makerDragSeg >= 0 && c.mouseDown {
		if !a.makerDragMoved && abs32(c.mouseX-a.makerDragX) > makerDragThreshPx {
			a.makerDragMoved = true
		}
		if a.makerDragMoved {
			c.dragID = makerSegDragID // claim the drag so editor sliders/scrollbars can't grab as it drifts up
			switch {                  // edge auto-scroll
			case c.mouseX < x+makerTLEdgePx:
				a.makerTLScroll -= makerTLWheelPx
			case c.mouseX > x+w-makerTLEdgePx:
				a.makerTLScroll += makerTLWheelPx
			}
			a.makerTLScroll = value.Clamp(a.makerTLScroll, 0, scrollMax)
			g := a.makerTLGapAt(c.mouseX, x)
			var caretRel int32
			switch {
			case g <= 0:
				caretRel = a.makerSegX[0]
			case g >= len(a.makerSegX):
				last := len(a.makerSegX) - 1
				caretRel = a.makerSegX[last] + a.makerSegW[last] - makerSegGap
			default:
				caretRel = a.makerSegX[g]
			}
			caretX := x + caretRel - a.makerTLScroll
			c.Fill(sdl.Rect{X: caretX - 1, Y: ty - 3, W: 2, H: makerTrackH + 6}, ColAccent)
		}
	}
	if a.makerDragSeg >= 0 && !c.mouseDown { // release
		if a.makerDragMoved {
			g := a.makerTLGapAt(c.mouseX, x)
			dst := g
			if dst > a.makerDragSeg { // removing the source shifts a later target left one
				dst--
			}
			if dst < 0 {
				dst = 0
			}
			if dst >= len(evs) {
				dst = len(evs) - 1
			}
			a.makerMoveEvent(a.makerDragSeg, dst)
			c.clicked = false // a finished drag is not a click (don't leak it underneath)
		}
		a.makerDragSeg = -1
		a.makerDragMoved = false
	}
}

// makerKindColor tints a timeline segment by event kind: dialogue, scene change, music, other.
func makerKindColor(kind int) sdl.Color {
	switch courtroom.EventKind(kind) {
	case courtroom.EventMessage:
		return sdl.Color{R: 90, G: 130, B: 220, A: 255} // blue
	case courtroom.EventBackground:
		return sdl.Color{R: 70, G: 165, B: 150, A: 255} // teal
	case courtroom.EventMusic:
		return sdl.Color{R: 110, G: 180, B: 90, A: 255} // green
	default:
		return sdl.Color{R: 125, G: 125, B: 135, A: 255} // grey
	}
}
