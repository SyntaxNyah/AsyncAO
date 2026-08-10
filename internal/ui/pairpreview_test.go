package ui

// REGRESSION: the Pairing dialog's sprite preview drew outside its own box.
//
// Live report, 2026-08-10 (screenshot): the ghost sprite rendered enormous and
// spilled off the panel onto the courtroom, the music list and the IC bar. The
// decisive field observation was that setting Offset X to 0 fixed the render —
// and that Offset X was sitting at 53% without the user having typed it.
//
// Two defects, and the second is what supplied the 53:
//
//  1. NO CLIP. The ghost is fitted to the stage HEIGHT with its own aspect ratio
//     (AO2's non-stretch arm, animationlayer.cpp:229-260 — the same rule the
//     live viewport uses), so a wide sprite is legitimately wider than the narrow
//     preview column, and the offset slides it further. AO2 survives that because
//     ui_vp_player_char is a CHILD WIDGET and Qt clips children; nothing clipped
//     here.
//  2. THE RESIZE GRIP DRAGGED THE SPRITE. floatGripSz is 16 and the stage runs to
//     pad (8) short of the panel edge, so the grip's top-left 8x8 quadrant is
//     inside the stage. floatWinResize consumed the press through the shared
//     `pressed` flag, but the ghost editor armed its own drag off raw
//     c.mouseDown — so resizing the Pairing window silently rewrote the pair
//     offsets to wherever the corner had been dragged.

import (
	"testing"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
)

// containedIn reports whether inner sits entirely inside outer.
func containedIn(inner, outer sdl.Rect) bool {
	return inner.X >= outer.X && inner.Y >= outer.Y &&
		inner.X+inner.W <= outer.X+outer.W && inner.Y+inner.H <= outer.Y+outer.H
}

// TestTheGhostOverhangsItsStageAndTheClipCatchesIt is the report, put as the two
// facts that make the fix load-bearing — and only those two, because an earlier
// draft of this sweep asserted neither.
//
//  1. THE PREMISE. The raw sprite rect really does escape the preview column: a
//     wide sprite fitted to the stage HEIGHT is legitimately wider than a narrow
//     column, and the offset slides it further still. That is why a clip is
//     required rather than tidy. Cropping the GEOMETRY instead is refused
//     deliberately — the preview would then lie about what the real viewport is
//     going to show, which is the one job this box has.
//  2. THE CLIP HOLDS. Driven through clipIntersect, which IS pushClip's own
//     arithmetic (ui.go), not a copy of it written here: a nested clip may only
//     ever narrow the region in force, and a disjoint result is clipNowhere.
//     Replace that rule with a plain "the inner rect wins" and this reddens.
//
// What it deliberately does NOT do is assert containment of an intersection with
// the stage, which is true for every input by construction and cannot fail. The
// sprite GEOMETRY is pinned next door (TestPairPreviewFitIsHeightBoundAndCentred)
// and the two brackets by the AST census at the bottom of this file; between the
// three, each part of the fix has a gate that a mutation actually reddens.
func TestTheGhostOverhangsItsStageAndTheClipCatchesIt(t *testing.T) {
	// Stages the pair panel produces at the window sizes the client runs at
	// (pv = the right column below the numeric rows; pairPanelMinW 440 upward).
	stages := []sdl.Rect{
		{X: 300, Y: 200, W: 190, H: 120}, // 720p, panel at its floor
		{X: 320, Y: 220, W: 274, H: 360}, // 1080p, the default 580-wide panel
		{X: 500, Y: 240, W: 420, H: 520}, // a user-widened panel on a big screen
		{X: 0, Y: 0, W: 190, H: 70},      // ghostMinHeightPx, the smallest stage drawn at all
	}
	// Sprite shapes: the AO 4:3 standard, a tall portrait, a widescreen pack, and
	// a square icon.
	sprites := [][2]int32{{256, 192}, {192, 256}, {960, 540}, {512, 512}}
	offsets := []int{-100, -53, 0, 4, 53, 100}

	escaped := 0
	for _, pv := range stages {
		for _, sp := range sprites {
			for _, ox := range offsets {
				for _, oy := range offsets {
					dst := ghostSpriteRect(pv, sp[0], sp[1], ox, oy)
					if !containedIn(dst, pv) {
						escaped++
					}
					// pushClip's arithmetic, driven rather than mirrored.
					painted := clipIntersect(dst, pv, true)
					if painted == clipNowhere {
						continue // slid entirely off the stage (±100% does exactly that): nothing lands, which is correct
					}
					if !containedIn(painted, pv) {
						t.Fatalf("stage %+v sprite %dx%d offset %d,%d: the clip resolved to %+v, "+
							"which is not inside the preview box — pushClip is no longer narrowing",
							pv, sp[0], sp[1], ox, oy, painted)
					}
				}
			}
		}
	}
	if escaped == 0 {
		t.Fatal("stale premise: no sprite in the sweep overhangs its stage any more, so the clip " +
			"this file exists for is guarding nothing — re-derive the case before deleting it")
	}

	// The reported case, measured. A widescreen sprite at the offset the field
	// report found sitting in the box (53%) does not merely peek over the edge: it
	// puts more than a whole stage width of character outside the panel, which is
	// how it reached the courtroom, the music list and the IC bar in the screenshot.
	pv := stages[1] // 1080p, the default 580-wide Pairing panel
	wide := ghostSpriteRect(pv, 960, 540, 53, 0)
	if containedIn(wide, pv) {
		t.Fatalf("the reported shape %+v now fits the stage %+v — the premise is stale", wide, pv)
	}
	if over := (wide.X + wide.W) - (pv.X + pv.W); over < pv.W {
		t.Errorf("the reported shape overhangs by %d px, expected at least a stage width (%d) — "+
			"the numbers behind the screenshot have moved", over, pv.W)
	}
}

// TestPairPreviewFitIsHeightBoundAndCentred pins the geometry itself, so a future
// change to the shout bubble's fit cannot quietly move everybody's pair
// placement — and vice versa. AO2 gives the character layer and the splash layer
// ONE shared height-factor scale, but only the character carries an offset, which
// is exactly why these are two rules in two homes.
func TestPairPreviewFitIsHeightBoundAndCentred(t *testing.T) {
	pv := sdl.Rect{X: 100, Y: 50, W: 200, H: 150}

	// Height-bound: the sprite is always exactly the stage's height, whatever its
	// own proportions, and the width follows the source aspect.
	for _, sp := range [][2]int32{{256, 192}, {960, 540}, {100, 400}} {
		dst := ghostSpriteRect(pv, sp[0], sp[1], 0, 0)
		if dst.H != pv.H {
			t.Errorf("sprite %dx%d: height %d, want the stage height %d", sp[0], sp[1], dst.H, pv.H)
		}
		if want := pv.H * sp[0] / sp[1]; dst.W != want {
			t.Errorf("sprite %dx%d: width %d, want %d (source aspect at the stage height)", sp[0], sp[1], dst.W, want)
		}
		// Centred at zero offset — the property the offset is measured FROM.
		if got, want := dst.X, pv.X+(pv.W-dst.W)/2; got != want {
			t.Errorf("sprite %dx%d: x %d, want %d (centred)", sp[0], sp[1], got, want)
		}
		if dst.Y != pv.Y {
			t.Errorf("sprite %dx%d: y %d, want the stage top %d", sp[0], sp[1], dst.Y, pv.Y)
		}
	}

	// The offset is a PERCENTAGE OF THE STAGE, on each axis independently — the
	// same unit the live viewport applies and the same unit the numeric rows show.
	// A px-vs-percent misread here is what the field clue pointed at.
	base := ghostSpriteRect(pv, 256, 192, 0, 0)
	half := ghostSpriteRect(pv, 256, 192, 50, 50)
	if got, want := half.X-base.X, pv.W/2; got != want {
		t.Errorf("50%% X moved the sprite %d px, want %d (half the STAGE width)", got, want)
	}
	if got, want := half.Y-base.Y, pv.H/2; got != want {
		t.Errorf("50%% Y moved the sprite %d px, want %d (half the STAGE height)", got, want)
	}

	// Degenerate source (a page with no height): answer the origin rather than
	// divide by zero.
	if got := ghostSpriteRect(pv, 256, 0, 20, 20); got.W != 0 || got.H != 0 {
		t.Errorf("a zero-height page = %+v, want an empty rect", got)
	}
}

// TestPairOffsetsDefaultToZero pins the value the field report found at 53%:
// there is no preference, no seed and no migration behind these — a fresh
// session's pair placement is dead centre, and anything else came from a WRITE.
func TestPairOffsetsDefaultToZero(t *testing.T) {
	a := testTabApp(t)
	if a.pairOffX != 0 || a.pairOffY != 0 {
		t.Fatalf("fresh pair offsets = %d,%d, want 0,0", a.pairOffX, a.pairOffY)
	}
	a.resetSessionState()
	if a.pairOffX != 0 || a.pairOffY != 0 {
		t.Fatalf("pair offsets after a session reset = %d,%d, want 0,0", a.pairOffX, a.pairOffY)
	}
}

// TestPairPlacementStillReachesTheWire is the end-to-end half. The report named
// pairing as a whole, not just the preview, so this drives a real send and reads
// the OUTGOING MS back the way a receiving client would rather than assuming the
// send path was fine. Partner, both offsets and the order all have to survive,
// unchanged.
//
// The order values are the repo's documented gotcha — ^0 is the speaker IN
// FRONT — and the sibling gates in icbar_flip_test.go pin order and flip; this
// one exists because the OFFSETS, the field the defect was about, had nothing
// holding them to the wire at all.
func TestPairPlacementStillReachesTheWire(t *testing.T) {
	a, sent := flipSendApp(t)
	a.pairWith = 1
	a.pairOffX, a.pairOffY = -37, 21
	a.pairOrder = protocol.PairSpeakerInFront
	a.sendICFresh()

	msg := lastSentMS(t, a, sent)
	if msg.Pair.CharID != 1 {
		t.Errorf("wire pair partner = %d, want 1", msg.Pair.CharID)
	}
	if msg.Pair.Order != protocol.PairSpeakerInFront {
		t.Errorf("wire pair order = %d, want speaker-in-front (^0)", msg.Pair.Order)
	}
	// The offset is read off the RAW field, not off ParseMS: outgoing MS is
	// asymmetric to incoming (the client never sends other_name / other_emote /
	// other_offset / other_flip — the server injects them), so the self-offset
	// sits at outgoing index 17 and at incoming index 19. Parsing our own packet
	// back would read somebody else's slot, which is a trap worth stating rather
	// than a bug worth "fixing".
	const outgoingSelfOffsetField = 17
	if got := (*sent)[len(*sent)-1].Field(outgoingSelfOffsetField); got != "-37" {
		t.Errorf("wire self offset field = %q, want %q — the pair placement is not reaching the wire", got, "-37")
	}
	// …and on a 2.9 server the VERTICAL half rides with it, in canon's "x&y"
	// spelling. Without y_offset only X is representable, which is why the arm
	// above asserts the bare number.
	a.sess.Features[protocol.FeatureYOffset] = struct{}{}
	a.icInput = "and vertically"
	a.sendICFresh()
	if got := (*sent)[len(*sent)-1].Field(outgoingSelfOffsetField); got != "-37&21" {
		t.Errorf("wire self offset field = %q, want %q (2.9 y_offset)", got, "-37&21")
	}
	// Unpairing clears the partner and leaves the offsets alone — they are YOUR
	// placement, not a property of the pair (AO2 keeps ui_pair_offset_spinbox
	// across an unpair too).
	a.pairWith = protocol.UnpairedCharID
	a.icInput = "solo now"
	a.sendICFresh()
	if msg := lastSentMS(t, a, sent); msg.Pair.Active() {
		t.Errorf("unpaired send still carries a pair block: %+v", msg.Pair)
	}
	if a.pairOffX != -37 || a.pairOffY != 21 {
		t.Errorf("unpairing moved the offsets to %d,%d", a.pairOffX, a.pairOffY)
	}
}

// TestPairGhostClipsAndRefusesTheResizeGrip is the deletion catcher for both
// halves of the fix. Neither has a return value to assert on: a missing clip
// paints over the rest of the screen and a missing grip guard silently rewrites
// a number, and every unit gate above stays green through both.
func TestPairGhostClipsAndRefusesTheResizeGrip(t *testing.T) {
	body := funcBodySource(t, "screens.go", "drawPairGhost")
	if !containsCall(body, "pushClip") {
		t.Error("drawPairGhost does not pushClip — a sprite wider than the preview column " +
			"paints straight over the courtroom (and a raw SetClipRect would not fence the " +
			"hit tests either)")
	}
	if !containsCall(body, "popClip") {
		t.Error("drawPairGhost pushes a clip it never pops — every widget drawn after the " +
			"Pair panel inherits it")
	}
	if !containsCall(body, "pointIn") {
		t.Error("drawPairGhost no longer excludes the resize grip from its drag — resizing " +
			"the Pairing window drags the sprite and rewrites the pair offsets")
	}
	// The grip has to actually REACH the function, or the exclusion tests a rect
	// nobody passed in.
	if !containsCall(funcBodySource(t, "screens.go", "drawPairPanel"), "drawPairGhost") {
		t.Fatal("drawPairPanel no longer draws the ghost editor at all")
	}
}

// TestTheGripOverlapsTheStage is the measurement the guard exists for, kept as a
// gate so a future layout change that moves them apart shows up as a failing
// assumption rather than as dead code. floatGripSz (16) against pad (8) is an
// 8x8 shared corner.
func TestTheGripOverlapsTheStage(t *testing.T) {
	if floatGripSz <= pad {
		t.Skipf("the grip (%d) no longer reaches inside the stage's %d px margin — "+
			"the exclusion is now belt-and-braces", floatGripSz, pad)
	}
	// Panel r, its grip, and the stage's bottom-right corner.
	r := sdl.Rect{X: 100, Y: 100, W: 580, H: 560}
	grip := sdl.Rect{X: r.X + r.W - floatGripSz, Y: r.Y + r.H - floatGripSz, W: floatGripSz, H: floatGripSz}
	pv := sdl.Rect{X: r.X + r.W/2 + pad, Y: r.Y + 300, W: r.W/2 - 2*pad, H: r.Y + r.H - pad - (r.Y + 300)}
	if !pointIn(grip.X, grip.Y, pv) {
		t.Fatalf("the grip's top-left %d,%d is outside the stage %+v — this gate's premise is stale",
			grip.X, grip.Y, pv)
	}
}
