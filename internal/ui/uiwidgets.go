package ui

import (
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/veandco/go-sdl2/sdl"
	"github.com/veandco/go-sdl2/ttf"

	"github.com/SyntaxNyah/AsyncAO/internal/render"
)

// LabelClippedFontAlpha is LabelClippedFont with a per-draw alpha, applied with
// SetAlphaMod on the cached texture rather than by changing the ink colour.
func (c *Ctx) LabelClippedFontAlpha(font *ttf.Font, x, y, maxW int32, text string, col sdl.Color, alpha uint8) {
	if alpha == 255 {
		c.LabelClippedFont(font, x, y, maxW, text, col)
		return
	}
	t, ok := c.textTexture(text, col, font)
	if !ok {
		return
	}
	w := t.logicalW()
	if w > maxW {
		w = maxW
	}
	_ = t.tex.SetAlphaMod(alpha)
	c.blitLabel(t, x, y, w)
	_ = t.tex.SetAlphaMod(255)
}

// TextWidth measures a label in the chrome font, memoized — screens call
// it per frame for fixed labels and each miss is a CGO TTF measure. The
// memo shares the text cache's lifecycle (purged together, same bound).
// #77: measures the LOGICAL chrome face (c.font), NOT the device sibling — so
func (c *Ctx) TextWidth(text string) int32 {
	if c.font == nil {
		return 0 // headless tests; real Ctx always has the chrome font
	}
	// A panel face measures in its OWN memo. widthCache is keyed by string alone
	// and holds the chrome face's advances, so letting a panel face write into it
	// would hand the next chrome label a width measured in a different font — and
	// layout, not just drawing, would be wrong.
	if c.panelFont != nil {
		return c.panelTextWidth(text)
	}
	// Non-Latin measures on the face it will actually DRAW with (chromeFaceFor).
	// widthCache holds the embedded chrome face's advances, and a fallback face's
	// are different — measuring one and painting the other is how a label ends up
	// clipped, mis-centred, or overlapping its neighbour. The font-keyed memo is
	// used instead, so a chain change can never leave a stale width behind: its key
	// carries the face pointer, and a rebuilt chain means new pointers.
	if hasNonASCII(text) {
		if f := c.chromeFaceFor(text); f != c.font {
			w, _ := c.fontTextWidth(f, text)
			return w
		}
	}
	if w, ok := c.widthCache[text]; ok {
		return w
	}
	w, _, err := c.font.SizeUTF8(text)
	if err != nil {
		return 0
	}
	if len(c.widthCache) >= textCacheMax {
		c.widthCache = make(map[string]int32, textCacheMax)
	}
	c.widthCache[text] = int32(w)
	return int32(w)
}

// panelTextWidth is TextWidth on the armed panel face. Same cap and the same
// wholesale-reset-on-overflow rule as widthCache; created lazily, so it costs
// nothing until a panel is actually dressed.
func (c *Ctx) panelTextWidth(text string) int32 {
	w, _ := c.fontTextWidth(c.panelFont, text)
	return w
}

// fontTextWidth measures text on an EXPLICIT face, memoized in the same
// font-keyed map panelTextWidth uses (the key already carries the face, so an
// arbitrary font can share it safely — a rebuilt face is a new pointer and so a
// new key, never a stale width for a live one). ok is false when the face is nil
// or SDL_ttf refused, so a caller can keep its measurement-failed branch.
// This exists because ttf.Font.SizeUTF8 is NOT free to call per frame: go-sdl2
// hands the out-params' addresses to cgo, so each call heap-allocates — the same
// escape the cgoRect idiom exists for. Any draw path that measures the same
func (c *Ctx) fontTextWidth(f *ttf.Font, text string) (int32, bool) {
	return c.fontTextWidthWeight(f, text, false)
}

// fontTextWidthWeight is fontTextWidth measured at a weight — the metric twin of
// LabelClippedFontWeight, so a bold name is placed by the width the bold glyphs
// actually occupy. bold==false takes the identical key and the identical measure
// as before.
func (c *Ctx) fontTextWidthWeight(f *ttf.Font, text string, bold bool) (int32, bool) {
	if f == nil {
		return 0, false
	}
	k := panelWidthKey{font: f, text: text, bold: bold}
	if w, ok := c.panelWidth[k]; ok {
		return w, true
	}
	if bold {
		f.SetStyle(ttf.STYLE_BOLD)
		defer f.SetStyle(ttf.STYLE_NORMAL) // never leave a weight on the shared face
	}
	w, _, err := f.SizeUTF8(text)
	if err != nil {
		return 0, false
	}
	if c.panelWidth == nil {
		c.panelWidth = make(map[panelWidthKey]int32, textCacheMax)
	} else if len(c.panelWidth) >= textCacheMax {
		c.panelWidth = make(map[panelWidthKey]int32, textCacheMax)
	}
	c.panelWidth[k] = int32(w)
	return int32(w), true
}

// devTextWidth measures text on the DEVICE chrome face — the metric the drawn
// field texture uses at a fractional UI scale — returning DEVICE px. It is the
// field-metric twin of TextWidth (which measures the logical face for
// scale-invariant layout): the value texture is rasterized on deviceTextFont, and
// SDL_ttf quantizes each glyph's advance independently per point size, so only a
// device measure lands the caret on the actual glyph seam (kills the
// length-growing caret-to-glyph drift, #77 S1b). Mirrors render/text.go:568-570,
// where the non-ASCII field path already measures device advances and folds back.
// Memoized in its own map (raw device px), CAPPED by textCacheMax like widthCache
// and PURGED wherever widthCache is (SetTextDevScale, SetChromeFont) — so a
// stale-size entry after a scale change is impossible. Callers at 100% must NOT
func (c *Ctx) devTextWidth(text string) int32 {
	if c.font == nil {
		return 0 // headless tests; real Ctx always has the chrome font
	}
	if w, ok := c.devWidthCache[text]; ok {
		return w
	}
	w, _, err := c.deviceTextFont(c.font).SizeUTF8(text)
	if err != nil {
		return 0
	}
	if c.devWidthCache == nil {
		c.devWidthCache = make(map[string]int32, textCacheMax)
	} else if len(c.devWidthCache) >= textCacheMax {
		c.devWidthCache = make(map[string]int32, textCacheMax)
	}
	c.devWidthCache[text] = int32(w)
	return int32(w)
}

// devTextWidthWeight is fontTextWidthWeight's device-exact twin — the same
// #77 S1b idiom devTextWidth already applies to the chrome face (see its doc
// comment for the underlying mechanism), generalized to an explicit font and
// weight so log-row selection can use it. Returns the RAW DEVICE-pixel width,
// unfolded, exactly like devTextWidth: callers fold with uiLogicalFromDevice
// at the read site (logPrefixWidth), the same two steps textTextureBold /
// cachedText.logicalW() perform for the actual draw.
// Memoized in its own map (logDevWidth), same key shape and cap as
// fontTextWidthWeight's panelWidth; see logDevWidth's field doc for why
// sharing panelWidth's purge site (purgeTextCache) is sufficient even though
func (c *Ctx) devTextWidthWeight(f *ttf.Font, text string, bold bool) (int32, bool) {
	if f == nil {
		return 0, false
	}
	k := panelWidthKey{font: f, text: text, bold: bold}
	if w, ok := c.logDevWidth[k]; ok {
		return w, true
	}
	dev := c.deviceTextFont(f)
	if bold {
		dev.SetStyle(ttf.STYLE_BOLD)
		defer dev.SetStyle(ttf.STYLE_NORMAL) // never leave a weight on the shared device face
	}
	w, _, err := dev.SizeUTF8(text)
	if err != nil {
		return 0, false
	}
	if c.logDevWidth == nil {
		c.logDevWidth = make(map[panelWidthKey]int32, textCacheMax)
	} else if len(c.logDevWidth) >= textCacheMax {
		c.logDevWidth = make(map[panelWidthKey]int32, textCacheMax)
	}
	c.logDevWidth[k] = int32(w)
	return int32(w), true
}

// --- widgets ---------------------------------------------------------------------

// Button draws a clickable button; returns true on click.
func (c *Ctx) Button(r sdl.Rect, label string) bool {
	return c.ButtonCol(r, label, ColPanel, ColPanelHi, ColAccent, ColText)
}

// ButtonCol is Button in explicit colours (bg, hover bg, border, label) — so a
// themed panel can colour its own buttons. Same look, click semantics and cost
// as Button; Button is just ButtonCol in the stock palette.
func (c *Ctx) ButtonCol(r sdl.Rect, label string, bg, hoverBg, border, text sdl.Color) bool {
	hover := c.hovering(r)
	col := bg
	if hover {
		col = hoverBg
	}
	// Chrome SHAPE (A5): a non-sharp preset reshapes the button silhouette via
	// 9-sliced alpha masks; the label then insets by shapeLabelInset so a glyph
	// never tucks under a corner curve. The hit-test rect (hovering above) is
	// unchanged. On "sharp" (or before masks are resident) shaped==false and the
	// body below is the LITERAL pre-A5 Fill+Border+label — byte-identical, so the
	// settled-frame 0-alloc gate (default sharp) is untouched by construction.
	shaped := c.activeShape != shapeSharp && c.activeShape != "" && c.shapeMaskReady
	if shaped {
		c.FillShaped(r, col)
		c.borderShaped(r, border)
	} else {
		c.Fill(r, col)
		c.Border(r, border)
	}
	if t, ok := c.textTexture(label, text, c.chromeFace()); ok {
		// Clip to the button: tiny themed rects must never leak their
		// label over the neighbors (Qt elided these). All #77-LOGICAL px.
		lw, lh := t.logicalW(), uiLogicalFromDevice(t.h, t.devPct)
		w, x := lw, r.X+(r.W-lw)/2
		// A shaped button reserves shapeLabelInset extra px each side (the sharp
		// path keeps its original r.W-8 clamp exactly — no change on the default).
		clampMargin := int32(8)
		edge := int32(4)
		if shaped {
			clampMargin = 8 + 2*shapeLabelInset
			edge = 4 + shapeLabelInset
		}
		if maxW := r.W - clampMargin; w > maxW && maxW > 0 {
			w, x = maxW, r.X+edge
		}
		c.blitLabel(t, x, r.Y+(r.H-lh)/2, w)
	}
	return hover && c.clicked
}

// Checkbox draws a toggle; returns the (possibly flipped) value.
// checkboxBoxPx is the tick box's side, checkboxLabelGapPx the gap between it and
// the label, and checkboxTickInsetPx the inset of the filled tick inside the box.
// Named (hard rule 9) so CheckboxWidth below can measure a checkbox WITHOUT drawing
// it — the lobby's direct-connect row has to know how much space two of them take
// before it can place the Connect button on a 640 px window.
const (
	checkboxBoxPx       = int32(16)
	checkboxLabelGapPx  = int32(6)
	checkboxTickInsetPx = int32(4)
)

// CheckboxWidth is the total width Checkbox(x, y, label, …) will occupy, box and
// label together. Alloc-free (TextWidth is memoized), so a layout pass may call it.
func (c *Ctx) CheckboxWidth(label string) int32 {
	return checkboxWidthFor(label, c.TextWidth)
}

// checkboxWidthFor is CheckboxWidth with the measurement injected, for a layout pass
// that runs without a renderer (the player-list toolbar planner). Shared with the
// method above so a font-free plan and the drawn widget can never disagree about how
// wide a tick box is — they would silently drift apart as two copies of the sum.
func checkboxWidthFor(label string, measure func(string) int32) int32 {
	return checkboxBoxPx + checkboxLabelGapPx + measure(label)
}

// rowLabelKey keys the truncated-row-label memo: the same label at the same
// available width always shortens the same way, and the shortening is a chop-and-
// re-measure LOOP — one pass per rune removed, each allocating a string. A settings
// row is drawn every frame the screen is up, so without this memo a single 200-
// character help line would allocate a hundred strings sixty times a second.
type rowLabelKey struct {
	label string
	avail int32
}

// pushRowLabelLimit arms a RIGHT EDGE (in logical window px) past which a chrome ROW
// widget's label must not draw, and returns the previous limit for popRowLabelLimit.
// 0 disarms — the pre-existing "labels grow to their text" behaviour, which every
// caller outside the bracket keeps.
// A bracket rather than a parameter because Checkbox has hundreds of call sites and
// the limit is a property of the CONTAINER, not of any one row: the settings card
// knows its own right edge, and a row inside it should not have to be told twice.
func (c *Ctx) pushRowLabelLimit(rightX int32) int32 {
	prev := c.rowLabelRight
	c.rowLabelRight = rightX
	return prev
}

func (c *Ctx) popRowLabelLimit(prev int32) { c.rowLabelRight = prev }

// fitRowLabel is the shortened label a row should DRAW at label-origin x under the
// armed limit: the label itself when it fits (and always when no limit is armed),
// otherwise AO2's own chop-two-append-ellipsis truncation. Returns fit=false when it
// had to shorten, so the caller can offer the full text some other way.
//
// The ellipsis matters: a hard clip is indistinguishable from a rendering fault —
// which is precisely how the overflowing settings text was reported ("cut off at the
// right border"). A trailing "…" says the sentence continues.
func (c *Ctx) fitRowLabel(x int32, label string) (string, bool) {
	if c.rowLabelRight <= 0 || label == "" {
		return label, true
	}
	avail := c.rowLabelRight - x
	if avail <= 0 {
		return label, true // degenerate geometry: draw as before rather than blank the row
	}
	if c.TextWidth(label) <= avail {
		return label, true
	}
	key := rowLabelKey{label: label, avail: avail}
	if s, ok := c.rowLabelMemo[key]; ok {
		return s, false
	}
	s := truncateLabelTo(label, avail, c.TextWidth)
	if c.rowLabelMemo == nil {
		c.rowLabelMemo = make(map[rowLabelKey]string, 64)
	} else if len(c.rowLabelMemo) >= textCacheMax {
		c.rowLabelMemo = make(map[rowLabelKey]string, 64) // wholesale reset, same rule as the width memos
	}
	c.rowLabelMemo[key] = s
	return s, false
}

func (c *Ctx) Checkbox(x, y int32, label string, value bool) bool {
	if c.onRow != nil {
		c.onRow(label, y) // settings-search collect pass (#26 gather): record and draw as normal — the FULL label, so search still matches text the row had to shorten
	}
	const box = checkboxBoxPx
	// A muted row is overruled by a master toggle upstream: draw it in the dim
	// palette and return the value untouched below, so a click can't set state
	// the master would immediately override anyway.
	frame, tick, text := ColAccent, ColAccent, ColText
	if c.muted {
		frame, tick, text = ColPanelHi, ColPanelHi, ColTextDim
	}
	r := sdl.Rect{X: x, Y: y, W: box, H: box}
	c.Fill(r, ColPanel)
	c.Border(r, frame)
	if value {
		inner := sdl.Rect{
			X: x + checkboxTickInsetPx, Y: y + checkboxTickInsetPx,
			W: box - 2*checkboxTickInsetPx, H: box - 2*checkboxTickInsetPx,
		}
		c.Fill(inner, tick)
	}
	labelX := x + box + checkboxLabelGapPx
	// F2: honour the container's right edge. Unbracketed callers get label back
	// verbatim, so every non-settings checkbox draws the identical pixels.
	shown, fit := c.fitRowLabel(labelX, label)
	w := c.Label(labelX, y, shown, text)
	hit := sdl.Rect{X: x, Y: y, W: box + checkboxLabelGapPx + w, H: box}
	if !fit {
		c.Tooltip(hit, label) // the sentence the row could not fit is one hover away
	}
	if !c.muted && c.hovering(hit) && c.clicked {
		return !value
	}
	return value
}

// truncateChopRunes / truncateEllipsis mirror AO2's own truncation step: it chops
// TWO characters and appends a single-character ellipsis, so each pass nets one
// character narrower (AO2-Client courtroom.cpp:6750-6754). Named per hard rule 9 —
// "chop(2)" at a call site says nothing about why two.
const (
	truncateChopRunes = 2
	truncateEllipsis  = "…"
)

// truncateLabelTo shortens label until it fits avail, AO2's way: chop two runes,
// append "…", re-measure, repeat (AO2-Client courtroom.cpp:6749-6774
// truncate_label_text, called for guard/pre/flip/slide at :1167-1170 and for
// immediate at :1078). A theme is free to give a toggle a 51x19 rect, and AO2
// sizes the widget to the rect and shortens the LABEL rather than overflowing it.
// Pure, and measure is injected, so it is testable without a renderer. Called at
// layout-build time rather than per frame; the happy path returns before the loop
// allocates anything.
func truncateLabelTo(label string, avail int32, measure func(string) int32) string {
	return truncateLabelToMarker(label, avail, truncateEllipsis, measure)
}

// truncateLabelToMarker is truncateLabelTo with the trailing marker chosen by the
// caller — for the surfaces that draw in a THEME-declared face, where U+2026 may
// have no glyph and renders as a .notdef box (labelellipsis.go).
// Both AO2 behaviours the single-marker version documented survive verbatim: a
// label measuring EXACTLY avail is left alone (AO2's condition is `>`), and a chop
// that would leave nothing but the marker abandons the truncation and keeps the
func truncateLabelToMarker(label string, avail int32, marker string, measure func(string) int32) string {
	if avail <= 0 || measure(label) <= avail {
		return label
	}
	chop := truncateChopFor(marker)
	out := label
	for measure(out) > avail && out != marker {
		// Chop off the CURRENT string and re-append the marker, exactly as AO2
		// does. The marker a previous pass appended is therefore among the chopped
		// runes, so each pass nets ONE original rune narrower — stripping it first
		// would net `chop` and diverge from AO2 from the second pass onward.
		r := []rune(out)
		if len(r) <= chop {
			return label // would collapse to the marker alone — AO2 keeps the original
		}
		out = string(r[:len(r)-chop]) + marker
	}
	if out == marker {
		return label
	}
	return out
}

// truncateChopFor is how many runes one pass removes so the net loss is exactly one
// original character: the marker's own runes (which the previous pass appended and
// this one must take back) plus one.
//
// For AO2's single-rune ellipsis this is 2 — courtroom.cpp:6750-6754's literal
// chop of two — which is what truncateChopRunes names and what
// TestTruncateChopMatchesAO2ForTheEllipsis (labelellipsis_test.go) pins.
func truncateChopFor(marker string) int {
	return len([]rune(marker)) + 1
}

// fitLabelToFont is truncateLabelTo measured in a SPECIFIC face — for the themed
// surfaces that draw in a theme-declared font rather than the chrome one, where
// c.TextWidth (the chrome face's memo) would ellipsize against the wrong metrics.
// Callers on a per-frame path should test the fits case with fontTextWidth first:
// The MARKER is chosen against the same face (ellipsisFor): a theme-declared
// display font that has no U+2026 gets "..." instead of the .notdef box that was
// showing up at the end of every shortened music-banner title. Resolved here rather
func (c *Ctx) fitLabelToFont(f *ttf.Font, label string, avail int32) string {
	return c.fitLabelToFontWeight(f, label, avail, false)
}

// fitLabelToFontWeight is fitLabelToFont measured at a weight — the third member of
// the weighted trio (LabelClippedFontWeight draws it, fontTextWidthWeight measures
// it, this one shortens it), so a bold themed label is cut to what the BOLD glyphs
// occupy. SDL_ttf's emboldening widens every glyph, so a string fitted on plain
// advances still overflows when it is drawn bold, and the ellipsis that was supposed
// to say "there is more" gets clipped off along with the tail it marks.
func (c *Ctx) fitLabelToFontWeight(f *ttf.Font, label string, avail int32, bold bool) string {
	return truncateLabelToMarker(label, avail, c.ellipsisFor(f), func(s string) int32 {
		w, ok := c.fontTextWidthWeight(f, s, bold)
		if !ok {
			return 0
		}
		return w
	})
}

// CheckboxLabelAvail is how much width a label has inside an AO2 design rect once
// the tick box and its gap are taken out. The layout pass hands this to
// truncateLabelTo; the draw site clips to the same number.
func (c *Ctx) CheckboxLabelAvail(r sdl.Rect) int32 {
	return r.W - checkboxBoxPx - checkboxLabelGapPx
}

// checkboxBoxY vertically centres the tick box in an AO2 design rect, FLOORED at
// the rect's own top. ThemeFitNative can downscale a canvas until the rect is
// shorter than the 16 px box, and a negative offset would lift the box out of its
// own row and into whatever is drawn above it.
//
// Extracted so a test can drive the real arithmetic: the first version of that
// test recomputed the formula in its own body and would have passed with the floor
// deleted from production.
func checkboxBoxY(r sdl.Rect) int32 {
	if off := (r.H - checkboxBoxPx) / 2; off > 0 {
		return r.Y + off
	}
	return r.Y
}

// CheckboxIn is Checkbox fitted to a RECT rather than anchored at an (x, y).
// AO2 places every toggle by design rect (set_size_and_pos), and several are far
// narrower than their label — aceattorney2x gives `flip` 51x19 — so a checkbox
// that grows to its text cannot honour a theme.
// No id parameter: Checkbox has none either, c.onRow collects by label, and an
// unused parameter is a staticcheck finding.
func (c *Ctx) CheckboxIn(r sdl.Rect, label string, value bool) bool {
	if c.onRow != nil {
		c.onRow(label, r.Y) // settings-search collect pass, as Checkbox does
	}
	box := checkboxBoxPx
	b := sdl.Rect{X: r.X, Y: checkboxBoxY(r), W: box, H: box}
	c.Fill(b, ColPanel)
	c.Border(b, ColAccent)
	if value {
		inner := sdl.Rect{
			X: b.X + checkboxTickInsetPx, Y: b.Y + checkboxTickInsetPx,
			W: box - 2*checkboxTickInsetPx, H: box - 2*checkboxTickInsetPx,
		}
		c.Fill(inner, ColAccent)
	}
	if avail := c.CheckboxLabelAvail(r); avail > 0 {
		c.LabelClipped(r.X+box+checkboxLabelGapPx, b.Y, avail, label, ColText)
	}
	// The hit area is the THEME'S rect, not the ink: AO2's widget is the rect, and
	// a truncated label would otherwise shrink the click target below its box.
	// The click is CONSUMED, unlike Checkbox's. AO2 design rects legitimately
	// overlap — aceattorney2x puts guard at 425,473,61x19 and additive at
	// 428,473,80x19, 58 px of overlap, and AO2's own stock default overlaps pre and
	// flip by 21 px. Qt resolves that by delivering the press to one widget;
	// immediate mode would fire BOTH, so one click would flip two toggles. Same
	if c.hovering(r) && c.clicked {
		c.clicked = false
		return !value
	}
	return value
}

// FocusField queues keyboard focus onto a TextField id (e.g. the IC input
// after an emote pick — AO2-Client's focus_ic_input parity).
func (c *Ctx) FocusField(id string) { c.focusNext = id }

// ReserveFieldTail declares that px logical pixels at the RIGHT edge of the next
// text field drawn with this id belong to something the caller paints on top of
// it — the IC character counter, the muted chip. The field shrinks its text
// interior by that much, so the value clips (and the keep-caret-visible scroll
// turns over) BEFORE the reserved band instead of typing straight through it.
// Declared by the caller rather than sniffed by the field because only the
// caller knows what it is about to paint, and the width is usually a measured
// string. One slot: the reservation is keyed to an id and consumed by the first
func (c *Ctx) ReserveFieldTail(id string, px int32) {
	if id == "" || px <= 0 {
		return
	}
	c.tailReserveID, c.tailReserveW = id, px
}

// takeFieldTail consumes a pending ReserveFieldTail for id (0 if none is
// pending, or it was declared for a different field).
func (c *Ctx) takeFieldTail(id string) int32 {
	if c.tailReserveID != id {
		return 0
	}
	w := c.tailReserveW
	c.tailReserveID, c.tailReserveW = "", 0
	return w
}

// WheelIn returns this frame's wheel ticks when the cursor is inside r and no
// earlier widget consumed them, else 0 — scrollables only react under the
// pointer (playtest: the music list scrolled on wheel from anywhere on screen),
// and only ONE scrollable reacts per frame. A hit marks the wheel taken, which
// now fences every LATER WheelIn too, not just the page-level handlers that
// check wheelTaken — two stacked surfaces could otherwise both scroll (the
// What's New modal scrolled AND the lobby list behind it scrolled). Priority
// follows processing order: pre-screen overlay handlers run first and win;
// anything drawn later that should win instead blinds the passes beneath it
// via the pointer fence (fencePointer / boxFencesPointer / modalOn).
func (c *Ctx) WheelIn(r sdl.Rect) int32 {
	if c.wheelTaken || c.wheelY == 0 || !c.hovering(r) {
		return 0
	}
	c.wheelTaken = true
	return c.wheelY
}

// fieldDrewLastFrame reports whether a TextField with this id was painted on the
// last frame that actually drew — the one honest answer to "is that box on screen
// right now?" available before this frame's draw pass has run.
func (c *Ctx) fieldDrewLastFrame(id string) bool {
	for _, f := range c.drawnFieldSeq {
		if f == id {
			return true
		}
	}
	return false
}

// cycleField picks the next focus target for Tab / Shift+Tab: the field
// after (or before) the current one in draw order, wrapping; when nothing
// is focused, the first field — standard toolkit behavior.
func cycleField(seq []string, cur string, back bool) string {
	for i, id := range seq {
		if id != cur {
			continue
		}
		if back {
			return seq[(i+len(seq)-1)%len(seq)]
		}
		return seq[(i+1)%len(seq)]
	}
	return seq[0]
}

// fieldFonts carries the optional emoji/unicode-aware faces for a text field. Zero value
// (both nil) = the plain single-font path (TextField / PasswordField), byte-identical to
// before. TextFieldEmoji passes a fallback-capable primary (a chat/log-set face, whose
// embedded member IS the chrome font at 1:1) + the colour-emoji face, so what you TYPE shows
// real glyphs instead of tofu — used by the IC / OOC inputs.
type fieldFonts struct{ primary, emoji *ttf.Font }

// TextField edits value in place; id keys focus. Returns (newValue,
// enterPressed).
func (c *Ctx) TextField(id string, r sdl.Rect, value string, placeholder string) (string, bool) {
	return c.textField(id, r, value, placeholder, false, fieldFonts{})
}

// TextFieldEmoji is TextField that renders typed emoji / non-Latin runes through the
// per-glyph fallback raster (primary = a chat/log-set face, emoji = the colour-emoji face),
// so the input box doesn't show tofu while you type them. Plain ASCII stays on the exact
// single-font fast path (the caret math is unchanged), so the common case is byte-identical.
func (c *Ctx) TextFieldEmoji(id string, r sdl.Rect, value, placeholder string, primary, emoji *ttf.Font) (string, bool) {
	return c.textField(id, r, value, placeholder, false, fieldFonts{primary: primary, emoji: emoji})
}

// PasswordField is TextField rendered as asterisks (screenshare-safe).
// Ctrl+C/X never put the secret on the clipboard either — clipboard
// viewers are just as visible on a stream; Ctrl+X still clears.
func (c *Ctx) PasswordField(id string, r sdl.Rect, value string, placeholder string) (string, bool) {
	return c.textField(id, r, value, placeholder, true, fieldFonts{})
}

// editOp is a caret movement / forward-delete a text field applies this frame.
type editOp int

const (
	editNone editOp = iota
	editLeft
	editRight
	editHome
	editEnd
	editDelete
	// Word-granular caret jumps (Ctrl+Left / Ctrl+Right). They are ops, not a
	// separate flag pair, so the ONE shift-extend rule in textField ("a nav op
	// with shift held moves only the caret") covers them without a second copy.
	editWordLeft
	editWordRight
)

// editInput is one frame of edits to a focused text field. selStart/selEnd
// carry the active selection as a rune range [selStart, selEnd) — selEnd <=
// selStart means none. Ctrl+A / double-click resolve to a real range before
// this runs, so editStep has exactly one selection representation.
type editInput struct {
	typed            string // inserted text (typed + pasted)
	back             bool   // backspace (delete the rune before the caret)
	wordBack         bool   // Ctrl+Backspace: delete the preceding word (trailing whitespace + the run before it)
	wordFwd          bool   // Ctrl+Delete: delete the FOLLOWING word (the run after the caret + the whitespace glued to it)
	op               editOp // caret move or forward delete
	selStart, selEnd int    // active selection (rune range); selEnd <= selStart = none
}

// wordDeleteBoundary returns the rune index a Ctrl+Backspace at pos should
// delete back to: it skips any trailing whitespace run immediately before the
// caret, then the non-whitespace run before that (the "word"). The result is
// the new caret position; [result, pos) is the range to remove. Pure and
// rune-aware (never byte-slices mid-rune) so it unit-tests without SDL. pos at
// or before 0 returns 0 (nothing to delete). This matches the near-universal
// editor convention (delete the word to the left, plus the spaces glued to it).
func wordDeleteBoundary(runes []rune, pos int) int {
	if pos > len(runes) {
		pos = len(runes)
	}
	i := pos
	for i > 0 && unicode.IsSpace(runes[i-1]) { // eat the whitespace run before the caret
		i--
	}
	for i > 0 && !unicode.IsSpace(runes[i-1]) { // then eat the non-whitespace word
		i--
	}
	return i
}

// wordForwardBoundary is wordDeleteBoundary's mirror: the rune index a
// Ctrl+Delete / Ctrl+Right at pos should reach, walking FORWARD. It eats the
// non-whitespace run under the caret first and then the whitespace glued to its
// right, which is the same convention read the other way round — Ctrl+Delete
// takes "word + the gap after it" exactly as Ctrl+Backspace takes "the gap
// before it + word". [pos, result) is the range to remove; the caret stays at
// pos for a delete and MOVES to result for a jump. Pure and rune-aware, so it
// unit-tests without SDL.
func wordForwardBoundary(runes []rune, pos int) int {
	if pos < 0 {
		pos = 0
	}
	if pos > len(runes) {
		pos = len(runes) // the clamp its backward twin has: an over-long caret answers the END, not itself
	}
	i := pos
	for i < len(runes) && !unicode.IsSpace(runes[i]) { // the word under/after the caret
		i++
	}
	for i < len(runes) && unicode.IsSpace(runes[i]) { // then the whitespace run after it
		i++
	}
	return i
}

// editStep applies one frame of edits to value at caret (a RUNE index), returning
// the new value and caret. Pure and rune-aware (multibyte shownames — Häschen,
// fünfzehn, 🍅 — so the caret is by rune, never by byte), so it carries all the
// edit logic that the draw path (which needs a renderer) can't unit-test.
// Native selection semantics: insert/backspace/delete replace the range;
// plain Left/Right collapse to the range's edge; Home/End collapse to the
// text's ends. (Shift-extension keeps the anchor OUT of the input — the
// caller moves only the caret.)
func editStep(value string, caret int, in editInput) (string, int) {
	runes := []rune(value)
	if caret < 0 {
		caret = 0
	}
	if caret > len(runes) {
		caret = len(runes)
	}
	selA, selB := in.selStart, in.selEnd
	if selA < 0 {
		selA = 0
	}
	if selB > len(runes) {
		selB = len(runes)
	}
	if sel := selB > selA; sel {
		// A word-delete with an active selection deletes just the selection,
		// exactly like a plain backspace does (in.wordBack / in.wordFwd join
		// in.back here).
		if in.typed != "" || in.back || in.wordBack || in.wordFwd || in.op == editDelete {
			t := []rune(in.typed)
			out := make([]rune, 0, len(runes)-(selB-selA)+len(t))
			out = append(out, runes[:selA]...)
			out = append(out, t...)
			out = append(out, runes[selB:]...)
			return string(out), selA + len(t)
		}
		switch in.op {
		case editLeft:
			return value, selA // collapse to the selection's left edge
		case editRight:
			return value, selB // …or its right edge
		case editHome:
			return value, 0
		case editEnd:
			return value, len(runes)
		case editWordLeft:
			// A word jump over a selection collapses to that edge and THEN jumps,
			// which is what a native field does (the selection is not a wall).
			return value, wordDeleteBoundary(runes, selA)
		case editWordRight:
			return value, wordForwardBoundary(runes, selB)
		}
		return value, caret
	}
	switch {
	case in.typed != "":
		t := []rune(in.typed)
		out := make([]rune, 0, len(runes)+len(t))
		out = append(out, runes[:caret]...)
		out = append(out, t...)
		out = append(out, runes[caret:]...)
		return string(out), caret + len(t)
	case in.wordBack && caret > 0:
		// Ctrl+Backspace: delete the whole preceding word (whitespace + word run).
		start := wordDeleteBoundary(runes, caret)
		out := make([]rune, 0, len(runes)-(caret-start))
		out = append(out, runes[:start]...)
		out = append(out, runes[caret:]...)
		return string(out), start
	case in.wordFwd && caret < len(runes):
		// Ctrl+Delete: remove [caret, end-of-next-word). The caret does not move.
		end := wordForwardBoundary(runes, caret)
		out := make([]rune, 0, len(runes)-(end-caret))
		out = append(out, runes[:caret]...)
		out = append(out, runes[end:]...)
		return string(out), caret
	case in.back && caret > 0:
		out := make([]rune, 0, len(runes)-1)
		out = append(out, runes[:caret-1]...)
		out = append(out, runes[caret:]...)
		return string(out), caret - 1
	case in.op == editDelete && caret < len(runes):
		out := make([]rune, 0, len(runes)-1)
		out = append(out, runes[:caret]...)
		out = append(out, runes[caret+1:]...)
		return string(out), caret
	case in.op == editLeft && caret > 0:
		caret--
	case in.op == editRight && caret < len(runes):
		caret++
	case in.op == editHome:
		caret = 0
	case in.op == editEnd:
		caret = len(runes)
	case in.op == editWordLeft:
		caret = wordDeleteBoundary(runes, caret)
	case in.op == editWordRight:
		caret = wordForwardBoundary(runes, caret)
	}
	return value, caret
}

func (c *Ctx) textField(id string, r sdl.Rect, value string, placeholder string, mask bool, fb fieldFonts) (string, bool) {
	c.fieldSeq = append(c.fieldSeq, id) // Tab-cycle order = draw order
	hover := c.hovering(r)
	// Out-of-band change detection + this field's undo history
	// (fieldhistory.go): a macro fill, a palette command template, or the
	// own-echo IC clear lands in the history HERE — whatever changed since
	// this field's last draw, focused or not — so Ctrl+Z brings it back.
	hist := c.fieldTrack(id, value)
	press := c.mouseDown && !c.prevMouseDown
	if press && hover && c.dragID == "" {
		// Focus moves on the PRESS (native), so a drag-selection starts
		// immediately; the release block below still covers click-away
		// unfocus and synthetic clicks.
		c.selectAll = false
		c.focusID = id
	}
	if c.clicked && c.dragID != id { // our drag-select's release is not a click: never unfocus/refocus on it
		c.selectAll = false // any single click drops a pending select-all
		if hover {
			c.focusID = id
		} else if c.focusID == id {
			c.focusID = ""
		}
	}
	focused := c.focusID == id

	c.Fill(r, ColPanel)
	border := ColTextDim
	if focused {
		border = ColAccent
	}
	c.Border(r, border)

	const padX = 6
	// The interior the VALUE may use. A caller-declared tail reservation
	// (ReserveFieldTail — the IC message counter) shrinks it from the right, so
	// typed text clips before the counter instead of running underneath it.
	// Consumed here so exactly one field can honour it per frame.
	avail := r.W - 2*padX - c.takeFieldTail(id)
	if avail < 0 {
		avail = 0
	}
	// maskOf maps a value to what's drawn (one '*' per rune when masked). The
	// caret math measures the DISPLAY, so a password field never leaks its value.
	maskOf := func(v string) string {
		if mask && v != "" {
			return strings.Repeat("*", utf8.RuneCountInString(v))
		}
		return v
	}
	if !focused && c.caretField == id {
		c.caretField = "" // dropped focus → a future re-focus resets the caret to the end
	}
	if !focused && c.fieldScrollID == id {
		c.fieldScrollID, c.fieldScroll = "", 0 // …and the view goes back to head-aligned
	}

	enter := false
	if focused {
		// The caret is the focused field's. Default to the end on a focus change,
		// and CLAMP every frame — value can shrink underneath us (click-to-pair,
		// macros set a.icInput while it's focused), so a stale caret must never
		// index past the end (the live-crash the advisor flagged).
		rc := utf8.RuneCountInString(value)
		if c.caretField != id {
			c.caret = rc
			c.caretField = id
			c.selAnchor = -1 // fresh focus: no selection
		}
		if c.caret > rc {
			c.caret = rc
		}
		if c.caret < 0 {
			c.caret = 0
		}
		if c.selAnchor > rc {
			c.selAnchor = rc // value shrank under a live selection
		}
		// Ctrl+A becomes a REAL whole-value selection (highlight + native
		// replace/copy/cut semantics), not just an armed flag.
		if c.selectAll {
			c.selAnchor, c.caret = 0, rc
			c.selectAll = false
		}
		// Mouse: the press places the caret AND anchors a selection; holding
		// drags the caret end (auto-scrolls via the keep-caret-visible
		// scroll); shift+press extends from the current caret. Measured on
		// the display through the SAME raster the field draws with
		// (fieldRaster), so non-Latin text maps clicks to the right rune
		// (the Cyrillic caret bug).
		if (press && hover && c.dragID == "") || (c.dragID == id && c.mouseDown) {
			pre := maskOf(value)
			preRaster := c.fieldRaster(fb, mask, pre)
			// The scroll the user is LOOKING AT — the one this field was drawn with
			// last frame — never a freshly re-derived one. Re-deriving it CENTRED the
			// caret, so the pixel under the pointer named a different rune than the
			// one drawn there and the text slid out from under a drag-selection. A
			// field that does not own the sticky scroll was drawn head-aligned, i.e. 0.
			preScroll := int32(0)
			if c.fieldScrollID == id {
				preScroll = c.fieldScroll
			}
			idx := c.fieldIndexAtX(pre, preRaster, c.mouseX-(r.X+padX)+preScroll)
			if press && hover && c.dragID == "" {
				if c.shiftHeld {
					if c.selAnchor < 0 {
						c.selAnchor = c.caret // shift+click: extend from the old caret
					}
				} else {
					c.selAnchor = idx
				}
				c.dragID = id // own the drag: selecting keeps working past the box edge
			}
			c.caret = idx
		}
		// Double-click selects the word under the caret, triple-click the
		// whole value (native). A masked field has no words — both select all.
		if c.dblClick && hover {
			if mask {
				c.selAnchor, c.caret = 0, rc
			} else {
				lo, hi := wordBoundsAt([]rune(value), c.caret)
				c.selAnchor, c.caret = lo, hi
			}
		}
		if c.tripleClick && hover {
			c.selAnchor, c.caret = 0, rc
		}
		// Ctrl+Z / Ctrl+Y (routed into undoReq/redoReq pre-screen): step this
		// field's history. Consumed even when the stack is empty — the chord
		// was aimed at the focused field, never at a z/y-bound hotkey.
		if c.undoReq || c.redoReq {
			if snap, ok := hist.step(value, c.caret, c.redoReq); ok {
				value = snap.value
				rc = utf8.RuneCountInString(value)
				c.caret = snap.caret
				if c.caret > rc {
					c.caret = rc
				}
				c.selAnchor = -1
			}
			c.undoReq, c.redoReq = false, false
		}
		selLo, selHi := c.fieldSel(rc)
		if c.copyReq && !mask {
			if selLo < selHi { // copy the selection when one exists, else the whole value
				_ = sdl.SetClipboardText(string([]rune(value)[selLo:selHi]))
			} else if value != "" {
				_ = sdl.SetClipboardText(value)
			}
		}
		prevVal, prevCaret := value, c.caret // undo snapshot base for this frame's edits
		switch {
		case c.holdOn && c.holdAcc >= c.holdThreshold && !c.holdFired && value != "":
			// Hold-to-clear: the bound key (default Backspace) held past the
			// threshold wipes the whole field at once — no slow char-by-char.
			c.holdFired = true
			value, c.caret = "", 0
			c.selAnchor = -1
		case c.cutReq:
			if selLo < selHi { // native cut: copy + remove the SELECTION when one exists
				if !mask {
					_ = sdl.SetClipboardText(string([]rune(value)[selLo:selHi]))
				}
				value, c.caret = editStep(value, c.caret, editInput{op: editDelete, selStart: selLo, selEnd: selHi})
			} else {
				if value != "" && !mask {
					_ = sdl.SetClipboardText(value)
				}
				value, c.caret = "", 0
			}
			c.selAnchor = -1
			c.selectAll = false
		default:
			// wordBack is gated on the WordDelete pref HERE (at consumption): with
			// the pref off, a focused Ctrl+Backspace was still captured (wordBack
			// set, never routed to c.hotkey), so it becomes a consumed no-op — it
			// can't leak into a Ctrl+Backspace hotkey mid-typing.
			in := editInput{
				typed:    c.typed + c.pasted,
				back:     c.backspace,
				wordBack: c.wordBack && c.wordDeleteOn,
				wordFwd:  c.wordFwdDel && c.wordDeleteOn,
			}
			switch c.keyPressed {
			case sdl.K_LEFT:
				in.op = editLeft
			case sdl.K_RIGHT:
				in.op = editRight
			case sdl.K_HOME:
				in.op = editHome
			case sdl.K_END:
				in.op = editEnd
			case sdl.K_DELETE:
				in.op = editDelete
			}
			// The Ctrl+arrow jumps arrive on their OWN flags, not keyPressed: a Ctrl
			// chord never reaches keyPressed (HandleEvent's ctrl branch returns), so
			// they are folded in here. A plain arrow this frame wins — pressing both
			// at once is not a thing a keyboard produces.
			if in.op == editNone {
				switch {
				case c.wordLeft:
					in.op = editWordLeft
				case c.wordRight:
					in.op = editWordRight
				}
			}
			if in.typed != "" || in.back || in.wordBack || in.wordFwd || in.op != editNone {
				nav := in.op == editLeft || in.op == editRight || in.op == editHome || in.op == editEnd ||
					in.op == editWordLeft || in.op == editWordRight
				extend := nav && in.typed == "" && !in.back && !in.wordBack && !in.wordFwd && c.shiftHeld
				if extend {
					// Shift+arrow/Home/End extends: fix the anchor, move only
					// the caret (the selection is anchor..caret).
					if c.selAnchor < 0 {
						c.selAnchor = c.caret
					}
				} else {
					in.selStart, in.selEnd = selLo, selHi // edits replace / plain nav collapses
				}
				value, c.caret = editStep(value, c.caret, in)
				if !extend {
					c.selAnchor = -1
				}
				switch c.keyPressed { // consume nav keys so char keybinds don't also fire
				case sdl.K_LEFT, sdl.K_RIGHT, sdl.K_HOME, sdl.K_END, sdl.K_DELETE:
					c.keyPressed = 0
				}
			}
		}
		if value != prevVal {
			hist.record(prevVal, prevCaret, value, time.Now()) // the field's own edits feed the history too
		}
		if c.enter {
			enter = true
		}
	}
	// Track the drawn value for the out-of-band detector (both branches: an
	// unfocused field must notice a rewrite too). lastCaret only matters as a
	// restore position, so it follows the live caret while focused.
	hist.lastKnown = value
	if focused {
		hist.lastCaret = c.caret
	}

	display := maskOf(value)
	// The fallback raster the value will DRAW with (nil = single-font path).
	// Resolved ONCE here so every measurement below — caret x, full width, the
	// keep-caret-visible scroll — reads the same glyph advances the draw blits.
	// Measuring with the chrome font while drawing with a fallback face put the
	// caret several letters off in Cyrillic (the playtest report).
	fbRaster := c.fieldRaster(fb, mask, display)
	show := display
	col := ColText
	if show == "" && !focused {
		show = placeholder
		col = ColTextDim
	}
	textY := r.Y + (r.H-int32(c.chromeFace().Height()))/2
	// Horizontal scroll keeps the CARET visible (roughly centered) once the text
	// overflows the box — type or arrow anywhere and you can see it, instead of
	// typing blind past the right edge. Unfocused/fitting fields stay head-aligned.
	fullW := c.fieldPrefixW(display, fbRaster, utf8.RuneCountInString(display))
	scroll, caretX := int32(0), int32(0)
	if focused {
		caretX = c.fieldPrefixW(display, fbRaster, c.caret)
		// STICKY: the view only moves when the caret would leave it. Ownership
		// transfers on focus (a fresh field starts head-aligned and scrollKeep
		// pulls the end into view on the first frame, which is where the caret
		// lands), so exactly one field's scroll is live at a time.
		if c.fieldScrollID != id {
			c.fieldScrollID, c.fieldScroll = id, 0
		}
		c.fieldScroll = scrollKeep(c.fieldScroll, fullW, caretX, avail)
		scroll = c.fieldScroll
	}
	// The focused ASCII field draws its moving parts (selection, value texture,
	// caret) DEVICE-EXACT at a fractional UI scale (#77 S1/S2): under a fractional
	// ren.SetScale, the whole-string label texture would resample at a NEW
	// sub-pixel phase every keystroke as the scroll advances (the "shimmer"), and
	// the 2px caret's device column count would flip 2↔3 with that phase (the
	// "stretch"). devFieldValue brackets SetScale(1,1) and projects each rect to
	// device pixels itself, so nothing phase-shifts. The gate is fractional-scale
	// only; at 100% and on the fallback-raster / placeholder / unfocused paths this
	// is false and the pre-fix scaled draws below run byte-identically.
	devExact := focused && fbRaster == nil && show == display && deviceExactText(c.textDevPct)

	// Selection highlight, UNDER the text: the ordered anchor..caret range,
	// measured through the same raster the glyphs draw with. A focused field
	// already pays per-frame measurement for the caret; the selection adds
	// two prefix widths only WHILE one exists.
	selX0, selX1, hasSel := int32(0), int32(0), false
	if focused {
		if selLo, selHi := c.fieldSel(utf8.RuneCountInString(display)); selLo < selHi {
			x0 := c.fieldPrefixW(display, fbRaster, selLo) - scroll
			x1 := c.fieldPrefixW(display, fbRaster, selHi) - scroll
			if x0 < 0 {
				x0 = 0
			}
			if x1 > avail {
				x1 = avail
			}
			if x1 > x0 {
				hasSel, selX0, selX1 = true, x0, x1
				if !devExact {
					c.Fill(sdl.Rect{X: r.X + padX + x0, Y: r.Y + 3, W: x1 - x0, H: r.H - 6},
						sdl.Color{R: ColAccent.R, G: ColAccent.G, B: ColAccent.B, A: 90})
				}
			}
		}
	}
	if devExact {
		// One SetScale(1,1) bracket for all three moving parts (selection under,
		// value texture, caret over) — the label blits 1:1 from its device-sized
		// texture (zero resample) and the caret gets a constant integer device
		// width. Skips the scaled selection Fill above and the scaled value/caret
		// draws below. The caret consumes the RAW DEVICE prefix width (caretXDev,
		// same device face as the drawn texture) so it lands on the exact device
		// glyph seam — folding to logical and re-projecting would lose up to 1
		// device px of the per-glyph quantization the texture preserves (#77 S1b).
		caretXDev := c.caretPixelXDev(show, c.caret)
		c.devFieldValue(r, padX, avail, scroll, caretXDev, textY, show, col, selX0, selX1, hasSel && focused, focused && c.caretOn)
		return value, enter
	}
	// #M5 emoji/unicode input: when the field opted in (IC/OOC) and the text has any
	// non-ASCII rune, draw it through the per-glyph fallback raster so emoji + non-Latin
	// scripts show real glyphs instead of the chrome font's tofu. Plain ASCII (the common
	// case) and every other field stay on the exact single-font path below — caret math is
	// unchanged, so it's approximate only for the rare wide glyph (far better than tofu).
	drawn := false
	if fbRaster != nil && show == display {
		cp, ch := c.pushClip(sdl.Rect{X: r.X + padX, Y: r.Y, W: avail, H: r.H})
		// DrawScaled: fbRaster is built via c.emojiRaster (fieldRaster), which rasterizes
		// its device-sibling faces exactly like the log/showname raster above — the ASCII
		// branch of this same field is already device-exact (devFieldValue); the emoji
		// branch was the one gap left (see the census in app.go).
		fbRaster.DrawScaled(c.Ren, fbRaster.TotalRunes(), r.X+padX-scroll, r.Y+(r.H-fbRaster.Height())/2, c.RenderScalePct(), &c.clipRect)
		c.popClip(cp, ch)
		drawn = true
	}
	// The PLACEHOLDER (shown empty + unfocused) also tofus on the chrome font for
	// non-ASCII — e.g. a saved Japanese showname greyed in the empty showname box.
	// Route it through the same per-glyph raster on an opted-in field (fb.primary !=
	// nil), keyed by the dim colour so the value/placeholder rasters never collide.
	// Cached + self-invalidating like the value raster (one map hit/frame, no
	// per-frame alloc); plain-ASCII placeholders stay on the single-font path.
	if !drawn && show == placeholder && show != "" && fb.primary != nil && !isASCII(show) {
		if m := c.emojiRaster(show, col, fb.primary, fb.emoji); m != nil {
			cp, ch := c.pushClip(sdl.Rect{X: r.X + padX, Y: r.Y, W: avail, H: r.H})
			m.DrawScaled(c.Ren, m.TotalRunes(), r.X+padX, r.Y+(r.H-m.Height())/2, c.RenderScalePct(), &c.clipRect)
			c.popClip(cp, ch)
			drawn = true
		}
	}
	if !drawn {
		if scroll > 0 {
			// Clip to the field interior so the scrolled-off head doesn't spill left.
			cp, ch := c.pushClip(sdl.Rect{X: r.X + padX, Y: r.Y, W: avail, H: r.H})
			c.Label(r.X+padX-scroll, textY, show, col)
			c.popClip(cp, ch)
		} else {
			c.LabelClipped(r.X+padX, textY, avail, show, col)
		}
	}
	if focused && c.caretOn {
		c.Fill(sdl.Rect{X: r.X + padX + caretX - scroll, Y: r.Y + 4, W: fieldCaretWLog, H: r.H - 8}, ColText)
	}
	return value, enter
}

// devFieldValue draws a focused ASCII text field's moving parts DEVICE-EXACT at a
// fractional UI scale (#77 S1/S2). It follows the split-log precedent
// (app.go:4366-4378): read the renderer's live scale, flip SetScale(1,1), draw
// with rects projected to device pixels by uiDeviceFromLogical (round half up —
// the twin of the label blit's rounding), then restore the scale and the prior
// clip. Under SetScale(1,1) the value texture blits src→dst 1:1 (no resample, so
// strictly crisper than the ambient fractional-scale blit) and the caret gets a
// constant integer device width (no 2↔3 flicker). Render thread; allocation-free
// (sdl.Rect VALUES + the Ctx scratch rects — a &stackRect into a Ren call would
// heap-escape through cgo every frame, the cgoRect lesson).
// texture's device origin so it lands on the exact device glyph seam (#77 S1b).
func (c *Ctx) devFieldValue(r sdl.Rect, padX, avail, scroll, caretXDev, textY int32, show string, col sdl.Color, selX0, selX1 int32, hasSel, caretOn bool) {
	dev := c.textDevPct
	// The device clip = the field interior projected to device px, so the
	// scrolled-off head still can't spill left. Set the SDL clip DIRECTLY (not
	// pushClip, which mirrors a LOGICAL rect for hovering()): device draws never
	// hit-test, so the mirror must keep reflecting the ambient logical clip. Save
	// the prior clip from the mirror and restore it after the scale is back.
	prevClip, prevOn := c.clipRect, c.clipOn
	clipX := uiDeviceFromLogical(r.X+padX, dev)
	clipY := uiDeviceFromLogical(r.Y, dev)
	clipRt := uiDeviceFromLogical(r.X+padX+avail, dev)
	clipBt := uiDeviceFromLogical(r.Y+r.H, dev)

	// Read the renderer's ACTUAL scale to restore it (ground truth, matching
	// app.go's pinned-pass restore); flip to 1:1 for the device-projected draws.
	sx, sy := c.Ren.GetScale()
	c.cgoRect = sdl.Rect{X: clipX, Y: clipY, W: clipRt - clipX, H: clipBt - clipY}
	_ = c.Ren.SetScale(1, 1)
	_ = c.Ren.SetClipRect(&c.cgoRect)

	// Selection highlight, UNDER the text. Project both edges independently so the
	// fill spans exactly the projected glyph gap (consistent with the value
	// texture, which starts at the same projected origin).
	if hasSel && selX1 > selX0 {
		sx0 := uiDeviceFromLogical(r.X+padX+selX0, dev)
		sx1 := uiDeviceFromLogical(r.X+padX+selX1, dev)
		sTop := uiDeviceFromLogical(r.Y+3, dev)     // +3/-6: the selection inset used by the scaled path
		sBot := uiDeviceFromLogical(r.Y+r.H-3, dev) // (r.H-6 tall, 3px from each edge)
		c.Fill(sdl.Rect{X: sx0, Y: sTop, W: sx1 - sx0, H: sBot - sTop},
			sdl.Color{R: ColAccent.R, G: ColAccent.G, B: ColAccent.B, A: 90})
	}

	// ox is the value texture's DEVICE origin (the projected left edge of the text
	// after scroll). The caret shares this exact anchor below: the value texture and
	// the caret metric are both measured on the device face, so caretXDev device px
	// into the texture IS the glyph seam. Hoisted so that shared anchor is visible.
	// UNIT SPLIT (deliberate, #77 S1b): the caret consumes RAW DEVICE caretXDev
	// (ox+caretXDev) with no logical round-trip, so it lands on the true device
	// seam. scroll and the selection edges stay on folded-LOGICAL widths — a
	ox := uiDeviceFromLogical(r.X+padX-scroll, dev)

	// Value texture, blitted 1:1 from its DEVICE-sized cache entry at the projected
	// text origin — zero resampling, so the string can't shimmer as the scroll
	// (hence the origin's sub-pixel phase) advances per keystroke.
	if t, ok := c.textTexture(show, col, c.chromeFace()); ok {
		oy := uiDeviceFromLogical(textY, dev)
		c.drawSrc = sdl.Rect{X: t.src.X, Y: t.src.Y, W: t.w, H: t.h}
		c.drawDst = sdl.Rect{X: ox, Y: oy, W: t.w, H: t.h} // device→device 1:1
		_ = c.Ren.Copy(t.tex, &c.drawSrc, &c.drawDst)
	}

	// Caret, OVER the text, at ox + caretXDev (the device glyph seam — same anchor
	// and same device face as the value texture, so it can't drift as the prefix
	// grows). Its width is fieldCaretWLog projected with the round-half-up rule: a
	// CONSTANT integer per scale (125%→3, 150%→3 device px), so it can't flip 2↔3
	// as its phase drifts. Y/H project the same +4/-8 inset.
	if caretOn {
		cx := ox + caretXDev
		cTop := uiDeviceFromLogical(r.Y+4, dev)
		cBot := uiDeviceFromLogical(r.Y+r.H-4, dev) // r.H-8 tall, 4px from each edge
		cw := uiDeviceFromLogical(fieldCaretWLog, dev)
		c.Fill(sdl.Rect{X: cx, Y: cTop, W: cw, H: cBot - cTop}, ColText)
	}

	// Restore the ambient render scale FIRST (SDL clip rects are in scaled
	// coordinates), then the prior clip exactly as popClip would.
	_ = c.Ren.SetScale(sx, sy)
	if prevOn {
		c.cgoRect = prevClip
		_ = c.Ren.SetClipRect(&c.cgoRect)
	} else {
		_ = c.Ren.SetClipRect(nil)
	}
}

// isASCII reports whether s is all 7-bit ASCII — the cheap gate that keeps a plain text
// field on the single-font fast path (no per-frame font work) until it actually holds
// emoji / non-Latin runes.
func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

// fieldRaster returns the fallback raster a text field draws its VALUE with
// (nil = the single-font Label path). One resolution point, shared by the draw
// and every caret/scroll measurement, so the two can never use different fonts.
func (c *Ctx) fieldRaster(fb fieldFonts, mask bool, display string) *render.MessageRaster {
	if fb.primary == nil || mask || display == "" || isASCII(display) {
		return nil
	}
	return c.emojiRaster(display, ColText, fb.primary, fb.emoji)
}

// fieldPrefixW is the LOGICAL width of display's first n runes as the field
// will draw them: the raster's own advances when one is in play, else the chrome
// font. On the ASCII path at a fractional scale it measures the DEVICE chrome
// face (what the drawn texture uses) and folds back to logical — so caretX,
// scroll and the selection all agree with the device-exact glyph seam, not the
// logical-face approximation (#77 S1b). At 100% the device face is the identity,
// so it stays on the logical widthCache path unchanged.
func (c *Ctx) fieldPrefixW(display string, m *render.MessageRaster, n int) int32 {
	if m != nil {
		return m.PrefixWidth(n)
	}
	if deviceExactText(c.textDevPct) {
		return uiLogicalFromDevice(c.caretPixelXDev(display, n), c.textDevPct)
	}
	return c.caretPixelX(display, n)
}

// fieldIndexAtX maps a click x (relative to the text's left edge) to the
// nearest rune boundary under the same metrics the field draws with.
func (c *Ctx) fieldIndexAtX(display string, m *render.MessageRaster, relX int32) int {
	if m == nil {
		if deviceExactText(c.textDevPct) {
			// Hit-test on the SAME device-face metric the caret/selection use, or a
			// click lands off by exactly the drift this fix removes (#77 point 4).
			return c.caretIndexAtXDev(display, relX)
		}
		return c.caretIndexAtX(display, relX)
	}
	if relX <= 0 {
		return 0
	}
	n := m.TotalRunes()
	prevW := int32(0)
	for i := 1; i <= n; i++ {
		w := m.PrefixWidth(i)
		if relX < (prevW+w)/2 {
			return i - 1
		}
		prevW = w
	}
	return n
}

// fieldCaretMarginPx is how much text scrollKeep keeps visible PAST the caret
// when it is forced to move the view. A hard 0 would park the caret exactly on
// the box edge, where the next keystroke immediately scrolls again (a one-glyph
// stutter per character); a small lead means one nudge covers several runes and
// you can see what you are about to overwrite. Logical px, so it tracks the UI
// scale like every other field metric.
const fieldCaretMarginPx = int32(16)

// scrollKeep is the STICKY keep-the-caret-visible horizontal scroll: it returns
// `cur` unchanged whenever the caret is already inside the visible interior, and
// otherwise moves the view the SMALLEST distance that brings the caret back in
// (plus fieldCaretMarginPx of lead). Always clamped to [0, fullW-avail], so a
// value that shrank underneath the field can never leave a gap on the right.
// It replaces the old stateless "centre the caret" rule. That rule recomputed
// the view from the caret every frame, which meant clicking into a long line —
// or dragging a selection through one — re-centred the text under the pointer
func scrollKeep(cur, fullW, caretX, avail int32) int32 {
	if fullW <= avail || avail <= 0 {
		return 0 // it all fits: head-aligned, no scroll state to keep
	}
	maxScroll := fullW - avail
	s := cur
	if s > maxScroll {
		s = maxScroll
	}
	if s < 0 {
		s = 0
	}
	// The margin can never exceed half the interior, or the two clamps below
	// would fight on a very narrow box and oscillate.
	margin := fieldCaretMarginPx
	if half := avail / 2; margin > half {
		margin = half
	}
	if caretX-s < margin { // caret ran off the left edge
		s = caretX - margin
	}
	if caretX-s > avail-margin { // …or the right one
		s = caretX - (avail - margin)
	}
	if s < 0 {
		s = 0
	}
	if m := fullW - avail; s > m {
		s = m
	}
	return s
}

// caretPixelX is the x-pixel offset of the caret (a rune index) within display.
func (c *Ctx) caretPixelX(display string, caret int) int32 {
	if caret <= 0 || display == "" {
		return 0
	}
	runes := []rune(display)
	if caret > len(runes) {
		caret = len(runes)
	}
	return c.TextWidth(string(runes[:caret]))
}

// caretIndexAtX returns the rune index nearest pixel relX (a click position
// relative to the text's left edge), measured on the display string — so a
// click lands the caret between the characters under the cursor.
func (c *Ctx) caretIndexAtX(display string, relX int32) int {
	if relX <= 0 || display == "" {
		return 0
	}
	runes := []rune(display)
	prevW := int32(0)
	for i := 1; i <= len(runes); i++ {
		w := c.TextWidth(string(runes[:i]))
		if relX < (prevW+w)/2 {
			return i - 1
		}
		prevW = w
	}
	return len(runes)
}

// caretPixelXDev is caretPixelX measured on the DEVICE chrome face, returning
// raw DEVICE px — the exact seam of the field's device-rasterized texture. The
// caret draw consumes this directly (no logical round-trip); fieldPrefixW folds
// it to logical for the scroll/selection consumers. Only reached at a fractional
// scale (the ASCII device-exact field path). Early return before any []rune/probe
// so an empty focused field stays zero-alloc.
func (c *Ctx) caretPixelXDev(display string, caret int) int32 {
	if caret <= 0 || display == "" {
		return 0
	}
	runes := []rune(display)
	if caret > len(runes) {
		caret = len(runes)
	}
	return c.devTextWidth(string(runes[:caret]))
}

// caretIndexAtXDev is caretIndexAtX on the DEVICE-face metric folded back to
// logical: relX is a LOGICAL click offset (mouse coords are logical), and the
// caret/selection sit on the folded-logical device widths, so hit-testing against
// those same folded widths lands the click on the drawn glyph seam (#77 point 4).
func (c *Ctx) caretIndexAtXDev(display string, relX int32) int {
	if relX <= 0 || display == "" {
		return 0
	}
	runes := []rune(display)
	prevW := int32(0)
	for i := 1; i <= len(runes); i++ {
		w := uiLogicalFromDevice(c.devTextWidth(string(runes[:i])), c.textDevPct)
		if relX < (prevW+w)/2 {
			return i - 1
		}
		prevW = w
	}
	return len(runes)
}

// SetHoldClear stamps the hold-to-clear config for the frame (App resolves it
// from prefs). The accumulation runs in BeginFrame; the focused field clears.
func (c *Ctx) SetHoldClear(on bool, key sdl.Keycode, threshold time.Duration) {
	c.holdOn, c.holdKey, c.holdThreshold = on, key, threshold
}

// SetWordDelete stamps the Ctrl+Backspace word-delete pref for the frame (App
// resolves it from prefs). textField reads the stamped field to gate the
// captured Ctrl+Backspace at consumption — without this stamp the gate would
// read the zero value and the feature would be dead despite defaulting ON.
func (c *Ctx) SetWordDelete(on bool) {
	c.wordDeleteOn = on
}

// keyHeld reports whether key is physically down right now, via SDL's live
// keyboard state — correct even if a KEYUP was missed (window focus loss).
func (c *Ctx) keyHeld(key sdl.Keycode) bool {
	if key == sdl.K_UNKNOWN {
		return false
	}
	sc := sdl.GetScancodeFromKey(key)
	state := sdl.GetKeyboardState()
	return int(sc) < len(state) && state[sc] != 0
}

// --- dropdown ---------------------------------------------------------------

// ddDraw is one deferred dropdown overlay: geometry and content resolve at
// Dropdown call time, FinishFrame only paints (so the list stacks above
// widgets drawn after the dropdown).
type ddDraw struct {
	list    sdl.Rect
	options []string
	cur     int
	scroll  int32
	rowH    int32
	thumbW  int32                     // per-row thumbnail strip width (0 = text-only)
	thumb   func(idx int, r sdl.Rect) // paints the idx-th row's thumbnail (deferred, in FinishFrame)
}

// ddMaxVisibleRows caps an open dropdown list's height; longer lists
// wheel-scroll inside the overlay.
const ddMaxVisibleRows = 12

// Dropdown is a click-to-open selector (playtest request: the colour and pos
// selectors should be dropdowns). Closed, it shows options[cur] plus a
// chevron; open, the option list paints above everything and the pointer
// is modally captured so widgets underneath stay inert. Returns the
// (possibly new) index and whether it changed this frame.
func (c *Ctx) Dropdown(id string, r sdl.Rect, options []string, cur int) (int, bool) {
	return c.dropdownEx(id, r, options, cur, 0, 0, nil)
}

// DropdownThumbs is Dropdown with a thumbnail strip down the left of each open
// row: rowHWant sets the (taller) open-row height, thumbW the strip width, and
// thumb paints the idx-th row's image at end-of-frame (it shares the deferred
// overlay paint, so the thumbnails sit above everything like the list itself).
// The closed control is unchanged. thumb runs on the render thread in
// FinishFrame, so an on-demand cachedPage/Prefetch from it is safe.
func (c *Ctx) DropdownThumbs(id string, r sdl.Rect, options []string, cur int, rowHWant, thumbW int32, thumb func(idx int, r sdl.Rect)) (int, bool) {
	return c.dropdownEx(id, r, options, cur, rowHWant, thumbW, thumb)
}

func (c *Ctx) dropdownEx(id string, r sdl.Rect, options []string, cur int, rowHWant, thumbW int32, thumb func(idx int, r sdl.Rect)) (int, bool) {
	if len(options) == 0 {
		return cur, false
	}
	if cur < 0 || cur >= len(options) {
		cur = 0
	}
	open := c.ddOpen == id

	// Closed control: button chrome with the pick and a chevron.
	bg := ColPanel
	if c.hovering(r) || open {
		bg = ColPanelHi
	}
	c.Fill(r, bg)
	c.Border(r, ColAccent)
	labelX := r.X + 6
	if thumbW > 0 && thumb != nil { // a thumbnail of the CURRENT pick on the closed control, sized to its height
		tw := (r.H - 4) * 4 / 3
		thumb(cur, sdl.Rect{X: r.X + 2, Y: r.Y + 2, W: tw, H: r.H - 4})
		labelX = r.X + 2 + tw + 4
	}
	// The pick can be SERVER/USER text (the showname presets, a server name), so it
	// goes through the covering path — a fixed single face boxed anything it
	// couldn't draw. Plain ASCII keeps the identical texture blit.
	c.labelCoveringCentered(labelX, r.Y, r.H, r.X+r.W-16-labelX, options[cur], ColText)
	c.Label(r.X+r.W-14, r.Y+(r.H-int32(c.chromeFace().Height()))/2, "▾", ColTextDim)

	if !open {
		if c.hovering(r) && c.clicked {
			c.ddOpen = id
			c.ddScroll = 0
		}
		return cur, false
	}

	// Open: geometry. The list grows to the widest option (tiny controls
	// stay usable), drops below the control, flips above at the window's
	// bottom edge, and shifts left at the right edge.
	rowH := r.H
	if rowH < int32(c.chromeFace().Height())+6 {
		rowH = int32(c.chromeFace().Height()) + 6
	}
	if rowHWant > rowH { // thumbnail rows want to be taller than the closed control
		rowH = rowHWant
	}
	visible := int32(len(options))
	if visible > ddMaxVisibleRows {
		visible = ddMaxVisibleRows
	}
	listW := r.W
	for _, o := range options {
		if w := c.TextWidth(o) + 16 + thumbW; w > listW { // leave room for the thumbnail strip
			listW = w
		}
	}
	list := sdl.Rect{X: r.X, Y: r.Y + r.H, W: listW, H: visible * rowH}
	if outW, outH, err := c.Ren.GetOutputSize(); err == nil {
		if limit := c.toLogical(outH); list.Y+list.H > limit && r.Y-list.H >= 0 {
			list.Y = r.Y - list.H
		}
		if limit := c.toLogical(outW); list.X+list.W > limit {
			list.X = limit - list.W
		}
	}

	// Modal capture: while open, the dropdown owns the pointer until the close
	// releases it (next BeginFrame). hovering() blanks for EVERY other widget —
	// even one drawn directly under the list — so a click on a list row can't also
	// fall through to it. The dropdown uses the raw pointIn() hit test below for
	// its own interaction (the fence would otherwise blank it too).
	c.modalOn = true
	// Publish the open list rect for boxFencesPointer: the list paints ABOVE the
	// floating panels (FinishFrame), so input must follow the visuals — while the
	// cursor is over the list, the courtroom pass must not run pointer-blind, or a
	// list flipped up over a torn tab has dead rows (the custom-layout playtest bug).
	c.ddOpenList = list

	// Wheel scrolls long lists (raw hit test, since hovering() is fenced while open).
	contentH := int32(len(options)) * rowH
	if c.wheelY != 0 && pointIn(c.mouseX, c.mouseY, list) {
		c.wheelTaken = true
		c.ddScroll -= c.wheelY * rowH
	}
	if max := contentH - list.H; c.ddScroll > max {
		c.ddScroll = max
	}
	if c.ddScroll < 0 {
		c.ddScroll = 0
	}

	// Interaction resolves NOW (frame-consistent); painting defers. Every branch
	// CONSUMES the click (the modal owns it): widgets later in the frame — floating
	// panels draw with the pointer restored — must never also react to a click that
	// visually landed on (or dismissed) the list.
	next, changed := cur, false
	if c.clicked {
		switch {
		case pointIn(c.mouseX, c.mouseY, list): // raw: the dropdown owns the pointer, bypass the fence
			idx := int((c.mouseY - list.Y + c.ddScroll) / rowH)
			if idx >= 0 && idx < len(options) {
				next, changed = idx, idx != cur
			}
			c.ddOpen = ""
			c.modalRelease = true
		case pointIn(c.mouseX, c.mouseY, r):
			// Toggling the control shut.
			c.ddOpen = ""
			c.modalRelease = true
		default:
			// Click-away closes and the fence swallows the click.
			c.ddOpen = ""
			c.modalRelease = true
		}
		c.clicked = false
	}
	c.ddDraws = append(c.ddDraws, ddDraw{
		list: list, options: options, cur: next, scroll: c.ddScroll, rowH: rowH,
		thumbW: thumbW, thumb: thumb,
	})
	c.ddHitID, c.ddHitList, c.ddHitRowH, c.ddHitScroll, c.ddHitRows = id, list, rowH, c.ddScroll, len(options)
	return next, changed
}

// DropdownRowRightClicked reports the row index under the pointer when the OPEN
// list belonging to id was right-clicked, and CONSUMES the click.
// It exists because the dropdown owns row hit-testing inside dropdownEx, not
// inside the thumb painter — so a caller that wants a per-row secondary action
// (audition this effect / this sound without selecting it) has no other way to
// Consuming the click is the fence discipline (app.go's floating-pointer
// save/restore): an unconsumed right-click would leak through to whatever widget
// sits under the list, which is exactly what the modal capture exists to stop.
func (c *Ctx) DropdownRowRightClicked(id string) (int, bool) {
	if !c.rightClicked || id == "" || c.ddHitID != id || c.ddOpen != id {
		return 0, false
	}
	if !pointIn(c.mouseX, c.mouseY, c.ddHitList) { // raw hit test: the open list owns the pointer
		return 0, false
	}
	if c.ddHitRowH <= 0 {
		return 0, false
	}
	idx := int((c.mouseY - c.ddHitList.Y + c.ddHitScroll) / c.ddHitRowH)
	if idx < 0 || idx >= c.ddHitRows {
		return 0, false
	}
	c.rightClicked = false
	return idx, true
}

// FinishFrame paints deferred overlays (open dropdown lists). Call after
// every screen draw of the frame.
// Tooltip arms a one-line hover hint for rect r; drawTooltip paints it near
// the cursor at end-of-frame so it sits above the grid cells it describes.
// Skipped while a dropdown owns the pointer (modal fence).
func (c *Ctx) Tooltip(r sdl.Rect, text string) {
	if text != "" && !c.modalOn && c.hovering(r) {
		c.tipText = text
		c.tipAnchor = unionRect(r, c.tipGroup)
	}
}

// TooltipGroup declares the SURFACE a run of child tooltips belongs to, so the
// hint stays clear of the whole thing rather than just the one chip it describes.
// Set it before the children, clear it (TooltipGroup(sdl.Rect{})) after — the
// compact toolbox wraps its chip loop this way, because a hint that covers the
// four chips beside the hovered one is as useless as one covering the chip itself.
func (c *Ctx) TooltipGroup(r sdl.Rect) { c.tipGroup = r }

// tooltipDwell is how long the pointer must rest on a TooltipAfter target
// before its hint shows — long enough to stay unobtrusive (the Extras buttons).
const tooltipDwell = 2 * time.Second

// TooltipAfter is Tooltip with a dwell: r's hint shows only after the pointer
// has rested on it (keyed by id) for tooltipDwell. id distinguishes adjacent
// targets so moving between them restarts the timer.
func (c *Ctx) TooltipAfter(id string, r sdl.Rect, text string) {
	c.TooltipAfterDelay(id, r, text, tooltipDwell)
}

// TooltipAfterDelay is TooltipAfter with a caller-chosen dwell instead of the
// fixed tooltipDwell — for hints whose delay is user-configurable (the emote-name
// hover tooltip's slider). A single shared dwell timer (tipHoverID/tipHoverSince)
// backs both: moving off r, or to a different id, resets the timer, so two
// TooltipAfterDelay targets can't fire each other early.
func (c *Ctx) TooltipAfterDelay(id string, r sdl.Rect, text string, delay time.Duration) {
	if text == "" || c.modalOn || !c.hovering(r) {
		if c.tipHoverID == id {
			c.tipHoverID = ""
		}
		return
	}
	if c.tipHoverID != id {
		c.tipHoverID = id
		c.tipHoverSince = time.Now()
		c.tipHoverDelay = delay // NextHoverDue schedules the wake off this
		return
	}
	if time.Since(c.tipHoverSince) >= delay {
		c.tipText = text
		c.tipAnchor = unionRect(r, c.tipGroup)
	}
}

const (
	tooltipMargin = 8   // keep the tooltip box this far inside the window edges
	tooltipPad    = 6   // inner padding around the text
	tooltipLineH  = 18  // wrapped-line pitch
	tooltipMaxLn  = 10  // line cap so a huge description can't wallpaper the screen (WrapText ellipsizes)
	tooltipMaxW   = 460 // preferred max text width before it word-wraps
)

// tipBox places a tooltip of size boxW×boxH near the cursor (mx,my): it offsets to
// the lower-right, flips to the other side of the pointer when that would overflow,
// and then clamps so the WHOLE box stays inside the w×h window — even when the box
// is wider/taller than the room on either side (a long server description). Pure, so
// the "never off-screen" rule (the reported bug) is unit-tested.
func tipBox(mx, my, boxW, boxH, w, h int32, avoid sdl.Rect) sdl.Rect {
	base := tipBoxDefault(mx, my, boxW, boxH, w, h)
	if avoid.W <= 0 || avoid.H <= 0 {
		return base
	}
	if _, hit := base.Intersect(&avoid); !hit {
		return base
	}
	// Alternatives in preference order: the other three cursor corners, then flush
	// above and flush below the avoided rect. The last two are what a bottom-corner
	// icon strip needs, where EVERY cursor-relative corner still lands on the
	// strip's own neighbours (a 122x22 strip at the window's bottom-right).
	for _, cand := range [...]sdl.Rect{
		tipBoxAt(mx+16, my-boxH-6, boxW, boxH, w, h),
		tipBoxAt(mx-boxW-8, my+18, boxW, boxH, w, h),
		tipBoxAt(mx-boxW-8, my-boxH-6, boxW, boxH, w, h),
		tipBoxAt(base.X, avoid.Y-boxH-tooltipMargin, boxW, boxH, w, h),
		tipBoxAt(base.X, avoid.Y+avoid.H+tooltipMargin, boxW, boxH, w, h),
	} {
		if _, hit := cand.Intersect(&avoid); !hit {
			return cand
		}
	}
	return base // nothing dodges it: staying on-screen wins over staying clear
}

// tipBoxDefault is the ORIGINAL cursor-relative placement, unchanged: offset to
// the lower-right, flip to the other side of the pointer on overflow, then clamp.
func tipBoxDefault(mx, my, boxW, boxH, w, h int32) sdl.Rect {
	x := mx + 16
	if x+boxW > w-tooltipMargin {
		x = mx - boxW - 8 // flip left of the pointer
	}
	if x+boxW > w-tooltipMargin {
		x = w - tooltipMargin - boxW // still over (box wider than the flip room): pin to the right edge
	}
	if x < tooltipMargin {
		x = tooltipMargin
	}
	y := my + 18
	if y+boxH > h-tooltipMargin {
		y = my - boxH - 6 // flip above the pointer
	}
	if y+boxH > h-tooltipMargin {
		y = h - tooltipMargin - boxH
	}
	if y < tooltipMargin {
		y = tooltipMargin
	}
	return sdl.Rect{X: x, Y: y, W: boxW, H: boxH}
}

// tipBoxAt clamps one candidate top-left fully inside the window. Split out so
// every alternative is clamped by the same arithmetic — the clamping IS the
// on-screen invariant, and a candidate that dodged it would reintroduce the very
// bug tipBox was written to fix.
func tipBoxAt(x, y, boxW, boxH, w, h int32) sdl.Rect {
	if x+boxW > w-tooltipMargin {
		x = w - tooltipMargin - boxW
	}
	if x < tooltipMargin {
		x = tooltipMargin
	}
	if y+boxH > h-tooltipMargin {
		y = h - tooltipMargin - boxH
	}
	if y < tooltipMargin {
		y = tooltipMargin
	}
	return sdl.Rect{X: x, Y: y, W: boxW, H: boxH}
}

// drawTooltip paints the armed hover hint near the cursor. Long text (e.g. a server
// description) WORD-WRAPS to a capped width instead of running off-screen, and the
// box flips/clamps at the window edges so it's always fully visible. Called last in
// the frame. The wrap is cached (tipWrapText/W) so a hovered tooltip costs nothing
// extra per frame.
func (c *Ctx) drawTooltip(w, h int32) {
	if c.tipText == "" {
		return
	}
	// Wrap to the smaller of the preferred max and the window width (minus margins/pad).
	maxW := int32(tooltipMaxW)
	if lim := w - 2*tooltipMargin - 2*tooltipPad; maxW > lim {
		maxW = lim
	}
	if maxW < 40 {
		maxW = 40
	}
	if c.tipWrapText != c.tipText || c.tipWrapW != maxW { // re-wrap only when the text or width changed
		c.tipWrapLn = c.WrapText(c.tipText, maxW, tooltipMaxLn)
		c.tipWrapText, c.tipWrapW = c.tipText, maxW
	}
	lines := c.tipWrapLn
	if len(lines) == 0 {
		return
	}
	boxW := int32(0)
	for _, ln := range lines {
		if tw := c.TextWidth(ln); tw > boxW {
			boxW = tw
		}
	}
	boxW += 2 * tooltipPad
	boxH := int32(len(lines))*tooltipLineH + 2*tooltipPad
	box := tipBox(c.mouseX, c.mouseY, boxW, boxH, w, h, c.tipAnchor)
	c.Fill(box, sdl.Color{R: 0, G: 0, B: 0, A: 235})
	c.Border(box, ColAccent)
	ty := box.Y + tooltipPad
	for _, ln := range lines {
		c.Label(box.X+tooltipPad, ty, ln, ColText)
		ty += tooltipLineH
	}
}

func (c *Ctx) FinishFrame() {
	for i := range c.ddDraws {
		d := &c.ddDraws[i]
		c.Fill(d.list, ColPanel)
		c.Border(d.list, ColAccent)
		y := d.list.Y - d.scroll
		for idx, opt := range d.options {
			row := sdl.Rect{X: d.list.X, Y: y, W: d.list.W, H: d.rowH}
			y += d.rowH
			if row.Y+row.H <= d.list.Y || row.Y >= d.list.Y+d.list.H {
				continue
			}
			switch {
			case idx == d.cur:
				c.Fill(row, sdl.Color{R: ColAccent.R, G: ColAccent.G, B: ColAccent.B, A: 110})
			case c.hovering(row):
				c.Fill(row, ColPanelHi)
			}
			labelX := row.X + 6
			if d.thumbW > 0 && d.thumb != nil { // thumbnail strip down the left; text shifts past it
				d.thumb(idx, sdl.Rect{X: row.X + 2, Y: row.Y + 2, W: d.thumbW, H: d.rowH - 4})
				labelX = row.X + d.thumbW + 8
			}
			// Same as the closed control: an open row is the one place a saved
			// showname or a server name is READ, so it must not box.
			c.labelCoveringCentered(labelX, row.Y, d.rowH, row.X+row.W-labelX-6, opt, ColText)
		}
	}
	c.ddDraws = c.ddDraws[:0]
}

// WrapText greedily word-wraps s to maxW pixels in the chrome font,
// capped at maxLines (the last line gains an ellipsis when truncated).
// Widths ride the TextWidth memo; callers cache the result per string.
func (c *Ctx) WrapText(s string, maxW int32, maxLines int) []string {
	words := strings.Fields(s)
	if len(words) == 0 {
		return nil
	}
	lines := make([]string, 0, maxLines)
	line := ""
	for _, word := range words {
		candidate := word
		if line != "" {
			candidate = line + " " + word
		}
		if c.TextWidth(candidate) <= maxW || line == "" {
			line = candidate
			continue
		}
		lines = append(lines, line)
		line = word
		if len(lines) == maxLines {
			lines[maxLines-1] += "…"
			return lines
		}
	}
	return append(lines, line)
}

// VScrollbar draws a proportional vertical scrollbar and returns the scroll
// offset, updated by thumb drags: pressing anywhere on the track centers
// the thumb there, so one click reaches any point — including the very
// bottom of a 4000-character list. content and visible are pixel heights;
// the result is clamped to [0, content-visible] (use it to clamp wheel
// scrolling too). Draws nothing when everything fits.
func (c *Ctx) VScrollbar(id string, track sdl.Rect, scroll, content, visible int32) int32 {
	// A degenerate panel (a layout-edited or fixed-geometry slot laid out to a
	// non-positive height) hands us visible <= 0. With an empty list content is
	// 0 too, so maxScroll = content - visible is POSITIVE and slips past the
	// guard below, then thumbH = track.H*visible/content divides by content=0.
	// Guard both: nothing scrolls (and nothing draws) when there's no content or
	// no visible viewport — the same "everything fits" outcome as maxScroll<=0.
	if content <= 0 || visible <= 0 {
		return 0
	}
	maxScroll := content - visible
	if maxScroll <= 0 {
		return 0
	}
	if scroll < 0 {
		scroll = 0
	}
	if scroll > maxScroll {
		scroll = maxScroll
	}

	thumbH := track.H * visible / content
	if thumbH < scrollThumbMinPx {
		thumbH = scrollThumbMinPx
	}
	if thumbH > track.H {
		thumbH = track.H
	}
	span := track.H - thumbH

	grab := sdl.Rect{X: track.X - scrollGrabSlopPx, Y: track.Y, W: track.W + 2*scrollGrabSlopPx, H: track.H}
	if c.mouseDown && (c.dragID == id || (c.dragID == "" && c.hovering(grab))) {
		c.dragID = id
		if span > 0 {
			pos := c.mouseY - track.Y - thumbH/2
			if pos < 0 {
				pos = 0
			}
			if pos > span {
				pos = span
			}
			scroll = pos * maxScroll / span
		}
	}

	c.Fill(track, ColPanel)
	thumbY := track.Y + scroll*span/maxScroll
	col := sliderIdleThumbColor()
	if c.dragID == id || c.hovering(grab) {
		col = ColAccent
	}
	c.Fill(sdl.Rect{X: track.X, Y: thumbY, W: track.W, H: thumbH}, col)
	return scroll
}

// Slider draws a horizontal value slider over [0, maxVal] and returns the
// (possibly updated) value. It mirrors VScrollbar's drag model: drag-captured
// by id, hover-gated, and mouse-down anywhere on the track jumps the thumb —
// so it's grabbable rather than a fiddly +/- button press. Continuous (any
// int in range).
// sliderWheelDivisor sets the wheel step to 1/100 of a slider's range (≥1), so
// the ubiquitous percent sliders step by exactly 1 per tick.
const sliderWheelDivisor = 100

func (c *Ctx) Slider(id string, track sdl.Rect, value, maxVal int32) int32 {
	if maxVal <= 0 {
		return 0
	}
	if value < 0 {
		value = 0
	}
	if value > maxVal {
		value = maxVal
	}
	const sliderThumbW = 10
	span := track.W - sliderThumbW
	if span < 0 {
		span = 0
	}
	grab := sdl.Rect{X: track.X - scrollGrabSlopPx, Y: track.Y - scrollGrabSlopPx, W: track.W + 2*scrollGrabSlopPx, H: track.H + 2*scrollGrabSlopPx}
	if c.mouseDown && (c.dragID == id || (c.dragID == "" && c.hovering(grab))) {
		c.dragID = id
		if span > 0 {
			pos := c.mouseX - track.X - sliderThumbW/2
			if pos < 0 {
				pos = 0
			}
			if pos > span {
				pos = span
			}
			value = pos * maxVal / span
		}
	}
	// Wheel over the track nudges the value (playtest ask: every slider scrolls).
	// One tick = 1/sliderWheelDivisor of the range, floored at 1 so a percent
	// slider steps by exactly 1. Claiming the wheel keeps the page beneath from
	// scrolling on the same tick (the hovered-control-owns-the-wheel contract).
	if !c.wheelTaken && c.wheelY != 0 && c.hovering(grab) {
		c.wheelTaken = true
		step := maxVal / sliderWheelDivisor
		if step < 1 {
			step = 1
		}
		value += c.wheelY * step
		if value < 0 {
			value = 0
		}
		if value > maxVal {
			value = maxVal
		}
	}
	c.Fill(track, ColPanel)
	thumbX := track.X
	if span > 0 {
		thumbX = track.X + value*span/maxVal
	}
	if thumbX > track.X { // filled portion left of the thumb reads as "level"
		c.Fill(sdl.Rect{X: track.X, Y: track.Y, W: thumbX - track.X, H: track.H}, ColPanelHi)
	}
	col := sliderIdleThumbColor()
	if c.dragID == id || c.hovering(grab) {
		col = ColAccent
	}
	c.Fill(sdl.Rect{X: thumbX, Y: track.Y, W: sliderThumbW, H: track.H}, col)
	return value
}

// HoverPreview tracks dwell time on a widget id; returns true when the
// full-size preview should show: the configured hover dwell, or right-click
// instantly. The Settings previews toggle gates ONLY the dwell path — it
// exists to stop popups you didn't ask for, and an explicit right-click IS
// asking (playtest: turning hover-previews off silently killed the
// right-click preview too — "the feature doesn't work").
func (c *Ctx) HoverPreview(id string, r sdl.Rect) bool {
	if !c.hovering(r) {
		if c.hoverID == id {
			c.hoverID = ""
		}
		return false
	}
	c.hoverRect = r // remember the hovered trigger's rect for the close-on-leave corridor
	if c.rightClicked {
		c.hoverID = id // pin it so close-on-leave keeps the box up after an instant open
		return true
	}
	if !c.hoverPreviewOn {
		// Dwell disabled: never START a preview from hover. hoverID is left
		// alone so a right-click-opened box keeps its active trigger (the
		// close-on-leave contract); each trigger clears its own id above the
		// moment the cursor leaves it.
		return false
	}
	if c.hoverID != id {
		c.hoverID = id
		c.hoverSince = time.Now()
		return false
	}
	return time.Since(c.hoverSince) >= c.hoverPreviewDelay
}

// SetHoverPreview stamps the per-frame sprite-preview config (enabled + dwell)
// onto the context; App.Frame calls it once before any screen draws.
func (c *Ctx) SetHoverPreview(on bool, delay time.Duration) {
	c.hoverPreviewOn = on
	c.hoverPreviewDelay = delay
}

// Destroy frees cached textures and fonts.
func (c *Ctx) Destroy() {
	c.purgeTextCache()
	for _, s := range []*fontSet{&c.chatSet, &c.logSet} {
		for _, f := range s.fonts {
			if f != c.font && f != nil {
				f.Close()
			}
		}
		s.fonts = nil
	}
	// Device-scaled sets (#77): their non-shared faces share c.fontDev.
	for _, s := range []*fontSet{&c.chatSetDev, &c.logSetDev} {
		for _, f := range s.fonts {
			if f != c.fontDev && f != nil {
				f.Close()
			}
		}
		s.fonts = nil
	}
	c.dropThemeElemSets() // per-element theme sets (#39), same close-guards
	// Device chrome faces: close only when NOT sharing the logical faces (the
	// 100% case shares them, so the logical closes below cover it).
	if c.fontDev != nil && c.fontDev != c.font {
		c.fontDev.Close()
	}
	if c.fontBigDev != nil && c.fontBigDev != c.fontBig {
		c.fontBigDev.Close()
	}
	if c.font != nil {
		c.font.Close()
	}
	if c.fontBig != nil {
		c.fontBig.Close()
	}
}
