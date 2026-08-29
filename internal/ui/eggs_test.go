package ui

import (
	"math"
	"strings"
	"testing"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
)

// --- the egg roster's behavioural oracles --------------------------------------
//
// An egg has no return value and no state a headless test can read: it is
// SetDrawColor and fills, straight at the renderer. Every gate below therefore
// drives the two real entry points (drawChatEgg / drawStageEgg) and reads the two
// things a draw does leave behind — the PIXELS, captured off an offscreen target,
// and the frame-pacing census flag. That is what makes them behavioural pins
// rather than source checks, and it is why the two eggs' accessibility gate and
// their pacing gate could both be deleted with the suite green before they
// existed.

// eggOracleW / eggOracleH size the offscreen frame those gates paint into, and
// eggOracleInset how far inside it the stand-in chatbox sits. Small on purpose:
// these gates COUNT painted pixels rather than look at them, and every egg's
// field scales with the rect it is handed, so a postage stamp runs the same loops
// a 4K stage would. The inset has to clear the OUTSET rings
// (eggRingCount*eggRingGap px of them) or ringVisible would legitimately skip
// them and the motion-allowed control arm would read a truthful zero.
const (
	eggOracleW     = int32(240)
	eggOracleH     = int32(180)
	eggOracleInset = int32(40)
)

// eggKindTriggers is one message that lights each egg. A table, and eggTriggerFor
// fails on a kind missing from it: an eighth egg joins these gates by having been
// added to the enum, rather than slipping past because nobody wrote it a row.
var eggKindTriggers = map[uint8]string{
	eggFanat:     "shoutout to FanatSors",
	eggOmni:      "credit to OmniTroid",
	eggCrystal:   "kept alive by CrystalWarrior",
	eggNyah:      "made by SyntaxNyah",
	eggScatter:   "inspired by Scatterflower",
	eggNorthgate: "hello Northgate",
	eggMint:      "hello Mint",
}

// eggTriggerFor returns kind's trigger message, having checked that the real scan
// agrees it lights that egg — a table row that stopped matching would otherwise
// quietly point a whole gate at the wrong egg (or at none).
func eggTriggerFor(t *testing.T, kind uint8) string {
	t.Helper()
	text, ok := eggKindTriggers[kind]
	if !ok {
		t.Fatalf("egg kind %d has no trigger message recorded — add one here rather than letting "+
			"the accessibility and pacing gates skip a whole egg", kind)
	}
	if got := creatorEgg(text); got != kind {
		t.Fatalf("the recorded trigger %q lights egg %d, not %d — the gate would measure the wrong egg", text, got, kind)
	}
	return text
}

// eggPaintedPixels runs draw into a black-cleared offscreen frame and counts the
// pixels it left non-black.
func eggPaintedPixels(t *testing.T, ren *sdl.Renderer, ct *render.CaptureTarget, draw func()) int {
	t.Helper()
	frame, err := ct.Capture(ren, func(sdl.Rect) { draw() })
	if err != nil {
		t.Skipf("offscreen capture unavailable headlessly: %v", err)
	}
	painted := 0
	for i := 0; i+3 < len(frame.Pix); i += 4 {
		if frame.Pix[i] != 0 || frame.Pix[i+1] != 0 || frame.Pix[i+2] != 0 {
			painted++
		}
	}
	return painted
}

// eggOracleRig stages a courtroom app with an offscreen target sized for the two
// gates below, and returns the stage rect and the stand-in chatbox rect they
// drive. Screen effects ON, because the eggs' gate is the PAIR
// (ScreenEffectsOn && !ReduceMotion) and every arm here is about the second half.
func eggOracleRig(t *testing.T) (*App, *render.CaptureTarget, sdl.Rect, sdl.Rect, func()) {
	t.Helper()
	a, cleanup := stageSettledCourtroom(t)
	ct, err := render.NewCaptureTarget(a.ctx.Ren, eggOracleW, eggOracleH)
	if err != nil {
		cleanup()
		t.Skipf("render targets unavailable: %v", err)
	}
	// drawCreatorEgg clamps its rings to (0,0,winW,winH); Frame never runs in this
	// harness, so pin them or every ring is skipped as off-window.
	a.winW, a.winH = eggOracleW, eggOracleH
	a.d.Prefs.SetScreenEffects(true)
	vp := sdl.Rect{X: 0, Y: 0, W: eggOracleW, H: eggOracleH}
	box := sdl.Rect{X: eggOracleInset, Y: eggOracleInset, W: eggOracleW - 2*eggOracleInset, H: eggOracleH - 2*eggOracleInset}
	return a, ct, vp, box, func() {
		ct.Close()
		cleanup()
	}
}

// TestReduceMotionFreezesEveryEgg is the accessibility gate for the whole egg
// roster, in the shape TestReduceMotionFreezesEveryElement (themeclock_test.go)
// uses for the theme clock.
//
// Reduce motion is not "most of the motion": somebody who ticks that box because
// movement makes them ill is not served by a chatbox that still breathes at them
// because a stranger typed a name. Both entry points read the preference, and
// NOTHING pinned it — the guest eggs shipped with an audit mutation that let Mint
// alone bypass the gate, and a second that deleted the term outright so all seven
// animated for a person who had asked them not to, and the entire package stayed
// green under both. Each kind is driven through the real entry point twice: once
// with the preference on (no pixels, no pacing) and once with it off, because the
// frozen arm is only worth anything if the same call paints when it is allowed to.
func TestReduceMotionFreezesEveryEgg(t *testing.T) {
	a, ct, vp, box, cleanup := eggOracleRig(t)
	defer cleanup()

	for kind := eggFanat; kind <= eggMint; kind++ {
		text := eggTriggerFor(t, kind)
		sc := courtroom.Scene{MessageText: text}
		for _, half := range []struct {
			name string
			live bool
			draw func()
		}{
			{"chatbox ring", true, func() { a.drawChatEgg(box, text) }},
			{"stage field", eggDrawsOnStage(kind), func() { a.drawStageEgg(vp, &sc) }},
		} {
			if !half.live {
				continue
			}
			for _, arm := range []struct {
				reduce bool
				paints bool
			}{{reduce: true, paints: false}, {reduce: false, paints: true}} {
				a.d.Prefs.SetReduceMotion(arm.reduce)
				// Clear the detection cache so each arm does its own scan through the
				// real compare-and-store guard rather than inheriting the last one's.
				a.eggMsg, a.eggKind = "", eggNone
				a.frameAnimChrome = false
				painted := eggPaintedPixels(t, a.ctx.Ren, ct, half.draw)
				switch {
				case arm.paints && painted == 0:
					t.Errorf("egg %d's %s painted nothing with motion ALLOWED — the frozen arm of this "+
						"gate is measuring an egg that never draws here, so it proves nothing", kind, half.name)
				case !arm.paints && painted != 0:
					t.Errorf("egg %d's %s painted %d px under ReduceMotion — the accessibility opt-out "+
						"does not reach this egg", kind, half.name, painted)
				}
				if got := a.frameAnimChrome; got != arm.paints {
					t.Errorf("egg %d's %s noted the pacing census = %v with ReduceMotion=%v, want %v — "+
						"a frozen egg still holding the client at the animation cadence is the motion "+
						"budget spent on motion the user asked not to see", kind, half.name, got, arm.reduce, arm.paints)
				}
			}
		}
	}
}

// TestEveryEggPacesTheFrameLimiter is the per-kind pacing census, and it is a
// BEHAVIOURAL counterpart to the source check TestStageEggCensusIsComplete makes
// (which asks only whether NoteAnimating appears somewhere in drawStageEgg's
// body). That is not enough, and an audit mutation proved it: a bare `return`
// added to the eggMint arm of the dispatch skips the census call at the bottom
// while the frost still animates, and the whole package stayed green. An egg that
// animates without noting parks the frame limiter at its idle cap and the field
// crawls at a few frames a second — the msAnim precedent this repo has already
// shipped once.
//
// Both dispatches, every kind, plus the two negatives: a message with no egg in
// it must not pace, and a kind with no stage half must not pace from the stage.
func TestEveryEggPacesTheFrameLimiter(t *testing.T) {
	a, _, vp, box, cleanup := eggOracleRig(t)
	defer cleanup()
	a.d.Prefs.SetReduceMotion(false)

	for kind := eggFanat; kind <= eggMint; kind++ {
		text := eggTriggerFor(t, kind)
		sc := courtroom.Scene{MessageText: text}

		a.eggMsg, a.eggKind = "", eggNone
		a.frameAnimChrome = false
		a.drawStageEgg(vp, &sc)
		if a.eggKind != kind {
			t.Fatalf("drawStageEgg resolved egg %d for %q, want %d", a.eggKind, text, kind)
		}
		if got, want := a.frameAnimChrome, eggDrawsOnStage(kind); got != want {
			t.Errorf("egg %d: drawStageEgg noted the pacing census = %v, want %v — a stage field that "+
				"animates without noting leaves the frame limiter at the idle cap", kind, got, want)
		}

		a.eggMsg, a.eggKind = "", eggNone
		a.frameAnimChrome = false
		a.drawChatEgg(box, text)
		if !a.frameAnimChrome {
			t.Errorf("egg %d: drawChatEgg did not note the pacing census — the ring animates while the "+
				"limiter idles, so the glow steps instead of glowing", kind)
		}
	}

	// The negatives, from the same two entry points: an ordinary line pins nothing
	// to the animation cadence.
	const inert = "plain courtroom banter"
	if got := creatorEgg(inert); got != eggNone {
		t.Fatalf("the control message %q lights egg %d — this arm would assert nothing", inert, got)
	}
	sc := courtroom.Scene{MessageText: inert}
	a.eggMsg, a.eggKind = "", eggNone
	a.frameAnimChrome = false
	a.drawStageEgg(vp, &sc)
	a.drawChatEgg(box, inert)
	if a.frameAnimChrome {
		t.Error("a message with no creator in it held the client at the animation cadence — every " +
			"ordinary post would keep the frame limiter awake")
	}
}

// TestCreatorEgg pins the detection scan: each creator name in mixed case and
// mid-sentence resolves to its egg, multiple-name messages honour the documented
// fanat > omni > nyah priority, and near-miss words / plain text / empty never
// false-trigger.
func TestCreatorEgg(t *testing.T) {
	cases := []struct {
		text string
		want uint8
		why  string
	}{
		// Each name, mixed case, embedded mid-sentence.
		{"thanks FanatSors for AO", eggFanat, "mixed-case fanat mid-sentence"},
		{"all hail fanatsors!!!", eggFanat, "lower-case fanat"},
		{"OMNITROID made AO2", eggOmni, "upper-case omni"},
		{"shoutout to OmniTroid here", eggOmni, "mixed-case omni mid-sentence"},
		{"written by SyntaxNyah btw", eggNyah, "mixed-case nyah mid-sentence"},
		{"syntaxnyah", eggNyah, "bare lower-case nyah"},
		// Multiple names in one message → first in fanat>omni>nyah wins,
		// regardless of textual position (lineage order AO→AO2→AsyncAO).
		{"omnitroid and fanatsors", eggFanat, "fanat outranks omni even when omni appears first"},
		{"syntaxnyah then omnitroid", eggOmni, "omni outranks nyah even when nyah appears first"},
		{"fanatsors omnitroid syntaxnyah", eggFanat, "all three → fanat"},
		// Scatterflower: an inspiration, so it sorts BELOW all three creators.
		{"ScatterFlower", eggScatter, "mixed-case scatter, bare"},
		{"big thanks to scatterflower for the idea", eggScatter, "scatter mid-sentence"},
		{"SCATTERFLOWER!!!", eggScatter, "upper-case scatter"},
		{"scatterflower and syntaxnyah", eggNyah, "a creator outranks an inspiration"},
		{"scatterflower fanatsors", eggFanat, "fanat still wins over scatter"},
		// Crystalwarrior: AO2's keeper, so above SyntaxNyah in the lineage and below
		// the two originals.
		{"CrystalWarrior", eggCrystal, "mixed-case crystal, bare"},
		{"ask crystalwarrior about it", eggCrystal, "crystal mid-sentence"},
		{"CRYSTALWARRIOR!!!", eggCrystal, "upper-case crystal"},
		{"syntaxnyah then crystalwarrior", eggCrystal, "crystal outranks nyah even when nyah appears first"},
		{"crystalwarrior omnitroid", eggOmni, "omni still outranks crystal"},
		{"crystalwarrior scatterflower", eggCrystal, "a lineage figure outranks an inspiration"},
		// Northgate: a guest, so it sorts below every name above it.
		{"Northgate", eggNorthgate, "mixed-case northgate, bare"},
		{"see you at northgate tonight", eggNorthgate, "northgate mid-sentence"},
		{"NORTHGATE!!!", eggNorthgate, "upper-case northgate"},
		{"northgate scatterflower", eggScatter, "the named inspiration still outranks a guest"},
		{"northgate fanatsors", eggFanat, "a lineage creator still outranks a guest"},
		// Mint: the word-boundary rung, and the LAST one — a line naming both
		// guests lights the one that could not have matched by accident.
		{"Mint", eggMint, "mixed-case mint, bare"},
		{"nice to see Mint here", eggMint, "mint mid-sentence"},
		{"MINT!!!", eggMint, "upper-case mint, trailing punctuation is a boundary"},
		{"(mint)", eggMint, "brackets are boundaries"},
		{"mint, northgate", eggNorthgate, "northgate outranks mint"},
		{"mint syntaxnyah", eggNyah, "a creator outranks a guest"},
		// …and the boundary rule doing its job. Every one of these CONTAINS
		// "mint": a plain substring match lights all four, which is the whole
		// reason this trigger is not one.
		{"the car is in mint condition", eggMint, "a boundary match still fires on the bare word"},
		{"he ate a peppermint", eggNone, "peppermint must not trigger mint"},
		{"freshly minted evidence", eggNone, "minted must not trigger mint"},
		{"spearmint gum", eggNone, "spearmint must not trigger mint"},
		{"the mintage was disputed", eggNone, "mintage must not trigger mint"},
		// No false positives.
		{"you are being fanatic about this", eggNone, "fanatic must not trigger fanat"},
		{"my android phone", eggNone, "android must not trigger omni"},
		{"she picked a flower", eggNone, "flower alone must not trigger scatter"},
		{"scatter the evidence", eggNone, "scatter alone must not trigger scatter"},
		{"scatter flower", eggNone, "the two halves split by a space must not trigger"},
		{"the alibi is crystal clear", eggNone, "crystal alone must not trigger crystal"},
		{"a lone warrior took the stand", eggNone, "warrior alone must not trigger crystal"},
		{"crystal warrior", eggNone, "the two halves split by a space must not trigger"},
		{"plain courtroom banter", eggNone, "ordinary text is inert"},
		{"", eggNone, "empty text is inert"},
	}
	for _, tc := range cases {
		if got := creatorEgg(tc.text); got != tc.want {
			t.Errorf("creatorEgg(%q) = %d, want %d (%s)", tc.text, got, tc.want, tc.why)
		}
	}
}

// TestRefreshEggKindCachesScan pins the compare-and-store guard: a new message
// rescans and stores; the SAME text again does NOT rescan (returns false); a
// changed text rescans anew. This is the once-per-message contract that keeps a
// settled frame at one string compare — tested via the pure guard, no SDL.
func TestRefreshEggKindCachesScan(t *testing.T) {
	a := &App{}

	if scanned := a.refreshEggKind("hello fanatsors"); !scanned {
		t.Fatal("first sight of a message must rescan")
	}
	if a.eggKind != eggFanat {
		t.Fatalf("stored egg = %d, want eggFanat", a.eggKind)
	}
	if scanned := a.refreshEggKind("hello fanatsors"); scanned {
		t.Error("the same text must NOT rescan (one scan per message)")
	}
	if a.eggKind != eggFanat {
		t.Errorf("cached egg changed on a no-op refresh: %d", a.eggKind)
	}
	// A changed message rescans and updates the kind.
	if scanned := a.refreshEggKind("now with syntaxnyah"); !scanned {
		t.Error("a changed message must rescan")
	}
	if a.eggKind != eggNyah {
		t.Errorf("stored egg = %d, want eggNyah after text change", a.eggKind)
	}
	// A plain message clears the egg (self-clearing when the trigger leaves the box).
	if scanned := a.refreshEggKind("plain line"); !scanned {
		t.Error("clearing text must rescan")
	}
	if a.eggKind != eggNone {
		t.Errorf("egg not cleared: %d", a.eggKind)
	}
}

// TestRingVisibleSkipsOnlyOffScreen pins the artifact fix: the OUTLINE rings use
// a skip-ONLY test (never clamp), so a ring that runs off a window edge is drawn
// in full and clipped by SDL — clamping its rect would make DrawRect paint a
// spurious colored line along the window edge. A fully-off-window ring is skipped;
// a partially-visible one is kept (drawn, then SDL-clipped). The sweep FILLS keep
// the in-place clamp, which is correct (a clamped fill has no spurious edge).
func TestRingVisibleSkipsOnlyOffScreen(t *testing.T) {
	win := sdl.Rect{X: 0, Y: 0, W: 1280, H: 720}
	// A ring straddling the left edge (negative X) must be KEPT — not clamped away.
	edge := sdl.Rect{X: -6, Y: 100, W: 200, H: 60}
	if !ringVisible(edge, win) {
		t.Error("a ring straddling the window edge must be kept (drawn in full, SDL-clipped)")
	}
	// A ring entirely off-window is skipped.
	if ringVisible(sdl.Rect{X: 2000, Y: 100, W: 50, H: 50}, win) {
		t.Error("a fully off-window ring must be skipped")
	}
	// A ring flush to the top-left touching (0,0) is visible.
	if !ringVisible(sdl.Rect{X: 0, Y: 0, W: 40, H: 40}, win) {
		t.Error("a ring at the origin must be visible")
	}
	// clampRingToWindow (sweep fills) DOES clamp in place and reports visibility.
	fill := edge
	if !clampRingToWindow(&fill, win) {
		t.Fatal("a partly-visible fill must clamp to something visible")
	}
	if fill.X != 0 || fill.W != 194 { // -6..194 clamps to 0..194
		t.Errorf("clamp math wrong: got X=%d W=%d, want X=0 W=194", fill.X, fill.W)
	}
	off := sdl.Rect{X: -100, Y: 100, W: 50, H: 50} // fully left of the window
	if clampRingToWindow(&off, win) {
		t.Error("a fully off-window fill must report not-visible")
	}
}

// TestPerimSegment pins the sweep geometry: perimSegment maps a 1-D perimeter
// offset (clockwise from the ring's top-left, going right along the top) to an
// edge-aligned rect on the edge that offset sits on, and reports how much
// perimeter length it consumed. TestDrawCourtroomEggZeroAlloc only COUNTS
// allocations, so a sweep drawn on the wrong edge, off-by-N, or with a spurious
// corner rect would fail no other test — this is the correctness gate for the
// most intricate math in the eggs change.
//
// The ring is {100,200,400,300}: w=400, h=300, so the edge boundaries along the
// perimeter are top=400, right=700, bottom=1100, and the full perimeter is 1400.
// Each row starts an integer offset on a known edge so `consumed` compares
// exactly. Rather than assert opaque rect literals, we check the geometric
// INVARIANT per edge (a top/bottom bar is th tall and hugs the top/bottom side;
// a left/right bar is th wide and hugs the left/right side), so a future
// edge-flip or off-by-one fails with a legible message instead of a bare diff.
func TestPerimSegment(t *testing.T) {
	ring := sdl.Rect{X: 100, Y: 200, W: 400, H: 300}
	const th = int32(3)
	// Edge classification for the message: which side the bar must hug.
	const (
		edgeTop = iota
		edgeRight
		edgeBottom
		edgeLeft
	)
	cases := []struct {
		name         string
		start        float64
		length       float64
		edge         int
		wantConsumed float64
	}{
		// Full-length runs, one starting at the head of each edge.
		{"top head", 0, 50, edgeTop, 50},
		{"right head", 400, 50, edgeRight, 50},
		{"bottom head", 700, 50, edgeBottom, 50},
		{"left head", 1100, 50, edgeLeft, 50},
		// A run that starts late on the TOP edge and would overrun the corner is
		// clamped to the rest of that edge (consumed=20, the caller then continues
		// onto the right edge) — this is the per-edge split that keeps a sweep from
		// painting a spurious corner rect.
		{"top clamps at corner", 380, 50, edgeTop, 20},
		// The LEFT edge clamps at the perimeter/seam boundary (2*(w+h)=1400): a run
		// from 1380 consumes only the last 20. The actual wrap-around back onto the
		// top edge is the caller's math.Mod in drawEggSweep (needs SDL, out of unit
		// scope); here we pin that perimSegment itself stops exactly at the seam.
		{"left clamps at seam", 1380, 50, edgeLeft, 20},
	}
	for _, tc := range cases {
		r, consumed := perimSegment(ring, tc.start, tc.length, th)
		if consumed != tc.wantConsumed {
			t.Errorf("%s: consumed = %v, want %v", tc.name, consumed, tc.wantConsumed)
		}
		// The visible bar length is `consumed`; the code adds +1 px so adjacent
		// segments overlap by a pixel and leave no seam gap.
		wantSpan := int32(tc.wantConsumed) + 1
		switch tc.edge {
		case edgeTop:
			if r.H != th || r.Y != ring.Y {
				t.Errorf("%s: not on the TOP edge (H=%d Y=%d, want H=%d Y=%d)", tc.name, r.H, r.Y, th, ring.Y)
			}
			if r.W != wantSpan {
				t.Errorf("%s: top bar span W=%d, want %d", tc.name, r.W, wantSpan)
			}
			if r.X != ring.X+int32(tc.start) {
				t.Errorf("%s: top bar X=%d, want %d", tc.name, r.X, ring.X+int32(tc.start))
			}
		case edgeBottom:
			if r.H != th || r.Y != ring.Y+ring.H-th {
				t.Errorf("%s: not on the BOTTOM edge (H=%d Y=%d, want H=%d Y=%d)", tc.name, r.H, r.Y, th, ring.Y+ring.H-th)
			}
			if r.W != wantSpan {
				t.Errorf("%s: bottom bar span W=%d, want %d", tc.name, r.W, wantSpan)
			}
			// Travel axis: the bottom edge runs right→left, so the bar's X sits at
			// the edge head (perimeter offset 700 = the ring's right end) minus how
			// far in the run starts and the span it covers.
			if r.X != ring.X+ring.W-int32(tc.start-700)-int32(tc.wantConsumed) {
				t.Errorf("%s: bottom bar X=%d, want %d", tc.name, r.X, ring.X+ring.W-int32(tc.start-700)-int32(tc.wantConsumed))
			}
		case edgeRight:
			if r.W != th || r.X != ring.X+ring.W-th {
				t.Errorf("%s: not on the RIGHT edge (W=%d X=%d, want W=%d X=%d)", tc.name, r.W, r.X, th, ring.X+ring.W-th)
			}
			if r.H != wantSpan {
				t.Errorf("%s: right bar span H=%d, want %d", tc.name, r.H, wantSpan)
			}
			if r.Y != ring.Y+int32(tc.start-400) {
				t.Errorf("%s: right bar Y=%d, want %d", tc.name, r.Y, ring.Y+int32(tc.start-400))
			}
		case edgeLeft:
			if r.W != th || r.X != ring.X {
				t.Errorf("%s: not on the LEFT edge (W=%d X=%d, want W=%d X=%d)", tc.name, r.W, r.X, th, ring.X)
			}
			if r.H != wantSpan {
				t.Errorf("%s: left bar span H=%d, want %d", tc.name, r.H, wantSpan)
			}
			// Travel axis: the left edge runs bottom→top, so the bar's Y sits at
			// the edge head (perimeter offset 1100 = the ring's bottom end) minus
			// how far in the run starts and the span it covers.
			if r.Y != ring.Y+ring.H-int32(tc.start-1100)-int32(tc.wantConsumed) {
				t.Errorf("%s: left bar Y=%d, want %d", tc.name, r.Y, ring.Y+ring.H-int32(tc.start-1100)-int32(tc.wantConsumed))
			}
		}
	}
}

// TestDrawCourtroomEggZeroAlloc gates the egg DRAW path (rings + sweep +
// heartbeat) at zero allocations per frame. The existing whole-screen gate
// stages a NON-triggering message, so it never exercises the egg branch; this
// sibling drives a creator-name message (ScreenEffects on, ReduceMotion off) so
// the full drawCreatorEgg runs each measured frame. A non-zero count means a
// per-frame allocation shipped in the egg draw (fix it, don't loosen the gate).
//
// THE ~5% FLAKE THIS USED TO CARRY IS FIXED — see the header in
// allocgate_test.go. The reading that kept coming back, exactly 1.0/op on the
// FanatSors arm, was diagnosed correctly here: one event of >= 200 allocations
// inside the window, not a per-frame cost. What nobody spotted was that the old
// settle() proved quiescence over 20 frames and then the assert measured 200, so
// the event cleared the warm-up and failed the gate. allocsPerFrame settles and
// asserts over the same window and outlasts a one-shot burst; a per-frame
// allocation still fails on the first reading.
//
// DO NOT WEAKEN OR SKIP IT. Every egg path is genuinely alloc-free in the steady
// state, and the class of bug this catches (a per-frame allocation in a draw
// body) is one this codebase has shipped more than once.
func TestDrawCourtroomEggZeroAlloc(t *testing.T) {
	a, cleanup := stageSettledCourtroom(t)
	defer cleanup()

	// The accessibility gate must be OPEN for the egg to draw at all.
	a.d.Prefs.SetScreenEffects(true)
	a.d.Prefs.SetReduceMotion(false)

	const w, h = 1280, 720
	// drawCreatorEgg clamps its rings to (0,0,winW,winH); Frame (which normally
	// sets these) never runs in this harness, so pin them or every ring clamps to
	// nothing and the fills — the primitives the gate most needs to measure — are
	// skipped.
	a.winW, a.winH = w, h
	draw := func() { a.drawCourtroom(w, h) }

	// Each egg has a DISTINCT draw path and every one of the SEVEN is a row here:
	// FanatSors' rainbow rings; OmniTroid's blue<->gold pulse plus the perimeter
	// sweep; Crystalwarrior's split-hue ring PLUS its stage shard field;
	// SyntaxNyah's pink heartbeat; Scatterflower's palette-drift ring PLUS its
	// stage petal field; Northgate's ramp ring PLUS its stage curtain field; and
	// Mint's banded ring PLUS its stage frost-and-fleck field. All eleven draw
	// bodies have to be gated — the sweep's per-edge fill loop and the five nested
	// stage loops are the alloc-prone ones. drawCourtroom covers both halves of the
	// four two-part eggs: renderViewportZoomed reaches drawStageEgg,
	// drawChatOverlay reaches drawChatEgg. Re-drive the SAME speaker (char 0
	// "Witch", whose stage bases the staging already made resident) with each
	// creator-name message, settle the typewriter, and assert zero allocs.
	for _, tc := range []struct {
		text string
		want uint8
	}{
		{"shoutout to FanatSors", eggFanat},
		{"credit to OmniTroid", eggOmni},
		{"kept alive by CrystalWarrior", eggCrystal},
		{"made by SyntaxNyah", eggNyah},
		{"inspired by Scatterflower", eggScatter},
		{"hello Northgate", eggNorthgate},
		{"hello Mint", eggMint},
	} {
		a.room.HandleEvent(courtroom.Event{Kind: courtroom.EventMessage, Message: msgFor(0, "Witch", tc.text)})
		a.room.SkipToIdle()
		// Sanity: the egg must actually resolve, or this gate would pass vacuously.
		draw()
		if a.eggKind != tc.want {
			t.Fatalf("egg for %q = %d, want %d — the zero-alloc gate would measure the wrong (or no) path", tc.text, a.eggKind, tc.want)
		}
		if n := allocsPerFrame(allocGateFrames, 0, draw); n != 0 {
			t.Fatalf("a settled %q egg frame allocates %.1f/op, want 0 — a per-frame allocation shipped in the egg draw", tc.text, n)
		}
	}
}

// TestStageEggCensusIsComplete is the encapsulation gate for the egg's STAGE
// half. eggDrawsOnStage is the one list of kinds that paint on the viewport, and
// drawStageEgg dispatches from it — a kind added to the list but left out of the
// dispatch (or the reverse) is the parsed-but-never-applied failure this codebase
// has shipped before: everything compiles, every other egg gate stays green, and
// the new egg simply never appears on the stage.
func TestStageEggCensusIsComplete(t *testing.T) {
	// The list itself: exactly the two-part eggs, and nothing else.
	want := map[uint8]bool{eggScatter: true, eggCrystal: true, eggNorthgate: true, eggMint: true}
	for kind := eggNone; kind <= eggMint; kind++ {
		if got := eggDrawsOnStage(kind); got != want[kind] {
			t.Errorf("eggDrawsOnStage(%d) = %v, want %v", kind, got, want[kind])
		}
	}
	// …and the dispatch: the gate is consulted, every stage draw is reachable, and
	// the frame limiter is told (an egg that animates without NoteAnimating freezes
	// at a low idle cap — the msAnim precedent).
	body := funcBodySource(t, "eggs.go", "drawStageEgg")
	for _, call := range []string{
		"eggDrawsOnStage", "drawEggPetals", "drawEggShards", "drawEggAurora", "drawEggFrost", "NoteAnimating",
	} {
		if !containsCall(body, call) {
			t.Errorf("drawStageEgg does not call %s — a stage egg is unreachable or unpaced", call)
		}
	}
	// The chatbox halves have the same deletion risk, and the two-part eggs have it
	// twice over: a kind can lose its ring while keeping its stage field, which
	// leaves the chatbox looking untouched while the stage goes on animating.
	ring := funcBodySource(t, "eggs.go", "drawCreatorEgg")
	for _, call := range []string{"drawEggCrystalRing", "drawEggAuroraRing", "drawEggMintRing"} {
		if !containsCall(ring, call) {
			t.Errorf("drawCreatorEgg no longer dispatches %s — that egg's chatbox half is gone", call)
		}
	}
}

// TestEveryEggKindHasBothDispatches is the encapsulation gate the roster needed
// once it stopped being five hand-remembered names.
//
// EVERY kind in the enum must resolve somewhere in drawCreatorEgg. A kind added
// to the trigger table and left out of the ring dispatch is the
// parsed-but-never-applied shape this repo has shipped before: the scan resolves,
// creatorEgg returns it, NoteAnimating fires, the frame limiter runs at full rate
// — and NOTHING is drawn. Everything compiles and every other egg gate stays
// green. Driven off the enum bound rather than a literal list, so a sixth guest
// is caught by having been added at all.
func TestEveryEggKindHasBothDispatches(t *testing.T) {
	ring := funcBodySource(t, "eggs.go", "drawCreatorEgg")
	stage := funcBodySource(t, "eggs.go", "drawStageEgg")
	// One painter per kind, named here so the gate can say WHICH one went missing.
	// A kind absent from this table fails loudly rather than being skipped.
	painters := map[uint8][2]string{
		eggFanat:     {"drawEggRainbow", ""},
		eggOmni:      {"drawEggOmni", ""},
		eggCrystal:   {"drawEggCrystalRing", "drawEggShards"},
		eggNyah:      {"drawEggNyah", ""},
		eggScatter:   {"drawEggScatter", "drawEggPetals"},
		eggNorthgate: {"drawEggAuroraRing", "drawEggAurora"},
		eggMint:      {"drawEggMintRing", "drawEggFrost"},
	}
	for kind := eggFanat; kind <= eggMint; kind++ {
		p, known := painters[kind]
		if !known {
			t.Fatalf("egg kind %d has no painter recorded — add it here, or a new egg can ship "+
				"triggering, pacing the frame limiter, and drawing nothing", kind)
		}
		if !containsCall(ring, p[0]) {
			t.Errorf("egg kind %d: drawCreatorEgg does not call %s", kind, p[0])
		}
		hasStage := p[1] != ""
		if hasStage != eggDrawsOnStage(kind) {
			t.Errorf("egg kind %d: the painter table says stage=%v, eggDrawsOnStage says %v",
				kind, hasStage, eggDrawsOnStage(kind))
		}
		if hasStage && !containsCall(stage, p[1]) {
			t.Errorf("egg kind %d: drawStageEgg does not call %s", kind, p[1])
		}
	}
}

// TestEggWordPresentIsABoundaryMatch pins the one trigger that is not a plain
// Contains, from both sides.
//
// "mint" is ordinary English, so a substring match fires on peppermint, minted,
// spearmint and mintage — the exact false-positive class the full-handle rule
// keeps out for the other five, applied to a handle that cannot buy specificity
// from length. The negative rows are the point; the positive ones stop a
// too-clever boundary rule from making the egg unreachable.
func TestEggWordPresentIsABoundaryMatch(t *testing.T) {
	for _, s := range []string{
		"mint", "Mint is here" /* callers lower-case first — see below */, "a mint",
		"mint.", "(mint)", "mint, please", "the-mint-condition", "chocolate mint chip",
	} {
		if !eggWordPresent(strings.ToLower(s), eggNameMint) {
			t.Errorf("eggWordPresent(%q) = false, want true — the egg is unreachable", s)
		}
	}
	for _, s := range []string{
		"peppermint", "spearmint", "minted", "mintage", "minty", "minting", "amint", "mint7", "7mint",
		"", "condition",
	} {
		if eggWordPresent(strings.ToLower(s), eggNameMint) {
			t.Errorf("eggWordPresent(%q) = true, want false — an innocent word lit an easter egg", s)
		}
	}
	// The empty needle is never a match, so a future trigger left blank cannot
	// silently fire on every message in the room.
	if eggWordPresent("anything at all", "") {
		t.Error("an empty trigger matched — every message would light that egg")
	}
	// The LAST occurrence still counts: the scan must keep walking past a
	// non-boundary hit rather than giving up at the first one.
	if !eggWordPresent("peppermint and mint", eggNameMint) {
		t.Error("a boundary match after a non-boundary one was missed — the scan stops too early")
	}
}

// TestGuestEggsAreProceduralAndBounded pins the two properties that make an egg
// shippable at all: the fields are FIXED caps (hard rule 4 — the per-frame cost
// is the same at 4K as at 720p) and the maths behind them is pure, so the whole
// effect is a function of (index, clock) with nothing to seed or reset.
func TestGuestEggsAreProceduralAndBounded(t *testing.T) {
	// The aurora ramp stays inside its own two hues however far the clock runs,
	// which is what keeps this egg out of the rainbow egg's territory. Sampled
	// across several drift cycles, including the seam where a naive lerp snaps.
	for i := 0; i <= 400; i++ {
		u := float64(i) / 100 // four full cycles
		col := eggAuroraColor(u, eggNorthSaturation)
		if col.A != 255 {
			t.Fatalf("eggAuroraColor(%.2f).A = %d, want 255 (the caller sets alpha)", u, col.A)
		}
		// Green→violet: blue is never the smallest channel at the violet end and
		// green never the smallest at the green end. A cheap, honest invariant —
		// the ramp must never wander through red/orange.
		if col.R > col.G && col.R > col.B {
			t.Fatalf("eggAuroraColor(%.2f) = %+v — the aurora ramp reached a red-dominant hue", u, col)
		}
	}
	// Continuity across the WRAP, which is the one place the fold is load-bearing.
	//
	// THE SEAM IS AT u≈1→0, NOT AT THE FOLD'S OWN APEX. This assertion used to
	// sample 0.499 against 0.501, which cannot fail: the fold maps 0.501 back onto
	// 0.499, so the two calls return the identical colour by construction — and an
	// UNFOLDED straight lerp is smooth at mid-ramp too, so it measured 0 for both
	// implementations. The discontinuity a straight lerp actually has is where the
	// caller's clock wraps: rampBase is Mod(t/eggNorthDriftSecs, 1), so u walks off
	// the top of the ramp and back onto the bottom once every drift cycle, and an
	// unfolded ramp snaps violet→green there (measured on that mutant: 204,97,255 →
	// 97,255,104). Sampled at the seam and one step wider, on EVERY channel.
	for _, seam := range []struct{ before, after float64 }{
		{0.999, 0.001},
		{0.98, 0.02},
	} {
		lo, hi := eggAuroraColor(seam.before, eggNorthSaturation), eggAuroraColor(seam.after, eggNorthSaturation)
		for _, ch := range []struct {
			name   string
			lo, hi uint8
		}{{"red", lo.R, hi.R}, {"green", lo.G, hi.G}, {"blue", lo.B, hi.B}} {
			if d := int(ch.lo) - int(ch.hi); d > eggAuroraSeamTol || d < -eggAuroraSeamTol {
				t.Errorf("the aurora ramp jumps %d in %s across its wrap (u=%.3f→%.3f) — the fold is "+
					"gone and the sky snaps once every %.0fs drift cycle", d, ch.name, seam.before, seam.after, eggNorthDriftSecs)
			}
		}
	}

	// The frost breath never lets go of the frame and never overshoots its reach:
	// below the floor the rime flickers out of existence, above 1 it would grow
	// past the length its own constants budgeted.
	for i := 0; i <= 200; i++ {
		got := eggMintCreep(float64(i)/10, 0.37, eggMintCreepSecs)
		if got < eggMintReachFloor-1e-9 || got > 1+1e-9 {
			t.Fatalf("eggMintCreep = %.4f at t=%.1f, want [%v,1]", got, float64(i)/10, eggMintReachFloor)
		}
	}
	// The mint ring alternates and MARCHES: at any instant the three rings are not
	// all one colour, and one band period later every ring has swapped.
	for _, at := range []float64{0, 1.1, eggMintBandSecs * 3.4, 61.7} {
		same := eggMintBandIsCocoa(0, at) == eggMintBandIsCocoa(1, at)
		if same {
			t.Errorf("at t=%.2f rings 0 and 1 wear the same colour — the band is not alternating", at)
		}
		for i := int32(0); i < eggRingCount; i++ {
			if eggMintBandIsCocoa(i, at) == eggMintBandIsCocoa(i, at+eggMintBandSecs) {
				t.Errorf("at t=%.2f ring %d did not swap after one band period — the march is gone", at, i)
			}
		}
	}
}

// eggAuroraSeamTol is how far a channel may move across the ramp's wrap, in
// 8-bit levels. The folded ramp measures EXACTLY 0 there (u and 1-u fold onto the
// same point), so every level of this is slack for a future ramp that is
// continuous without being symmetric; the unfolded lerp the fold exists to
// prevent moves 107 / 158 / 151, two orders past it.
const eggAuroraSeamTol = 8

// TestMintHashWindowsAreDisjoint pins the Mint egg's own distinct-salt rule
// against the three fields that share eggHash.
//
// Each field takes FOUR draws per element, so it owns the 4n indices from its
// salt; overlapping windows mean an element of one field wearing another's lane,
// size, speed and phase all at once. The flecks shipped at salt 0 — entirely
// inside the top frost edge's window — which is invisible only because the two
// spend those numbers on different axes, and would stop being invisible the day
// somebody gave a fleck a vertical drift. Driven off the caps, so growing a field
// past its neighbour's salt fails here rather than silently re-correlating.
func TestMintHashWindowsAreDisjoint(t *testing.T) {
	// eggHashDrawsPerElement is the four draws every element of every egg field
	// takes (lane, size, speed, phase) — the width one element occupies.
	const eggHashDrawsPerElement = 4
	windows := []struct {
		name  string
		start uint32
		count uint32
	}{
		{"top frost edge", 0, eggMintFingers},
		{"bottom frost edge", eggMintBottomSalt, eggMintFingers},
		{"cocoa flecks", eggMintFleckSalt, eggMintFlecks},
	}
	for i, a := range windows {
		aEnd := a.start + a.count*eggHashDrawsPerElement
		for _, b := range windows[i+1:] {
			bEnd := b.start + b.count*eggHashDrawsPerElement
			if a.start < bEnd && b.start < aEnd {
				t.Errorf("the %s window [%d,%d) overlaps the %s window [%d,%d) — an element of one "+
					"wears all four of the other's hashed properties, which is the correlation the "+
					"per-field salts exist to prevent",
					a.name, a.start, aEnd, b.name, b.start, bEnd)
			}
		}
	}
}

// TestEggShardTaperIsAFacet pins the shard silhouette: widest in the middle,
// pointed at both ends, and symmetric — a stack of rects that did NOT taper would
// draw a bar, which is the difference between a crystal facet and a coloured
// rectangle sitting on the stage.
func TestEggShardTaperIsAFacet(t *testing.T) {
	const last = eggCrystalSegments - 1
	if got := eggShardTaper(0); got != 0 {
		t.Errorf("the top segment must come to a point, got %.3f", got)
	}
	if got := eggShardTaper(last); got != 0 {
		t.Errorf("the bottom segment must come to a point, got %.3f", got)
	}
	mid := eggShardTaper(eggCrystalSegments / 2)
	for s := int32(0); s <= last; s++ {
		if eggShardTaper(s) > mid+1e-9 {
			t.Errorf("segment %d is wider than the middle — the shard is not a facet", s)
		}
		if got, sym := eggShardTaper(s), eggShardTaper(last-s); math.Abs(got-sym) > 1e-9 {
			t.Errorf("segment %d (%.3f) and its mirror %d (%.3f) differ — the shard is lopsided", s, got, last-s, sym)
		}
	}
}

// TestEggShardGlintIsAFlashNotAPulse pins the glint's shape: zero for most of the
// cycle, a smooth hump over the leading eggCrystalGlintFrac, and offset per shard
// so the field twinkles instead of strobing as one object.
func TestEggShardGlintIsAFlashNotAPulse(t *testing.T) {
	if got := eggShardGlint(0, 0); got != 0 {
		t.Errorf("the glint must start dark (a cosine hump from 0), got %.3f", got)
	}
	peak := eggShardGlint(eggCrystalGlintSecs*eggCrystalGlintFrac/2, 0)
	if peak < 0.99 {
		t.Errorf("the glint must reach full brightness mid-hump, got %.3f", peak)
	}
	// Everything past the hump is dark — that is what makes it a flash.
	for _, frac := range []float64{0.3, 0.5, 0.9} {
		if got := eggShardGlint(eggCrystalGlintSecs*frac, 0); got != 0 {
			t.Errorf("the glint must be dark at %.0f%% of the cycle, got %.3f", frac*100, got)
		}
	}
	// Two shards a phase apart must not flash together.
	if a, b := eggShardGlint(0.2, 0), eggShardGlint(0.2, 0.5); a == b {
		t.Error("two shards with different phases flashed identically — the field would strobe as one")
	}
}

// TestEggSceneTextMirrorsChatboxGate pins the agreement between the egg's two
// halves. The stage petals refresh from eggSceneText while the chatbox ring
// refreshes from drawChatEgg, and drawChatOverlay early-returns on a blankpost /
// empty scene — so if eggSceneText did NOT reproduce that same gate, a blankpost
// carrying a trigger word would latch the kind and leave petals falling over a
// stage whose chatbox never drew (and never cleared it).
func TestEggSceneTextMirrorsChatboxGate(t *testing.T) {
	for _, tc := range []struct {
		name string
		sc   courtroom.Scene
		want string
	}{
		{"plain message", courtroom.Scene{MessageText: "scatterflower"}, "scatterflower"},
		{"showname only still displays a box", courtroom.Scene{ShownameText: "Witch"}, ""},
		{"blankpost hides the box", courtroom.Scene{MessageText: "scatterflower", IsBlankPost: true}, ""},
		{"idle: no message, no showname", courtroom.Scene{}, ""},
	} {
		sc := tc.sc
		if got := eggSceneText(&sc); got != tc.want {
			t.Errorf("%s: eggSceneText = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestEggPetalFadeRampsBothEnds pins that petals ease in at the stage's top edge
// and out at its bottom one: a petal that appeared at full opacity would visibly
// pop into existence at the clip boundary.
func TestEggPetalFadeRampsBothEnds(t *testing.T) {
	if got := eggPetalFade(0); got != 0 {
		t.Errorf("fade at the very top = %v, want 0 (petals must not pop in)", got)
	}
	if got := eggPetalFade(1); got > 1e-9 {
		t.Errorf("fade at the very bottom = %v, want ~0 (petals must not pop out)", got)
	}
	if got := eggPetalFade(0.5); got != 1 {
		t.Errorf("fade mid-fall = %v, want 1 (full opacity between the ramps)", got)
	}
	// Monotone rise across the in-ramp, monotone fall across the out-ramp.
	for i := 1; i <= 10; i++ {
		lo, hi := eggPetalFade(float64(i-1)/10*eggScatterFadeFrac), eggPetalFade(float64(i)/10*eggScatterFadeFrac)
		if hi < lo {
			t.Fatalf("in-ramp not monotone at step %d: %v then %v", i, lo, hi)
		}
	}
	// Every sample stays a legal alpha multiplier, so the uint8 cast can't wrap.
	for i := 0; i <= 100; i++ {
		if f := eggPetalFade(float64(i) / 100); f < 0 || f > 1 {
			t.Fatalf("fade(%v) = %v, outside [0,1] — the alpha cast would wrap", float64(i)/100, f)
		}
	}
}

// TestEggPaletteAtWrapsContinuously pins that the ring's colour cycle is closed:
// sampling just before 1 must be close to sampling 0, or the ring would snap
// colour once per bloom period instead of drifting.
func TestEggPaletteAtWrapsContinuously(t *testing.T) {
	near, start := eggPaletteAt(0.9999), eggPaletteAt(0)
	if d := int(near.R) - int(start.R); d > 2 || d < -2 {
		t.Errorf("palette seam is discontinuous: R %d then %d", near.R, start.R)
	}
	// In-range for the whole cycle (the lerp must never overshoot a channel).
	for i := 0; i < 100; i++ {
		_ = eggPaletteAt(float64(i) / 100) // uint8 channels cannot leave range; this pins no panic on index wrap
	}
}

// TestEggHashSpreadsPetals guards against a degenerate mixer: the petal field's
// whole visual depends on the per-index hash decorrelating lane / speed / size /
// phase. If the hash collapsed, every petal would fall in one column and the egg
// would read as a single dropping line, which no pixel test would catch.
func TestEggHashSpreadsPetals(t *testing.T) {
	// Lanes are the salt-0 draw the petal loop uses for horizontal placement.
	var buckets [4]int
	for i := uint32(0); i < eggScatterPetals; i++ {
		u := eggUnit(eggHash(i * 4))
		if u < 0 || u >= 1 {
			t.Fatalf("eggUnit out of [0,1) for petal %d: %v", i, u)
		}
		buckets[int(u*4)]++
	}
	for q, n := range buckets {
		if n == 0 {
			t.Errorf("no petal lands in horizontal quarter %d — the hash is not spreading (buckets=%v)", q, buckets)
		}
	}
	// Distinct salts must not alias: lane and phase sharing a value would lock
	// every petal's sway to its column.
	same := 0
	for i := uint32(0); i < eggScatterPetals; i++ {
		if eggHash(i*4) == eggHash(i*4+3) {
			same++
		}
	}
	if same != 0 {
		t.Errorf("%d petals have lane == phase — salts are aliasing", same)
	}
}
