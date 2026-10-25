package ui

// Where the showname and the message actually land INSIDE a themed chatbox, and
// how the showname is placed across its own box.
// This lives in one file because the geometry has two consumers that must never
// drift: the live themed chatbox (drawThemedChatBox, theme_layout.go) and the
// video / comic export's themed chatbox (drawGifThemedChatbox, gifexport.go).

import (
	"strconv"
	"strings"

	"github.com/veandco/go-sdl2/sdl"
	"github.com/veandco/go-sdl2/ttf"

	"github.com/SyntaxNyah/AsyncAO/internal/render"
)

const (
	// themedMessageInsetPx is the inset AO2's chatbox message gets for free, on
	// ALL FOUR sides.
	// This is deliberately NOT the chatlog's asymmetric inset: that one leaves the
	// RIGHT edge alone because a scrollbar track lives there, and the message has
	// no scrollbar at all.
	themedMessageInsetPx int32 = 4

	// themedShownameInsetPx is ZERO, and that is a finding rather than an
	// oversight — giving the showname the message's 4 px would be a divergence.
	// AOChatboxLabel does own an indent, but exclusively inside its OUTLINED
	// paintEvent (aotextboxwidgets.cpp:83), a mode AsyncAO does not implement.
	themedShownameInsetPx int32 = 0
)

// insetChatRect shrinks r by n on every side, which is what a Qt document margin
// does. It never returns a negative extent: a theme whose message rect is
// narrower than 2×the inset (none in the reference corpus, but theme data is
// user data) would otherwise produce a negative wrap width and a raster the
// text layout cannot fill.
func insetChatRect(r sdl.Rect, n int32) sdl.Rect {
	r.X += n
	r.Y += n
	if r.W -= 2 * n; r.W < 0 {
		r.W = 0
	}
	if r.H -= 2 * n; r.H < 0 {
		r.H = 0
	}
	return r
}

// shownameAlign is the theme's `showname_align`, resolved to the three
// placements AO2 can actually produce (AO2-Client courtroom.cpp:3338-3354).
type shownameAlign uint8

const (
	// shownameAlignLeft is AO2's `else` arm and therefore the value a theme that
	// says nothing gets. It is also the zero value on purpose: an App with no
	// theme applied behaves exactly as it did before this existed.
	shownameAlignLeft shownameAlign = iota
	shownameAlignCenter
	shownameAlignRight
)

// shownameAlignKey is the courtroom_design.ini key. Declared by 66 of the 97
// design files in the 74-theme reference corpus (50 center, 16 left) and, until
// now, read by nothing.
const shownameAlignKey = "showname_align"

// parseShownameAlign maps a raw design value onto shownameAlign.
// The corpus ships only `center` and `left`, so `right` and `justify` are
// carried purely so a theme that does declare one is not silently mis-drawn.
func parseShownameAlign(raw string) shownameAlign {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "right":
		return shownameAlignRight
	case "center", "justify":
		return shownameAlignCenter
	default:
		return shownameAlignLeft
	}
}

// chatboxTextRects resolves ONE themed chatbox's two text children into
// window-absolute rects: the showname box and the message box, the latter
// already carrying its Qt document margin.
func chatboxTextRects(box sdl.Rect, lay *themeLayoutCache, fallbackMsgY int32) (name, msg sdl.Rect) {
	name = sdl.Rect{
		X: box.X + chatOverlayPadX,
		Y: box.Y + chatOverlayNameY,
		W: box.W - 2*chatOverlayPadX,
		H: chatBoxTopStrip - chatOverlayNameY,
	}
	if r, ok := lay.rect("showname"); ok {
		name = sdl.Rect{X: box.X + r.X, Y: box.Y + r.Y, W: r.W, H: r.H}
	}
	msg = sdl.Rect{
		X: box.X + chatOverlayPadX,
		Y: fallbackMsgY,
		W: box.W - 2*chatOverlayPadX,
		H: box.Y + box.H - fallbackMsgY,
	}
	if r, ok := lay.rect("message"); ok {
		msg = sdl.Rect{X: box.X + r.X, Y: box.Y + r.Y, W: r.W, H: r.H}
	}
	// The inset is applied to the FALLBACK rects too. That is not an accident:
	return insetChatRect(name, themedShownameInsetPx), insetChatRect(msg, themedMessageInsetPx)
}

// ---------------------------------------------------------------------------
// AO2's showname widen-and-swap ladder.
// ---------------------------------------------------------------------------

// shownameExtraWidthKey is the courtroom_design.ini key that drives the ladder:
// how many DESIGN pixels the showname box grows by at each rung. Declared by 65
// of the 74 reference themes (32 of them 24, 14 of them 10, four 48, and one —
// P5Theme — 225); the other nine declare 0 or omit it, which switches the whole
// mechanism off exactly as it does in AO2.
const shownameExtraWidthKey = "showname_extra_width"

// parseShownameExtraWidth reads that key the way AO2 reads it: as a bare
// QString::toInt(), which yields 0 for a blank, absent or unparseable value and
// is then tested with `extra_width > 0` (AO2-Client courtroom.cpp:3357, :3374).
func parseShownameExtraWidth(raw string) int {
	v, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return 0
	}
	return v
}

// chatboxRung names one step of AO2's widen-and-swap ladder — which chatbox
// skin is showing, and therefore how much room the showname has.
// The ladder is AO2's ONLY answer to a showname too wide for the box the theme
// drew. There is no font shrinking and no eliding: the box widens, the chatbox
// art swaps to a variant the theme author drew with a wider name plate, and past
type chatboxRung uint8

const (
	// chatboxRungBase is the theme's ordinary skin, and the zero value: every
	// path that cannot or must not widen returns it.
	chatboxRungBase chatboxRung = iota
	chatboxRungMed
	chatboxRungBig
	// chatboxRungBlank is NOT part of the widen-and-swap ladder — it is AO2's
	// OTHER per-message skin decision, the plate-less box a narration post gets
	// (see chatboxSkinForShowname). It rides the same enum because it is resolved
	// by the same loader, out of the same directory, into the same pinned tier.
	chatboxRungBlank
)

// themeStemChatboxBlank is the blank rung's T1 stem. Declared here rather than
// beside themeStemChatboxMed / themeStemChatboxBig because the SELECTION that
// uses it lives in this file; the two widening stems are named where the widening
// ladder's art is described.
const themeStemChatboxBlank = "chatboxblank"

// themeChatBlankFileStem is AO2's literal file stem for the plate-less chatbox
// (AO2-Client courtroom.cpp:3322, `setImage("chatblank", p_misc)`). It is a FIXED
// name, unlike med/big: AO2 does not derive it from the resolved base skin's path,
// it names it outright — which is why fileStem below ignores its argument for this
// rung except to notice that the base skin already IS this file.
const themeChatBlankFileStem = "chatblank"

// texStem is the rung's T1 stem — the key its art is pinned under.
func (r chatboxRung) texStem() string {
	switch r {
	case chatboxRungMed:
		return themeStemChatboxMed
	case chatboxRungBig:
		return themeStemChatboxBig
	case chatboxRungBlank:
		return themeStemChatboxBlank
	}
	return themeStemChatbox
}

// fileStem is the rung's file stem on disk, given the base skin stem the theme
// actually shipped.
// The base is NOT a constant: AsyncAO probes chat → chatbox → chatblank
// (AO2-Client courtroom.cpp:3327-3331), 64 of the 74 reference themes ship
// `chat`, P5Theme ships `chatbox`, and two ship only `chatblank`. AO2 sidesteps
func (r chatboxRung) fileStem(base string) string {
	switch r {
	case chatboxRungMed:
		return base + chatboxMedSuffix
	case chatboxRungBig:
		return base + chatboxBigSuffix
	case chatboxRungBlank:
		if base == themeChatBlankFileStem {
			// The theme's BASE skin already resolved to chatblank (two of the 74
			// reference themes ship only that file). There is no second, distinct
			// blank skin to pin, and pinning the same art twice would double a
			// pinned page for no visual difference — so this rung declines, and
			// chatboxSkinForShowname falls through to the base page, which IS the
			// blank plate. theme.FindAssetIn refuses the empty stem outright.
			return ""
		}
		return themeChatBlankFileStem
	}
	return base
}

// chatboxLadderRungs is every chatbox VARIANT the loader resolves out of the base
// skin's own directory, in the order AO2 reaches for them: the widen-and-swap
// ladder's med and big, then the plate-less blank. A fixed array, so the load is
// bounded by construction (hard rule 4).
var chatboxLadderRungs = [...]chatboxRung{chatboxRungMed, chatboxRungBig, chatboxRungBlank}

// chatboxSkinForShowname is AO2's per-MESSAGE chatbox choice: a post with a blank
// showname gets the plate-less skin, everything else gets the ordinary one.
// Returns the page to blit and whether the blank variant was chosen. A theme that
// ships no separate blank skin — most of them — returns its base page and false,
// which is byte-identical to the pre-fix draw.
func (a *App) chatboxSkinForShowname(showname string, base *render.TexturePage) (*render.TexturePage, bool) {
	if strings.TrimSpace(showname) != "" {
		return base, false
	}
	blank, ok := a.themePage(themeStemChatboxBlank)
	if !ok {
		return base, false
	}
	return blank, true
}

// shownameLadder is AO2's ladder as pure arithmetic: given the showname box the
// theme declared, how wide the name actually measures, the per-rung extra, and
// which variant skins the theme shipped, it answers which skin to draw and how
// wide the showname box becomes.
// The med rung is checked before big and big only from inside it, which matters:
func shownameLadder(boxW, textW, extra int32, hasMed, hasBig bool) (chatboxRung, int32) {
	if extra <= 0 || textW <= boxW || !hasMed {
		return chatboxRungBase, boxW
	}
	w := boxW + extra
	if textW > w && hasBig {
		return chatboxRungBig, boxW + 2*extra
	}
	return chatboxRungMed, w
}

// chatboxSkinLadder runs the ladder against the ACTIVE theme's resident art and
// returns the showname box to place text in plus the chatbox page to blit.
// The gates are ordered by cost on purpose: an integer test on the theme's
// extra_width, then a map probe for the med skin, and only then the text
// measure. Left-aligned shownames still skip measuring entirely unless the
func (a *App) chatboxSkinLadder(nameBox sdl.Rect, lay *themeLayoutCache, base *render.TexturePage,
	primary, emoji *ttf.Font, text string, col sdl.Color,
) (sdl.Rect, *render.TexturePage) {
	extra := lay.shownameExtraPx()
	// base == nil is the flat-panel chatbox. AO2 only reaches the widen branch
	// after a base skin resolved (it derives the variants' paths from that skin's
	// own path), and our loader can only have found a variant if a base resolved,
	// so this arm is belt and braces on a state the loader cannot produce.
	if extra <= 0 || base == nil || text == "" {
		return nameBox, base
	}
	med, hasMed := a.themePage(chatboxRungMed.texStem())
	if !hasMed {
		return nameBox, base
	}
	big, hasBig := a.themePage(chatboxRungBig.texStem())
	rung, w := shownameLadder(nameBox.W, a.labelEmojiWidth(primary, emoji, text, col), extra, hasMed, hasBig)
	nameBox.W = w
	switch rung {
	case chatboxRungMed:
		return nameBox, med
	case chatboxRungBig:
		return nameBox, big
	}
	return nameBox, base
}

// shownameSpan places the showname's text horizontally inside its box per
// showname_align, returning the draw origin and the width the label may use.
// ONE deliberate divergence, for a name too wide for its box. Qt's offset goes
// Left alignment returns the box unchanged WITHOUT measuring anything, so the
// default path — and every one of the 16 corpus design files that ask for
// `left` — is byte-identical to the pre-alignment code and costs nothing per
func shownameSpan(box sdl.Rect, textW int32, al shownameAlign) (x, maxW int32) {
	off := int32(0)
	switch al {
	case shownameAlignCenter:
		off = (box.W - textW) / 2
	case shownameAlignRight:
		off = box.W - textW
	}
	if off < 0 {
		off = 0
	}
	return box.X + off, box.W - off
}

// ---------------------------------------------------------------------------
// Text-to-box scale parity: folding the canvas scale into the chatbox font.
// ---------------------------------------------------------------------------

// AO2 multiplies design RECTS and courtroom_fonts.ini POINT SIZES by the same
// Options::themeScalingFactor() — rects in AOApplication::get_element_dimensions
// (text_file_functions.cpp:236-239), fonts in Courtroom::set_font
// (courtroom.cpp:1217). So `rect width ÷ font size` is an invariant the theme
// author fixed when they drew the art, and AO2 preserves it at any scaling
// factor. AsyncAO scaled the rects by the window-fit scale and left the theme's
// point size alone, so the invariant broke by exactly that factor: too little
// room and the message overruns the frame it was drawn for, too much and the
// text is lost in the middle of a box far bigger than it needs.

// themedCanvasFontMaxPct bounds the chatbox font AFTER the canvas fold. It is a
// memory bound, not a typographic one: the fold multiplies a point size that is
// already clamped to themeFontMaxPct by a canvas scale with no upper limit of
// its own, and an unbounded font size means an unbounded glyph atlas and an
// unbounded message-raster texture. This client has already shipped one bug
// where oversized text exhausted the GPU budget and every icon collapsed to a
// shared fallback, so the product of two clamped-looking numbers gets its own
// clamp (hard rules 4 and 9).
const themedCanvasFontMaxPct = 1600

// canvasTextScalePct rounds a themed canvas's scale to an integer percent for
// the chatbox font fold.
func canvasTextScalePct(scaleX, scaleY float64) int {
	pct := int(emoteCellScale(scaleX, scaleY)*float64(DefaultScalePct) + 0.5)
	if pct < 1 {
		pct = 1 // a canvas scaled to nothing still has to name a font size
	}
	return pct
}

// foldCanvasFontPct folds a canvas scale into an ALREADY-RESOLVED element scale.
// The order matters and is the order AO2 uses. elemPct has already clamped the
// The canvas scale is not theme data and must not be judged by that clamp, so it
// multiplies OUTSIDE it and the product gets its own cap.
func foldCanvasFontPct(pct, canvasPct, devPct int) int {
	if canvasPct == DefaultScalePct || canvasPct <= 0 {
		return pct // 1:1 canvas (the Native default): identity, not arithmetic
	}
	return canonFontPct(clampInt(pct*canvasPct/DefaultScalePct, themeFontMinPct, themedCanvasFontMaxPct), devPct)
}

// canonFontPct snaps a scale to the SMALLEST percent that opens the very same
// face size, and it is what keeps a drag-resize from melting.
// Without this, dragging a window across 600 px would ask for ~600 distinct
// percents and pay ~600 full atlas teardowns for maybe 40 distinct font sizes.
// devPct is Ctx.textDevPct: buildSet folds it into the size too (#77), so the
// canonical percent is computed against the DEVICE size. Doing it against the
// logical one would let a high-DPI device face land a pixel under its ideal
// size, which is precisely the crispness #77 went and fixed.
func canonFontPct(pct, devPct int) int {
	if devPct <= 0 {
		devPct = DefaultScalePct // a Ctx that never had a device scale set
	}
	// buildSet: size = UIFontSize * pct * devPct / (DefaultScalePct ^ 2).
	den := UIFontSize * devPct
	size := pct * den / (DefaultScalePct * DefaultScalePct)
	if size < 1 {
		size = 1 // buildSet's own floor; keep the inverse on the same branch
	}
	// The smallest percent that still reaches `size`: ceil, because truncating
	// would land one pixel short and defeat the whole point.
	return (size*DefaultScalePct*DefaultScalePct + den - 1) / den
}

// themedChatPct is element el's draw scale inside a THEMED chatbox: the theme's
// own point size, the user's zoom knob, and the canvas scale, in that order.
func (a *App) themedChatPct(el themeFontElem, userPct int, lay *themeLayoutCache) int {
	return foldCanvasFontPct(a.elemPct(el, userPct), lay.textScalePct(), int(a.ctx.textDevPct))
}

// themedChatFace resolves one themed-chatbox text child's pair of faces at the
// folded scale: the covering face for the text itself and the colour-emoji face
// that draws alongside it.
func (a *App) themedChatFace(el themeFontElem, userPct int, lay *themeLayoutCache, text string) (primary, emoji *ttf.Font) {
	pct := a.themedChatPct(el, userPct, lay)
	return a.elemFontAtPct(el, pct, text), a.ctx.EmojiFont(pct)
}

// shownameSpanFor is shownameSpan bound to the active theme's alignment and to
// the face the name will actually be drawn in.
// The LEFT short-circuit is load-bearing, not an optimisation flourish: it is
// what keeps the default and every `left` theme on a per-frame path that never
// measures text at all, byte-identical to the code before alignment existed.
func (a *App) shownameSpanFor(box sdl.Rect, primary, emoji *ttf.Font, text string, col sdl.Color) (x, maxW int32) {
	if a.skin.themeShownameAlign == shownameAlignLeft {
		return box.X, box.W
	}
	return shownameSpan(box, a.labelEmojiWidth(primary, emoji, text, col), a.skin.themeShownameAlign)
}
