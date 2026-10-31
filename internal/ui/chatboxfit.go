package ui

// Where the showname and message land inside a themed chatbox. One file, because the live chatbox (drawThemedChatBox)
// and the export chatbox (drawGifThemedChatbox) must never drift. The insets are Qt defaults, not theme data.

import (
	"strconv"
	"strings"

	"github.com/SyntaxNyah/AsyncAO/internal/helper/value"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
	"github.com/SyntaxNyah/AsyncAO/internal/ttf"
)

const (
	// themedMessageInsetPx is the inset on all four sides. ui_vp_message is a frameless QTextEdit with both scrollbars
	// off (courtroom.cpp:97-102), so only QTextDocument::documentMargin insets it. Measured 4 against Qt 6.5.3. Not the
	// chatlog's asymmetric inset: that leaves room for a scrollbar track, and the message has none.
	themedMessageInsetPx int32 = 4

	// themedShownameInsetPx is zero deliberately. ui_vp_showname is a QLabel (aotextboxwidgets.h:12), which applies
	// `indent` only with a frame, and AO2 gives it none. AOChatboxLabel's own indent is only in its outlined paintEvent
	// (aotextboxwidgets.cpp:83), a mode we don't implement.
	themedShownameInsetPx int32 = 0
)

// insetChatRect shrinks r by n per side, never to a negative extent, which would give a negative wrap width.
// Not stageframe.go's insetRect, which collapses to the centre: text must still start where the theme put it.
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

// shownameAlign is the theme's `showname_align`, resolved to AO2's three placements (courtroom.cpp:3338-3354).
type shownameAlign uint8

const (
	// shownameAlignLeft is AO2's `else` arm and the zero value, so an App with no theme gets it.
	shownameAlignLeft shownameAlign = iota
	shownameAlignCenter
	shownameAlignRight
)

// shownameAlignKey is the courtroom_design.ini key.
const shownameAlignKey = "showname_align"

// parseShownameAlign maps a raw design value the way AO2 does: lowercased, anything unknown is left, and "justify" is
// centre because AO2 sets Qt::AlignHCenter for it. Runs on the theme-apply goroutine, keeping ToLower off draw paths.
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

// chatboxTextRects resolves a themed chatbox's showname and message into window-absolute rects, with Qt insets applied.
// AO2 parents both to the chatbox (courtroom.cpp:91), so lay's rects are chatbox-relative. Without those keys it falls
// back to the classic overlay inset; fallbackMsgY differs because live and export size their name row differently.
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
	// Fallbacks get the insets too: they stand in for widgets AO2 would still draw through Qt.
	return insetChatRect(name, themedShownameInsetPx), insetChatRect(msg, themedMessageInsetPx)
}

// shownameExtraWidthKey is the courtroom_design.ini key for how many design pixels the showname box grows per step.
// 0 or absent turns off widening, as in AO2.
const shownameExtraWidthKey = "showname_extra_width"

// parseShownameExtraWidth reads the key like AO2's QString::toInt(), tested with `extra_width > 0`
// (courtroom.cpp:3357): garbage is 0, which disables widening rather than aborting the chatbox.
func parseShownameExtraWidth(raw string) int {
	v, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return 0
	}
	return v
}

// chatboxRung is one step of AO2's widen-and-swap, its only answer to a showname too wide for the box: the box widens
// and the art swaps to a wider plate. Past the last step the text is cut off (courtroom.cpp:3356-3378).
type chatboxRung uint8

const (
	// chatboxRungBase is the ordinary skin and the zero value; every path that can't widen returns it.
	chatboxRungBase chatboxRung = iota
	chatboxRungMed
	chatboxRungBig
	// chatboxRungBlank is not part of widening: it is the plate-less narration box (chatboxSkinForShowname),
	// on this enum because the same loader resolves it.
	chatboxRungBlank
)

// themeStemChatboxBlank is the blank rung's T1 stem.
const themeStemChatboxBlank = "chatboxblank"

// themeChatBlankFileStem is AO2's fixed file stem for the plate-less chatbox (courtroom.cpp:3322), not derived from the
// base skin.
const themeChatBlankFileStem = "chatblank"

// texStem is the rung's T1 stem.
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

// fileStem is the rung's file stem on disk given the base stem the theme shipped. The base varies: chat, chatbox or
// chatblank (courtroom.cpp:3327-3331). AO2 appends to the resolved image's path, and hardcoding "chatmed" would skip
// others.
func (r chatboxRung) fileStem(base string) string {
	switch r {
	case chatboxRungMed:
		return base + chatboxMedSuffix
	case chatboxRungBig:
		return base + chatboxBigSuffix
	case chatboxRungBlank:
		if base == themeChatBlankFileStem {
			// The base already is chatblank, and pinning the same art twice wastes a page: decline, so
			// chatboxSkinForShowname uses the base page. theme.FindAssetIn refuses the empty stem.
			return ""
		}
		return themeChatBlankFileStem
	}
	return base
}

// chatboxLadderRungs is every chatbox variant loaded from the base skin's directory, in AO2's order.
// A fixed array, so the load is bounded (hard rule 4).
var chatboxLadderRungs = [...]chatboxRung{chatboxRungMed, chatboxRungBig, chatboxRungBlank}

// chatboxSkinForShowname is AO2's per-message choice: a blank showname gets the plate-less skin
// (courtroom.cpp:3320-3331). Returns the page and whether blank was chosen; with no separate blank skin it returns the
// base page and false. TrimSpace cuts Unicode space, while QString::trimmed() cuts ASCII only, so U+3000 reads blank
// here and not in AO2. That is the safe direction, since the plate is empty either way.
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

// shownameLadder is courtroom.cpp:3356-3378 as arithmetic: which skin to draw and how wide the showname box becomes.
// AO2's resize-back `else` arms are omitted: our box is recomputed from the design rect every frame.
// Big is tested only inside med, as in AO2, so a theme with big but no med never widens.
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

// chatboxSkinLadder returns the showname box and chatbox page for the active theme's resident art.
// It never touches network or disk: variants are pinned per theme apply and absent ones are left out of themeTex,
// so "no med" costs one map read. Checks run by cost: extra_width, a map probe, and only then the text measure.
func (a *App) chatboxSkinLadder(nameBox sdl.Rect, lay *themeLayoutCache, base *render.TexturePage,
	primary, emoji *ttf.Font, text string, col sdl.Color,
) (sdl.Rect, *render.TexturePage) {
	extra := lay.shownameExtraPx()
	// base == nil is the flat-panel chatbox; the loader can only find a variant if a base resolved.
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

// shownameSpan places the showname inside its box per showname_align, returning the draw origin and usable width.
// Deliberate divergence from Qt: its offset goes negative for an oversized name and clips the head, while ours floors
// at zero and clips the tail, since missing first letters are worse. Only a name too long for the widest skin gets
// here.
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

// AO2 multiplies design rects and courtroom_fonts.ini point sizes by the same themeScalingFactor()
// (text_file_functions.cpp:236-239, courtroom.cpp:1217), so `rect width / font size` is an invariant of the theme art.
// We scale rects by the window fit, so the canvas scale must be folded into the font to keep it.
// Only the chatbox pair folds: other panels carry the user's Ctrl+wheel zoom and rewrap through caches keyed on it.
// At a 1:1 canvas (the ThemeFitNative default) the fold is the identity.

// themedCanvasFontMaxPct bounds the chatbox font after the fold. A memory bound (hard rules 4 and 9): the canvas scale
// has no upper limit, and an unbounded size means an unbounded glyph atlas and message raster.
// 1600% never binds in aspect-preserving modes on real themes; it catches ThemeFitCustom's manual zoom.
const themedCanvasFontMaxPct = 1600

// canvasTextScalePct rounds a canvas's scale to a percent for the font fold, using emoteCellScale because AO2 has one
// scaling factor. Only ThemeFitStretch differs per axis, and the looser axis would size text past its box.
func canvasTextScalePct(scaleX, scaleY float64) int {
	pct := int(emoteCellScale(scaleX, scaleY)*float64(DefaultScalePct) + 0.5)
	if pct < 1 {
		pct = 1 // a canvas scaled to nothing still has to name a font size
	}
	return pct
}

// foldCanvasFontPct folds a canvas scale into an already-resolved element scale, in AO2's order. The theme clamp in
// elemPct rejects sloppy theme data; the canvas scale is not theme data, so it multiplies outside and gets its own cap.
func foldCanvasFontPct(pct, canvasPct, devPct int) int {
	if canvasPct == DefaultScalePct || canvasPct <= 0 {
		return pct // 1:1 canvas (the Native default): identity, not arithmetic
	}
	return canonFontPct(value.Clamp(pct*canvasPct/DefaultScalePct, themeFontMinPct, themedCanvasFontMaxPct), devPct)
}

// canonFontPct snaps a scale to the smallest percent opening the same face size. A fontSet rebuild purges every label
// texture and the text atlas, and the percent now moves with the window, so a drag-resize would rebuild every frame;
// this collapses it to one rebuild per distinct size. devPct is Ctx.textDevPct: buildSet folds it into the size too,
// so the snap uses device size, or a high-DPI face lands a pixel under ideal.
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
	// Ceil: truncating would land one pixel short of `size`.
	return (size*DefaultScalePct*DefaultScalePct + den - 1) / den
}

// themedChatPct is element el's draw scale in a themed chatbox: theme point size, user zoom, then canvas scale.
func (a *App) themedChatPct(el themeFontElem, userPct int, lay *themeLayoutCache) int {
	return foldCanvasFontPct(a.elemPct(el, userPct), lay.textScalePct(), int(a.ctx.textDevPct))
}

// themedChatFace resolves a themed-chatbox child's text and emoji faces from one scale, since they must open at the
// same percent or the baselines part.
func (a *App) themedChatFace(el themeFontElem, userPct int, lay *themeLayoutCache, text string) (primary, emoji *ttf.Font) {
	pct := a.themedChatPct(el, userPct, lay)
	return a.elemFontAtPct(el, pct, text), a.ctx.EmojiFont(pct)
}

// shownameSpanFor is shownameSpan bound to the active theme. The left short-circuit keeps the default path from
// measuring text at all; centre and right cost one cache probe per frame.
func (a *App) shownameSpanFor(box sdl.Rect, primary, emoji *ttf.Font, text string, col sdl.Color) (x, maxW int32) {
	if a.themeShownameAlign == shownameAlignLeft {
		return box.X, box.W
	}
	return shownameSpan(box, a.labelEmojiWidth(primary, emoji, text, col), a.themeShownameAlign)
}
