package ui

// Where the showname and the message actually land INSIDE a themed chatbox, and
// how the showname is placed across its own box.
//
// This lives in one file because the geometry has two consumers that must never
// drift: the live themed chatbox (drawThemedChatBox, theme_layout.go) and the
// video / comic export's themed chatbox (drawGifThemedChatbox, gifexport.go).
// Both used to resolve the same two rects with their own copy of the same
// arithmetic, so a fix to one silently diverged the export from what the player
// had been looking at. There is now exactly one resolution and both call it.
//
// The numbers here are Qt DEFAULTS, not theme data. A corpus-wide grep over 74
// real AO2 themes finds no theme declaring padding or margin on the showname or
// the message; the only `padding` rule any of them ships is on the clock label.
// A themed chatbox that looks unpadded is therefore never a dropped theme value;
// it is Qt's own insets never having been reproduced — see the two constants
// below, both measured against a real Qt 6.5.3 build rather than reasoned about.

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
	//
	// ui_vp_message is a frameless QTextEdit with BOTH scrollbar policies forced
	// to ScrollBarAlwaysOff (AO2-Client courtroom.cpp:97-102), so nothing insets
	// its text except Qt's own QTextDocument::documentMargin — whose default is 4.
	// The stock AO2 theme documents the consequence in a comment above its own
	// `message` key.
	//
	// Measured, not assumed. A probe built against the Qt 6.5.3 kit on this box,
	// constructing the widget exactly the way courtroom.cpp does and sizing it to
	// the stock theme's `message = 10, 13, 242, 57`, reports
	// documentMargin = 4.0, a text cursor at (4, 4) for document position 0, and
	// a first-block bounding rect of (4, 4, 234x14) — i.e. precisely
	// X+4, Y+4, W-8, H-8.
	//
	// This is deliberately NOT the chatlog's asymmetric inset: that one leaves the
	// RIGHT edge alone because a scrollbar track lives there, and the message has
	// no scrollbar at all.
	themedMessageInsetPx int32 = 4

	// themedShownameInsetPx is ZERO, and that is a finding rather than an
	// oversight — giving the showname the message's 4 px would be a divergence.
	//
	// ui_vp_showname is an AOChatboxLabel, i.e. a QLabel (AO2-Client
	// aotextboxwidgets.h:12). A QLabel applies its `indent` only when it has a
	// frame — QLabelPrivate::documentRect takes the indent branch on
	// `m < 0 && frameWidth()` — and AO2 never gives this one a frame or a margin.
	// The same Qt 6.5.3 probe reports indent = -1, margin = 0, frameWidth = 0, and
	// paints AlignRight text flush against the right edge of the widget rect.
	//
	// AOChatboxLabel does own an indent, but exclusively inside its OUTLINED
	// paintEvent (aotextboxwidgets.cpp:83), a mode AsyncAO does not implement.
	themedShownameInsetPx int32 = 0
)

// insetChatRect shrinks r by n on every side, which is what a Qt document margin
// does. It never returns a negative extent: a theme whose message rect is
// narrower than 2×the inset (none in the reference corpus, but theme data is
// user data) would otherwise produce a negative wrap width and a raster the
// text layout cannot fill.
//
// NOT stageframe.go's insetRect, whose degenerate case collapses the rect to its
// CENTRE POINT. That is right for a decorative border and wrong here: a chatbox
// too small for its margin must keep its origin so the text still starts where
// the theme put it, merely with no room left.
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
//
// AO2 lowercases the value and tests it against three literals; ANY other
// string — including the empty one a missing key yields — falls through to
// AlignLeft. "justify" deliberately maps to CENTRE: Qt has no single-line
// justification to apply, and AO2 spells that out by handling `justify` with its
// own branch that sets Qt::AlignHCenter, identical to `center`.
//
// The corpus ships only `center` and `left`, so `right` and `justify` are
// carried purely so a theme that does declare one is not silently mis-drawn.
// Runs on the theme-apply goroutine, once per theme, so the ToLower allocation
// is off every draw path (hard rule 2).
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
//
// box is the scaled, window-absolute ao2_chatbox rect. lay supplies the two
// CHATBOX-RELATIVE child rects the theme declared (AO2 parents both widgets to
// the chatbox — courtroom.cpp:91/:3300-3302 — which is why theme_layout.go
// deliberately leaves these two un-offset). A theme that declares an
// ao2_chatbox but no showname / message child falls back to the classic
// overlay's own inset, which is what both call sites already did; fallbackMsgY
// is that fallback's message row, because the live chatbox and an export frame
// size their name row differently.
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
	// the fallback stands in for a widget AO2 would still have drawn through a
	// QTextEdit / QLabel, so it should carry the same Qt defaults. In practice it
	// is unreachable for the message — every one of the 74 corpus themes declares
	// both `ao2_chatbox` and `message` — so this only ever matters for hand-written
	// or truncated theme data.
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
// So garbage disables the ladder rather than aborting the chatbox — the same
// tolerance internal/theme's atoiTrim documents for every other design number.
//
// Runs on the theme-apply goroutine, once per theme (hard rule 2).
func parseShownameExtraWidth(raw string) int {
	v, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return 0
	}
	return v
}

// chatboxRung names one step of AO2's widen-and-swap ladder — which chatbox
// skin is showing, and therefore how much room the showname has.
//
// The ladder is AO2's ONLY answer to a showname too wide for the box the theme
// drew. There is no font shrinking and no eliding: the box widens, the chatbox
// art swaps to a variant the theme author drew with a wider name plate, and past
// the last rung the text is simply cut off (there is no fourth branch in
// courtroom.cpp:3356-3378). A theme that ships no variants gets no widening at
// all — documented AO2 behaviour, not a gap.
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
//
// The base is NOT a constant: AsyncAO probes chat → chatbox → chatblank
// (AO2-Client courtroom.cpp:3327-3331), 64 of the 74 reference themes ship
// `chat`, P5Theme ships `chatbox`, and two ship only `chatblank`. AO2 sidesteps
// the question by string-appending to the resolved image's own path, which is
// what this reproduces — hardcoding "chatmed" would silently skip the ladder on
// every theme that spells its base differently.
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
//
// It is deliberately ONE array even though the blank rung is not part of the
// widening ladder: what the array actually names is "extra chatbox art pinned per
// theme apply, located relative to the resolved base skin", and every rung in it
// is loaded, budgeted and evicted identically. Splitting it would mean a second
// loop over the same directory with the same body.
var chatboxLadderRungs = [...]chatboxRung{chatboxRungMed, chatboxRungBig, chatboxRungBlank}

// chatboxSkinForShowname is AO2's per-MESSAGE chatbox choice: a post with a blank
// showname gets the plate-less skin, everything else gets the ordinary one.
//
// AO2-Client courtroom.cpp:3320-3331, inside the same set_chatbox pass that places
// the widgets:
//
//	if (ui_vp_showname->text().trimmed().isEmpty())   // Whitespace showname
//	  ui_vp_chatbox->setImage("chatblank", p_misc);
//	else { ui_vp_showname->setVisible(true);
//	       if (!ui_vp_chatbox->setImage("chat", p_misc)) ui_vp_chatbox->setImage("chatbox", p_misc); }
//
// Two things that branch decides, and BOTH are per message rather than per theme:
// which art the box wears, and whether the showname label is shown at all. AsyncAO
// resolved the skin once per theme apply, so a narration post — the AO idiom of
// posting with the showname cleared — kept drawing the ordinary skin's empty name
// plate under nothing (the reported "narration bug"). It also drew the showname
// label, which is harmless while the text is empty but is the same decision and is
// answered by the same return.
//
// TRIMMED, like AO2: a showname of spaces is a blank showname. The trim is
// strings.TrimSpace, whose cut set is Unicode space — QString::trimmed() cuts
// ASCII whitespace only, so a name of U+3000 IDEOGRAPHIC SPACE reads blank here and
// not in AO2. That is the safe direction (the plate is empty either way) and it
// keeps one definition of "blank showname" across the client.
//
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
//
// It is courtroom.cpp:3356-3378 line for line, with one thing deliberately left
// out. AO2's `else` arms call resize(default_width.width, ...) because its label
// is a persistent widget that may still be carrying the PREVIOUS message's
// widened size; ours is recomputed from the design rect every frame, so there is
// nothing to reset and the base rung simply returns the box it was handed.
//
// The med rung is checked before big and big only from inside it, which matters:
// a theme with big but no med never widens, because AO2 nests the second test
// inside the first. That is not a corpus concern (all 60 themes with big also
// ship med) but it is the contract.
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
//
// THE RESIDENCY CONTRACT, which is what makes this affordable in a zero-fallback
// streaming client: nothing here can touch the network or the disk. The two
// variant skins are loaded and pinned once per theme apply, on the theme
// goroutine, from the directory the base skin came out of (applyThemeAsync); a
// theme that ships neither leaves both stems out of themeTex, and themePage's
// first line is a themeTex map probe. So the "this theme has no med" answer
// costs one map read per frame, forever, with no probe of any kind — and the 14
// reference themes in that position, P5Theme among them, never even measure the
// name.
//
// The gates are ordered by cost on purpose: an integer test on the theme's
// extra_width, then a map probe for the med skin, and only then the text
// measure. Left-aligned shownames still skip measuring entirely unless the
// theme actually shipped a ladder (shownameSpanFor), so the common case is
// unchanged.
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
//
// AO2 gets this from Qt: the alignment flag goes to QPainter::drawText, which
// offsets the run by (boxW - textW) for AlignRight and half that for
// AlignHCenter. We reproduce the offset directly because our label draws from an
// (x, maxW) window rather than into a widget.
//
// ONE deliberate divergence, for a name too wide for its box. Qt's offset goes
// NEGATIVE there, so AO2 starts the text left of the widget and the widget clips
// the name's HEAD. Our clip window cannot express that, and a showname whose
// first letters are missing is worse than one whose last letters are: the offset
// floors at zero, so an oversized name always starts at the left edge and clips
// its tail — which is exactly what every AsyncAO build has always done, for
// every alignment.
//
// It is also the LAST resort, not the first: AO2's own answer to an oversized
// name is the widen-and-swap ladder above, which runs before this and hands it a
// box already widened by up to two rungs. Only a name too long for the biggest
// skin the theme shipped — the point where AO2 cuts the text off too — reaches
// the floor here.
//
// Left alignment returns the box unchanged WITHOUT measuring anything, so the
// default path — and every one of the 16 corpus design files that ask for
// `left` — is byte-identical to the pre-alignment code and costs nothing per
// frame.
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
//
// Measured over the 74-theme reference corpus at 1152x864 Letterbox, our text
// capacity against AO2's ranged 0.37x .. 3.38x — a 9x spread, with 19 of 72
// themes inside 10% of parity. Folding the canvas scale in takes that to
// 1.00x .. 1.14x with 65 of 72 inside 10%; the residue is the pre-existing
// point-size-to-pixel truncation in themeFontPct, not the fold.
//
// SCOPE, deliberately narrow: only the CHATBOX PAIR (showname + message) folds.
// They are the two widgets AO2 parents to the chatbox, the two whose rects are
// chatbox-relative here, and the two whose text visibly overran or floated in
// its box. The IC log, the
// server log, the music list, the area list and the music name have the same
// parity problem — AO2 scales their fonts too — but they also carry the user's
// own per-panel Ctrl+wheel zoom, they re-wrap through caches keyed on that zoom,
// and folding them is a bigger behaviour change than one batch should make
// silently. Recorded in docs/ROADMAP.md rather than half-done here.
//
// AND IT ONLY BITES AWAY FROM 1:1. The shipped default fit is ThemeFitNative, in
// which a window at least as large as the theme's canvas scales by exactly 1.0 —
// so canvasPct is 100, the fold is the identity, and the out-of-the-box chatbox
// is byte-identical to what it was. Every path below is the CUSTOMIZATION path:
// the user resized under the canvas, or picked Letterbox / Stretch / Crop /
// Custom.

// themedCanvasFontMaxPct bounds the chatbox font AFTER the canvas fold. It is a
// memory bound, not a typographic one: the fold multiplies a point size that is
// already clamped to themeFontMaxPct by a canvas scale with no upper limit of
// its own, and an unbounded font size means an unbounded glyph atlas and an
// unbounded message-raster texture. This client has already shipped one bug
// where oversized text exhausted the GPU budget and every icon collapsed to a
// shared fallback, so the product of two clamped-looking numbers gets its own
// clamp (hard rules 4 and 9).
//
// 1600% is UIFontSize x 16 = 192 px. Sized from the corpus rather than picked:
// across all 72 measurable themes the largest scale the fold can ask for is 844%
// under Letterbox and 1500% under Crop, both on a 3840x2160 window — the latter
// being the 256x256 Viewport theme blown up 15x. So the cap never binds in any
// aspect-preserving mode on any real theme at any real display size. What it
// does catch is ThemeFitCustom, whose manual zoom multiplies the fit by up to
// MaxThemeZoom (300%) and would take that same theme past 2500%: there the clamp
// is the safety net it is meant to be, and the user asked for the distortion.
const themedCanvasFontMaxPct = 1600

// canvasTextScalePct rounds a themed canvas's scale to an integer percent for
// the chatbox font fold.
//
// emoteCellScale, NOT a per-axis factor, and for the reason spelled out on that
// function: AO2 has exactly ONE Options::themeScalingFactor(), so nothing in an
// AO2 theme is ever scaled differently on the two axes. Only ThemeFitStretch
// makes scaleX and scaleY differ here, and taking the LOOSER axis would size the
// text past the height of the box the theme drew for it. Taking the tighter one
// means a stretched window's text is smaller than its message rect strictly
// wants — measured at up to 3.8x under-capacity on a 3840x2160 stretch of a
// 256x256 theme — which is the same trade Stretch already makes for the emote
// grid, and Stretch is the mode that by definition abandons the theme's aspect.
func canvasTextScalePct(scaleX, scaleY float64) int {
	pct := int(emoteCellScale(scaleX, scaleY)*float64(DefaultScalePct) + 0.5)
	if pct < 1 {
		pct = 1 // a canvas scaled to nothing still has to name a font size
	}
	return pct
}

// foldCanvasFontPct folds a canvas scale into an ALREADY-RESOLVED element scale.
//
// The order matters and is the order AO2 uses. elemPct has already clamped the
// theme's DECLARED point size (folded with the user's zoom knob) into
// [themeFontMinPct, themeFontMaxPct] — that clamp exists to reject sloppy theme
// data, and it genuinely binds: `microsoft surface 4k webao` declares
// `message = 50`, which is 416% and clips to 400% before any canvas is involved.
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
//
// Every fontSet keys on the percent (Ctx.buildSet), and a rebuild there is not
// cheap: it closes and re-opens the whole chain and then calls purgeTextCache,
// which destroys every cached label texture and the entire text atlas. That is
// priced as a settings action — the comment on fontsFor says "never per frame" —
// and it was fine while the only things that moved a percent were the Ctrl+wheel
// zooms. Folding the canvas scale in changes that: the percent now moves with
// the WINDOW, and a live drag-resize delivers a new size every frame.
//
// Without this, dragging a window across 600 px would ask for ~600 distinct
// percents and pay ~600 full atlas teardowns for maybe 40 distinct font sizes.
// Snapping to the canonical percent for each size collapses that to those ~40 —
// the irreducible number, since the face really does have to be re-opened when
// its pixel size changes — and leaves nothing to debounce.
//
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
//
// BOTH, from ONE call, on purpose. The emoji face has to be opened at the same
// percent as the text it shares a row with or the two baselines part company,
// and the two used to be resolved by two separate calls that each recomputed the
// scale. With a canvas factor in that computation, "recomputed the same way" is
// no longer a safe assumption to leave to a call site.
func (a *App) themedChatFace(el themeFontElem, userPct int, lay *themeLayoutCache, text string) (primary, emoji *ttf.Font) {
	pct := a.themedChatPct(el, userPct, lay)
	return a.elemFontAtPct(el, pct, text), a.ctx.EmojiFont(pct)
}

// shownameSpanFor is shownameSpan bound to the active theme's alignment and to
// the face the name will actually be drawn in.
//
// The LEFT short-circuit is load-bearing, not an optimisation flourish: it is
// what keeps the default and every `left` theme on a per-frame path that never
// measures text at all, byte-identical to the code before alignment existed.
// Centre and right cost one cache probe per frame (labelEmojiWidth), which
// TestDrawCourtroomThemedCentredShownameZeroAlloc covers.
func (a *App) shownameSpanFor(box sdl.Rect, primary, emoji *ttf.Font, text string, col sdl.Color) (x, maxW int32) {
	if a.themeShownameAlign == shownameAlignLeft {
		return box.X, box.W
	}
	return shownameSpan(box, a.labelEmojiWidth(primary, emoji, text, col), a.themeShownameAlign)
}
