package ui

import (
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	hvalue "github.com/SyntaxNyah/AsyncAO/internal/helper/value"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
	"github.com/SyntaxNyah/AsyncAO/internal/ttf"
)

// LabelClippedFontAlpha is LabelClippedFont with a per-draw alpha applied by SetAlphaMod on the cached texture. Fading
// by darkening the ink would change textKey and rasterise a new texture every frame. The texture is a shared atlas
// page, so the alpha is restored inline for the next label. Alpha 255 takes the ordinary path.
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

// TextWidth measures a label in the chrome font, memoized: each miss is a cgo TTF measure. The memo is purged together
// with the text cache. It measures the logical face, so the width is scale-invariant layout units; caret and selection
// metrics measure the device face instead (devTextWidth, render/text.go:568-570).
func (c *Ctx) TextWidth(text string) int32 {
	if c.font == nil {
		return 0 // headless tests; real Ctx always has the chrome font
	}
	// A panel face measures in its own memo; widthCache holds only the chrome face's advances.
	if c.panelFont != nil {
		return c.panelTextWidth(text)
	}
	// Non-Latin measures on the face it will draw with (chromeFaceFor); the font-keyed memo carries the face pointer,
	// so a rebuilt chain never leaves a stale width.
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

// panelTextWidth is TextWidth on the armed panel face, with the same cap and wholesale reset as widthCache.
func (c *Ctx) panelTextWidth(text string) int32 {
	w, _ := c.fontTextWidth(c.panelFont, text)
	return w
}

// fontTextWidth measures text on an explicit face, memoized by font and text. ok is false when the face is nil or
// SDL_ttf refused. Each SizeUTF8 is a cgo call that shapes the whole string, so per-frame measures must come through
// here.
func (c *Ctx) fontTextWidth(f *ttf.Font, text string) (int32, bool) {
	return c.fontTextWidthWeight(f, text, false)
}

// fontTextWidthWeight is fontTextWidth measured at a weight; bold==false uses the plain key and measure.
func (c *Ctx) fontTextWidthWeight(f *ttf.Font, text string, bold bool) (int32, bool) {
	if f == nil {
		return 0, false
	}
	k := panelWidthKey{font: f, text: text, bold: bold}
	if w, ok := c.panelWidth[k]; ok {
		return w, true
	}
	w, err := c.measureLabel(f, text, bold) // the width the label draws at, run by run when mixed
	if err != nil {
		return 0, false
	}
	if c.panelWidth == nil {
		c.panelWidth = make(map[panelWidthKey]int32, textCacheMax)
	} else if len(c.panelWidth) >= textCacheMax {
		c.panelWidth = make(map[panelWidthKey]int32, textCacheMax)
	}
	c.panelWidth[k] = w
	return w, true
}

// devTextWidth measures text on the device chrome face and returns device px, the metric the drawn field texture uses
// at a fractional UI scale. SDL_ttf quantizes each glyph advance per point size, so only a device measure lands the
// caret on the glyph edge (render/text.go:568-570). The memo is capped by textCacheMax and purged with widthCache.
// Callers at 100% must use the widthCache path instead. The map is created lazily because test-built Ctx literals leave
// it nil.
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

// devTextWidthWeight is fontTextWidthWeight on the device face, for log-row selection. It returns raw device px;
// callers fold with uiLogicalFromDevice (logPrefixWidth). Measuring the logical face instead drifted from the drawn
// device texture at every scale but 100%, so selection and the copied runes disagreed. Memoized in logDevWidth; it
// shares panelWidth's purge site (purgeTextCache).
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
	// A non-sharp chrome shape draws 9-sliced alpha masks and insets the label by shapeLabelInset so glyphs avoid the
	// corner curve. Sharp, or before the masks are resident, draws plain Fill+Border+label.
	shaped := c.activeShape != shapeSharp && c.activeShape != "" && c.shapeMaskReady
	if shaped {
		c.FillShaped(r, col)
		c.borderShaped(r, border)
	} else {
		c.Fill(r, col)
		c.Border(r, border)
	}
	if t, ok := c.textTexture(label, text, c.chromeFace()); ok {
		// Clip to the button so tiny themed rects cannot leak the label over neighbours.
		lw, lh := t.logicalW(), uiLogicalFromDevice(t.h, t.devPct)
		w, x := lw, r.X+(r.W-lw)/2
		// A shaped button reserves shapeLabelInset extra px on each side.
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

// Checkbox draws a toggle; returns the (possibly flipped) value. checkboxBoxPx is the tick box side, checkboxLabelGapPx
// the gap to the label, checkboxTickInsetPx the tick inset. They are named so CheckboxWidth can measure without
// drawing.
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

// checkboxWidthFor is CheckboxWidth with the measurement injected, for layout passes without a renderer.
func checkboxWidthFor(label string, measure func(string) int32) int32 {
	return checkboxBoxPx + checkboxLabelGapPx + measure(label)
}

// rowLabelKey keys the truncated-label memo: shortening is a per-rune chop-and-measure loop that allocates, and
// settings rows draw every frame.
type rowLabelKey struct {
	label string
	avail int32
}

// pushRowLabelLimit arms a right edge (logical window px) past which a chrome row label must not draw, and returns the
// previous limit for popRowLabelLimit. 0 disarms. It is a bracket rather than a parameter because the limit belongs to
// the container (the settings card) and Checkbox has hundreds of call sites. Restore it with defer, as pushPanelFont
// does.
func (c *Ctx) pushRowLabelLimit(rightX int32) int32 {
	prev := c.rowLabelRight
	c.rowLabelRight = rightX
	return prev
}

func (c *Ctx) popRowLabelLimit(prev int32) { c.rowLabelRight = prev }

// fitRowLabel is the label a row should draw at label-origin x under the armed limit: the label itself when it fits,
// otherwise AO2's chop-two-append-ellipsis truncation. fit is false when it had to shorten. The ellipsis shows the
// sentence continues; a hard clip looks like a rendering fault.
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
	// A muted row is overruled by a master toggle upstream: draw it dim and return the value untouched.
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
	// Honour the container's right edge; unbracketed callers get the label back verbatim.
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

// truncateChopRunes and truncateEllipsis mirror AO2's truncation: chop two characters, append one ellipsis, netting one
// character per pass (courtroom.cpp:6750-6754).
const (
	truncateChopRunes = 2
	truncateEllipsis  = "…"
)

// truncateLabelTo shortens label until it fits avail by chopping two runes and appending "…" (courtroom.cpp:6749-6774
// truncate_label_text). A label that measures exactly avail is left alone, matching AO2's `>` loop condition. If
// chopping would leave only the ellipsis, the original text is kept; this also ends the loop when the ellipsis is wider
// than the rect. Pure, with measure injected.
func truncateLabelTo(label string, avail int32, measure func(string) int32) string {
	return truncateLabelToMarker(label, avail, truncateEllipsis, measure)
}

// truncateLabelToMarker is truncateLabelTo with a caller-chosen marker, for themed faces where U+2026 may have no
// glyph. Each pass chops truncateChopFor runes, so it nets one original character narrower; with AO2's marker that is
// two. Exact-fit and lone-marker behave as in truncateLabelTo.
func truncateLabelToMarker(label string, avail int32, marker string, measure func(string) int32) string {
	if avail <= 0 || measure(label) <= avail {
		return label
	}
	chop := truncateChopFor(marker)
	out := label
	for measure(out) > avail && out != marker {
		// Chop the current string and re-append the marker, as AO2 does; the previous marker is among the chopped
		// runes, so each pass nets one original rune.
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

// truncateChopFor is how many runes one pass removes so the net loss is one original character: the marker's runes plus
// one. For a single-rune ellipsis that is AO2's chop of two (courtroom.cpp:6750-6754).
func truncateChopFor(marker string) int {
	return len([]rune(marker)) + 1
}

// fitLabelToFont is truncateLabelTo measured in a specific face, for themed surfaces drawn in a theme font. On
// per-frame paths test the fits case with fontTextWidth first; only an overlong label needs the measure closure. The
// marker is chosen against the same face (ellipsisFor), so a font without U+2026 gets "..." instead of a missing glyph
// box.
func (c *Ctx) fitLabelToFont(f *ttf.Font, label string, avail int32) string {
	return c.fitLabelToFontWeight(f, label, avail, false)
}

// fitLabelToFontWeight is fitLabelToFont measured at a weight; bold glyphs are wider, so a plain fit would overflow.
// bold==false uses the plain measure and memo key.
func (c *Ctx) fitLabelToFontWeight(f *ttf.Font, label string, avail int32, bold bool) string {
	return truncateLabelToMarker(label, avail, c.ellipsisFor(f), func(s string) int32 {
		w, ok := c.fontTextWidthWeight(f, s, bold)
		if !ok {
			return 0
		}
		return w
	})
}

// CheckboxLabelAvail is the label width inside an AO2 design rect after the tick box and gap.
func (c *Ctx) CheckboxLabelAvail(r sdl.Rect) int32 {
	return r.W - checkboxBoxPx - checkboxLabelGapPx
}

// checkboxBoxY vertically centres the tick box in a design rect, floored at the rect's top: ThemeFitNative can shrink
// the rect below the 16 px box.
func checkboxBoxY(r sdl.Rect) int32 {
	if off := (r.H - checkboxBoxPx) / 2; off > 0 {
		return r.Y + off
	}
	return r.Y
}

// CheckboxIn is Checkbox fitted to a design rect (AO2 set_size_and_pos), for toggles narrower than their label. The box
// is centred vertically (floored at zero) and the label is clipped to the rest. It takes no id, like Checkbox: c.onRow
// collects by label.
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
	// The hit area is the theme's rect, not the ink, so a truncated label does not shrink the click target. The click
	// is consumed because AO2 design rects can overlap (aceattorney2x guard and additive overlap by 58 px) and
	// immediate mode would otherwise fire both.
	if c.hovering(r) && c.clicked {
		c.clicked = false
		return !value
	}
	return value
}

// FocusField queues keyboard focus onto a TextField id (AO2-Client focus_ic_input).
func (c *Ctx) FocusField(id string) { c.focusNext = id }

// ReserveFieldTail declares px logical pixels at the right edge of the next text field with this id as covered by
// something the caller paints on top (the IC counter, the muted chip), so the value clips before that band. One
// reservation is kept, consumed by the first matching field and cleared by BeginFrame. px <= 0 is a no-op.
func (c *Ctx) ReserveFieldTail(id string, px int32) {
	if id == "" || px <= 0 {
		return
	}
	c.tailReserveID, c.tailReserveW = id, px
}

// takeFieldTail consumes a pending ReserveFieldTail for id (0 if none or declared for another field).
func (c *Ctx) takeFieldTail(id string) int32 {
	if c.tailReserveID != id {
		return 0
	}
	w := c.tailReserveW
	c.tailReserveID, c.tailReserveW = "", 0
	return w
}

// WheelIn returns this frame's wheel ticks when the cursor is inside r and no earlier widget consumed them, else 0. A
// hit marks the wheel taken, which also blocks every later WheelIn, so stacked surfaces never both scroll. Pre-screen
// overlay handlers run first and win; later draws that should win blind the passes beneath via the pointer block
// (fencePointer / boxFencesPointer / modalOn).
func (c *Ctx) WheelIn(r sdl.Rect) int32 {
	if c.wheelTaken || c.wheelY == 0 || !c.hovering(r) {
		return 0
	}
	c.wheelTaken = true
	return c.wheelY
}

// fieldDrewLastFrame reports whether a TextField with this id was painted on the last frame that drew. It reads
// drawnFieldSeq, not fieldSeq: fieldSeq is the list this pass is building and is already emptied by BeginDraw when
// pre-screen callers run. drawnFieldSeq survives skipped frames, which the Tab cycle needs too.
func (c *Ctx) fieldDrewLastFrame(id string) bool {
	for _, f := range c.drawnFieldSeq {
		if f == id {
			return true
		}
	}
	return false
}

// cycleField picks the next focus target for Tab / Shift+Tab in draw order, wrapping; with nothing focused, the first.
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

// fieldFonts carries the optional emoji-aware faces for a text field. The zero value is the plain single-font path.
// TextFieldEmoji passes a fallback-capable primary plus the colour-emoji face (IC / OOC inputs).
type fieldFonts struct{ primary, emoji *ttf.Font }

// TextField edits value in place; id keys focus. Returns (newValue, enterPressed).
func (c *Ctx) TextField(id string, r sdl.Rect, value string, placeholder string) (string, bool) {
	return c.textField(id, r, value, placeholder, false, fieldFonts{})
}

// TextFieldEmoji is TextField that draws emoji and non-Latin runes through the per-glyph fallback raster; plain ASCII
// stays on the single-font path.
func (c *Ctx) TextFieldEmoji(id string, r sdl.Rect, value, placeholder string, primary, emoji *ttf.Font) (string, bool) {
	return c.textField(id, r, value, placeholder, false, fieldFonts{primary: primary, emoji: emoji})
}

// PasswordField is TextField rendered as asterisks. Ctrl+C/X never put the secret on the clipboard; Ctrl+X still
// clears.
func (c *Ctx) PasswordField(id string, r sdl.Rect, value string, placeholder string) (string, bool) {
	return c.textField(id, r, value, placeholder, true, fieldFonts{})
}

// editOp is a caret movement or forward-delete a text field applies this frame.
type editOp int

const (
	editNone editOp = iota
	editLeft
	editRight
	editHome
	editEnd
	editDelete
	// Word-granular jumps (Ctrl+Left / Ctrl+Right) are ops so textField's one shift-extend rule covers them.
	editWordLeft
	editWordRight
)

// editInput is one frame of edits to a focused text field. selStart/selEnd are a rune range [selStart, selEnd); selEnd
// <= selStart means none.
type editInput struct {
	typed            string // inserted text (typed + pasted)
	back             bool   // backspace (delete the rune before the caret)
	wordBack         bool   // Ctrl+Backspace: delete the preceding word (trailing whitespace + the run before it)
	wordFwd          bool   // Ctrl+Delete: delete the FOLLOWING word (the run after the caret + the whitespace glued to it)
	op               editOp // caret move or forward delete
	selStart, selEnd int    // active selection (rune range); selEnd <= selStart = none
}

// wordDeleteBoundary returns the rune index a Ctrl+Backspace at pos deletes back to: it skips the whitespace run before
// the caret, then the non-whitespace run before that. [result, pos) is removed. Pure and rune-aware; pos <= 0 returns
// 0.
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

// wordForwardBoundary is wordDeleteBoundary's mirror for Ctrl+Delete / Ctrl+Right: the non-whitespace run under the
// caret, then the whitespace after it. [pos, result) is removed for a delete; a jump moves the caret to result.
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

// editStep applies one frame of edits to value at caret (a rune index) and returns the new value and caret. Pure and
// rune-aware, so the edit logic is testable without a renderer. Insert, backspace and delete replace the selection;
// plain Left/Right collapse to its edge; Home/End to the text ends. Shift-extension keeps the anchor outside this
// input; the caller moves only the caret.
func editStep(value string, caret int, in editInput) (string, int) {
	runes := []rune(value)
	caret = hvalue.Clamp(caret, 0, len(runes))
	selA, selB := in.selStart, in.selEnd
	if selA < 0 {
		selA = 0
	}
	if selB > len(runes) {
		selB = len(runes)
	}
	if sel := selB > selA; sel {
		// A word-delete with a selection deletes just the selection, like a plain backspace.
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
			// A word jump over a selection collapses to that edge, then jumps.
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
		// Ctrl+Backspace: delete the preceding whitespace and word run.
		start := wordDeleteBoundary(runes, caret)
		out := make([]rune, 0, len(runes)-(caret-start))
		out = append(out, runes[:start]...)
		out = append(out, runes[caret:]...)
		return string(out), start
	case in.wordFwd && caret < len(runes):
		// Ctrl+Delete: remove [caret, end of next word); the caret stays.
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
	// Out-of-band changes (macro fill, palette template, own-echo IC clear) enter the history here so Ctrl+Z restores
	// them (fieldhistory.go).
	hist := c.fieldTrack(id, value)
	press := c.mouseDown && !c.prevMouseDown
	if press && hover && c.dragID == "" {
		// Focus moves on press so a drag-selection starts immediately; the release block still covers click-away and
		// synthetic clicks.
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
	// The value interior, shrunk from the right by a ReserveFieldTail reservation; consumed here so only one field
	// honours it.
	avail := r.W - 2*padX - c.takeFieldTail(id)
	if avail < 0 {
		avail = 0
	}
	// maskOf maps a value to what is drawn (one '*' per rune when masked); caret math measures the display.
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
		// The caret defaults to the end on a focus change and is clamped every frame: the value can shrink while
		// focused (click-to-pair, macros).
		rc := utf8.RuneCountInString(value)
		if c.caretField != id {
			c.caret = rc
			c.caretField = id
			c.selAnchor = -1 // fresh focus: no selection
		}
		c.caret = hvalue.Clamp(c.caret, 0, rc)
		if c.selAnchor > rc {
			c.selAnchor = rc // value shrank under a live selection
		}
		// Ctrl+A becomes a real whole-value selection.
		if c.selectAll {
			c.selAnchor, c.caret = 0, rc
			c.selectAll = false
		}
		// Mouse: press places the caret and anchors a selection, holding drags the caret end, shift+press extends from
		// the caret. Measured through fieldRaster, the raster the field draws with, so non-Latin text maps clicks to
		// the right rune.
		if (press && hover && c.dragID == "") || (c.dragID == id && c.mouseDown) {
			pre := maskOf(value)
			preRaster := c.fieldRaster(fb, mask, pre)
			// Use the scroll this field was drawn with last frame, never a re-derived one: re-centring the caret moved
			// the text out from under a drag-selection. A field that does not own the sticky scroll was drawn
			// head-aligned, i.e. 0.
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
		// Double-click selects the word under the caret, triple-click the whole value; a masked field has no words.
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
		// Ctrl+Z / Ctrl+Y (routed to undoReq/redoReq pre-screen) step this field's history, consumed even when the
		// stack is empty so the chord never reaches a z/y hotkey.
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
			// Hold-to-clear: the bound key (default Backspace) held past the threshold wipes the field.
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
			// wordBack is gated on the WordDelete pref here, at consumption: with the pref off a focused Ctrl+Backspace
			// is a consumed no-op and cannot reach a Ctrl+Backspace hotkey.
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
			// Ctrl+arrow jumps arrive on their own flags because a Ctrl chord never reaches keyPressed (HandleEvent's
			// ctrl branch returns). A plain arrow this frame wins.
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
					// Shift+arrow/Home/End fixes the anchor and moves only the caret.
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
	// Track the drawn value for the out-of-band detector in both branches; lastCaret is only a restore position.
	hist.lastKnown = value
	if focused {
		hist.lastCaret = c.caret
	}

	display := maskOf(value)
	// The fallback raster the value draws with (nil = single-font path), resolved once so caret x, full width and
	// scroll all use the glyph advances the draw blits.
	fbRaster := c.fieldRaster(fb, mask, display)
	show := display
	col := ColText
	if show == "" && !focused {
		show = placeholder
		col = ColTextDim
	}
	textY := r.Y + (r.H-int32(c.chromeFace().Height()))/2
	// Horizontal scroll keeps the caret visible once the text overflows; unfocused or fitting fields stay head-aligned.
	fullW := c.fieldPrefixW(display, fbRaster, utf8.RuneCountInString(display))
	scroll, caretX := int32(0), int32(0)
	if focused {
		caretX = c.fieldPrefixW(display, fbRaster, c.caret)
		// Sticky: the view moves only when the caret would leave it. Ownership transfers on focus, so one field's
		// scroll is live.
		if c.fieldScrollID != id {
			c.fieldScrollID, c.fieldScroll = id, 0
		}
		c.fieldScroll = scrollKeep(c.fieldScroll, fullW, caretX, avail)
		scroll = c.fieldScroll
	}
	// At a fractional UI scale the focused ASCII field draws selection, value texture and caret device-exact
	// (devFieldValue): under a fractional SetScale the label would resample at a new phase every keystroke and the
	// caret width would flip between 2 and 3 device px. False at 100% and on the fallback-raster, placeholder and
	// unfocused paths.
	devExact := focused && fbRaster == nil && show == display && deviceExactText(c.textDevPct)

	// Selection highlight under the text: the anchor..caret range, measured through the raster the glyphs draw with.
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
		// One SetScale(1,1) bracket for selection, value texture and caret; it skips the scaled selection, value and
		// caret draws below. The caret uses the raw device prefix width (caretXDev) so it lands on the device glyph
		// edge.
		caretXDev := c.caretPixelXDev(show, c.caret)
		c.devFieldValue(r, padX, avail, scroll, caretXDev, textY, show, col, selX0, selX1, hasSel && focused, focused && c.caretOn)
		return value, enter
	}
	// When the field opted in (IC/OOC) and the text has a non-ASCII rune, draw through the per-glyph fallback raster so
	// emoji and non-Latin scripts show glyphs. Plain ASCII stays on the single-font path; caret math is approximate for
	// wide glyphs.
	drawn := false
	if fbRaster != nil && show == display {
		cp, ch := c.pushClip(sdl.Rect{X: r.X + padX, Y: r.Y, W: avail, H: r.H})
		// DrawScaled: fbRaster comes from c.emojiRaster (fieldRaster), which rasterizes its device-sibling faces like
		// the log and showname raster.
		fbRaster.DrawScaled(c.Ren, fbRaster.TotalRunes(), r.X+padX-scroll, r.Y+(r.H-fbRaster.Height())/2, c.RenderScalePct(), &c.clipRect)
		c.popClip(cp, ch)
		drawn = true
	}
	// A non-ASCII placeholder (empty, unfocused) goes through the same raster on an opted-in field (fb.primary != nil),
	// keyed by the dim colour so value and placeholder rasters never collide.
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
			// Clip to the field interior so the scrolled-off head does not spill left.
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

// devFieldValue draws a focused ASCII field's selection, value texture and caret device-exact at a fractional UI scale.
// It reads the renderer's scale, sets SetScale(1,1), draws rects projected by uiDeviceFromLogical (round half up), then
// restores scale and clip (app.go:4366-4378). Render thread, allocation-free: sdl.Rect values and Ctx scratch rects,
// because a &stackRect into a Ren call escapes through cgo. selX0/selX1 are interior-relative logical offsets, already
// clamped; hasSel/caretOn arrive ANDed with focus. caretXDev is the caret's prefix width in raw device px, added to the
// texture's device origin.
func (c *Ctx) devFieldValue(r sdl.Rect, padX, avail, scroll, caretXDev, textY int32, show string, col sdl.Color, selX0, selX1 int32, hasSel, caretOn bool) {
	dev := c.textDevPct
	// The device clip is the field interior in device px. Set the SDL clip directly, not pushClip: the mirror must keep
	// reflecting the ambient logical clip for hovering(). The prior clip comes from the mirror.
	prevClip, prevOn := c.clipRect, c.clipOn
	clipX := uiDeviceFromLogical(r.X+padX, dev)
	clipY := uiDeviceFromLogical(r.Y, dev)
	clipRt := uiDeviceFromLogical(r.X+padX+avail, dev)
	clipBt := uiDeviceFromLogical(r.Y+r.H, dev)

	// Read the renderer's actual scale to restore it, then flip to 1:1.
	sx, sy := c.Ren.GetScale()
	c.cgoRect = sdl.Rect{X: clipX, Y: clipY, W: clipRt - clipX, H: clipBt - clipY}
	_ = c.Ren.SetScale(1, 1)
	_ = c.Ren.SetClipRect(&c.cgoRect)

	// Selection highlight under the text; both edges are projected independently.
	if hasSel && selX1 > selX0 {
		sx0 := uiDeviceFromLogical(r.X+padX+selX0, dev)
		sx1 := uiDeviceFromLogical(r.X+padX+selX1, dev)
		sTop := uiDeviceFromLogical(r.Y+3, dev)     // +3/-6: the selection inset used by the scaled path
		sBot := uiDeviceFromLogical(r.Y+r.H-3, dev) // (r.H-6 tall, 3px from each edge)
		c.Fill(sdl.Rect{X: sx0, Y: sTop, W: sx1 - sx0, H: sBot - sTop},
			sdl.Color{R: ColAccent.R, G: ColAccent.G, B: ColAccent.B, A: 90})
	}

	// ox is the value texture's device origin. The caret uses the same anchor: both are measured on the device face, so
	// caretXDev device px in is the glyph edge. Unit split: the caret uses raw device caretXDev; scroll and selection
	// edges stay on folded-logical widths, where a sub-pixel error only shifts the starting texture column.
	ox := uiDeviceFromLogical(r.X+padX-scroll, dev)

	// Value texture, blitted 1:1 from its device-sized cache entry at the projected origin, so it cannot shimmer as
	// scroll advances.
	if t, ok := c.textTexture(show, col, c.chromeFace()); ok {
		oy := uiDeviceFromLogical(textY, dev)
		c.drawSrc = sdl.Rect{X: t.src.X, Y: t.src.Y, W: t.w, H: t.h}
		c.drawDst = sdl.Rect{X: ox, Y: oy, W: t.w, H: t.h} // device→device 1:1
		_ = c.Ren.Copy(t.tex, &c.drawSrc, &c.drawDst)
	}

	// Caret over the text at ox + caretXDev. Its width is fieldCaretWLog projected round half up, a constant integer
	// per scale. Y/H use the same +4/-8 inset.
	if caretOn {
		cx := ox + caretXDev
		cTop := uiDeviceFromLogical(r.Y+4, dev)
		cBot := uiDeviceFromLogical(r.Y+r.H-4, dev) // r.H-8 tall, 4px from each edge
		cw := uiDeviceFromLogical(fieldCaretWLog, dev)
		c.Fill(sdl.Rect{X: cx, Y: cTop, W: cw, H: cBot - cTop}, ColText)
	}

	// Restore the ambient render scale first (SDL clip rects are in scaled coordinates), then the prior clip as popClip
	// would.
	_ = c.Ren.SetScale(sx, sy)
	if prevOn {
		c.cgoRect = prevClip
		_ = c.Ren.SetClipRect(&c.cgoRect)
	} else {
		_ = c.Ren.SetClipRect(nil)
	}
}

// isASCII reports whether s is all 7-bit ASCII, which keeps a plain field on the single-font path.
func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

// fieldRaster returns the fallback raster a text field draws its value with (nil = single-font path), shared by the
// draw and every caret/scroll measurement.
func (c *Ctx) fieldRaster(fb fieldFonts, mask bool, display string) *render.MessageRaster {
	if fb.primary == nil || mask || display == "" || isASCII(display) {
		return nil
	}
	return c.emojiRaster(display, ColText, fb.primary, fb.emoji)
}

// fieldPrefixW is the logical width of display's first n runes as the field draws them: the raster's advances when one
// is in play, else the chrome font. On the ASCII path at a fractional scale it measures the device face and folds back,
// so caret, scroll and selection agree. At 100% it uses the logical widthCache.
func (c *Ctx) fieldPrefixW(display string, m *render.MessageRaster, n int) int32 {
	if m != nil {
		return m.PrefixWidth(n)
	}
	if deviceExactText(c.textDevPct) {
		return uiLogicalFromDevice(c.caretPixelXDev(display, n), c.textDevPct)
	}
	return c.caretPixelX(display, n)
}

// fieldIndexAtX maps a click x (relative to the text's left edge) to the nearest rune boundary under the draw metrics.
func (c *Ctx) fieldIndexAtX(display string, m *render.MessageRaster, relX int32) int {
	if m == nil {
		if deviceExactText(c.textDevPct) {
			// Hit-test on the same device-face metric as the caret and selection.
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

// fieldCaretMarginPx is how much text scrollKeep keeps visible past the caret when it must move the view; a hard 0
// would re-scroll on every keystroke. Logical px.
const fieldCaretMarginPx = int32(16)

// scrollKeep is the sticky keep-the-caret-visible scroll: it returns cur while the caret is inside the interior,
// otherwise moves the view the smallest distance that brings it back (plus fieldCaretMarginPx). Clamped to [0,
// fullW-avail]. Re-centring each frame moved the pressed glyph mid-click or mid-drag.
func scrollKeep(cur, fullW, caretX, avail int32) int32 {
	if fullW <= avail || avail <= 0 {
		return 0 // it all fits: head-aligned, no scroll state to keep
	}
	maxScroll := fullW - avail
	s := cur
	s = hvalue.Clamp(s, 0, maxScroll)
	// The margin never exceeds half the interior, or the two clamps below would oscillate on a narrow box.
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

// caretIndexAtX returns the rune index nearest pixel relX (relative to the text's left edge), measured on display.
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

// caretPixelXDev is caretPixelX on the device chrome face, in raw device px. The caret draw consumes it directly and
// fieldPrefixW folds it for scroll/selection. Only reached at a fractional scale. Returns early so an empty focused
// field stays zero-alloc.
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

// caretIndexAtXDev is caretIndexAtX on the device-face metric folded to logical: relX is a logical click offset,
// matched against the same folded widths the caret and selection use.
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

// SetHoldClear stamps the hold-to-clear config for the frame; accumulation runs in BeginFrame and the focused field
// clears.
func (c *Ctx) SetHoldClear(on bool, key sdl.Keycode, threshold time.Duration) {
	c.holdOn, c.holdKey, c.holdThreshold = on, key, threshold
}

// SetWordDelete stamps the Ctrl+Backspace word-delete pref for the frame; textField gates the captured chord on it at
// consumption.
func (c *Ctx) SetWordDelete(on bool) {
	c.wordDeleteOn = on
}

// keyHeld reports whether key is physically down via SDL's live keyboard state, correct even after a missed KEYUP.
func (c *Ctx) keyHeld(key sdl.Keycode) bool {
	if key == sdl.K_UNKNOWN {
		return false
	}
	sc := sdl.GetScancodeFromKey(key)
	state := sdl.GetKeyboardState()
	return int(sc) < len(state) && state[sc] != 0
}

// ddDraw is one deferred dropdown overlay: geometry resolves at Dropdown call time, FinishFrame only paints, so the
// list stacks above later widgets.
type ddDraw struct {
	list    sdl.Rect
	options []string
	cur     int
	scroll  int32
	rowH    int32
	thumbW  int32                     // per-row thumbnail strip width (0 = text-only)
	thumb   func(idx int, r sdl.Rect) // paints the idx-th row's thumbnail (deferred, in FinishFrame)
}

// ddMaxVisibleRows caps an open dropdown list's height; longer lists wheel-scroll.
const ddMaxVisibleRows = 12

// Dropdown is a click-to-open selector. Closed, it shows options[cur] and a chevron; open, the list paints above
// everything and the pointer is modally captured. Returns the (possibly new) index and whether it changed.
func (c *Ctx) Dropdown(id string, r sdl.Rect, options []string, cur int) (int, bool) {
	return c.dropdownEx(id, r, options, cur, 0, 0, nil)
}

// DropdownThumbs is Dropdown with a thumbnail strip on each open row: rowHWant is the open-row height, thumbW the strip
// width, and thumb paints the idx-th image in FinishFrame on the render thread, so on-demand cachedPage/Prefetch is
// safe.
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
	// The pick can be server or user text, so it goes through the covering path; plain ASCII keeps the texture blit.
	c.labelCoveringCentered(labelX, r.Y, r.H, r.X+r.W-16-labelX, options[cur], ColText)
	c.Label(r.X+r.W-14, r.Y+(r.H-int32(c.chromeFace().Height()))/2, "▾", ColTextDim)

	if !open {
		if c.hovering(r) && c.clicked {
			c.ddOpen = id
			c.ddScroll = 0
		}
		return cur, false
	}

	// Open geometry: as wide as the widest option, below the control, flipped above at the window bottom, shifted left
	// at the right edge.
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

	// Modal capture: while open the dropdown owns the pointer until close releases it (next BeginFrame), so hovering()
	// is blank for every other widget. The dropdown itself uses raw pointIn().
	c.modalOn = true
	// Publish the open list rect for boxFencesPointer: the list paints above the floating panels, so input must follow
	// while the cursor is over it.
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

	// Interaction resolves now; painting defers. Every branch consumes the click so later widgets cannot react to a
	// click that landed on or dismissed the list.
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
			// Click-away closes and the click is swallowed.
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

// DropdownRowRightClicked reports the row index under the pointer when the open list for id was right-clicked, and
// consumes the click so it cannot leak to a widget under the list. Row hit-testing lives in dropdownEx, so this is the
// only way to get a per-row secondary action. Call it in the same frame, right after Dropdown/DropdownThumbs.
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

// FinishFrame paints deferred overlays (open dropdown lists). Call after every screen draw of the frame. Tooltip arms a
// one-line hover hint for rect r; drawTooltip paints it near the cursor at end of frame. It is skipped while a dropdown
// owns the pointer.
func (c *Ctx) Tooltip(r sdl.Rect, text string) {
	if text != "" && !c.modalOn && c.hovering(r) {
		c.tipText = text
		c.tipAnchor = unionRect(r, c.tipGroup)
	}
}

// TooltipGroup declares the surface a run of child tooltips belongs to, so the hint stays clear of the whole surface.
// Set it before the children and clear it with TooltipGroup(sdl.Rect{}) after.
func (c *Ctx) TooltipGroup(r sdl.Rect) { c.tipGroup = r }

// tooltipDwell is how long the pointer must rest on a TooltipAfter target before its hint shows.
const tooltipDwell = 2 * time.Second

// TooltipAfter is Tooltip with a dwell: the hint shows after the pointer rests on r (keyed by id) for tooltipDwell.
func (c *Ctx) TooltipAfter(id string, r sdl.Rect, text string) {
	c.TooltipAfterDelay(id, r, text, tooltipDwell)
}

// TooltipAfterDelay is TooltipAfter with a caller-chosen dwell. One shared timer (tipHoverID/tipHoverSince) backs both;
// moving off r or to another id resets it.
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

// tipBox places a tooltip of size boxW×boxH near the cursor (mx,my): offset to the lower-right, flipped on overflow,
// then clamped so the whole box stays inside the w×h window. Pure. avoid is the armed tip's anchor (Ctx.tipAnchor).
// tipBox tries the four cursor-relative corners, then two flush placements clear of avoid, and takes the first fully
// on-screen and clear of it. If none qualifies it returns the cursor-relative result, so on-screen always wins. A zero
// avoid gives the cursor-relative placement.
func tipBox(mx, my, boxW, boxH, w, h int32, avoid sdl.Rect) sdl.Rect {
	base := tipBoxDefault(mx, my, boxW, boxH, w, h)
	if avoid.W <= 0 || avoid.H <= 0 {
		return base
	}
	if _, hit := base.Intersect(&avoid); !hit {
		return base
	}
	// Alternatives in order: the other three cursor corners, then flush above and below avoid. The flush ones serve a
	// bottom-corner icon strip, where every cursor-relative corner lands on the strip's neighbours.
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

// tipBoxDefault is the cursor-relative placement: offset to the lower-right, flip on overflow, then clamp.
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

// tipBoxAt clamps one candidate top-left inside the window; every alternative shares it so none can escape the window.
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

// drawTooltip paints the armed hover hint near the cursor. Long text word-wraps to a capped width (cached in
// tipWrapText/W) and the box is clamped on-screen. Called last in the frame.
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
			// Same as the closed control: an open row can show a saved showname or server name, so it must not box.
			c.labelCoveringCentered(labelX, row.Y, d.rowH, row.X+row.W-labelX-6, opt, ColText)
		}
	}
	c.ddDraws = c.ddDraws[:0]
}

// WrapText greedily word-wraps s to maxW pixels in the chrome font, capped at maxLines (the last gains an ellipsis).
// Widths use the TextWidth memo; callers cache the result per string.
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

// VScrollbar draws a proportional vertical scrollbar and returns the scroll offset, updated by thumb drags: pressing
// the track centres the thumb there. content and visible are pixel heights; the result is clamped to [0,
// content-visible]. Draws nothing when everything fits.
func (c *Ctx) VScrollbar(id string, track sdl.Rect, scroll, content, visible int32) int32 {
	// A degenerate panel gives visible <= 0; with empty content maxScroll is positive and thumbH divides by zero. Guard
	// both: nothing scrolls or draws without content or a viewport.
	if content <= 0 || visible <= 0 {
		return 0
	}
	maxScroll := content - visible
	if maxScroll <= 0 {
		return 0
	}
	scroll = hvalue.Clamp(scroll, 0, maxScroll)

	thumbH := track.H * visible / content
	thumbH = min(max(thumbH, scrollThumbMinPx), track.H) // a track shorter than the minimum thumb: the thumb fills it
	span := track.H - thumbH

	grab := sdl.Rect{X: track.X - scrollGrabSlopPx, Y: track.Y, W: track.W + 2*scrollGrabSlopPx, H: track.H}
	if c.mouseDown && (c.dragID == id || (c.dragID == "" && c.hovering(grab))) {
		c.dragID = id
		if span > 0 {
			pos := c.mouseY - track.Y - thumbH/2
			pos = hvalue.Clamp(pos, 0, span)
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

// Slider draws a horizontal value slider over [0, maxVal] and returns the value. Like VScrollbar it is drag-captured by
// id and hover-gated, and mouse-down on the track jumps the thumb. Any int in range. sliderWheelDivisor sets the wheel
// step to 1/100 of the range (at least 1), so percent sliders step by 1.
const sliderWheelDivisor = 100

func (c *Ctx) Slider(id string, track sdl.Rect, value, maxVal int32) int32 {
	if maxVal <= 0 {
		return 0
	}
	value = hvalue.Clamp(value, 0, maxVal)
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
			pos = hvalue.Clamp(pos, 0, span)
			value = pos * maxVal / span
		}
	}
	// Wheel over the track nudges the value by 1/sliderWheelDivisor of the range, at least 1. Claiming the wheel keeps
	// the page beneath from scrolling.
	if !c.wheelTaken && c.wheelY != 0 && c.hovering(grab) {
		c.wheelTaken = true
		step := maxVal / sliderWheelDivisor
		if step < 1 {
			step = 1
		}
		value += c.wheelY * step
		value = hvalue.Clamp(value, 0, maxVal)
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

// HoverPreview tracks dwell time on a widget id and returns true when the full-size preview should show: after the
// hover dwell, or instantly on right-click. The Settings previews toggle gates only the dwell path.
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
		// Dwell disabled: never start a preview from hover. hoverID is left alone so a right-click-opened box keeps its
		// trigger (close-on-leave); each trigger clears its own id when the cursor leaves.
		return false
	}
	if c.hoverID != id {
		c.hoverID = id
		c.hoverSince = time.Now()
		return false
	}
	return time.Since(c.hoverSince) >= c.hoverPreviewDelay
}

// SetHoverPreview stamps the per-frame sprite-preview config (enabled + dwell); App.Frame calls it before any screen.
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
	// Device-scaled sets share c.fontDev for their non-shared faces.
	for _, s := range []*fontSet{&c.chatSetDev, &c.logSetDev} {
		for _, f := range s.fonts {
			if f != c.fontDev && f != nil {
				f.Close()
			}
		}
		s.fonts = nil
	}
	c.dropThemeElemSets() // per-element theme sets (#39), same close-guards
	// Device chrome faces close only when not sharing the logical faces (at 100% they share).
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
