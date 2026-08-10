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
	"go/ast"
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
	clip := chatCrawlClip(box, textTop)

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
		if painted := max32(rowTop, clip.Y); painted < textTop {
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

	got := chatCrawlClip(box, 46)
	if want := (sdl.Rect{X: 10, Y: 46, W: 200, H: 74}); got != want {
		t.Errorf("chatCrawlClip(%+v, 46) = %+v, want %+v", box, got, want)
	}
	// A text origin at or above the box top is the unscrolled classic case: the box
	// back, untouched, so an ordinary post draws byte-identically to before.
	for _, top := range []int32{box.Y, box.Y - 1, -1000} {
		if got := chatCrawlClip(box, top); got != box {
			t.Errorf("chatCrawlClip(box, %d) = %+v, want the box unchanged", top, got)
		}
	}
	// An origin at or below the bottom edge has no room at all. It must never
	// answer an empty rect: SDL reads w<=0||h<=0 as "clipping OFF", which would
	// hand an overhanging theme the whole window (see clipNowhere).
	for _, top := range []int32{box.Y + box.H, box.Y + box.H + 40} {
		got := chatCrawlClip(box, top)
		if got != clipNowhere {
			t.Errorf("chatCrawlClip(box, %d) = %+v, want clipNowhere", top, got)
		}
		if got.W <= 0 || got.H <= 0 {
			t.Errorf("chatCrawlClip(box, %d) = %+v — SDL reads that as clipping DISABLED", top, got)
		}
	}
}

// TestBothChatboxesClipAtTheTextOrigin is the deletion catcher for the top bound.
// Two painters again, and neither has a return value: a clip set to the wrong
// rect just paints over the name.
//
// It follows the BINDING rather than a literal, so renaming the variable keeps
// the gate honest, and it checks both places the clip is handed over — the
// renderer's scratch rect and DrawScaled's re-assert argument, which the
// device-exact bracket sets a SECOND time at 1:1 (render/text.go). Passing `box`
// to either one restores the defect.
func TestBothChatboxesClipAtTheTextOrigin(t *testing.T) {
	for _, site := range []struct{ file, fn string }{
		{"screens.go", "drawChatOverlay"},
		{"theme_layout.go", "drawThemedChatBox"},
	} {
		body := funcBodySource(t, site.file, site.fn)
		clipVar := bindingOf(body, "chatCrawlClip")
		if clipVar == "" {
			t.Errorf("%s does not bind a chatCrawlClip — a scrolled message paints over the "+
				"showname plate (Qt clips ui_vp_message at the widget's own top edge, courtroom.cpp:3310)", site.fn)
			continue
		}
		if !assignsFieldFrom(body, "cgoRect", clipVar) {
			t.Errorf("%s sets its renderer clip from something other than %s — the crawl's "+
				"scrolled-off rows land on the name band", site.fn, clipVar)
		}
		calls := callsNamed(body, "DrawScaled")
		if len(calls) != 1 {
			t.Fatalf("%s: %d DrawScaled calls, want exactly 1", site.fn, len(calls))
		}
		args := calls[0].Args
		if !mentionsIdent(args[len(args)-1], clipVar) {
			t.Errorf("%s hands DrawScaled a clip that is not %s — the device-exact bracket "+
				"re-asserts it in device pixels, so the wrong rect there is the defect at every scaled UI", site.fn, clipVar)
		}
	}
}

// assignsFieldFrom reports whether n contains `<x>.<field> = <expr mentioning src>`.
// The chatbox clip reaches SDL through the Ctx scratch field rather than as a call
// argument (&local would escape through cgo), so the gate above has to read an
// assignment to catch it.
func assignsFieldFrom(n ast.Node, field, src string) bool {
	found := false
	ast.Inspect(n, func(node ast.Node) bool {
		as, ok := node.(*ast.AssignStmt)
		if !ok || len(as.Lhs) != 1 || len(as.Rhs) != 1 {
			return true
		}
		sel, ok := as.Lhs[0].(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != field {
			return true
		}
		if mentionsIdent(as.Rhs[0], src) {
			found = true
		}
		return !found
	})
	return found
}

// TestBothChatboxesFollowTheCrawl is the deletion catcher. There are TWO chatbox
// painters — AsyncAO's own overlay and the themed one — and a fix applied to one
// is the failure mode this repo has hit repeatedly (the two IC bars, the two
// draw sites for the blank-plate skin). Source, because a missing offset has no
// return value: the text simply stops where it always did.
func TestBothChatboxesFollowTheCrawl(t *testing.T) {
	for _, site := range []struct{ file, fn string }{
		{"screens.go", "drawChatOverlay"},
		{"theme_layout.go", "drawThemedChatBox"},
	} {
		body := funcBodySource(t, site.file, site.fn)
		if !containsCall(body, "chatCrawlOffset") {
			t.Errorf("%s does not follow its own crawl (chatCrawlOffset) — a message taller "+
				"than the box types into the void below it (courtroom.cpp:4514-4521)", site.fn)
		}
	}
}
