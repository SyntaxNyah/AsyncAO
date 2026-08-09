package ui

// The music banner's marquee (musicmarquee.go) against AO2's ScrollText
// (../AO2-Client/src/scrolltext.cpp), plus the two wirings that make it real: the
// draw joins the pacing census, and the plate reaches it only when a title does not
// fit and ReduceMotion is off.
//
// The port shipped with NO tests at all, which is how the census call came to be
// deletable with the whole package still green.

import (
	"go/ast"
	"testing"
	"time"
)

// containsIdent reports whether n mentions the identifier name anywhere. The
// sibling of containsCall (srcgate_test.go) for the gates that must catch a VALUE
// being used, not a function being called.
func containsIdent(n ast.Node, name string) bool {
	found := false
	ast.Inspect(n, func(node ast.Node) bool {
		if id, ok := node.(*ast.Ident); ok && id.Name == name {
			found = true
		}
		return !found
	})
	return found
}

// TestMarqueeConstantsAreAO2s pins the four numbers the port copied out of
// scrolltext.cpp. They are not tunables: a "nicer" separator or a faster step is a
// deviation from the client this one is a port of, and each is cited to its line.
func TestMarqueeConstantsAreAO2s(t *testing.T) {
	if ao2MarqueeSeparator != "   ---   " {
		t.Errorf("separator = %q, want AO2's %q (scrolltext.cpp:12 setSeparator)", ao2MarqueeSeparator, "   ---   ")
	}
	if ao2MarqueeStartPx != -64 {
		t.Errorf("start = %d, want -64 (scrolltext.cpp:51 scrollPos = -64)", ao2MarqueeStartPx)
	}
	if ao2MarqueeStepPx != 2 {
		t.Errorf("step = %d px, want 2 (scrolltext.cpp:145 scrollPos + 2)", ao2MarqueeStepPx)
	}
	if ao2MarqueeTickMs != 50 {
		t.Errorf("tick = %d ms, want 50 (scrolltext.cpp:15 timer.setInterval(50))", ao2MarqueeTickMs)
	}
	// The rate those two together define, stated once so a compensating pair of
	// edits (4 px every 100 ms) cannot slip through the two checks above.
	if px := ao2MarqueeStepPx * 1000 / ao2MarqueeTickMs; px != 40 {
		t.Errorf("scroll rate = %d px/s, want AO2's 40", px)
	}
	// The tile cap is ours (hard rule 4), not AO2's — but it must still be big
	// enough that a title exactly one widget wide covers the widget.
	if marqueeMaxTiles < 2 {
		t.Errorf("marqueeMaxTiles = %d: fewer than two copies cannot cover a widget the title is wider than", marqueeMaxTiles)
	}
}

// TestMarqueeScrollsOnlyWhenItDoesNotFit pins AO2's scrollEnabled
// (scrolltext.cpp:47, `singleTextWidth > width() - leftMargin * 2`): strictly
// GREATER. A title that measures exactly the available width is left still, which is
// also the boundary the static ellipsizing path uses — the two must never disagree,
// or a title would both scroll and be cut.
func TestMarqueeScrollsOnlyWhenItDoesNotFit(t *testing.T) {
	const avail = 200
	for _, tc := range []struct {
		textW int32
		want  bool
	}{
		{0, false},
		{avail - 1, false},
		{avail, false}, // AO2's condition is >, so equality does not scroll
		{avail + 1, true},
		{avail * 10, true},
	} {
		if got := marqueeScrolls(tc.textW, avail); got != tc.want {
			t.Errorf("marqueeScrolls(%d, %d) = %v, want %v", tc.textW, avail, got, tc.want)
		}
	}
}

// TestMarqueeScrollPosMatchesAO2sTimer walks the position AO2's 50 ms timer would
// produce: the -64 pre-roll, the crossing to zero, the 2 px step and the wrap at the
// tiled width.
func TestMarqueeScrollPosMatchesAO2sTimer(t *testing.T) {
	const tick = ao2MarqueeTickMs * time.Millisecond
	const wholeW = 300

	// The pre-roll. AO2 starts at -64 and climbs 2 px a tick, so it takes 32 ticks
	// (1.6 s) to reach zero — the pause that makes a new title readable.
	if got := marqueeScrollPos(0, wholeW); got != ao2MarqueeStartPx {
		t.Errorf("t=0 scrollPos = %d, want %d (the fresh-title start)", got, ao2MarqueeStartPx)
	}
	if got := marqueeScrollPos(31*tick, wholeW); got != -2 {
		t.Errorf("t=31 ticks scrollPos = %d, want -2 (still in the pre-roll)", got)
	}
	if got := marqueeScrollPos(32*tick, wholeW); got != 0 {
		t.Errorf("t=32 ticks scrollPos = %d, want 0 (the pre-roll ends after 1.6 s)", got)
	}
	// Past zero it is AO2's `(scrollPos + 2) % wholeTextSize.width()`.
	if got := marqueeScrollPos(40*tick, wholeW); got != 16 {
		t.Errorf("t=40 ticks scrollPos = %d, want 16", got)
	}
	// The wrap: 32 ticks of pre-roll plus wholeW/2 ticks of travel is exactly one
	// tiled width, which AO2's modulo maps back to 0.
	if got := marqueeScrollPos((32+wholeW/2)*tick, wholeW); got != 0 {
		t.Errorf("scrollPos at one full tiled width = %d, want 0 (it must wrap, not run away)", got)
	}
	if got := marqueeScrollPos((32+wholeW/2+1)*tick, wholeW); got != 2 {
		t.Errorf("scrollPos just past the wrap = %d, want 2", got)
	}
	// Within a tick nothing moves: the position is quantised to AO2's timer, not
	// smeared across a free-running clock.
	if a, b := marqueeScrollPos(40*tick, wholeW), marqueeScrollPos(40*tick+tick-time.Millisecond, wholeW); a != b {
		t.Errorf("position moved inside one 50 ms tick: %d then %d", a, b)
	}
	// Total on degenerate input — a zero/negative tiled width would be a division
	// by zero in AO2's modulo.
	if got := marqueeScrollPos(10*tick, 0); got != 0 {
		t.Errorf("wholeW=0 must be total, got %d", got)
	}
	if got := marqueeScrollPos(-time.Second, wholeW); got != ao2MarqueeStartPx {
		t.Errorf("negative elapsed = %d, want the start offset", got)
	}
}

// TestMarqueeFirstXClampsThePreRoll pins AO2's `qMin(-scrollPos, 0) + leftMargin`
// (scrolltext.cpp:75). The clamp is what makes the negative start a PAUSE at the
// margin instead of the title sliding in from off-screen.
func TestMarqueeFirstXClampsThePreRoll(t *testing.T) {
	const margin = 10
	for _, tc := range []struct {
		pos, want int32
		why       string
	}{
		{ao2MarqueeStartPx, margin, "the whole pre-roll sits still at the margin"},
		{-1, margin, "one pixel of pre-roll left: still parked"},
		{0, margin, "the moment travel starts, the title is still at the margin"},
		{6, margin - 6, "travelling: the first copy walks left out of the widget"},
		{margin, 0, "at margin px of travel the copy's left edge is the widget edge"},
	} {
		if got := marqueeFirstX(tc.pos, margin); got != tc.want {
			t.Errorf("marqueeFirstX(%d, %d) = %d, want %d — %s", tc.pos, margin, got, tc.want, tc.why)
		}
	}
}

// TestMarqueeSlotRestartsOnANewTitle pins the reason the slot exists: AO2 resets the
// scroll in updateText (scrolltext.cpp:42-54), so a new track starts from the
// pre-roll pause rather than halfway through the previous track's cycle.
func TestMarqueeSlotRestartsOnANewTitle(t *testing.T) {
	var m marqueeSlot
	const first = "[Cat] Also Sprach Zarathustra"

	tiled, elapsed := m.frame(first, 5*time.Second)
	if elapsed != 0 {
		t.Errorf("a first sighting must start at 0, got %v", elapsed)
	}
	if want := first + ao2MarqueeSeparator; tiled != want {
		t.Errorf("tiled = %q, want %q (AO2 tiles m_text + separator)", tiled, want)
	}
	if _, elapsed = m.frame(first, 7*time.Second); elapsed != 2*time.Second {
		t.Errorf("the same title must keep its epoch, got %v want 2s", elapsed)
	}
	// A new title restarts the clock AND the tiled string.
	const second = "Climax Rush"
	tiled, elapsed = m.frame(second, 9*time.Second)
	if elapsed != 0 {
		t.Errorf("a new title must restart the scroll, got %v", elapsed)
	}
	if want := second + ao2MarqueeSeparator; tiled != want {
		t.Errorf("tiled = %q, want %q", tiled, want)
	}
	// …and the restart must land in the pre-roll, which is the whole point.
	if pos := marqueeScrollPos(elapsed, 500); pos != ao2MarqueeStartPx {
		t.Errorf("a new title's first frame is at %d, want the pre-roll %d", pos, ao2MarqueeStartPx)
	}
	// A clock that goes backwards (fresh Viewport, replay scrub) re-anchors instead
	// of returning a negative elapsed that would never advance.
	if _, elapsed = m.frame(second, 1*time.Second); elapsed != 0 {
		t.Errorf("a backwards clock must re-anchor to 0, got %v", elapsed)
	}
	if _, elapsed = m.frame(second, 3*time.Second); elapsed != 2*time.Second {
		t.Errorf("after re-anchoring the clock must advance again, got %v", elapsed)
	}
}

// TestMarqueeSlotSteadyStateAllocatesNothing is the render-loop gate for the port.
// The tiled string is one heap allocation, and it was built at the draw site — i.e.
// on every frame the banner scrolled, inside the loop the whole-screen AllocsPerRun
// gates hold at zero. Pure Go, no renderer: the slot is the only thing that can
// allocate here, so this pins it directly.
func TestMarqueeSlotSteadyStateAllocatesNothing(t *testing.T) {
	var m marqueeSlot
	now := time.Second
	m.frame("[Cat] Also Sprach Zarathustra", now) // the one build, outside the gate
	if n := testing.AllocsPerRun(500, func() {
		now += 16 * time.Millisecond
		if s, _ := m.frame("[Cat] Also Sprach Zarathustra", now); s == "" {
			t.Fatal("empty tiled string")
		}
	}); n != 0 {
		t.Errorf("a steady marquee frame allocates %.1f/op, want 0 — the tiled string is being rebuilt per frame", n)
	}
}

// TestMusicMarqueeJoinsThePacingCensus is the deletion catcher for the frame-pacing
// wiring, in the shape TestStageEggCensusIsComplete uses for the eggs.
//
// WHY A SOURCE GATE. The census is a CALL with no return value and no observable
// state of its own from a headless test: removing `a.NoteAnimating()` from
// drawMusicMarquee leaves every behavioural test in this package green while the
// pacer parks at the idle cadence and the marquee crawls at a few pixels a second —
// the msAnim precedent, and exactly the regression this port was warned about.
func TestMusicMarqueeJoinsThePacingCensus(t *testing.T) {
	body := funcBodySource(t, "theme_layout.go", "drawMusicMarquee")
	// The census plus the three calls that make the draw AO2's: the position, the
	// clamped first copy, and the clip that cuts the tiling at the widget.
	for _, call := range []string{"NoteAnimating", "marqueeScrollPos", "marqueeFirstX", "pushClip"} {
		if !containsCall(body, call) {
			t.Errorf("drawMusicMarquee no longer calls %s — the marquee is unpaced or no longer AO2's", call)
		}
	}
	// The clip bracket must be released on EVERY exit, including the early return
	// the tile loop sits after.
	if !deferredCall(body, "popClip") {
		t.Error("drawMusicMarquee must release its clip with defer — an early return would leak the widget clip onto the rest of the frame")
	}
	// The tiled string comes from the SLOT, not from a concatenation at the draw
	// site: built here it is a heap allocation on every scrolling frame, and the
	// slot is the only thing that already invalidates on the title changing.
	if !containsCall(body, "frame") {
		t.Error("drawMusicMarquee no longer takes its tiled string and epoch from the marquee slot")
	}
	if containsIdent(body, "ao2MarqueeSeparator") {
		t.Error("drawMusicMarquee builds the tiled string itself again — that concatenation allocates once per frame inside the render loop; it belongs in marqueeSlot.frame")
	}

	// And the gate on the caller: AO2 scrolls only what does not fit, and
	// ReduceMotion is the accessibility opt-out. Losing either turns every title
	// into a permanently animating surface.
	plate := funcBodySource(t, "theme_layout.go", "drawThemedMusicPlate")
	for _, call := range []string{"marqueeScrolls", "ReduceMotion", "drawMusicMarquee"} {
		if !containsCall(plate, call) {
			t.Errorf("drawThemedMusicPlate no longer calls %s — the marquee's entry condition is gone", call)
		}
	}
}

// TestMusicNameMeasuresAtTheWeightItDraws is the source gate for the weighted-metric
// wiring, in the shape of the ao2MarqueeSeparator check above: the ABSENCE of the
// unweighted twin, not just the presence of the weighted one.
//
// WHY A SOURCE GATE. The plate and the marquee both draw through
// LabelClippedFontWeight, and every measurement feeding that draw has to be taken at
// the same weight (AO2 measures with fontMetrics() of the widget's own font —
// scrolltext.cpp:45 for singleTextWidth, :62 for wholeTextSize). Swapping any of
// them back to the unweighted twin is a one-token edit that leaves every behavioural
// test in this package green, because a headless test of the DRAW cannot see the
// clamp: it only shows up as a cut tail and creeping tiles on a theme that sets
// music_name_bold. TestMarqueeTileWidthIsTheDrawnWidth below pins the consequence;
// this pins the call.
func TestMusicNameMeasuresAtTheWeightItDraws(t *testing.T) {
	for _, tc := range []struct {
		fn      string
		want    []string
		forbid  map[string]string
		context string
	}{
		{
			fn:   "drawMusicMarquee",
			want: []string{"fontTextWidthWeight"},
			forbid: map[string]string{
				"fontTextWidth": "the tile width is measured plain while the tile is drawn bold: LabelClippedFontWeight clamps the blit to it (cutting AO2's \"   ---   \" separator off every copy) and `x += wholeW` under-steps, so the copies creep together instead of repeating at wholeTextSize.width() (scrolltext.cpp:79)",
			},
		},
		{
			fn:   "drawThemedMusicPlate",
			want: []string{"fontTextWidthWeight", "fitLabelToFontWeight"},
			forbid: map[string]string{
				"fontTextWidth":  "the fit test is measured plain while the title is drawn bold (AO2's singleTextWidth is fontMetrics() on the widget font, scrolltext.cpp:45): a title that fits plain but not bold gets neither the scroll nor the ellipsis, and is hard-cut mid-glyph at avail",
				"fitLabelToFont": "the ellipsis is placed on plain advances while the title is drawn bold, so the \"fitted\" string still overflows and the marker it ends with is clipped off along with the tail it marks",
			},
		},
	} {
		body := funcBodySource(t, "theme_layout.go", tc.fn)
		for _, call := range tc.want {
			if !containsCall(body, call) {
				t.Errorf("%s no longer calls %s — its measurement is no longer taken at the weight it draws at", tc.fn, call)
			}
		}
		for call, why := range tc.forbid {
			if containsCall(body, call) {
				t.Errorf("%s calls the unweighted %s: %s", tc.fn, call, why)
			}
		}
	}
}

// TestMarqueeTileWidthIsTheDrawnWidth is the behavioural half: the number
// drawMusicMarquee passes as maxW AND as the tile step must be at least the width of
// the texture LabelClippedFontWeight actually blits, or the draw clamps
// (ui.go LabelClippedFontWeight: `if w > maxW { w = maxW }`) and the tail of every
// copy — the separator — is cut.
//
// It drives the two production functions against each other rather than
// re-implementing either, and it states the failure it is guarding: measured plain,
// the bound is SHORTER than the bold texture, which is the clamp.
func TestMarqueeTileWidthIsTheDrawnWidth(t *testing.T) {
	ren, cleanup := newCaptureHarness(t)
	defer cleanup()
	c, err := NewCtx(ren)
	if err != nil {
		t.Fatalf("NewCtx: %v", err)
	}
	defer c.Destroy()

	var m marqueeSlot
	tiled, _ := m.frame("[Cat] Also Sprach Zarathustra", time.Second)

	boldTex, ok := c.textTextureBold(tiled, ColText, c.font, true)
	if !ok {
		t.Fatal("the bold tile did not rasterize")
	}
	boldW, okb := c.fontTextWidthWeight(c.font, tiled, true)
	plainW, okp := c.fontTextWidth(c.font, tiled)
	if !okb || !okp {
		t.Fatalf("measure failed: bold ok=%v plain ok=%v", okb, okp)
	}
	if boldW < boldTex.logicalW() {
		t.Errorf("weighted measure %d < the drawn texture's %d px — LabelClippedFontWeight would clamp the blit and cut the separator off every tile",
			boldW, boldTex.logicalW())
	}
	// The regression this replaces, stated as the thing that must NOT be the bound.
	// (If a face ever emboldened to the same advance the guard is vacuous, not wrong,
	// so it is skipped rather than failed — scaleblur_test.go owns the bold>plain
	// property for the chrome face.)
	if plainW >= boldTex.logicalW() {
		t.Skipf("this face emboldens without widening (plain %d, bold texture %d): nothing to clamp", plainW, boldTex.logicalW())
	}
	if boldW <= plainW {
		t.Errorf("fontTextWidthWeight(bold) = %d <= plain %d for the tiled string: the tile step would under-run the drawn glyphs and consecutive copies would creep together",
			boldW, plainW)
	}
	// The separator is the part that gets eaten, so say how much of it the plain
	// bound would have thrown away.
	sepW, _ := c.fontTextWidthWeight(c.font, ao2MarqueeSeparator, true)
	if lost := boldTex.logicalW() - plainW; sepW > 0 && lost <= 0 {
		t.Errorf("expected the plain bound to be short by some of the %d px separator, lost %d", sepW, lost)
	}
}

// TestFitLabelToFontWeightFitsAtTheDrawnWeight pins the third member of the weighted
// trio from the OUTSIDE: whatever fitLabelToFontWeight returns for bold must actually
// measure within avail AT BOLD. A body that accepted the flag and then measured plain
// (the twin's whole failure mode, and one the theme_layout source gate cannot see
// because the call site would still read correctly) returns a string that overflows
// by the emboldening — so the ellipsis meant to say "there is more" is itself clipped.
func TestFitLabelToFontWeightFitsAtTheDrawnWeight(t *testing.T) {
	ren, cleanup := newCaptureHarness(t)
	defer cleanup()
	c, err := NewCtx(ren)
	if err != nil {
		t.Fatalf("NewCtx: %v", err)
	}
	defer c.Destroy()

	const label = "[Cat] Also Sprach Zarathustra (Deluxe Remaster)"
	full, ok := c.fontTextWidthWeight(c.font, label, true)
	if !ok || full <= 0 {
		t.Fatalf("measure failed (ok=%v w=%d)", ok, full)
	}
	// Three quarters of the bold width: comfortably overlong, so the truncation
	// actually runs, and far from the boundary where a one-rune difference decides it.
	avail := full * 3 / 4

	shownPlain := c.fitLabelToFont(c.font, label, avail)
	plainAtBold, _ := c.fontTextWidthWeight(c.font, shownPlain, true)
	if plainAtBold <= avail {
		t.Skipf("this face emboldens without widening: the plain fit %q already fits %d px at bold", shownPlain, avail)
	}

	shownBold := c.fitLabelToFontWeight(c.font, label, avail, true)
	boldAtBold, okb := c.fontTextWidthWeight(c.font, shownBold, true)
	if !okb {
		t.Fatal("measuring the weighted fit failed")
	}
	if boldAtBold > avail {
		t.Errorf("fitLabelToFontWeight(bold) returned %q, %d px wide at bold in %d px of room — it measured with the plain advances",
			shownBold, boldAtBold, avail)
	}
	if shownBold == shownPlain {
		t.Errorf("the weighted fit is identical to the plain one (%q) even though the plain one overflows by %d px at bold — the weight is being dropped",
			shownPlain, plainAtBold-avail)
	}
	// And the unweighted entry point is unchanged: same measure, same result.
	if again := c.fitLabelToFontWeight(c.font, label, avail, false); again != shownPlain {
		t.Errorf("fitLabelToFont(%q) = %q but fitLabelToFontWeight(..., false) = %q — the plain path is no longer byte-identical",
			label, shownPlain, again)
	}
}
