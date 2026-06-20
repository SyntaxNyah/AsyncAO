package ui

import (
	"fmt"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/veandco/go-sdl2/sdl"
)

// Scene-maker timeline (#75): a horizontal film-strip of the scene's events, the
// one view the vertical list can't give — each segment's WIDTH is proportional to
// how long that event was on screen in the recording (its OffsetMs gap to the
// next event), so you SEE the pacing: which message dominates, where the dead air
// is. Click a segment to select it; drag the ⟦ ⟧ handles to crop In/Out directly.
//
// Honest-axis guards: segments are by ARRAY ORDER (offsets drive width only, so
// edits don't reshuffle them); idle gaps are CLAMPED [min,max] in ms so an AFK
// pause can't swallow the strip, plus a MIN PIXEL floor so every event stays
// clickable (the strip scrolls when they overflow); a hand-built scene with no
// recorded pacing (all-zero offsets) clamps to equal widths automatically. Only
// drawn while the maker is open — no live-render cost.

const (
	makerTimelineH = int32(58) // total strip height (label + track + handle overhang)
	makerTrackH    = int32(30) // coloured segment track height
	makerSegMinPx  = int32(9)  // min clickable segment width
	makerSegGap    = int32(1)  // 1px seam between segments
	makerHandleW   = int32(9)  // crop-handle grab width
	makerTLWheelPx = int32(48) // horizontal scroll per wheel notch

	makerTLMinMs  = 150.0  // floor so a near-instant bg/music change still shows
	makerTLMaxMs  = 6000.0 // ceiling so dead air can't dominate the axis
	makerTLTailMs = 2000.0 // the last event has no successor — assume this on-screen
)

// makerTLDurations fills durs (reused) with each event's display duration in ms:
// the recorded OffsetMs gap to the next event, clamped [min,max]. When the
// offsets carry no pacing signal at all (a hand-built scene, or an archive that
// didn't capture wall-clock — every gap <= 0), it falls back to equal widths
// rather than a degenerate fused bar. By array order.
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

// makerTLLayout fills a.makerSegX/W (px, pre-scroll) from the clamped durations,
// applying a minimum pixel floor so every segment stays clickable. Returns the
// total content width including seams (for scroll clamping).
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

// makerTLSegAt returns the segment index under screen-x mx (track left edge at
// stripX, honouring the horizontal scroll), or -1 if past the ends.
func (a *App) makerTLSegAt(mx, stripX int32) int {
	rel := mx - stripX + a.makerTLScroll
	for i := range a.makerSegX {
		if rel >= a.makerSegX[i] && rel < a.makerSegX[i]+a.makerSegW[i] {
			return i
		}
	}
	return -1
}

// drawMakerTimeline paints the strip and handles its clicks. press is this
// frame's mouse-press edge (so a handle is grabbed on press, not on a drag-in).
func (a *App) drawMakerTimeline(x, y, w int32, press bool) {
	c := a.ctx
	evs := a.makerScene.Events
	c.Label(x, y, "Timeline — width ∝ recorded pacing · click a block to select · drag ⟦ ⟧ to crop In/Out", ColTextDim)
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
	if a.makerTLScroll < 0 {
		a.makerTLScroll = 0
	}
	if a.makerTLScroll > scrollMax {
		a.makerTLScroll = scrollMax
	}

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
		if c.hovering(seg) {
			tag, text := eventSummary(evs[i])
			if len(text) > 60 {
				text = text[:60] + "…"
			}
			c.Tooltip(seg, fmt.Sprintf("#%d  %s  %s", i+1, tag, text))
		}
	}

	// Crop handles: In at the left edge of segment s, Out at the right edge of e.
	inX := x + a.makerSegX[s] - a.makerTLScroll
	outX := x + a.makerSegX[e] + a.makerSegW[e] - makerSegGap - a.makerTLScroll
	inH := sdl.Rect{X: inX - makerHandleW/2, Y: ty - 3, W: makerHandleW, H: makerTrackH + 6}
	outH := sdl.Rect{X: outX - makerHandleW/2, Y: ty - 3, W: makerHandleW, H: makerTrackH + 6}
	c.Fill(inH, ColAccent)
	c.Fill(outH, ColAccent)

	// Interaction: grab a handle on the press edge, else select the clicked block.
	if press {
		switch {
		case pointIn(c.mouseX, c.mouseY, inH):
			a.makerDragHandle = 1
		case pointIn(c.mouseX, c.mouseY, outH):
			a.makerDragHandle = 2
		case pointIn(c.mouseX, c.mouseY, track):
			if seg := a.makerTLSegAt(c.mouseX, x); seg >= 0 {
				a.makerSel = seg
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
}

// makerKindColor tints a timeline segment by event kind, so the scene's shape
// reads at a glance: dialogue, scene change, music, other.
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
