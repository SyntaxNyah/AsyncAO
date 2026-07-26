package ui

import "github.com/veandco/go-sdl2/sdl"

// Overlay pointer exclusivity (issue #26; a partial, opt-in answer to the open
// issue #37 "buttons over other ui elements do not block mouse input behind them").
//
// SCOPE — read this before claiming #37 is closed. The registry is consulted in
// exactly ONE place, hovering() (ui.go), so it covers every hovering()-BASED hit
// test and nothing else. Sites that hit-test with raw pointIn() still see through
// a fence: editOverToolbox, an open dropdown's own list rows, the SFX browser's
// backdrop-close, the layout editors' drag tests, and every ClickedIn/press-origin
// probe that bypasses hovering(). Those are deliberate — several of them EXIST to
// dodge a fence — but a new raw site that ought to yield to chrome must call
// c.overlayFenced(x, y) itself; nothing does it for them.
//
// The kit is immediate-mode and SINGLE-PASS: every widget independently asks
// `c.hovering(rect) && c.clicked`, and c.clicked (ui.go) is a bare per-frame flag,
// not a claimable token. So a widget drawn EARLY cannot know that a widget drawn
// LATER paints over it — the later widget wins the PIXELS, but BOTH win the click.
// Under an AO2 theme the compact toolbox parks on top of the theme's own control
// buttons, so every press on a toolbox chip also pressed the "Call Mod" button
// underneath it.
//
// The house already solves this class by PUBLISHING a rect that everyone else
// consults, and this is the same idea generalised — do not read it as a new
// paradigm. Its siblings:
//   - c.modalOn: an open dropdown (or a layout editor) owns the pointer outright;
//     hovering() returns false for every other widget in the frame.
//   - c.ddOpenList: the open dropdown's list rect, published so boxFencesPointer
//     can keep a list that paints over a floating panel clickable.
//   - c.clipOn / c.clipRect: input must agree with the pixels, so a widget past
//     its clip edge does not hit-test.
// The difference from modalOn is scope: modalOn is all-or-nothing for the whole
// frame, whereas an overlay fence blanks input only under the RECT the occluder
// actually paints, which is what a piece of always-on chrome (the toolbox today,
// the menu bar next) needs — the rest of the screen must stay live.
//
// OWNERSHIP / RESET DISCIPLINE. app.go warns that an un-released modalOn FREEZES
// the UI; this registry is deliberately built so that failure mode cannot happen:
//
//   - Publication is FRAME-SCOPED and self-healing. BeginFrame zeroes the count
//     unconditionally, so a publisher that returns early — or a pass that unwinds
//     past its release — can never strand a fence into the next frame. That is the
//     one behavioural difference from modalOn, which persists ON PURPOSE (it has to
//     survive the frame that closes the dropdown) and therefore must be hand-released.
//   - Within a frame it is PASS-SCOPED through overlayFenceMark / overlayFenceRelease,
//     the same push/pop shape as pushClip / popClip. A pass takes a mark before it
//     publishes and releases back to it on the way out, because anything drawn AFTER
//     the pass (the post-courtroom floating panels, palette and pickers in app.go)
//     paints ABOVE the occluder and must not inherit its fence.
//   - The occluder hit-tests its OWN widgets from inside pushOverlayOwner /
//     popOverlayOwner. This is exactly the problem an open dropdown has — its own
//     modal fence would blank it too — and it is solved the same way: the dropdown
//     drops to the raw pointIn() test for its list rows. Suspending is the scoped
//     equivalent of that, so an occluder assembled out of ordinary kit widgets
//     (Button / Tooltip / Checkbox) keeps working with no call-site changes.
//     The suspend is MARK-SCOPED, not global, and that distinction is load-bearing:
//     an occluder must stop being fenced by ITS OWN rect without also becoming
//     immune to everyone else's. Registry order IS paint order (a fence is published
//     immediately before the widgets it covers are drawn), so "published before mine"
//     means "painted under mine" and must still fence me; only my own publication
//     and anything after it is suspended. Without that, the app-wide menu bar's open
//     dropdown — published at App level, before the screen dispatch — would stop
//     occluding the compact toolbox, which brackets its own draw: the same
//     click-through bug one level up with the roles swapped.
//   - Publish the rect that is actually PAINTED, never a larger "safety" region: a
//     fence that disagrees with the draw eats clicks over empty pixels, the mirror
//     image of the leak it is fixing (the same draw-vs-fence agreement rule
//     toolboxPiecesRect and compactToolboxStripRect already exist to keep).

// overlayFenceCap bounds the registry — hard rule 4, no unbounded anything. It is a
// fixed-size array on Ctx, so publishing never grows a slice and never allocates on
// the render hot path. Four is already generous for what can legitimately occlude a
// single pass: the app-wide menu bar strip, one open menu dropdown, and the compact
// toolbox strip, with one spare. A publication past the cap is DROPPED rather than
// growing the array: the degraded behaviour is then "the click leaks through", which
// is exactly today's behaviour and recoverable, whereas an unbounded array on a
// per-frame path is a standing rule violation.
const overlayFenceCap = 4

// fenceOverlay publishes r as an occluding rect for the rest of this pass: every
// hovering()-BASED test inside r reads false, so widgets drawn BENEATH the overlay
// neither highlight nor fire. Raw pointIn() sites are NOT covered (see the SCOPE
// note at the top) — they must ask c.overlayFenced themselves. Call it BEFORE the
// widgets it must occlude are drawn (the registry is consulted at hit-test time,
// and a single-pass kit hit-tests as it draws), and pair the pass with
// overlayFenceMark / overlayFenceRelease.
//
// An empty rect is ignored: nothing is painted there, so fencing it would only eat
// clicks over live widgets.
func (c *Ctx) fenceOverlay(r sdl.Rect) {
	if r.W <= 0 || r.H <= 0 {
		return
	}
	if c.overlayFenceN >= overlayFenceCap {
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
//
// mark is the registry depth the occluder took IMMEDIATELY BEFORE publishing its
// own rect (overlayFenceMark), so "index >= mark" is exactly "mine, and anything
// published after mine". An occluder that publishes NOTHING passes the CURRENT
// depth, which suspends nothing at all — it has no rect of its own to be blanked
// by, and it must still yield to whatever is painted above it.
//
// Nested/re-entrant safe in both directions: the previous value is restored rather
// than force-cleared, and an inner owner can only ever suspend MORE than the outer
// one (it takes the smaller mark), because it draws inside the outer occluder and
// therefore above everything the outer one already ignores.
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
