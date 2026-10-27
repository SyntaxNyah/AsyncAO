package ui

// CHATBOX OVERFLOW: the crawl kept revealing text below the visible box.
//
// Live report, 2026-08-10 (repro "a\nb\nc\nd", screenshot): a message taller
// than the chatbox is cut off with no way to see the rest, and the typewriter
// carries on typing into the void underneath.
//
// This is a PRE-EXISTING gap that AO2's row pitch (b44b186) made easy to hit —
// the rows got taller, so fewer of them fit — rather than a defect that commit
// introduced. Canon has always followed the crawl: chat_tick moves the cursor to
// the revealed position and calls ensureCursorVisible on every tick
// (courtroom.cpp:4514-4521), with both scrollbars policy-hidden (:99-100), so
// AO2 shows no bar and never loses the line either.
//
// The gates below drive the real raster where they can and the pure arithmetic
// where a raster needs a font.

import (
	"testing"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/render"
)

// TestAFittingMessageNeverScrolls is the no-op half, and it is the one that
// matters for everybody: the overwhelming majority of AO posts fit, and they
// must draw from exactly the origin they always did. An exact zero, not a small
// number — this feeds a device-exact blit.
func TestAFittingMessageNeverScrolls(t *testing.T) {
	const lineH, availH = 20, 100 // five rows fit
	for row := 0; row < 5; row++ {
		if got := chatCrawlScrollPx(row, lineH, availH); got != 0 {
			t.Errorf("row %d of 5 visible: scroll = %d, want an exact 0", row, got)
		}
	}
	// Degenerate inputs are no-ops too, so a raster that has not been built yet
	// (or a zero-height box mid-resize) can never shift the text.
	for _, tc := range [][3]int32{{3, 0, 100}, {3, 20, 0}, {-1, 20, 100}, {0, 0, 0}} {
		if got := chatCrawlScrollPx(int(tc[0]), tc[1], tc[2]); got != 0 {
			t.Errorf("chatCrawlScrollPx(%d, %d, %d) = %d, want 0", tc[0], tc[1], tc[2], got)
		}
	}
	if got := chatCrawlOffset(nil, 40, 100); got != 0 {
		t.Errorf("a nil raster = %d, want 0", got)
	}
}

// TestSixLinesInAThreeLineBoxKeepTheRevealInView is the report: at EVERY step of
// the crawl the row being revealed has to be inside the box.
//
// It also pins Qt's MINIMUM-movement rule — ensureCursorVisible scrolls just far
// enough, so the reader keeps as much earlier text as the box can hold and the
// view advances one row at a time instead of jumping to the end.
func TestSixLinesInAThreeLineBoxKeepTheRevealInView(t *testing.T) {
	const lineH = 20
	const availH = 3 * lineH // a three-row box
	const rows = 6

	for row := 0; row < rows; row++ {
		off := chatCrawlScrollPx(row, lineH, availH)
		top := int32(row)*lineH - off      // the revealing row's top, relative to the box
		bottom := int32(row+1)*lineH - off // …and its bottom
		if top < 0 || bottom > availH {
			t.Fatalf("row %d draws at [%d,%d) in a box of %d — the crawl is outside the visible area",
				row, top, bottom, availH)
		}
		if off < 0 {
			t.Fatalf("row %d scrolled BACKWARD by %d", row, off)
		}
	}
	// Minimum movement, row by row: rows 0-2 fit, then one row of scroll each.
	for row, want := range map[int]int32{0: 0, 1: 0, 2: 0, 3: 20, 4: 40, 5: 60} {
		if got := chatCrawlScrollPx(row, lineH, availH); got != want {
			t.Errorf("row %d: scroll = %d, want %d (ensureCursorVisible moves the minimum)", row, got, want)
		}
	}
}

// TestCrawlRowFollowsTheRevealedRune pins the COUNT-to-index step and the
// no-op boundaries. The row lookup itself lives on the raster (RowOfRune) and is
// driven against real line data by internal/render's own gates — this is the
// half that belongs here: `visible` is a count, so the last drawn rune is at
// index visible-1, and 0 or 1 of them means the top of the box.
func TestCrawlRowFollowsTheRevealedRune(t *testing.T) {
	if got := crawlRow(nil, 99); got != 0 {
		t.Fatalf("a nil raster = row %d, want 0", got)
	}
	m := &render.MessageRaster{}
	for _, visible := range []int{0, 1, -3} {
		if got := crawlRow(m, visible); got != 0 {
			t.Errorf("visible=%d: row %d, want 0 (the top of the box)", visible, got)
		}
	}
}

// TestTheScrolledMessageNeverPaintsOnTheNamePlate is the SECOND report off the
// same repro, and it is a defect the scroll itself introduced: with the message
// drawn from textOrigin-offset but the clip still set to the whole chatbox, the
// rows that scrolled out the top landed on the showname strip. "a\nb\nc\nd"
// produces about one row of scroll, and one row (>=20 px at AO2's pitch) is nearly
// as tall as the 22 logical px the classic overlay gives the plate.
//
// The gate measures the TOP EDGE OF THE PAINT, which nothing did before: the
// topmost row's origin against the clip that is actually in force. It asserts the
// premise as well as the fix — that the unbounded origin really does reach into
// the name band — so it cannot pass by clipping something that was never in
// danger.
func TestTheScrolledMessageNeverPaintsOnTheNamePlate(t *testing.T) {
	// The classic overlay's real geometry (screens.go): the name inks at
	// box.Y+chatOverlayNameY and the message starts at box.Y+chatBoxTopStrip, so
	// [chatOverlayNameY, chatBoxTopStrip) is the plate band. Two rows of room.
	const lineH, rows = 20, 4
	box := sdl.Rect{X: 40, Y: 100, W: 300, H: chatBoxTopStrip + 2*lineH + chatBoxBottomPad}
	textTop := box.Y + chatBoxTopStrip
	availH := box.H - chatBoxTopStrip
	// The plate band, straight off the constants the overlay inks the name with:
	// its bottom edge IS the text origin, which is what makes any scroll past the
	// origin a scroll onto the name.
	plate := sdl.Rect{X: box.X, Y: box.Y + chatOverlayNameY, W: box.W, H: chatBoxTopStrip - chatOverlayNameY}
	if plate.H <= 0 {
		t.Fatalf("stale premise: the showname band is %d px tall — there is nothing above the "+
			"text origin to paint over", plate.H)
	}
	plateBottom := plate.Y + plate.H
	clip := chatCrawlClip(box, textTop, nil) // nil = the scrolling raster painter is live

	// THE PREMISE. Somewhere in this crawl the unbounded draw origin climbs into
	// the plate band — that is why a clip is needed at all. If a future layout
	// makes this impossible the gate says "stale premise", not "passed".
	reached := false
	for row := 0; row < rows; row++ {
		if textTop-chatCrawlScrollPx(row, lineH, availH) < plateBottom {
			reached = true
		}
	}
	if !reached {
		t.Fatalf("stale premise: a %d-row message in a %d px box never scrolls its first row "+
			"above the text origin, so this gate is measuring nothing", rows, availH)
	}

	// THE FIX. At every step of the crawl the painted top edge — the topmost row's
	// origin, cut by the clip in force — is at or below the text origin.
	for row := 0; row < rows; row++ {
		off := chatCrawlScrollPx(row, lineH, availH)
		rowTop := textTop - off // row 0 is the topmost thing the raster draws
		if painted := max(rowTop, clip.Y); painted < textTop {
			t.Errorf("row %d revealing: paint starts at y=%d, above the text origin %d — "+
				"%d px of message is on the showname plate", row, painted, textTop, textTop-painted)
		}
	}
	// …and the clip cannot reach the plate for any other reason either.
	if clip.Y < plateBottom {
		t.Errorf("message clip top = %d, want >= %d (the showname band is off limits)", clip.Y, plateBottom)
	}
	// The other three edges are the box's, unchanged: themes ship message rects
	// that overhang their own chatbox and must still draw in full.
	if clip.X != box.X || clip.W != box.W || clip.Y+clip.H != box.Y+box.H {
		t.Errorf("message clip = %+v, want the box %+v with only its top raised", clip, box)
	}
}

// TestChatCrawlClipRaisesOnlyTheTop is the arithmetic on its own, including the
// two ends a chatbox really hits mid-resize and under a theme whose message
// origin sits outside its own chatbox.
func TestChatCrawlClipRaisesOnlyTheTop(t *testing.T) {
	box := sdl.Rect{X: 10, Y: 20, W: 200, H: 100}

	got := chatCrawlClip(box, 46, nil)
	if want := (sdl.Rect{X: 10, Y: 46, W: 200, H: 74}); got != want {
		t.Errorf("chatCrawlClip(%+v, 46) = %+v, want %+v", box, got, want)
	}
	// A text origin at or above the box top is the unscrolled classic case: the box
	// back, untouched, so an ordinary post draws byte-identically to before.
	for _, top := range []int32{box.Y, box.Y - 1, -1000} {
		if got := chatCrawlClip(box, top, nil); got != box {
			t.Errorf("chatCrawlClip(box, %d) = %+v, want the box unchanged", top, got)
		}
	}
	// An origin at or below the bottom edge has no room at all. It must never
	// answer an empty rect: SDL reads w<=0||h<=0 as "clipping OFF", which would
	// hand an overhanging theme the whole window (see clipNowhere).
	for _, top := range []int32{box.Y + box.H, box.Y + box.H + 40} {
		got := chatCrawlClip(box, top, nil)
		if got != clipNowhere {
			t.Errorf("chatCrawlClip(box, %d) = %+v, want clipNowhere", top, got)
		}
		if got.W <= 0 || got.H <= 0 {
			t.Errorf("chatCrawlClip(box, %d) = %+v — SDL reads that as clipping DISABLED", top, got)
		}
	}
}

// TestAnimatedSpansKeepTheirTopPixels is the field-8 reviewer's deferred defect,
// pinned from both ends at once.
//
// THE DEFECT. A wave/shake/bounce span DISPLACES its glyphs off the layout row
// (render/animtext.go: dy = round(sin·waveAmpPx), so ±3 px, and the shake and
// bounce offsets are the same order). On the FIRST row the layout origin IS the
// text origin, so a clip raised to that origin cuts the crests — 2-3 px off the
// top of every animated span in the first line of every post that wears one.
//
// THE FIX has to be exactly as narrow as the defect, which is why both halves
// are here: the animated painter gets the whole box back, and a SCROLLED
// (non-animated) message still may not reach the name plate. A remedy that
// answered `box` unconditionally would pass the first half and fail the second.
func TestAnimatedSpansKeepTheirTopPixels(t *testing.T) {
	// The classic overlay's real geometry, same as the name-plate gate above.
	const lineH, rows = 20, 6
	box := sdl.Rect{X: 40, Y: 100, W: 300, H: chatBoxTopStrip + 2*lineH + chatBoxBottomPad}
	textTop := box.Y + chatBoxTopStrip

	// THE PREMISE: there really is room above the text origin inside the box, so
	// "keep the box" is a change that can be seen. Without it this gate would pass
	// on a layout whose text starts flush with the chatbox top.
	if textTop <= box.Y {
		t.Fatalf("stale premise: the text origin (%d) is at or above the box top (%d), so a raised "+
			"clip could never shave anything", textTop, box.Y)
	}

	// THE FIX. The animated painter's clip is the whole box, so a glyph lifted the
	// full wave amplitude above the first row still paints. A zero-valued layout is
	// the honest stand-in: chatCrawlClip asks only whether the #M5 painter EXISTS,
	// never what it holds — which is why the parameter is the painter itself.
	animClip := chatCrawlClip(box, textTop, &render.AnimatedText{})
	if animClip != box {
		t.Fatalf("the #M5 clip = %+v, want the whole box %+v — an animated span on the first row "+
			"loses its crest to the raised edge", animClip, box)
	}
	// Measured against the amplitude the renderer actually uses: the topmost pixel a
	// first-row wave can reach must be inside the clip. maxAnimLiftPx is this
	// package's copy of that budget (render owns the offsets; a UI gate cannot
	// import the private constant, so the number is named and justified here).
	if lifted := textTop - maxAnimLiftPx; lifted < animClip.Y {
		t.Errorf("a first-row span lifted %d px reaches y=%d, above the clip top %d — %d px shaved",
			maxAnimLiftPx, lifted, animClip.Y, animClip.Y-lifted)
	}

	// THE OTHER HALF, unchanged: a scrolled RASTER message still never paints on
	// the plate. Same walk as TestTheScrolledMessageNeverPaintsOnTheNamePlate, run
	// here so the two halves of the branch are asserted against each other.
	availH := box.H - chatBoxTopStrip
	rasterClip := chatCrawlClip(box, textTop, nil)
	if rasterClip.Y != textTop {
		t.Fatalf("the raster clip top = %d, want the text origin %d — the scrolled-off rows are "+
			"back on the showname plate", rasterClip.Y, textTop)
	}
	scrolled := false
	for row := 0; row < rows; row++ {
		off := chatCrawlScrollPx(row, lineH, availH)
		if off > 0 {
			scrolled = true
		}
		if painted := max(textTop-off, rasterClip.Y); painted < textTop {
			t.Errorf("row %d: a scrolled raster paints from y=%d, above the text origin %d",
				row, painted, textTop)
		}
	}
	if !scrolled {
		t.Fatalf("stale premise: a %d-row message in a %d px box never scrolls, so the "+
			"still-clipped half of this gate measures nothing", rows, availH)
	}
}

// maxAnimLiftPx is the largest number of pixels an #M5 span can lift a glyph
// ABOVE its layout row. render/animtext.go's displacing effects are wave
// (round(sin·2.6) → 3), bounce (round(·3.0) → 3) and shake (round(sin·1.6) → 2),
// so 3 is the budget — and this gate is what fails if a future effect grows a
// taller lift than the clip branch was justified against.
const maxAnimLiftPx = 3

// TestTheAnimatedExemptionKnowsWhichWayRoundItIs is the polarity gate, and it is
// deliberately a BEHAVIOURAL one on the seam rather than a source check at the
// sites: the decision has exactly one home now, so this drives that home with
// both shapes it can be handed and asserts the two answers are the two DIFFERENT
// rects — which is what an inverted predicate cannot satisfy.
//
// Its sibling gates assert the two answers are each CORRECT (the whole box for
// the #M5 painter, the raised edge for the raster). This one asserts they are
// not the same rect, so a branch that collapsed to one answer — `anim == nil`,
// `true ||`, the term deleted — fails here with a message that names the
// polarity instead of only the arm that happened to be checked first.
func TestTheAnimatedExemptionKnowsWhichWayRoundItIs(t *testing.T) {
	// A box whose text origin is genuinely inside it, or both arms would answer
	// the box and the difference this gate measures would not exist.
	box := sdl.Rect{X: 40, Y: 100, W: 300, H: 120}
	textTop := box.Y + chatBoxTopStrip
	if textTop <= box.Y || textTop >= box.Y+box.H {
		t.Fatalf("stale premise: a text origin of %d in a box of [%d,%d) leaves no raised edge to tell apart",
			textTop, box.Y, box.Y+box.H)
	}
	anim := chatCrawlClip(box, textTop, &render.AnimatedText{})
	raster := chatCrawlClip(box, textTop, nil)
	if anim == raster {
		t.Fatalf("both painters get %+v — the exemption is not branching at all", anim)
	}
	if anim != box {
		t.Errorf("the #M5 painter gets %+v, want the whole box %+v — the polarity is inverted: the "+
			"painter that lifts glyphs off their row is the one being clipped", anim, box)
	}
	if raster.Y != textTop {
		t.Errorf("the raster painter gets a clip topped at %d, want the text origin %d — the polarity "+
			"is inverted: a scrolled ordinary post paints straight onto the showname plate", raster.Y, textTop)
	}
}
