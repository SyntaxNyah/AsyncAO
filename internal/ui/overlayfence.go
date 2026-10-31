package ui

import (
	"github.com/SyntaxNyah/AsyncAO/internal/helper/value"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

// Overlay pointer exclusivity. Immediate-mode widgets each ask `c.hovering(rect) && c.clicked` in a single pass,
// so a widget drawn early cannot know a later one paints over it: both win the click. An occluder publishes the rect
// it paints, and hovering() reads false there, covering only that rect, unlike modalOn which owns the whole frame.
// Only hovering() consults this: raw pointIn() sites see through it,
// and one that should yield must call c.overlayFenced.
// BeginFrame zeroes the count, so a publisher that returns early can't strand a rect into the next frame.
// Within a frame, a pass releases back to its mark, because whatever draws after it paints above the occluder.
// An occluder's own widgets hit-test inside pushOverlayOwner/popOverlayOwner, which is mark-scoped: registry order is
// paint order, so rects published before mine must still block me; a global suspend would bring the click-through back.
// Publish exactly the painted rect: a larger one eats clicks over empty pixels.

// overlayFenceCap bounds the registry (hard rule 4), a fixed array on Ctx, so publishing never allocates.
// Five publishers can be live at once, and courtroom-pass ones publish last, so overflow would drop those; overflow
// leaks the click and increments overlayFenceDrops.
const overlayFenceCap = 7

// fenceOverlay publishes r for the rest of this pass; call it before drawing what it occludes, since this kit
// hit-tests as it draws. Empty rects are ignored.
func (c *Ctx) fenceOverlay(r sdl.Rect) {
	if r.W <= 0 || r.H <= 0 {
		return
	}
	if c.overlayFenceN >= overlayFenceCap {
		// A dropped rect silently leaks input, so it is counted; BeginFrame clears the counter with the registry.
		c.overlayFenceDrops++
		return
	}
	c.overlayFences[c.overlayFenceN] = r
	c.overlayFenceN++
}

// overlayFenceMark snapshots the registry depth so a pass can release only its own publications.
func (c *Ctx) overlayFenceMark() int { return c.overlayFenceN }

// overlayFenceRelease drops everything published since mark;
// a stale mark only shrinks the count, so it is safe to defer.
func (c *Ctx) overlayFenceRelease(mark int) {
	if mark >= 0 && mark < c.overlayFenceN {
		c.overlayFenceN = mark
	}
}

// overlayFenced is the raw counterpart of pointIn; an owner is not blocked by its own rect or later ones, only earlier.
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

// overlayOwnerScope is the saved owner state, a plain value so the push and pop don't allocate.
type overlayOwnerScope struct {
	on   bool
	mark int
}

// pushOverlayOwner suspends rects at index >= mark for the caller's draw, or an occluder would blank itself.
// mark is the depth taken just before publishing; an occluder publishing nothing passes the current depth.
// Nesting restores the previous value, and an inner owner takes the smaller mark as it draws inside the outer.
func (c *Ctx) pushOverlayOwner(mark int) overlayOwnerScope {
	prev := overlayOwnerScope{on: c.overlayOwner, mark: c.overlayOwnerMark}
	// A stale mark must not index past the live registry, and a negative one must not mean suspend everything.
	mark = value.Clamp(mark, 0, c.overlayFenceN)
	if prev.on && prev.mark < mark {
		mark = prev.mark
	}
	c.overlayOwner, c.overlayOwnerMark = true, mark
	return prev
}

func (c *Ctx) popOverlayOwner(prev overlayOwnerScope) {
	c.overlayOwner, c.overlayOwnerMark = prev.on, prev.mark
}
