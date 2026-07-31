package ui

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/veandco/go-sdl2/sdl"
)

// ---------------------------------------------------------------------------
// Fixtures: real theme numbers, never invented ones.
// ---------------------------------------------------------------------------

// stockChatboxRects is AO2's shipped `default` theme, the same source
// ao2DefaultDesignRects (ao2defaultrects.go) is transcribed from:
//
//	ao2_chatbox = 0, 114, 256, 78
//	showname    = 1, 0, 46, 15
//	message     = 10, 13, 242, 57
//
// The two children are CHATBOX-RELATIVE, which is why theme_layout.go leaves
// them un-offset (AO2 parents both widgets to ui_vp_chatbox, courtroom.cpp:91).
var stockChatboxRects = map[string]sdl.Rect{
	"showname": {X: 1, Y: 0, W: 46, H: 15},
	"message":  {X: 10, Y: 13, W: 242, H: 57},
}

// layWith builds the minimum themeLayoutCache chatboxTextRects reads: the scaled
// child-rect map. Nothing else in the cache is consulted, which is itself part of
// the contract — the resolution is pure geometry.
func layWith(rects map[string]sdl.Rect) *themeLayoutCache {
	l := &themeLayoutCache{valid: true, r: make(map[string]sdl.Rect, len(rects))}
	for k, r := range rects {
		l.r[k] = r
	}
	return l
}

// ---------------------------------------------------------------------------
// The message's Qt document margin.
// ---------------------------------------------------------------------------

// TestThemedMessageCarriesQtDocumentMargin pins the inset AO2's chatbox message
// gets for free on all four sides. ui_vp_message is a frameless QTextEdit with
// both scrollbars forced off (AO2-Client courtroom.cpp:97-102), so its only inset
// is QTextDocument::documentMargin — default 4. A Qt 6.5.3 probe of that exact
// widget at this exact size reports documentMargin 4.0, a position-0 text cursor
// at (4, 4) and a first-block bounding rect 234 px wide inside a 242 px viewport.
//
// SYMMETRIC on purpose: the chatlog's inset deliberately spares the right edge
// because a scrollbar track lives there, and reusing it here would leave the
// message text hard against the theme's right margin.
func TestThemedMessageCarriesQtDocumentMargin(t *testing.T) {
	box := sdl.Rect{X: 400, Y: 300, W: 256, H: 78}
	_, msg := chatboxTextRects(box, layWith(stockChatboxRects), box.Y+chatBoxTopStrip)

	want := sdl.Rect{X: 400 + 10 + 4, Y: 300 + 13 + 4, W: 242 - 8, H: 57 - 8}
	if msg != want {
		t.Fatalf("message rect = %+v, want %+v — the 4 px document margin is AO2's, not ours to drop", msg, want)
	}
	if themedMessageInsetPx != 4 {
		t.Fatalf("themedMessageInsetPx = %d, want 4 (QTextDocument::documentMargin's default, measured on Qt 6.5.3)",
			themedMessageInsetPx)
	}
}

// TestThemedShownameIsNotInset pins the OTHER half of the finding, which is
// easier to get wrong because it looks like an omission: the showname gets NO
// inset. ui_vp_showname is an AOChatboxLabel : QLabel (aotextboxwidgets.h:12),
// and QLabel applies its indent only when it has a frame — this one has none, so
// the Qt 6.5.3 probe reads indent -1, margin 0, frameWidth 0 and paints text
// flush to the widget edge. AOChatboxLabel's own indent lives exclusively in its
// OUTLINED paint path (aotextboxwidgets.cpp:83), which AsyncAO does not
// implement.
func TestThemedShownameIsNotInset(t *testing.T) {
	if themedShownameInsetPx != 0 {
		t.Fatalf("themedShownameInsetPx = %d, want 0 — AO2's showname label has no frame, so no indent applies",
			themedShownameInsetPx)
	}
	box := sdl.Rect{X: 400, Y: 300, W: 256, H: 78}
	name, _ := chatboxTextRects(box, layWith(stockChatboxRects), box.Y+chatBoxTopStrip)

	want := sdl.Rect{X: 400 + 1, Y: 300 + 0, W: 46, H: 15}
	if name != want {
		t.Fatalf("showname rect = %+v, want %+v — the theme's rect verbatim, offset only by the chatbox origin", name, want)
	}
}

// TestChatboxTextRectsFallbackKeepsClassicOffsets covers the theme that declares
// an ao2_chatbox but no showname / message child. Every one of the 74 reference
// themes declares both, so this is hand-written or truncated theme data — it must
// still land on the classic overlay's own inset rather than at the box origin.
func TestChatboxTextRectsFallbackKeepsClassicOffsets(t *testing.T) {
	box := sdl.Rect{X: 10, Y: 20, W: 300, H: 100}
	fallbackMsgY := box.Y + chatBoxTopStrip
	name, msg := chatboxTextRects(box, layWith(nil), fallbackMsgY)

	if name.X != box.X+chatOverlayPadX || name.Y != box.Y+chatOverlayNameY || name.W != box.W-2*chatOverlayPadX {
		t.Fatalf("fallback showname = %+v, want the classic overlay's own inset", name)
	}
	// The fallback message still carries the Qt margin: it stands in for a widget
	// AO2 would have drawn through a QTextEdit.
	if msg.X != box.X+chatOverlayPadX+themedMessageInsetPx || msg.Y != fallbackMsgY+themedMessageInsetPx {
		t.Fatalf("fallback message = %+v, want the classic inset plus the document margin", msg)
	}
}

// TestChatboxTextRectsHonourExportFallbackRow pins the ONE difference the export
// mirror is allowed to keep: its taller fallback name row. It may only bite when
// the theme declares no `message` rect — with one declared, the live chatbox and
// an export frame must resolve byte-identical geometry or the video diverges from
// what the player watched.
func TestChatboxTextRectsHonourExportFallbackRow(t *testing.T) {
	box := sdl.Rect{X: 0, Y: 0, W: 256, H: 78}

	liveName, liveMsg := chatboxTextRects(box, layWith(stockChatboxRects), box.Y+chatBoxTopStrip)
	expName, expMsg := chatboxTextRects(box, layWith(stockChatboxRects), box.Y+gifChatNameRowH)
	if liveName != expName || liveMsg != expMsg {
		t.Fatalf("declared child rects diverged between live %+v/%+v and export %+v/%+v", liveName, liveMsg, expName, expMsg)
	}

	_, bareLive := chatboxTextRects(box, layWith(nil), box.Y+chatBoxTopStrip)
	_, bareExp := chatboxTextRects(box, layWith(nil), box.Y+gifChatNameRowH)
	if bareLive.Y == bareExp.Y {
		t.Fatal("the fallback message row is supposed to differ between the live chatbox and an export frame")
	}
}

// TestChatboxTextRectsDoNotClampToTheBox is the guard for the clip caution.
//
// AO2 does not clip ui_vp_message to the chatbox: the widget is re-parented to
// the Courtroom and only offset by the chatbox origin (courtroom.cpp:3310), so a
// theme may legitimately declare a message wider than the box it sits in. Eight
// design files in the reference corpus do, three of them badly enough to lose a
// column if we ever "tidied" the rect: CCSmol (message right edge 259 in a
// 256-wide chatbox), Mobile (393 in 392) and HDF-Fullscreen 1080p (770 in 765).
// Resolution must pass the overhang through untouched.
func TestChatboxTextRectsDoNotClampToTheBox(t *testing.T) {
	// CCSmol, verbatim: ao2_chatbox = 0,114,256,78 / message = 3,18,256,59.
	box := sdl.Rect{X: 0, Y: 114, W: 256, H: 78}
	_, msg := chatboxTextRects(box, layWith(map[string]sdl.Rect{
		"showname": {X: 6, Y: 0, W: 256, H: 17},
		"message":  {X: 3, Y: 18, W: 256, H: 59},
	}), box.Y+chatBoxTopStrip)

	if got, want := msg.X+msg.W, box.X+3+256-themedMessageInsetPx; got != want {
		t.Fatalf("message right edge = %d, want %d — the theme's overhang must survive resolution", got, want)
	}
	if msg.W != 256-2*themedMessageInsetPx {
		t.Fatalf("message width = %d, want %d — the box must not clamp the child", msg.W, 256-2*themedMessageInsetPx)
	}
}

// TestInsetChatRectNeverInverts covers theme data smaller than the margin it is
// asked to carry. It must keep its ORIGIN (so text still starts where the theme
// put it) and report no room, never a negative extent and never stageframe.go's
// collapse-to-centre.
func TestInsetChatRectNeverInverts(t *testing.T) {
	got := insetChatRect(sdl.Rect{X: 30, Y: 40, W: 5, H: 3}, themedMessageInsetPx)
	want := sdl.Rect{X: 30 + themedMessageInsetPx, Y: 40 + themedMessageInsetPx, W: 0, H: 0}
	if got != want {
		t.Fatalf("insetChatRect of an undersized rect = %+v, want %+v", got, want)
	}
}

// ---------------------------------------------------------------------------
// showname_align
// ---------------------------------------------------------------------------

// TestParseShownameAlign mirrors AO2's own ladder (courtroom.cpp:3338-3354):
// lowercase the value, test "right" / "center" / "justify", else left. `justify`
// maps to CENTRE because that is the branch AO2 wrote for it — Qt::AlignHCenter,
// identical to "center".
func TestParseShownameAlign(t *testing.T) {
	cases := []struct {
		raw  string
		want shownameAlign
	}{
		{"center", shownameAlignCenter},
		{"CENTER", shownameAlignCenter},
		{"  Center  ", shownameAlignCenter},
		{"justify", shownameAlignCenter},
		{"right", shownameAlignRight},
		{"left", shownameAlignLeft},
		{"", shownameAlignLeft},       // key absent
		{"middle", shownameAlignLeft}, // AO2's else arm swallows anything else
		{"centre", shownameAlignLeft}, // the British spelling is NOT AO2's literal
		{"0", shownameAlignLeft},
	}
	for _, c := range cases {
		if got := parseShownameAlign(c.raw); got != c.want {
			t.Errorf("parseShownameAlign(%q) = %d, want %d", c.raw, got, c.want)
		}
	}
}

// TestShownameAlignReadFromDesignINI walks the real plumbing: the key name, the
// DesignValue lookup the theme-apply goroutine uses, and the INI reader in front
// of it. The trailing-comment row is the one that would have silently failed
// before the reader learned Qt's inline-';' rule — the whole AOHDUltra family
// ships `clock_0_align = center;`-shaped values.
func TestShownameAlignReadFromDesignINI(t *testing.T) {
	for _, tc := range []struct {
		name   string
		design string
		want   shownameAlign
	}{
		{"center", "showname_align = center", shownameAlignCenter},
		{"left", "showname_align = left", shownameAlignLeft},
		{"absent", "showname = 1, 0, 46, 15", shownameAlignLeft},
		{"trailing comment", "showname_align = center ; centred like the art", shownameAlignCenter},
		{"uppercase", "showname_align = Center", shownameAlignCenter},
	} {
		t.Run(tc.name, func(t *testing.T) {
			th := writeThemeDesign(t, tc.design+"\n")
			raw, _ := th.DesignValue(shownameAlignKey)
			if got := parseShownameAlign(raw); got != tc.want {
				t.Fatalf("design %q → raw %q → %d, want %d", tc.design, raw, got, tc.want)
			}
		})
	}
}

// TestShownameAlignZeroValueIsLeft pins that a themeless App — and every theme
// that says nothing — keeps the placement AsyncAO has always drawn.
func TestShownameAlignZeroValueIsLeft(t *testing.T) {
	var a App
	if a.themeShownameAlign != shownameAlignLeft {
		t.Fatalf("zero-value themeShownameAlign = %d, want left", a.themeShownameAlign)
	}
	var zero shownameAlign
	if zero != shownameAlignLeft {
		t.Fatal("shownameAlignLeft must be the zero value, or dropping a theme changes the layout")
	}
}

// TestShownameSpanPlacement runs the placement over real corpus showname rects,
// including the flat ones that are SHORTER than their text is tall and the tall
// ones where a mis-set alignment is most visible.
func TestShownameSpanPlacement(t *testing.T) {
	cases := []struct {
		name     string
		box      sdl.Rect
		textW    int32
		al       shownameAlign
		wantX    int32
		wantMaxW int32
	}{
		// AceAttorney2x, showname = 92x32.
		{"2x left", sdl.Rect{X: 100, Y: 50, W: 92, H: 32}, 40, shownameAlignLeft, 100, 92},
		{"2x center", sdl.Rect{X: 100, Y: 50, W: 92, H: 32}, 40, shownameAlignCenter, 126, 66},
		{"2x right", sdl.Rect{X: 100, Y: 50, W: 92, H: 32}, 40, shownameAlignRight, 152, 40},
		// microsoft surface 4k webao, showname = 232x120 — the tall fixture.
		{"4k center", sdl.Rect{X: 0, Y: 0, W: 232, H: 120}, 100, shownameAlignCenter, 66, 166},
		// va-11 hall-a, showname = 310x13 — a rect shorter than the text is tall.
		// Height plays no part in horizontal placement, which is the point.
		{"flat center", sdl.Rect{X: 5, Y: 5, W: 310, H: 13}, 110, shownameAlignCenter, 105, 210},
		// CCSmol, showname = 256x17.
		{"smol right", sdl.Rect{X: 0, Y: 0, W: 256, H: 17}, 56, shownameAlignRight, 200, 56},
		// A name WIDER than its box: the offset floors at zero for every alignment,
		// so the name's head always survives and its tail clips.
		{"overflow center", sdl.Rect{X: 20, Y: 0, W: 46, H: 15}, 300, shownameAlignCenter, 20, 46},
		{"overflow right", sdl.Rect{X: 20, Y: 0, W: 46, H: 15}, 300, shownameAlignRight, 20, 46},
		{"exact fit right", sdl.Rect{X: 20, Y: 0, W: 46, H: 15}, 46, shownameAlignRight, 20, 46},
	}
	for _, c := range cases {
		x, maxW := shownameSpan(c.box, c.textW, c.al)
		if x != c.wantX || maxW != c.wantMaxW {
			t.Errorf("%s: shownameSpan = (%d, %d), want (%d, %d)", c.name, x, maxW, c.wantX, c.wantMaxW)
		}
		if x < c.box.X {
			t.Errorf("%s: origin %d is left of the box (%d) — the name's head would be cut", c.name, x, c.box.X)
		}
		if x+maxW != c.box.X+c.box.W {
			t.Errorf("%s: the clip window ends at %d, want the box's right edge %d", c.name, x+maxW, c.box.X+c.box.W)
		}
	}
}

// TestShownameSpanLeftIsIdentity is the perf-shaped half of the contract: left
// alignment must return the box untouched for ANY text width, which is what lets
// shownameSpanFor skip measuring the name on the default path.
func TestShownameSpanLeftIsIdentity(t *testing.T) {
	box := sdl.Rect{X: 77, Y: 12, W: 46, H: 15}
	for _, w := range []int32{0, 1, 45, 46, 47, 4000} {
		x, maxW := shownameSpan(box, w, shownameAlignLeft)
		if x != box.X || maxW != box.W {
			t.Fatalf("left alignment moved the label for textW=%d: (%d, %d)", w, x, maxW)
		}
	}
}

// ---------------------------------------------------------------------------
// The classic twin, and the anti-drift guard.
// ---------------------------------------------------------------------------

// TestClassicChatOverlayInsetValuesUnchanged pins the classic overlay's inset to
// the exact pixels it drew before those literals were given names. Naming was the
// whole change there (hard rule 9); a value edit hiding behind it would move every
// classic chatbox on every screen.
func TestClassicChatOverlayInsetValuesUnchanged(t *testing.T) {
	if chatOverlayPadX != 8 || chatOverlayNameY != 4 || chatBoxTopStrip != 26 || chatOverlayBoldNudge != 1 {
		t.Fatalf("classic overlay inset changed: padX=%d nameY=%d topStrip=%d boldNudge=%d, want 8/4/26/1",
			chatOverlayPadX, chatOverlayNameY, chatBoxTopStrip, chatOverlayBoldNudge)
	}
	// Every expression drawChatOverlay builds out of them, against the literals it
	// used to build them out of. A representative box, not a special one.
	box := sdl.Rect{X: 120, Y: 400, W: 300, H: 90}
	for _, c := range []struct {
		what string
		got  int32
		want int32
	}{
		{"showname x", box.X + chatOverlayPadX, box.X + 8},
		{"faux-bold showname x", box.X + chatOverlayPadX + chatOverlayBoldNudge, box.X + 9},
		{"showname y", box.Y + chatOverlayNameY, box.Y + 4},
		{"showname clip width", box.W - 2*chatOverlayPadX, box.W - 16},
		{"message wrap width", box.W - 2*chatOverlayPadX, box.W - 16},
		{"message x", box.X + chatOverlayPadX, box.X + 8},
		{"message y", box.Y + chatBoxTopStrip, box.Y + 26},
		{"message/selection height", box.H - chatBoxTopStrip, box.Y + box.H - (box.Y + 26)},
	} {
		if c.got != c.want {
			t.Errorf("classic overlay %s = %d, want %d — naming the literals must not move a pixel", c.what, c.got, c.want)
		}
	}
}

// ---------------------------------------------------------------------------
// Text-to-box scale parity: the canvas fold.
// ---------------------------------------------------------------------------

// faceSizeFor mirrors Ctx.buildSet's own size arithmetic
//
//	size = UIFontSize * pct * devPct / (DefaultScalePct ^ 2)
//
// which is the only thing about a percent that reaches the screen. Two percents
// with the same face size are the same picture drawn twice, and the point of
// canonFontPct is that they are never asked for as two different cache keys.
func faceSizeFor(pct, devPct int) int {
	if devPct <= 0 {
		devPct = DefaultScalePct
	}
	return UIFontSize * pct * devPct / (DefaultScalePct * DefaultScalePct)
}

// TestCanvasTextScalePctTakesTheTighterAxis pins that the fold rides
// emoteCellScale — one factor for both axes, as AO2 has exactly one
// Options::themeScalingFactor() — and that it rounds rather than truncates.
func TestCanvasTextScalePctTakesTheTighterAxis(t *testing.T) {
	for _, tc := range []struct {
		name           string
		scaleX, scaleY float64
		want           int
	}{
		{"native 1:1 is exactly 100", 1, 1, DefaultScalePct},
		// Letterbox/Native/Crop/Custom always hand in equal axes.
		{"uniform downscale", 0.5, 0.5, 50},
		{"uniform upscale", 2.5, 2.5, 250},
		// Stretch is the only mode with independent axes. The TIGHTER one wins, so
		// the text can never outgrow the height of the box the theme drew for it.
		{"stretch takes the tighter axis (height)", 2.4, 1.1, 110},
		{"stretch takes the tighter axis (width)", 0.8, 3.0, 80},
		// Rounding, not truncation: the stock 714x579 canvas letterboxed into the
		// default 1152x864 window is 1.4922, which must land on 149 and not 149.22
		// truncated to a different pixel.
		{"rounds to nearest", 1.4922, 1.4922, 149},
		{"rounds up above the half", 1.007, 1.007, 101},
		{"rounds down below the half", 1.004, 1.004, 100},
		// A canvas scaled into nothing still has to name a size.
		{"degenerate canvas floors at 1", 0.0001, 0.0001, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := canvasTextScalePct(tc.scaleX, tc.scaleY); got != tc.want {
				t.Fatalf("canvasTextScalePct(%v, %v) = %d, want %d", tc.scaleX, tc.scaleY, got, tc.want)
			}
		})
	}
}

// TestFoldIsIdentityAtOneToOne is the "the default case does not move" guard.
//
// ThemeFitNative is the shipped default and pins the scale at exactly 1.0 for
// any window at least as large as the theme's canvas — the majority of the
// reference corpus, since the import auto-resize snaps the window to the canvas.
// The fold must be the IDENTITY there, not arithmetic that happens to round back
// to the same number, so that an out-of-the-box themed chatbox is byte-identical
// to the one before this existed.
func TestFoldIsIdentityAtOneToOne(t *testing.T) {
	for _, pct := range []int{themeFontMinPct, 42, 83, DefaultScalePct, 133, 416, themeFontMaxPct, themedCanvasFontMaxPct + 500} {
		for _, dev := range []int{0, 75, DefaultScalePct, 125, 200} {
			if got := foldCanvasFontPct(pct, DefaultScalePct, dev); got != pct {
				t.Fatalf("foldCanvasFontPct(%d, 100, %d) = %d, want %d unchanged", pct, dev, got, pct)
			}
		}
	}
	// A cache that never carried a canvas reads as 1:1 too: the early return in
	// themeLayoutIn for a theme with no courtroom/viewport rect leaves textPct at
	// zero, and so does every hand-built cache in a test.
	if got := (&themeLayoutCache{}).textScalePct(); got != DefaultScalePct {
		t.Fatalf("a canvas-less layout reports textScalePct %d, want %d (no fold)", got, DefaultScalePct)
	}
	if got := layWith(stockChatboxRects).textScalePct(); got != DefaultScalePct {
		t.Fatalf("a rects-only layout reports textScalePct %d, want %d", got, DefaultScalePct)
	}
}

// TestFoldAppliesTheCanvasOutsideTheDeclaredClamp pins the ORDER of the two
// clamps, which is the whole hazard.
//
// themeFontMinPct/themeFontMaxPct exist to reject sloppy theme data and they
// genuinely bind on real themes: `microsoft surface 4k webao` declares
// `message = 50`, i.e. 416%, which clips to 400% before any canvas is involved.
// The canvas scale is not theme data and must not be judged by that clamp — it
// multiplies outside it — and the product then gets its own, higher cap.
func TestFoldAppliesTheCanvasOutsideTheDeclaredClamp(t *testing.T) {
	// The declared clamp still bites where it always did.
	if got := clampInt(themeFontPct(50), themeFontMinPct, themeFontMaxPct); got != themeFontMaxPct {
		t.Fatalf("a declared 50 pt resolves to %d%%, want the %d%% ceiling", got, themeFontMaxPct)
	}
	// ...and the canvas may then take the result ABOVE it. 400% x 250% = 1000%,
	// which themeFontMaxPct would have thrown away.
	if got := foldCanvasFontPct(themeFontMaxPct, 250, DefaultScalePct); got <= themeFontMaxPct {
		t.Fatalf("folded scale = %d%%, want more than the declared ceiling %d%% — "+
			"the canvas factor must apply OUTSIDE the theme-data clamp", got, themeFontMaxPct)
	}
	// ...but not without a bound of its own. ThemeFitCustom multiplies the fit by
	// up to MaxThemeZoom, which on the corpus's smallest canvas asks for >2500%.
	if got := foldCanvasFontPct(themeFontMaxPct, 2530, DefaultScalePct); got != themedCanvasFontMaxPct {
		t.Fatalf("folded scale = %d%%, want the %d%% cap — an unbounded font size is an unbounded glyph atlas",
			got, themedCanvasFontMaxPct)
	}
	// The floor survives too: a canvas shrunk to nothing must not produce a
	// zero-size face.
	if got := foldCanvasFontPct(themeFontMinPct, 1, DefaultScalePct); got < themeFontMinPct {
		t.Fatalf("folded scale = %d%%, want at least the %d%% floor", got, themeFontMinPct)
	}
}

// TestCanonFontPctOpensTheSameFaceSize pins the two properties the drag-resize
// safety net rests on: the canonical percent renders IDENTICALLY to the one it
// replaces, and it is the smallest such percent (so re-folding it is a no-op and
// two neighbouring window widths cannot ask for two cache keys that draw the
// same thing).
func TestCanonFontPctOpensTheSameFaceSize(t *testing.T) {
	for _, dev := range []int{0, 75, DefaultScalePct, 125, 150, 200} {
		for pct := themeFontMinPct; pct <= themedCanvasFontMaxPct; pct++ {
			canon := canonFontPct(pct, dev)
			if got, want := faceSizeFor(canon, dev), faceSizeFor(pct, dev); got != want {
				t.Fatalf("canonFontPct(%d, %d) = %d opens size %d, want %d — the canonical percent must draw the same picture",
					pct, dev, canon, got, want)
			}
			if canon > pct {
				t.Fatalf("canonFontPct(%d, %d) = %d rounded UP past what the fold asked for", pct, dev, canon)
			}
			if again := canonFontPct(canon, dev); again != canon {
				t.Fatalf("canonFontPct is not idempotent at %d (dev %d): %d then %d", pct, dev, canon, again)
			}
		}
	}
}

// TestCanvasFontFoldDoesNotThrashOnDragResize is the performance contract.
//
// Every fontSet keys on the percent, and a rebuild there closes and re-opens the
// whole chain and then calls purgeTextCache — which destroys every cached label
// texture and the entire text atlas. That is priced as a settings action. Folding
// the canvas scale in means the percent now moves with the WINDOW, and a live
// drag-resize delivers a new size every frame, so the fold must not ask for a new
// percent unless the font size on screen genuinely changed.
//
// The drag simulated here is a 4:3 window dragged from 400 to 1600 px wide over
// the stock 714x579 canvas — 1201 distinct sizes, all of which the layout cache
// rebuilds for.
func TestCanvasFontFoldDoesNotThrashOnDragResize(t *testing.T) {
	const (
		designW, designH = 714.0, 579.0
		firstW, lastW    = 400, 1600
	)
	declared := clampInt(themeFontPct(10), themeFontMinPct, themeFontMaxPct) // the stock theme's `message = 10`

	pcts := map[int]bool{}
	sizes := map[int]bool{}
	rawPcts := map[int]bool{}
	for w := firstW; w <= lastW; w++ {
		h := float64(w) * 3 / 4
		s := math.Min(float64(w)/designW, h/designH) // Letterbox
		canvas := canvasTextScalePct(s, s)
		folded := foldCanvasFontPct(declared, canvas, DefaultScalePct)
		pcts[folded] = true
		sizes[faceSizeFor(folded, DefaultScalePct)] = true
		rawPcts[clampInt(declared*canvas/DefaultScalePct, themeFontMinPct, themedCanvasFontMaxPct)] = true
	}
	// One percent per font size, plus AT MOST one. The extra is the exact-1:1 point
	// the drag passes through: foldCanvasFontPct returns the declared percent
	// UNCHANGED there so a Native-fit chatbox is byte-identical to the unfolded
	// one, and that percent is not the canonical spelling of its size. It costs one
	// rebuild, once, when a drag crosses 1.0 — cheap for the guarantee it buys.
	if extra := len(pcts) - len(sizes); extra > 1 {
		t.Fatalf("the drag asked for %d distinct percents but only %d distinct font sizes — "+
			"the %d extra rebuilds each tear down the whole text atlas for an identical picture",
			len(pcts), len(sizes), extra)
	}
	// And the saving is the point: without the canonical snap the same drag asks
	// for a percent per window width.
	if len(rawPcts) <= len(pcts) {
		t.Fatalf("canonicalisation saved nothing: %d raw percents vs %d canonical", len(rawPcts), len(pcts))
	}
	if len(pcts) > (lastW-firstW+1)/10 {
		t.Fatalf("a %d-step drag rebuilds the chat font %d times, want at most one per ~10 steps",
			lastW-firstW+1, len(pcts))
	}
}

// TestThemedChatboxScaleParityAcrossTheCorpus is the measurement this work was
// justified by, frozen as a test.
//
// AO2 multiplies design rects AND courtroom_fonts.ini point sizes by the one
// Options::themeScalingFactor(), so how much text fits a theme's message rect —
// rect width divided by font size — is fixed by the theme author. AsyncAO scaled
// the rect and not the font, so the ratio moved by exactly the canvas scale.
//
// The rows are real numbers read out of the reference corpus (design canvas from
// `courtroom`, message width from `message`, point size from courtroom_fonts.ini
// `message`) and span the whole measured range. The window is 1152x864 on
// Letterbox, the shipped default window on the mode that keeps a theme's aspect.
func TestThemedChatboxScaleParityAcrossTheCorpus(t *testing.T) {
	const winW, winH = 1152.0, 864.0
	for _, tc := range []struct {
		theme            string
		designW, designH float64
		msgW             float64
		points           int
	}{
		// The one corpus theme whose canvas is bigger than a normal window, and the
		// only one that exercises the declared-percent ceiling (50 pt = 416%).
		{"microsoft surface 4k webao", 3240, 2014, 1300, 50},
		{"WebAO Client", 1920, 1018, 690, 30},
		{"AOHDUltra", 1918, 982, 600, 16},
		{"AceAttorney2x", 944, 600, 484, 24},
		{"default (stock AO2)", 714, 579, 242, 10},
		{"Tiniest", 450, 256, 250, 10},
		// The smallest canvas in the corpus, and the worst divergence in it.
		{"Viewport", 256, 256, 238, 12},
	} {
		t.Run(tc.theme, func(t *testing.T) {
			scale := math.Min(winW/tc.designW, winH/tc.designH)
			declared := clampInt(themeFontPct(tc.points), themeFontMinPct, themeFontMaxPct)
			// AO2 draws the theme's rect and the theme's point size, both times the
			// same factor, so its capacity is the authored one at any scale.
			capAO2 := tc.msgW / float64(tc.points)
			capBefore := tc.msgW * scale / float64(faceSizeFor(declared, DefaultScalePct))
			folded := foldCanvasFontPct(declared, canvasTextScalePct(scale, scale), DefaultScalePct)
			capAfter := tc.msgW * scale / float64(faceSizeFor(folded, DefaultScalePct))

			before, after := capBefore/capAO2, capAfter/capAO2
			if before >= 0.9 && before <= 1.1 {
				t.Fatalf("%s was already at parity (%.2fx) — this row proves nothing; pick a divergent theme", tc.theme, before)
			}
			// 15%: the residue is themeFontPct's point-size-to-pixel truncation (a
			// declared 10 pt resolves to 83%, which opens a 9 px face, not a 10 px
			// one), which predates this work and is not the fold's to fix.
			if after < 0.85 || after > 1.15 {
				t.Fatalf("%s: capacity ratio %.2fx before, %.2fx after — the fold must land inside 15%% of AO2", tc.theme, before, after)
			}
			t.Logf("%-28s scale %.3f  capacity %.2fx -> %.2fx", tc.theme, scale, before, after)
		})
	}
}

// TestThemeLayoutBakesTheCanvasTextScale walks the real plumbing: the cache field
// is filled on the cold rebuild, it agrees with emoteCellScale, and the draw path
// reads it without touching a float.
func TestThemeLayoutBakesTheCanvasTextScale(t *testing.T) {
	// The 4k canvas at the default window: the only corpus theme that scales DOWN
	// at a normal size, so textPct is meaningfully != 100.
	a := stageNativeFit(t, 3240, 2014)
	lay := a.themeLayout(1152, 864)
	if !lay.valid {
		t.Fatal("themeLayout did not validate")
	}
	if want := canvasTextScalePct(lay.scaleX, lay.scaleY); lay.textScalePct() != want {
		t.Fatalf("baked textPct = %d, want %d (emoteCellScale of the built canvas)", lay.textScalePct(), want)
	}
	if lay.textScalePct() >= DefaultScalePct {
		t.Fatalf("a canvas shrunk to fit reports textPct %d, want below %d", lay.textScalePct(), DefaultScalePct)
	}

	// ...and the Native default on a window that fits the canvas is the identity,
	// so a stock themed chatbox resolves exactly the scale it always did.
	b := stageNativeFit(t, 714, 579)
	b.chatPct = DefaultScalePct
	fit := b.themeLayout(1152, 864)
	if fit.textScalePct() != DefaultScalePct {
		t.Fatalf("Native at a window larger than the canvas baked textPct %d, want %d", fit.textScalePct(), DefaultScalePct)
	}
	if got, want := b.themedChatPct(elemMessage, b.chatPct, fit), b.messagePct(); got != want {
		t.Fatalf("themed message scale %d != unfolded %d at 1:1 — the default must not move", got, want)
	}
	if got, want := b.themedChatPct(elemShowname, DefaultScalePct, fit), b.elemPct(elemShowname, DefaultScalePct); got != want {
		t.Fatalf("themed showname scale %d != unfolded %d at 1:1", got, want)
	}
}

// TestThemedChatboxRectsResolvedInOnePlace is the mirror-site guard.
//
// The live themed chatbox (theme_layout.go) and the export's themed chatbox
// (gifexport.go) each used to resolve the showname / message child rects with
// their own copy of the arithmetic, so a fix to one silently left the other
// behind — which is exactly how the export would have kept drawing an un-inset,
// hard-left showname after the live box was fixed. Both now call
// chatboxTextRects, and this test fails the moment either grows a second copy.
func TestThemedChatboxRectsResolvedInOnePlace(t *testing.T) {
	for _, file := range []string{"theme_layout.go", "gifexport.go"} {
		src, err := os.ReadFile(filepath.Join(".", file))
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		for _, key := range []string{`lay.rect("showname")`, `lay.rect("message")`} {
			if strings.Contains(string(src), key) {
				t.Errorf("%s resolves %s itself — the chatbox child rects belong to chatboxTextRects (chatboxfit.go), "+
					"or the live chatbox and the video export will drift apart again", file, key)
			}
		}
	}
}
