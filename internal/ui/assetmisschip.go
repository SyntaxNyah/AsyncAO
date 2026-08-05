package ui

import (
	"strconv"
	"strings"
	"time"

	"github.com/veandco/go-sdl2/sdl"
)

// GH #27 — the missing-asset report gets a surface of its own.
//
// It used to ride warnLine, the client's ONE shared single-line toast channel,
// which every screen paints as bare ColDanger glyphs directly over whatever is
// underneath: on the classic courtroom at oocY-20 (15 px into the last row of
// emote buttons), and on an AO2 theme at h-44 — i.e. INSIDE the theme's design
// canvas, over music_list / ao2_ooc_chat, which the #21 rule forbids outright.
// It also carried the full base URL including the mount pseudo-origin, and
// re-stamped its 12 s timer on every warning with no dedup, so a source that
// re-fires faster than that made the red line permanent — and warnActive() is in
// the full-rate gate, so a permanent banner also pinned the frame pacer.
//
// The replacement is a compact right-aligned chip inside the app's ALREADY
// RESERVED menu-bar band: no new reserved space, no reflow, no layout-cache
// churn, identical on AO2 themes and native layouts, and outside every design
// canvas by construction. The chip says only how many assets are missing; the
// F8 Debug ▸ Log tab keeps the full detail (pushDebug is unconditional and
// un-gated, complete with the tried-extension list and the ×N collapse), and
// clicking the chip goes straight there.

const (
	// assetMissDedupCap bounds the recently-warned ring. Small on purpose: it is
	// a burst suppressor, not a history — the debug log is the history. A scene
	// re-demands a handful of bases (background, desk, two sprite layers, blips,
	// chatbox skin), so this comfortably covers one room's worth of churn.
	assetMissDedupCap = 16
	// assetMissRepeatCooldown is how long one base stays "already reported": a
	// repeat inside the window still reaches the debug log (with its ×N collapse)
	// but does not re-arm the chip. Comfortably longer than the heal cadence
	// (charIconRetryInterval) that produced the observed ×5..×9 runs.
	assetMissRepeatCooldown = 60 * time.Second
	// assetMissChipShowDuration is how long the chip lingers after the most
	// recent NEW miss. Long enough to notice on the way past, short enough that a
	// one-off sparse-pack gap doesn't leave a permanent badge in the chrome.
	assetMissChipShowDuration = 30 * time.Second
	// assetMissChipPadX is the horizontal padding inside the chip's plate.
	assetMissChipPadX = int32(8)
	// assetMissChipMarginX insets the chip from the window's right edge, matching
	// the breathing room the menu titles get on the left (menuBarPadX).
	assetMissChipMarginX = int32(4)
	// assetMissChipInsetY keeps the plate clear of the strip's top edge and of
	// the hairline along its bottom (menuBarHairlineH).
	assetMissChipInsetY = int32(2)
	// assetMissChipMinGapX is the clearance the chip demands from the last menu
	// title. Below it the chip is dropped for the frame rather than drawn over a
	// title — chrome must never lie about what is clickable.
	assetMissChipMinGapX = int32(12)
)

// assetMissRing is the fixed-size "recently reported" set. A plain array, so it
// allocates nothing and cannot grow (hard rule 4).
type assetMissRing struct {
	base [assetMissDedupCap]string
	at   [assetMissDedupCap]time.Time
	next int
}

// seen reports whether base was reported within cooldown, and records it when it
// was not. now is passed in so tests drive the clock.
func (r *assetMissRing) seen(base string, now time.Time, cooldown time.Duration) bool {
	for i := range r.base {
		if r.base[i] == base {
			if now.Sub(r.at[i]) < cooldown {
				return true
			}
			r.at[i] = now // stale entry: refresh in place, keep the slot
			return false
		}
	}
	r.base[r.next] = base
	r.at[r.next] = now
	r.next = (r.next + 1) % assetMissDedupCap
	return false
}

// noteAssetMiss is the asset-warning half of drainWarnings: it counts the miss,
// dedups it against the recent ring and arms the chip. It never touches warnLine
// — ordinary toasts keep that channel to themselves.
//
// deskOnlyMiss is reported but never counted toward the chip: a background that
// ships no desk image for a position is EXPECTED AO2 behaviour (set_scene simply
// hides the desk layer, ../AO2-Client/src/courtroom.cpp:4628-4634), so it is not
// a fault and must not raise a user-facing error.
func (a *App) noteAssetMiss(base string, deskOnly bool, now time.Time) {
	if deskOnly {
		return
	}
	if a.assetMissSeen.seen(base, now, assetMissRepeatCooldown) {
		return // still inside the cooldown: the debug log already has it
	}
	a.assetMissCount++
	a.assetMissAt = now
	// The tooltip string is built HERE, not in the draw: a warning arrives at
	// most once per base per cooldown, whereas the chip draws every frame.
	a.assetMissTip = "Missing: " + assetMissTail(base) +
		"\nClick for the full list, with the extensions tried (Debug ▸ Log)."
}

// assetMissChipActive reports whether the chip should draw/take input this frame.
// Gated on the same preference the old banner used (default OFF), so a player who
// deliberately silenced missing-asset reporting keeps it silenced.
func (a *App) assetMissChipActive() bool {
	return a.assetMissCount > 0 && a.d.Prefs.AssetWarningsOn() &&
		a.now().Sub(a.assetMissAt) < assetMissChipShowDuration
}

// assetMissChipLabel is the chip's text. Built only while the chip is live, and
// only when the count changed, so a settled frame stays allocation-free.
func (a *App) assetMissChipLabel() string {
	if a.assetMissLabelN != a.assetMissCount {
		n := strconv.Itoa(a.assetMissCount)
		if a.assetMissCount == 1 {
			a.assetMissLabel = "! 1 asset missing"
		} else {
			a.assetMissLabel = "! " + n + " assets missing"
		}
		a.assetMissLabelN = a.assetMissCount
	}
	return a.assetMissLabel
}

// assetMissChipRect is the chip's rect inside the menu-bar band, right-aligned.
// ok is false when the bar is not painting its band this frame (a layout editor
// owns the top band whole), or when the menu titles have grown far enough right
// that the chip would sit on one of them.
//
// menuBarPaints is deliberately the SAME predicate menuBarFrame gates its own
// input on, so the chip is live exactly when the titles beside it are.
//
// Input and draw call this at two different points of one frame (pre-screen vs
// drawMenuBar phase 2), so the two answers can differ by a frame in both
// directions — a miss that arrives after the input pass draws before it is
// clickable, and the click that zeroes assetMissCount suppresses the same frame's
// draw. Both are self-correcting within one frame and neither leaves live chrome
// unclickable at rest, which is the property that matters here.
func (a *App) assetMissChipRect(w int32) (sdl.Rect, bool) {
	if !a.menuBarPaints() {
		return sdl.Rect{}, false
	}
	cw := a.ctx.TextWidth(a.assetMissChipLabel()) + 2*assetMissChipPadX
	r := sdl.Rect{
		// Anchored to menuBarRightContentLeft, not to `w`: the theme-sidecar refusal
		// chip (themeerrchip.go) is right-anchored OUTSIDE this one, and two chips
		// both measured from the window edge would draw on top of each other. With no
		// theme error on screen that helper returns `w` and this is unchanged.
		X: a.menuBarRightContentLeft(w) - cw - assetMissChipMarginX,
		Y: assetMissChipInsetY,
		W: cw,
		H: menuBarH - menuBarHairlineH - 2*assetMissChipInsetY,
	}
	// The clearance is measured against everything LEFT-anchored in the band, not
	// against the menu titles alone: the Iniswap chip sits after them (iniswapchip.go)
	// and is a permanent control, so a transient badge must give way to it rather than
	// paint over it.
	if r.X < a.menuBarLeftContentRight(w)+assetMissChipMinGapX {
		return sdl.Rect{}, false
	}
	return r, true
}

// handleAssetMissChipInput consumes a click on the chip before the screens see
// it. Runs pre-screen for the same reason handleUpdateInput does: the chip paints
// over the band the screens offset below, so its click must be claimed first.
func (a *App) handleAssetMissChipInput(w int32) {
	if !a.assetMissChipActive() {
		return
	}
	r, ok := a.assetMissChipRect(w)
	if !ok {
		return
	}
	c := a.ctx
	if !c.hovering(r) {
		return
	}
	c.Tooltip(r, a.assetMissTip)
	if !c.clicked {
		return
	}
	c.clicked = false
	a.showDebugPanel = true
	a.debugSection = assetMissDebugLogTab()
	a.assetMissCount = 0 // acknowledged: the log owns the detail from here
}

// assetMissDebugLogTab resolves the Debug panel's "Log" tab index by NAME, so a
// reordered debugSections table can't silently send the chip to the wrong tab.
func assetMissDebugLogTab() int {
	for i, name := range debugSections {
		if name == "Log" {
			return i
		}
	}
	return 0
}

// drawAssetMissChip paints the chip. Called from drawMenuBar (phase 2), so it
// lands on top of the band it shares with the menu titles.
func (a *App) drawAssetMissChip(w int32) {
	if !a.assetMissChipActive() {
		return
	}
	r, ok := a.assetMissChipRect(w)
	if !ok {
		return
	}
	c := a.ctx
	// A filled plate, not bare glyphs over art — that was half of what made the
	// old banner read as splatter.
	c.Fill(r, ColPanelHi)
	c.Border(r, ColDanger)
	c.Label(r.X+assetMissChipPadX, r.Y+a.menuBarTextY(r.H), a.assetMissChipLabel(), ColDanger)
}

// assetMissTail trims a missing-asset base to the part a human can act on: the
// last two path segments. The full URL — origin, mount pseudo-host and all — is
// what made the old banner unreadable, and it is already in the debug log.
func assetMissTail(base string) string {
	trimmed := strings.TrimSuffix(base, "/")
	cut := strings.LastIndexByte(trimmed, '/')
	if cut <= 0 {
		return trimmed
	}
	if head := strings.LastIndexByte(trimmed[:cut], '/'); head >= 0 {
		return trimmed[head+1:]
	}
	return trimmed[cut+1:]
}
