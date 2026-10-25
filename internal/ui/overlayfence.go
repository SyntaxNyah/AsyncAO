package ui

import "github.com/veandco/go-sdl2/sdl"

// Overlay pointer exclusivity (issue #26; a partial, opt-in answer to the open
// issue #37 "buttons over other ui elements do not block mouse input behind them").
// SCOPE — read this before claiming #37 is closed. The registry is consulted in

// overlayFenceCap bounds the registry — hard rule 4, no unbounded anything. It is a
// fixed-size array on Ctx, so publishing never grows a slice and never allocates on
// the render hot path. A publication past the cap is DROPPED rather than growing the
// array: the degraded behaviour is "the click leaks through", which is exactly the
// pre-fence behaviour and is recoverable, whereas an unbounded array on a per-frame
// path is a standing rule violation.
// the sprite-preview box (#37). The cap was FOUR — exactly the live count of the day
// quietly restoring the very click-through issue #26 fixed. Seven leaves room for two
const overlayFenceCap = 7

// fenceOverlay publishes r as an occluding rect for the rest of this pass: every
// hovering()-BASED test inside r reads false, so widgets drawn BENEATH the overlay
// neither highlight nor fire. Raw pointIn() sites are NOT covered (see the SCOPE
// note at the top) — they must ask c.overlayFenced themselves. Call it BEFORE the
// widgets it must occlude are drawn (the registry is consulted at hit-test time,
// and a single-pass kit hit-tests as it draws), and pair the pass with
// overlayFenceMark / overlayFenceRelease.
// An empty rect is ignored: nothing is painted there, so fencing it would only eat
// clicks over live widgets.
func (c *Ctx) fenceOverlay(r sdl.Rect) {
	if r.W <= 0 || r.H <= 0 {
		return
	}
	if c.overlayFenceN >= overlayFenceCap {
		// Count it. A dropped fence is a SILENT input leak — the occluder still paints,
		// the widgets under it still fire — so the only way it ever gets noticed is by
		// being counted and asserted on (BeginFrame clears the counter with the
		// registry; the gates below read it after a full frame).
		c.overlayFenceDrops++
		return
	}
	c.overlayFences[c.overlayFenceN] = r
	c.overlayFenceN++
}

// overlayFenceMark snapshots the registry depth so a pass can release exactly its
// own publications (pushClip/popClip shape). Cheap enough to take unconditionally.
func (c *Ctx) overlayFenceMark() int { return c.overlayFenceN }

// overlayFenceRelease drops every fence published since mark. Safe to call with a
// stale mark (it only ever shrinks the count), so it is safe to defer.
func (c *Ctx) overlayFenceRelease(mark int) {
	if mark >= 0 && mark < c.overlayFenceN {
		c.overlayFenceN = mark
	}
}

// overlayFenced reports whether (x, y) sits under an overlay fence published this
// pass. It is the raw query — the counterpart of pointIn() — for callers that
// hit-test outside hovering(). While an occluder owns the pointer the scan stops at
// the owner's mark, so the owner is never fenced by its own rect (nor by anything
// published after it) but every EARLIER fence still applies.
func (c *Ctx) overlayFenced(x, y int32) bool {
	n := c.overlayFenceN
	if c.overlayOwner && c.overlayOwnerMark < n {
		n = c.overlayOwnerMark
	}
	for i := 0; i < n; i++ {
		if pointIn(x, y, c.overlayFences[i]) {
			return true
		}
	}
	return false
}

// overlayOwnerScope is the saved owner state pushOverlayOwner hands back. A plain
// value (no pointer, no allocation) so the push/pop pair stays free on the render
// path; popOverlayOwner restores it verbatim.
type overlayOwnerScope struct {
	on   bool
	mark int
}

// pushOverlayOwner suspends fences at registry index >= mark for the caller's own
// draw, and returns the previous state; pass that back to popOverlayOwner. An
// occluder must bracket its own widgets with these, or its published rect would
// blank ITSELF — the same self-fencing problem the open dropdown works around with
// raw pointIn().
func (c *Ctx) pushOverlayOwner(mark int) overlayOwnerScope {
	prev := overlayOwnerScope{on: c.overlayOwner, mark: c.overlayOwnerMark}
	// Clamp: a mark taken before a release (or from a latch computed earlier in the
	// frame) must never index past the live registry, and a negative one must not
	// silently mean "suspend everything".
	if mark < 0 {
		mark = 0
	}
	if mark > c.overlayFenceN {
		mark = c.overlayFenceN
	}
	if prev.on && prev.mark < mark {
		mark = prev.mark
	}
	c.overlayOwner, c.overlayOwnerMark = true, mark
	return prev
}

// popOverlayOwner restores what pushOverlayOwner returned.
func (c *Ctx) popOverlayOwner(prev overlayOwnerScope) {
	c.overlayOwner, c.overlayOwnerMark = prev.on, prev.mark
}
