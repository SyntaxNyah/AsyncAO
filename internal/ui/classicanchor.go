package ui

// Persistent window anchoring for classic-layout slots (playtest, Tifera:
// "anchor layout items to corners and center of the entire screen"). The
// alignment magnet (classicalign.go) places a box flush ONCE; an anchor keeps
// it there THROUGH WINDOW RESIZES: the slot's fraction override is re-based
// to pixel offsets from the pinned reference, reconstructed from the window
// size the override was saved at. Un-anchored slots keep today's pure
// fraction scaling, byte-identical.

import (
	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
)

// Per-axis anchor modes (parsed from config.ClassicAnchor.Mode letters).
const (
	anchorFrac uint8 = iota // follow the stored fraction — today's scaling
	anchorLow               // pixel-glued to the left / top window edge
	anchorHigh              // pixel-glued to the right / bottom window edge
	anchorMid               // pixel-glued to the window centre
)

// anchorRef is the App-local parsed form of config.ClassicAnchor — resolved
// per slot per frame, so the string mode is parsed once at load.
type anchorRef struct {
	h, v       uint8
	winW, winH int32
}

// anchorCycle is the editor's A-key order: unpinned, the four corners, then
// dead centre. Mode strings are horizontal-then-vertical (config letters).
var anchorCycle = []string{"", "lt", "rt", "lb", "rb", "cc"}

// nextAnchorMode advances the A-key cycle; unknown modes (a hand-edited
// single-axis pin like "rf") restart at unpinned so the key always works.
func nextAnchorMode(cur string) string {
	for i, m := range anchorCycle {
		if m == cur {
			return anchorCycle[(i+1)%len(anchorCycle)]
		}
	}
	return anchorCycle[0]
}

// anchorModeLabel names a mode for the editor's hint line.
func anchorModeLabel(m string) string {
	switch m {
	case "lt":
		return "top-left"
	case "rt":
		return "top-right"
	case "lb":
		return "bottom-left"
	case "rb":
		return "bottom-right"
	case "cc":
		return "centre"
	}
	return "off"
}

// parseAnchorMode decodes the two-letter mode; ok=false on junk (the caller
// treats that as unpinned).
func parseAnchorMode(m string) (h, v uint8, ok bool) {
	if len(m) != 2 {
		return anchorFrac, anchorFrac, false
	}
	switch m[0] {
	case 'f':
		h = anchorFrac
	case 'l':
		h = anchorLow
	case 'r':
		h = anchorHigh
	case 'c':
		h = anchorMid
	default:
		return anchorFrac, anchorFrac, false
	}
	switch m[1] {
	case 'f':
		v = anchorFrac
	case 't':
		v = anchorLow
	case 'b':
		v = anchorHigh
	case 'c':
		v = anchorMid
	default:
		return anchorFrac, anchorFrac, false
	}
	return h, v, true
}

// parseAnchors converts the persisted map into the App-local resolved form.
func parseAnchors(in map[string]config.ClassicAnchor) map[string]anchorRef {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]anchorRef, len(in))
	for k, a := range in {
		h, v, ok := parseAnchorMode(a.Mode)
		if !ok || a.WinW <= 0 || a.WinH <= 0 {
			continue // sanitize already dropped these; belt and braces
		}
		out[k] = anchorRef{h: h, v: v, winW: int32(a.WinW), winH: int32(a.WinH)}
	}
	return out
}

// formatAnchorMode is parseAnchorMode's inverse (for persisting).
func formatAnchorMode(h, v uint8) string {
	hs, vs := byte('f'), byte('f')
	switch h {
	case anchorLow:
		hs = 'l'
	case anchorHigh:
		hs = 'r'
	case anchorMid:
		hs = 'c'
	}
	switch v {
	case anchorLow:
		vs = 't'
	case anchorHigh:
		vs = 'b'
	case anchorMid:
		vs = 'c'
	}
	return string([]byte{hs, vs})
}

// applyAnchor resolves an anchored slot for the CURRENT window (w,h): the
// override fraction is first reconstructed at its saved window (the exact
// pixel rect the user placed), then each pinned axis keeps its pixel offset
// from the pinned reference and its pixel size; a 'f' axis falls back to the
// fraction. The result clamps on-screen so a hard window shrink can't strand
// a pinned box outside. Pure — unit-tested without SDL.
func applyAnchor(ov [4]float64, ar anchorRef, w, h int32) sdl.Rect {
	r0 := fracToRect(ov, ar.winW, ar.winH)
	r := sdl.Rect{X: r0.X, Y: r0.Y, W: r0.W, H: r0.H}
	switch ar.h {
	case anchorLow: // keep X and W as placed
	case anchorHigh:
		r.X = w - (ar.winW - (r0.X + r0.W)) - r0.W
	case anchorMid:
		r.X = w/2 + (r0.X + r0.W/2 - ar.winW/2) - r0.W/2
	default: // fraction axis: today's proportional scaling
		r.X = int32(ov[0] * float64(w))
		r.W = int32(ov[2] * float64(w))
	}
	switch ar.v {
	case anchorLow:
	case anchorHigh:
		r.Y = h - (ar.winH - (r0.Y + r0.H)) - r0.H
	case anchorMid:
		r.Y = h/2 + (r0.Y + r0.H/2 - ar.winH/2) - r0.H/2
	default:
		r.Y = int32(ov[1] * float64(h))
		r.H = int32(ov[3] * float64(h))
	}
	// On-screen clamp (position only — sizes are the user's).
	if r.X+r.W > w {
		r.X = w - r.W
	}
	if r.Y+r.H > h {
		r.Y = h - r.H
	}
	if r.X < 0 {
		r.X = 0
	}
	if r.Y < 0 {
		r.Y = 0
	}
	return r
}

// anchoredRect resolves one slot's override honouring its window pin, if any.
// The no-anchor path is the historical fracToRect — anchored resolution costs
// one extra map lookup, only for slots that HAVE an override.
func (a *App) anchoredRect(name string, ov [4]float64, w, h int32) sdl.Rect {
	if ar, ok := a.classicAnchor[name]; ok {
		return applyAnchor(ov, ar, w, h)
	}
	return fracToRect(ov, w, h)
}

// slotAnchorMode reports a slot's current pin mode string ("" = unpinned).
func (a *App) slotAnchorMode(name string) string {
	ar, ok := a.classicAnchor[name]
	if !ok {
		return ""
	}
	return formatAnchorMode(ar.h, ar.v)
}

// cycleSlotAnchor advances the hovered slot through the A-key pin cycle. A
// slot with no override yet gets one minted from its current on-screen rect
// (pin-in-place without a drag); clearing the pin keeps the override.
func (a *App) cycleSlotAnchor(name string, w, h int32) {
	next := nextAnchorMode(a.slotAnchorMode(name))
	if _, ok := a.classicOv[name]; !ok {
		// Pin-in-place: the anchor re-bases an override, so mint one from the
		// rect the slot drew at this frame.
		info, reg := a.slotReg[name]
		if !reg {
			return
		}
		ov := rectToFrac(info.cur, w, h)
		if a.classicOv == nil {
			a.classicOv = make(map[string][4]float64, classicSlotRegCap)
		}
		a.classicOv[name] = ov
		a.d.Prefs.SetClassicSlot(name, ov)
	}
	if next == "" {
		delete(a.classicAnchor, name)
		a.d.Prefs.ClearClassicAnchor(name)
		a.pushDebug("layout: " + classicSlotLabel(name) + " unpinned")
		return
	}
	hm, vm, _ := parseAnchorMode(next)
	if a.classicAnchor == nil {
		a.classicAnchor = make(map[string]anchorRef, classicSlotRegCap)
	}
	a.classicAnchor[name] = anchorRef{h: hm, v: vm, winW: w, winH: h}
	a.d.Prefs.SetClassicAnchor(name, config.ClassicAnchor{Mode: next, WinW: int(w), WinH: int(h)})
	a.pushDebug("layout: " + classicSlotLabel(name) + " pinned to the " + anchorModeLabel(next))
}

// syncAnchorWindow re-bases a slot's LOCAL anchor to the current window —
// called whenever the editor rewrites the slot's fraction override (the
// override now describes the rect at THIS window size). Persisted at
// drag-release alongside the override.
func (a *App) syncAnchorWindow(name string, w, h int32) {
	if ar, ok := a.classicAnchor[name]; ok {
		ar.winW, ar.winH = w, h
		a.classicAnchor[name] = ar
	}
}

// anchorPinDot is the size of the editor's pin marker on an anchored box.
const anchorPinDot = int32(7)

// drawAnchorPin marks an anchored box's pinned reference in the editor: a
// small filled dot at the pinned corner (or centre; edge-midpoints for
// single-axis pins from hand-edited prefs).
func (a *App) drawAnchorPin(r sdl.Rect, name string) {
	ar, ok := a.classicAnchor[name]
	if !ok {
		return
	}
	px := r.X + r.W/2 - anchorPinDot/2 // frac/mid default: centre
	switch ar.h {
	case anchorLow:
		px = r.X + 1
	case anchorHigh:
		px = r.X + r.W - anchorPinDot - 1
	}
	py := r.Y + r.H/2 - anchorPinDot/2
	switch ar.v {
	case anchorLow:
		py = r.Y + 1
	case anchorHigh:
		py = r.Y + r.H - anchorPinDot - 1
	}
	dot := sdl.Rect{X: px, Y: py, W: anchorPinDot, H: anchorPinDot}
	a.ctx.Fill(dot, ColTierGreen)
	a.ctx.Border(dot, ColBackground)
}
