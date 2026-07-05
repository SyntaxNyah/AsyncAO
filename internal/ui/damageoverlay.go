package ui

import (
	"fmt"
	"time"

	"github.com/veandco/go-sdl2/sdl"
)

// The damage-region overlay: the compositor's X-ray (F8 → Perf → "Show
// damage regions"). Every walk, TakeDamage records the pre-union damage
// list; every PASS, the main loop draws that snapshot onto the BACKBUFFER
// after the cache blit — never into the cache — so the overlay cannot dirty
// the census or grow the damage it visualizes. That makes it observer-safe
// by construction: watching the compositor does not change what it does
// (unlike the F3 HUD, which forces full rate, or the old test11 behavior
// where ANY diagnostics view pinned full-frame walks). Reading it:
//
//	red wash      — a full-frame walk (input, packets, texture traffic,
//	                continuous surfaces, promotions)
//	blue rect     — the stage viewport (anim flips, ceremony)
//	green rect    — the chatbox (typewriter)
//	cyan rect     — the live log column (ceremony mirror)
//	yellow rect   — the focused text field (caret flip / rider)
//	violet rect   — a diagnostics tick (F8 panel / Settings debug overlay)
//	amber rect    — hover crossings and anything unclassified
//	white outline — the union clip the walk actually repainted
//
// A static screen shows rects fading out and none arriving — "walk … ago"
// climbing under a steady "presents/s" is selective rendering working. The
// whole screen washing red every pass means some source promotes to full
// every pass: that is the bug this overlay exists to catch (screenshot it).
//
// Allocations here (fmt for the corner readout) are accepted: an opt-in
// diagnostics path, never on by default, nothing runs while the toggle is
// off (same policy as the perf HUD; the zero-alloc gates bench with it off).

// Damage-region kinds for the overlay's color code, matched by rect
// identity against the census sources that damage with a KNOWN region.
const (
	dmgKindOther    = uint8(iota) // hover crossings, riders, unclassified — amber
	dmgKindViewport               // the stage rect (AnimGen / ceremony) — blue
	dmgKindChat                   // the chatbox (ceremony) — green
	dmgKindLog                    // the live log column (ceremony) — cyan
	dmgKindField                  // the focused field (caret / rider) — yellow
	dmgKindDiag                   // a diagnostics tick (F8 panel / debug overlay) — violet
)

// dmgOvLinger keeps a recorded walk's rects visible, fading, after the walk
// — a one-pass clip at a 165 Hz present rate would flash for 6 ms,
// unreadably fast. Long enough to see, short enough that stale rects don't
// read as current damage.
const dmgOvLinger = 600 * time.Millisecond

// Overlay paint strengths: fills translucent enough to read the UI under
// them, outlines near-opaque so a 1-px rect (a caret field) stays findable.
const (
	dmgOvRectAlpha    = 72  // per-region fill
	dmgOvFullAlpha    = 36  // the full-frame wash (covers everything: keep it light)
	dmgOvOutlineAlpha = 220 // region outlines
	dmgOvClipAlpha    = 200 // the white union-clip outline
)

// dmgOvColors indexes by dmgKind* (alpha applied at draw time via fadeCol).
var dmgOvColors = [...]sdl.Color{
	dmgKindOther:    {R: 255, G: 176, B: 32, A: 255},
	dmgKindViewport: {R: 72, G: 136, B: 255, A: 255},
	dmgKindChat:     {R: 64, G: 224, B: 96, A: 255},
	dmgKindLog:      {R: 48, G: 216, B: 216, A: 255},
	dmgKindField:    {R: 255, G: 232, B: 64, A: 255},
	dmgKindDiag:     {R: 200, G: 112, B: 255, A: 255},
}

// dmgOvFullCol is the full-frame wash/outline base (red — "everything").
var dmgOvFullCol = sdl.Color{R: 255, G: 64, B: 48, A: 255}

// Corner-readout metrics (bottom-right, clear of the ping chip and the
// Settings overlay, both bottom-left).
const (
	dmgOvReadoutLineH = int32(16) // matches the small-font row the panels use
	dmgOvReadoutPad   = int32(6)
	dmgOvReadoutInset = int32(8)
)

// DamageOverlayOn reports the F8 toggle for the main loop (render thread).
func (a *App) DamageOverlayOn() bool { return a.dmgOvOn }

// recordDamageOverlay snapshots the walk's damage BEFORE TakeDamage unions
// it away: the pre-union rects keep their identities (and so their colors).
// Caller checks dmgOvOn; render thread only; fixed arrays, no allocation.
func (a *App) recordDamageOverlay(full bool, n int) {
	a.dmgOvAt = time.Now()
	a.dmgOvFull = full || n == 0
	a.dmgOvN = 0
	a.dmgOvClip = sdl.Rect{} // full walks have no union clip; TakeDamage sets it for clipped ones
	if a.dmgOvFull {
		return
	}
	a.dmgOvN = n
	for i := 0; i < n; i++ {
		a.dmgOvRects[i] = a.dmgRects[i]
		a.dmgOvKinds[i] = a.classifyDamage(a.dmgRects[i])
	}
}

// classifyDamage matches a damage rect against the census sources that
// damage with a known region this pass. Identity (not overlap) is right:
// those sources damage exactly their drawn rects, and a hover rect that
// merely overlaps the stage is still hover damage.
func (a *App) classifyDamage(r sdl.Rect) uint8 {
	switch r {
	case a.drawnVPRect:
		return dmgKindViewport
	case a.drawnChatRect:
		return dmgKindChat
	case a.drawnLogRect:
		return dmgKindLog
	case a.drawnDebugPanelRect, a.drawnDebugOvRect:
		return dmgKindDiag
	}
	if a.ctx != nil && r == a.ctx.focusRect {
		return dmgKindField
	}
	return dmgKindOther
}

// fadeCol returns col at the given base alpha scaled by the linger fade.
func fadeCol(col sdl.Color, alpha uint8, fade float32) sdl.Color {
	col.A = uint8(float32(alpha) * fade)
	return col
}

// DrawDamageOverlay paints the overlay in logical coordinates onto the
// current render target — the backbuffer, AFTER the cache blit (main loop,
// compositor path only). The blit erases last pass's overlay, so this
// redraws every pass and the fade animates at the present rate for free.
func (a *App) DrawDamageOverlay(ren *sdl.Renderer, scale float32, lw, lh int32) {
	_ = ren.SetScale(scale, scale)
	c := a.ctx
	if age := time.Since(a.dmgOvAt); !a.dmgOvAt.IsZero() && age < dmgOvLinger {
		fade := 1 - float32(age)/float32(dmgOvLinger)
		if a.dmgOvFull {
			full := sdl.Rect{X: 0, Y: 0, W: lw, H: lh}
			c.Fill(full, fadeCol(dmgOvFullCol, dmgOvFullAlpha, fade))
			c.Border(full, fadeCol(dmgOvFullCol, dmgOvOutlineAlpha, fade))
		} else {
			for i := 0; i < a.dmgOvN; i++ {
				col := dmgOvColors[a.dmgOvKinds[i]]
				c.Fill(a.dmgOvRects[i], fadeCol(col, dmgOvRectAlpha, fade))
				c.Border(a.dmgOvRects[i], fadeCol(col, dmgOvOutlineAlpha, fade))
			}
			c.Border(a.dmgOvClip, fadeCol(sdl.Color{R: 255, G: 255, B: 255}, dmgOvClipAlpha, fade))
		}
	}

	// Corner readout, live on every pass (presents keep coming even when
	// walks don't — that asymmetry IS the design, so show both). drawnFPS
	// is a rolling window that only updates during walks: once no walk has
	// landed for a full second the honest number is zero, not the stale
	// window. The age line is the static-screen proof — it climbs, the GPU
	// draw cost rests. Age at 0.1 s resolution bounds the text-cache churn.
	walks := a.drawnFPS
	sinceWalk := time.Since(a.lastFrameDrawn)
	if sinceWalk >= time.Second {
		walks = 0
	}
	line1 := fmt.Sprintf("damage X-ray — walks %d/s · presents %d/s", walks, a.presFPS)
	var line2 string
	switch {
	case a.dmgOvAt.IsZero():
		line2 = "no walk recorded yet"
	case a.dmgOvFull:
		line2 = fmt.Sprintf("last walk: FULL FRAME · %.1fs ago", sinceWalk.Seconds())
	default:
		line2 = fmt.Sprintf("last walk: %d rect(s) · clip %dx%d · %.1fs ago",
			a.dmgOvN, a.dmgOvClip.W, a.dmgOvClip.H, sinceWalk.Seconds())
	}
	w := c.TextWidth(line1)
	if w2 := c.TextWidth(line2); w2 > w {
		w = w2
	}
	box := sdl.Rect{
		X: lw - w - 2*dmgOvReadoutPad - dmgOvReadoutInset,
		Y: lh - 2*dmgOvReadoutLineH - 2*dmgOvReadoutPad - dmgOvReadoutInset,
		W: w + 2*dmgOvReadoutPad,
		H: 2*dmgOvReadoutLineH + 2*dmgOvReadoutPad,
	}
	c.Fill(box, sdl.Color{R: 0, G: 0, B: 0, A: 200})
	c.Label(box.X+dmgOvReadoutPad, box.Y+dmgOvReadoutPad, line1, ColText)
	c.Label(box.X+dmgOvReadoutPad, box.Y+dmgOvReadoutPad+dmgOvReadoutLineH, line2, ColTextDim)
}
