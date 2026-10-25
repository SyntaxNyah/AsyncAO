package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
)

// drawChatOverlay paints the message box (showname + spoken text) over the stage.
// movableBox (classic layout only) routes the box rect through slotRect so it can
// be dragged off the sprites / out of the stage in the layout editor; w,h give the
// override its window-fraction frame. The themed and replay paths pass false (the
// theme owns its chatbox geometry there).
func (a *App) drawChatOverlay(vp sdl.Rect, movableBox bool, w, h int32) {
	c := a.ctx
	sc := a.renderScene() // live room, slideshow override, OR the replay scene (M16)
	// Blankpost hides the whole chatbox (frame + showname + text) so only the
	// sprite shows; the second clause is the unchanged idle/no-message case.
	if sc.IsBlankPost || (sc.MessageText == "" && sc.ShownameText == "") {
		return
	}
	// Box height follows the MsgBox knob ONLY; text size (the Text knob)
	// lives inside it and clips at the box edge — big text never inflates
	// the box, small text never shrinks it.
	boxH := vp.H / 4 * int32(a.boxPct) / DefaultScalePct
	if max := vp.H * 3 / 5; boxH > max {
		boxH = max
	}
	box := sdl.Rect{X: vp.X, Y: vp.Y + vp.H - boxH, W: vp.W, H: boxH}
	// Movable chatbox (classic layout): its default sits at the stage bottom, but a
	// layout-editor override lifts it anywhere — off the sprites or out of the stage
	// entirely. Everything below lays out relative to box, so move + resize follow.
	if movableBox {
		box = a.classic.slotRect(slotChatbox, box, w, h)
	}
	// Per-character chatbox skin (char.ini chat=<misc>, playtest ask): the
	// SPEAKER's own box art wins over the theme skin — AO2's get_chat priority.
	// Drawn only once the misc texture is resident; until then the theme/flat
	// box covers the fetch gap, so nothing ever flashes empty.
	var charSkin *sdl.Texture
	if sc.ChatSkinBase != "" {
		if page, ok := a.d.Store.Get(sc.ChatSkinBase); ok && len(page.Frames) > 0 {
			charSkin = page.Frames[0]
		}
	}
	// Grow the FLAT fallback panel to fit the whole wrapped message so long text isn't cut off
	// at the bottom — notably after a viewport/box resize wraps it to more lines (reported bug).
	// Authored skin art (theme OR per-character) keeps its box (stretching looks wrong). Grows
	// UPWARD from the current bottom edge, capped inside grownChatBoxH.
	if _, hasSkin := a.themePage(themeStemChatbox); !hasSkin && charSkin == nil {
		// #39: the theme's "message" point size decides the line height, so a
		// theme-sized message grows the flat panel by the right amount.
		msgPct := a.messagePct()
		lineH := a.messageRowPitchLogical(msgPct, a.messageFontFor(msgPct, sc.MessageText))
		if g := grownChatBoxH(box.H, vp.H, int32(a.chatMsgLines(box.W-2*chatOverlayPadX, sc)), lineH); g > box.H {
			box.Y -= g - box.H
			box.H = g
			a.raster.chatDiag.grew = true
		} else {
			a.raster.chatDiag.grew = false
		}
	}
	// Skin priority: the speaker's own art, else the theme's, else the flat
	// translucent panel (themePage self-heals T1 eviction). Skin blits go via
	// the Ctx scratch rect: &box into cgo would heap-allocate box at its
	// CREATION above, every frame, even when neither skin branch runs.
	skinned := false
	if charSkin != nil {
		c.cgoRect = box
		_ = c.Ren.Copy(charSkin, nil, &c.cgoRect)
		skinned = true
	}
	if !skinned {
		if page, ok := a.themePage(themeStemChatbox); ok {
			// AO2's per-MESSAGE skin choice: a blank showname takes the plate-less
			// `chatblank` art (courtroom.cpp:3320-3331). This overlay is AsyncAO's
			// own chatbox, but it wears the THEME's skin whenever the theme shipped
			// one, so the empty name plate a narration post left on screen is the
			// same defect here — one selection, both paths (chatboxfit.go). A theme
			// with no separate blank variant returns its base page, which is
			// byte-identical to what this drew before.
			page, _ = a.chatboxSkinForShowname(sc.ShownameText, page)
			c.cgoRect = box
			_ = c.Ren.Copy(a.themeFrame(page), nil, &c.cgoRect)
			skinned = true
		}
	}
	if !skinned {
		// Panel opacity is user-tunable (Settings → see-through chatbox); the
		// border stays solid for legibility. Read once here, off the 0-alloc
		// render gate (that's render.Viewport; this UI overlay already reads prefs).
		alpha := uint8(255 * a.d.Prefs.Load().ChatboxOpacity / 100)
		bg := sdl.Color{R: 16, G: 16, B: 24, A: alpha}
		if col, ok := a.partPanel(partChatbox); ok { // per-part tint: custom base, opacity kept
			bg = sdl.Color{R: col.R, G: col.G, B: col.B, A: alpha}
		}
		if a.d.Prefs.Load().ChatboxTint && sc.ShownameText != "" { // #14 per-character tint
			bg = chatboxTintFor(sc.ShownameText, bg)
		}
		c.Fill(box, bg)
		c.Border(box, ColAccent)
	}
	// Creator easter egg: shared with the themed chatbox path (drawChatEgg
	// documents the once-per-message detection, the accessibility gate, and the
	// NoteAnimating cadence). Placed AFTER the skin/panel/border so the OUTSET
	// glow rings ring around the box on top of any skin without covering the art.
	a.drawChatEgg(box, sc.MessageText)
	// Theme text ink applies over any drawn chatbox skin — the theme's own art OR
	// a per-character skin (AO2's get_chat priority draws the speaker's box but still
	// inks it with the theme's message/showname colour). Only the FLAT fallback panel
	// (no skin at all) keeps the client's readable defaults, because there is no theme
	// art to design against (black-on-dark was a real report).
	nameCol := ColAccent
	if skinned && sc.ChatSkinNameHas {
		nameCol = sdl.Color{R: sc.ChatSkinNameColor.R, G: sc.ChatSkinNameColor.G, B: sc.ChatSkinNameColor.B, A: 255}
	} else if skinned && a.skin.themeHasName {
		nameCol = a.skin.themeNameCol
	}
	if a.d.Prefs.Load().NameColors { // per-speaker name colour wins over accent/theme
		nameCol = nameColor(sc.ShownameText, float64(a.d.Prefs.Load().NameSat)/100, float64(a.d.Prefs.Load().NameVal)/100)
	}
	// Pick a covering face for the showname (a Tifinagh / Cyrillic NAME would tofu on
	// the fixed chrome font); elemFontFor returns the chat set's chrome-sized face for
	// a theme that says nothing, so plain names are unchanged. #39: a theme's
	// "showname"/"showname_font"/"showname_bold" now drive size, family and weight.
	snFont := a.elemFontFor(elemShowname, DefaultScalePct, sc.ShownameText)
	snEmoji := a.elemEmoji(elemShowname, DefaultScalePct)
	// NOT showname_align: this overlay is AsyncAO's own chatbox, invented for
	// themes that declare no ao2_chatbox rect, so there is no theme-authored
	// showname box for a theme-authored alignment to place text across. The themed
	// chatbox honours it (chatboxfit.go).
	snBold := a.d.Prefs.Load().BoldNamesOn() || a.elemBold(elemShowname)
	a.labelEmojiWeight(snFont, snEmoji, box.X+chatOverlayPadX, box.Y+chatOverlayNameY, box.W-2*chatOverlayPadX, sc.ShownameText, nameCol, snBold)

	wrapW := box.W - 2*chatOverlayPadX
	// messagePct, NOT the themed fold: this overlay is AsyncAO's own chatbox, sized
	// from the viewport rather than from a theme-authored design rect, so there is
	// no canvas scale for it to keep a ratio to.
	a.ensureChatRaster(wrapW, skinned, a.messagePct()) // theme ink over any drawn skin; only the flat panel keeps the client default
	// Drag the message to highlight it, Ctrl+C / right-click to copy (webAO-style).
	textRect := sdl.Rect{X: box.X + chatOverlayPadX, Y: box.Y + chatBoxTopStrip, W: wrapW, H: box.H - chatBoxTopStrip}
	// The box follows its own crawl (chatcrawlscroll.go = AO2's ensureCursorVisible,
	// courtroom.cpp:4514-4521). Exactly 0 for a message that fits, so the ordinary
	// post draws from the same origin it always did.
	msgScroll := chatCrawlOffset(a.raster.msRaster, sc.VisibleRunes, textRect.H)
	msgY := textRect.Y - msgScroll
	// …and the clip that contains it starts at the TEXT origin, not at the top of
	// the box: a scrolled message draws from above textRect.Y, and the showname
	// plate is up there (box.Y+chatOverlayNameY). Clipping to `box` put the
	// scrolled-off rows on the name band. chatcrawlscroll.go explains why the other
	// three edges stay the box's — and why the #M5 animated painter, which cannot
	// scroll but DOES lift glyphs off their row, keeps the whole box instead.
	msgClip := chatCrawlClip(box, textRect.Y, a.raster.msAnim)
	a.handleChatSelect(textRect, msgScroll, sc)
	// Snapshot the geometry for Debug → Session. Plain field stores, no allocation,
	// so it costs the draw nothing; it is the only way to see these numbers on a
	// machine we can't run.
	a.raster.chatDiag.vpH, a.raster.chatDiag.boxY, a.raster.chatDiag.boxH = vp.H, box.Y, box.H
	a.raster.chatDiag.textY, a.raster.chatDiag.wrapW = textRect.Y, wrapW
	a.raster.chatDiag.renderPct, a.raster.chatDiag.devScale = c.RenderScalePct(), a.ctx.textDevPct
	a.raster.chatDiag.visible = int32(sc.VisibleRunes)
	if a.raster.msRaster != nil {
		a.raster.chatDiag.lines = int32(a.raster.msRaster.Lines())
		a.raster.chatDiag.lineH = a.raster.msRaster.LineH()
		a.raster.chatDiag.total = int32(a.raster.msRaster.TotalRunes())
	} else {
		a.raster.chatDiag.lines, a.raster.chatDiag.lineH, a.raster.chatDiag.total = 0, 0, 0
	}
	if a.raster.msAnim != nil || a.raster.msRaster != nil {
		// Clip to the box below the name plate: oversized Text settings stay INSIDE
		// it, and the crawl's scrolled-off rows stay OUT of the showname strip. Via
		// the Ctx scratch rect — &msgClip into cgo would heap-allocate it at its
		// creation every frame (SDL copies the rect, so later scratch reuse is safe).
		c.cgoRect = msgClip
		_ = c.Ren.SetClipRect(&c.cgoRect)
		if a.sel.chatSelActive { // selection highlight, UNDER the text so it reads through
			a.drawChatSelHighlight(textRect.X, msgY, wrapW, sc) // scrolled with the text it marks
		}
		if a.raster.msAnim != nil { // #M5 animated message (shake/wave/rainbow spans)
			// The master off-switch implies reduce-motion for text too: the glyphs
			// still lay out and read normally, they just stop moving (animtext.go
			// pins everything static under this flag), which is exactly what the
			// toggle promises — no spinning, no waving, text still legible.
			reduce := a.d.Prefs.Load().ReduceMotionOn || a.d.Prefs.Load().DisableEffects
			if a.raster.msAnim.Animates(reduce) {
				// Clock-driven text FX on screen: keep frames coming through the
				// static skip (idle=0 froze/stuttered them — the FX-at-idle
				// report). Draw-site census, so it self-clears the moment the
				// message leaves the box; gradient-only / reduce-motion render
				// static and deliberately don't hold frames. Minimized still
				// draws nothing (no Frame at all), and unfocused with the
				// background cap off still parks (that gate outranks this).
				a.NoteAnimating()
			}
			a.raster.msAnim.Draw(c.Ren, a.raster.glyphCache, a.raster.msAnimFont, a.d.Viewport.AnimClock(), sc.VisibleRunes, textRect.X, msgY, reduce)
		} else {
			// DrawScaled, not Draw: at a scaled UI the revealed prefix must blit
			// device-exact or the crawl re-stretches the whole run by a pixel every
			// time its width changes parity, and the settled text wobbles as it types.
			a.raster.msRaster.DrawScaled(c.Ren, sc.VisibleRunes, textRect.X, msgY, c.RenderScalePct(), &msgClip)
		}
		_ = c.Ren.SetClipRect(nil)
	} else if a.raster.rasterFailedText != "" && sc.MessageText != "" {
		// Last resort: the raster could not be built at all. Draw the words with the
		// ordinary wrapped label path so the player still reads the message. It gets
		// no typewriter and no per-glyph effects — but a chatbox that silently shows
		// NOTHING while the showname above it draws normally is the worst possible
		// failure, because it looks like the client simply stopped receiving IC.
		c.cgoRect = box
		_ = c.Ren.SetClipRect(&c.cgoRect)
		msgPct := a.messagePct()
		msgFont := a.messageFontFor(msgPct, sc.MessageText)
		lineH := a.messageRowPitchLogical(msgPct, msgFont) // same row pitch the raster would have used
		y := textRect.Y
		for _, line := range c.WrapText(sc.MessageText, wrapW, 0) {
			if y+lineH > box.Y+box.H {
				break
			}
			a.labelEmoji(msgFont, a.elemEmoji(elemMessage, a.messagePct()), textRect.X, y, wrapW, line, chatBaseColor(a, sc, skinned, false))
			y += lineH
		}
		_ = c.Ren.SetClipRect(nil)
	}

	a.chatZoomWheel(box)
}

// handleChatSelect runs the chatbox's text selection + Ctrl+C / right-click
// copy: drag across the message to select a RANGE of it (per-rune, snapped
// to boundaries via the raster's own glyph advances — "so people can copy one
// word instead of the whole line"), double-click selects the word under the
// cursor, triple-click the whole message; a plain click clears. The animated
// path (msAnim — per-glyph motion, no static raster) keeps the old
// whole-message selection. Its own press edge so it's independent of the log
// selection's; activating either clears the other so they never fight over a
// Ctrl+C. Shared by the classic overlay and the themed chatbox.
// scrollY is the crawl-follow offset the text is DRAWN with
func (a *App) handleChatSelect(textRect sdl.Rect, scrollY int32, sc *courtroom.Scene) {
	c := a.ctx
	pressed := c.mouseDown && !a.sel.chatSelPrevDown
	a.sel.chatSelPrevDown = c.mouseDown
	inText := c.hovering(textRect)
	if pressed {
		if inText {
			a.sel.chatSelDragging = true
			a.sel.chatSelDownX, a.sel.chatSelDownY = c.mouseX, c.mouseY
			if a.raster.msRaster != nil { // anchor the range at the pressed boundary
				a.sel.chatSelA = a.raster.msRaster.RuneAt(c.mouseX-textRect.X, c.mouseY-textRect.Y+scrollY)
				a.sel.chatSelB = a.sel.chatSelA
			}
		} else {
			a.sel.chatSelActive = false // a press elsewhere clears the highlight
		}
	}
	if a.sel.chatSelDragging {
		if c.mouseDown {
			if a.raster.msRaster != nil { // the held drag moves the range's head
				a.sel.chatSelB = a.raster.msRaster.RuneAt(c.mouseX-textRect.X, c.mouseY-textRect.Y+scrollY)
			}
			if absInt(int(c.mouseX-a.sel.chatSelDownX))+absInt(int(c.mouseY-a.sel.chatSelDownY)) > 3 {
				if !a.sel.chatSelActive {
					a.sel.chatSelActive = true // moved enough → a selection, not a click
					a.sel.logSelActive = false // …and it owns the highlight now
				}
			}
		} else {
			a.sel.chatSelDragging = false
			switch {
			case a.sel.chatSelActive && a.raster.msRaster != nil && a.sel.chatSelA == a.sel.chatSelB:
				a.sel.chatSelActive = false // wobbled past the slop but landed on one boundary — not a selection
			case a.sel.chatSelActive:
				c.clicked = false // a real drag isn't a click on whatever's under it
				c.focusID = ""    // unfocus so Ctrl+C copies the selection, not a still-focused field
			}
		}
	}
	// Double-click: the word under the cursor; triple-click: the whole
	// message (native text gestures — mirrors the fields and the logs).
	if a.raster.msRaster != nil && inText && (c.dblClick || c.tripleClick) && sc.MessageText != "" {
		runes := []rune(sc.MessageText)
		if c.tripleClick {
			a.sel.chatSelA, a.sel.chatSelB = 0, len(runes)
			c.tripleClick = false
		} else {
			idx := a.raster.msRaster.RuneAt(c.mouseX-textRect.X, c.mouseY-textRect.Y+scrollY)
			a.sel.chatSelA, a.sel.chatSelB = wordBoundsAt(runes, idx)
			c.dblClick = false
		}
		if a.sel.chatSelB > a.sel.chatSelA {
			a.sel.chatSelActive = true
			a.sel.logSelActive = false
			c.clicked = false
			c.focusID = ""
		}
	}
	if a.sel.chatSelActive && sc.MessageText != "" {
		if (c.copyReq && c.focusID == "") || (c.rightClicked && inText) {
			_ = sdl.SetClipboardText(a.chatSelText(sc))
			a.warnLine = "Copied selection to clipboard"
			a.warnAt = time.Now()
			c.copyReq = false
			if inText {
				c.rightClicked = false
			}
		}
	}
}

// chatSelText is the text the chatbox selection copies: the selected rune
// range, or the whole message on the animated path / a degenerate range.
func (a *App) chatSelText(sc *courtroom.Scene) string {
	if a.raster.msRaster == nil {
		return sc.MessageText
	}
	runes := []rune(sc.MessageText)
	lo, hi := a.sel.chatSelA, a.sel.chatSelB
	if lo > hi {
		lo, hi = hi, lo
	}
	if lo < 0 {
		lo = 0
	}
	if hi > len(runes) {
		hi = len(runes)
	}
	if lo >= hi {
		return sc.MessageText
	}
	return string(runes[lo:hi])
}

// drawChatSelHighlight fills the selected range behind the message, one band
// per wrapped line: partial first/last lines measure their end runes through
// the raster's own advances, interior lines span their drawn text, and the
// typewriter clamp keeps the band off glyphs that haven't revealed yet. The
// animated path has no per-rune raster — the whole visible block highlights,
// exactly the old behavior. Measurement only (no allocations): fine per frame
// while a selection is up.
func (a *App) drawChatSelHighlight(x, y, wrapW int32, sc *courtroom.Scene) {
	c := a.ctx
	m := a.raster.msRaster
	if m == nil { // animated message (msAnim): whole-block highlight
		// a.rasterScale, not a fresh messagePct(): it is the percent ensureChatRaster
		// actually built msAnim at, and the two now differ inside a themed chatbox,
		// which folds the canvas scale in (chatboxfit.go). Reaching here means msAnim
		// is non-nil — this is only called from inside that guard — so ensureChatRaster
		// has run and rasterScale is the scale on screen.
		// #39: theme "message" size, stepped by the raster's own row pitch
		// (render.FontLineSpacing) — a highlight built on the shorter glyph box would
		// leave an unpainted stripe between every pair of wrapped rows.
		lineH := a.raster.msAnim.RowPitch() // the step Draw will actually use, override included
		c.Fill(sdl.Rect{X: x, Y: y, W: wrapW, H: int32(a.chatMsgLines(wrapW, sc)) * lineH}, a.highlightFill())
		return
	}
	lo, hi := a.sel.chatSelA, a.sel.chatSelB
	if lo > hi {
		lo, hi = hi, lo
	}
	if hi > sc.VisibleRunes {
		hi = sc.VisibleRunes // typewriter: never highlight unrevealed glyphs
	}
	fill := a.highlightFill()
	for i := 0; i < m.Lines(); i++ {
		if x0, x1, ok := m.LineSpanX(i, lo, hi); ok && x1 > x0 {
			c.Fill(sdl.Rect{X: x + x0, Y: y + int32(i)*m.LineH(), W: x1 - x0, H: m.LineH()}, fill)
		}
	}
}

// sfxAutoLabel is the SFX picker's "no override" entry: use the selected emote's own
// char.ini sound (the default behaviour). It is AO2's "Default" row — index 0 of
// ui_sfx_dropdown (courtroom.cpp:5488-5493), the row get_char_sfx answers with
// get_sfx_name(current_char, current_emote) (:5680-5683).
const sfxAutoLabel = "SFX: auto"

// ensureSFXChoices (re)builds the IC-bar SFX picker list when the character or its emote
// list changes: "SFX: auto" (the emote's own sound) then this character's DISTINCT emote
// sounds (char.ini [SoundN]; "0"/"1"/empty are AO silence, skipped). The picked sound
// overrides the emote's on every send until set back to auto. Off the per-frame path
// (rebuild only on a char/emote-count change) so the IC bar stays alloc-free.
func (a *App) ensureSFXChoices() {
	// Plain-field guard, not a concatenated key: this runs every frame, and
	// building a "<char>:<n>" string just to compare it allocated per frame.
	name := a.activeCharName()
	if a.emotepick.sfxChoicesForName == name && a.emotepick.sfxChoicesForCount == len(a.emotepick.emotes) {
		return
	}
	a.emotepick.sfxChoicesForName, a.emotepick.sfxChoicesForCount = name, len(a.emotepick.emotes)
	a.emotepick.sfxChoices = append(a.emotepick.sfxChoices[:0], sfxAutoLabel)
	for i := range a.emotepick.emotes {
		s := a.emotepick.emotes[i].SFXName
		if s == "" || s == "0" || s == "1" {
			continue
		}
		dup := false
		for _, ex := range a.emotepick.sfxChoices {
			if ex == s {
				dup = true
				break
			}
		}
		if !dup {
			a.emotepick.sfxChoices = append(a.emotepick.sfxChoices, s)
		}
	}
	if a.emotepick.sfxChoiceIdx >= len(a.emotepick.sfxChoices) {
		a.emotepick.sfxChoiceIdx = 0
	}
}

// chatMsgLines is the chatbox message's wrapped-line count (the raster's own count when
// present, else an approximate re-wrap) — sizes the whole-message selection block.
func (a *App) chatMsgLines(wrapW int32, sc *courtroom.Scene) int {
	if a.raster.msRaster != nil {
		if n := a.raster.msRaster.Lines(); n > 0 {
			return n
		}
	}
	if n := len(a.ctx.WrapText(sc.MessageText, wrapW, 0)); n > 0 {
		return n
	}
	return 1
}

// ensureChatRaster (re)rasterizes the current message when the text,
// color, zoom, wrap width, or skin presence changed — shared by the
// classic overlay and the themed chatbox.
// (the theme's declared point size folded with the Text zoom knob, #39), while
func (a *App) ensureChatRaster(wrapW int32, skinned bool, pct int) {
	sc := a.renderScene() // matches drawChatOverlay (live / slideshow / replay scene)
	effSig := effectsSig(sc.MessageEffects)
	// staleAnimChain: an effects line built BEFORE a lazily-loaded CJK/emoji tier landed
	// carries the old font-chain generation; its glyphs are still tofu/uniform-advance until
	// it re-rasters against the new chain. Keyed on fontChainGen the same way the log wrap
	// caches are (qol.go) — Task E. Cheap field compare; only the effects path builds msAnim.
	staleAnimChain := a.raster.msAnim != nil && a.raster.msAnim.ChainGen() != a.ctx.fontChainGen
	if !staleAnimChain && (a.raster.msRaster != nil || a.raster.msAnim != nil) && a.raster.rasterRaw == sc.MessageRaw && a.raster.rasterText == sc.MessageText && a.raster.rasterColor == sc.TextColor &&
		a.raster.rasterScale == pct && a.raster.rasterW == wrapW && a.raster.rasterSkinned == skinned && a.raster.rasterEffSig == effSig && a.raster.rasterCentered == sc.Centered &&
		a.raster.rasterChatMsg == sc.ChatSkinMsgColor && a.raster.rasterChatMsgHas == sc.ChatSkinMsgHas &&
		a.raster.rasterDevPct == a.ctx.textDevPct { // #77: a UI-scale change re-rasterizes at the new device size
		return
	}
	if a.raster.rasterRaw != sc.MessageRaw {
		a.sel.chatSelActive = false // a new message — drop the stale chatbox highlight/selection
	}
	if a.raster.msRaster != nil {
		a.raster.msRaster.Destroy()
		a.raster.msRaster = nil
	}
	a.raster.msAnim = nil // AnimatedText owns no textures (the glyph cache does); just drop it
	a.raster.msAnimFont = nil
	if sc.MessageText == "" {
		return
	}
	// #M5: a message with effect spans takes the per-glyph animated path; every other
	// message keeps the untouched MessageRaster fast paths (zero change for the common case).
	if len(sc.MessageEffects) > 0 {
		if a.raster.glyphCache == nil {
			a.raster.glyphCache = render.NewGlyphCache(glyphCacheCap)
		}
		anim, font := renderAnimated(a, sc, wrapW, skinned, pct)
		anim.Warm(a.ctx.Ren, a.raster.glyphCache, font) // render all glyphs up front → 0-alloc draws
		a.raster.msAnim = anim
		a.raster.msAnimFont = font
	} else {
		raster, err := renderRaster(a, sc, wrapW, skinned, pct, false)
		if err != nil {
			// A raster that fails to build used to return here with msRaster nil AND
			// without recording rasterText, so every later frame re-entered, failed
			// identically, and drew NOTHING — a permanently blank chatbox under a
			// showname that still painted, with no message anywhere and no diagnostic.
			a.debug.push("chatbox raster failed: " + err.Error() + " — drawing the message unrasterized")
			a.raster.rasterFailedText = sc.MessageText
			return
		}
		a.raster.msRaster = raster
		a.raster.rasterFailedText = ""
	}
	a.raster.rasterText = sc.MessageText
	a.raster.rasterRaw = sc.MessageRaw
	a.raster.rasterColor = sc.TextColor
	a.raster.rasterScale = pct
	a.raster.rasterW = wrapW
	a.raster.rasterSkinned = skinned
	a.raster.rasterChatMsg = sc.ChatSkinMsgColor
	a.raster.rasterChatMsgHas = sc.ChatSkinMsgHas
	a.raster.rasterEffSig = effSig
	a.raster.rasterCentered = sc.Centered
	a.raster.rasterDevPct = a.ctx.textDevPct // #77
}

// chatZoomWheel: Ctrl+wheel over the chatbox zooms the chat text
// (browser convention) — shared by both chatbox flavors.
func (a *App) chatZoomWheel(box sdl.Rect) {
	// Ctrl+wheel (fine) or wheel-button-held (fast) resizes the chatbox text.
	a.zoomWheel(box, &a.chatPct, config.MinChatScalePercent, config.MaxChatScalePercent)
}

func clampInt(v, min, max int) int {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

func clampI32(v, min, max int32) int32 {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

func clampF64(v, min, max float64) float64 {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

// volStripRowsH is the height of the quick volume popover's two slider rows:
const volStripRowsH = int32(44)

// persistChannelMute saves one channel's click-to-mute toggle so it survives a
// restart (and, via resetSessionState's reseed, Connect/Disconnect too — see
// tabs.go). id matches drawVolumeStrip's cell ids ("master"/"music"/"sfx"/
// "blip"); the "rate" cell passes a nil mute pointer and never reaches this
// call. The single switch keeps one persistence call site per channel instead
// of four near-identical branches at each cell(...) call above. The SFX channel
// is the deliberate exception: it is session-only and not persisted here.
func (a *App) persistChannelMute(id string, on bool) {
	switch id {
	case "master":
		a.d.Prefs.Update(func(
			sn *config.
				Settings) {

			sn.
				MasterVolMuted =

				on

		})

	case "music":
		a.d.Prefs.Update(func(
			sn *config.
				Settings) {

			sn.
				MusicVolMuted =

				on

		})

	case "blip":
		a.d.Prefs.Update(func(
			sn *config.
				Settings) {

			sn.
				BlipVolMuted =

				on

		})

	}
}

// drawVolumeStrip draws the toggleable on-screen volume row (Master + the three
// channels) at the top of the log panel — quick volume without leaving the chat.
func (a *App) drawVolumeStrip(r sdl.Rect) {
	c := a.ctx
	// FIX #9 (ZeitHeld): drive the EFFECTIVE (per-server when connected) volumes, exactly
	// like the Settings and Extras sliders. The old code read/wrote the GLOBAL prefs, which
	// a connected server's per-server audio profile overrides — so once you touched volume
	// anywhere else (Settings/Extras both write per-server), these sidebar sliders moved
	// nothing audible. Plus a "Rate" column for the blip cadence (the user's ask).
	master, music, sfx, blip := a.effectiveVolumes()
	rate, onSpaces := a.d.Prefs.Load().BlipTyping()
	const cols = 5
	colW := r.W / cols
	// Playtest ("kinda bare bones"): two rows per column — "label NN%" (the
	// label is still the click-to-mute) above a full-width slider, so the
	// actual level is always READABLE, not just a thumb position. The sliders
	// also take the wheel now (kit-wide), so fine steps don't need a drag.
	cell := func(i int32, id, label, valText, tip string, val, lo, hi int32, mute *bool) int32 {
		x := r.X + i*colW
		labelCol := ColTextDim
		if mute != nil { // #10: the channel label is a click-to-mute toggle (red = muted)
			lbl := sdl.Rect{X: x, Y: r.Y, W: colW - 6, H: 16}
			if c.ClickedIn(lbl) {
				*mute = !*mute
				a.applyAudioVolumes()           // the muted channel goes silent; the slider level is kept
				a.persistChannelMute(id, *mute) // survives a restart, and Connect/Disconnect (resetSessionState reseeds it)
			}
			if *mute {
				labelCol = ColDanger
				valText = "muted"
			}
			c.Tooltip(lbl, "Click to mute / unmute (the slider level is kept)")
		}
		c.Label(x, r.Y+2, label, labelCol)
		c.LabelClipped(x+c.TextWidth(label)+6, r.Y+2, colW-c.TextWidth(label)-12, valText, ColAccent)
		track := sdl.Rect{X: x, Y: r.Y + 22, W: colW - 8, H: 14}
		v := clampI32(c.Slider("volstrip:"+id, track, val, hi), lo, hi)
		if tip != "" {
			c.Tooltip(track, tip)
		}
		return v
	}
	if nv := cell(0, "master", "Master", fmt.Sprintf("%d%%", master), "", int32(master), 0, 100, &a.audio.masterMuted); int(nv) != master {
		a.setEffectiveVolumes(int(nv), music, sfx, blip)
	}
	if nv := cell(1, "music", "Music", fmt.Sprintf("%d%%", music), "", int32(music), 0, 100, &a.audio.musicMuted); int(nv) != music {
		a.setEffectiveVolumes(master, int(nv), sfx, blip)
	}
	if nv := cell(2, "sfx", "SFX", fmt.Sprintf("%d%%", sfx), "", int32(sfx), 0, 100, &a.audio.sfxMuted); int(nv) != sfx {
		a.setEffectiveVolumes(master, music, int(nv), blip)
	}
	if nv := cell(3, "blip", "Blips", fmt.Sprintf("%d%%", blip), "", int32(blip), 0, 100, &a.audio.blipMuted); int(nv) != blip {
		a.setEffectiveVolumes(master, music, sfx, int(nv))
	}
	// Blip cadence (1 blip / N letters; left = faster). Mirrors Settings → Blips. No mute.
	if nv := cell(4, "rate", "Rate", fmt.Sprintf("1/%d", rate), "Blip cadence: 1 blip every N letters (left = faster)", int32(rate), int32(config.MinBlipRate), int32(config.MaxBlipRate), nil); int(nv) != rate {
		a.d.Prefs.SetBlipTyping(int(nv), onSpaces)
		a.applyTimingToRoom()
	}
}

func (a *App) drawLogPanel(r sdl.Rect, vp sdl.Rect) {
	c := a.ctx
	c.Fill(r, a.partPanelOr(partLog, ColPanel)) // per-part tint: the log column can carry its own colour
	c.Border(r, ColAccent)                      // match the rest of the UI (buttons/OOC box/panels all border in the accent) — playtest: the log panel was the lone grey outline
	// A "🔊" toggle at the right end drops a compact volume strip above the panel
	// content — adjust volume while the log stays on screen and you keep chatting
	// (the IC box below is untouched). The tabs share the rest of the row.
	const volBtnW = int32(36)
	// By default OOC is its OWN box, so the OOC tab is dropped (and the rest reindex). The Legacy theme
	// — or the opt-in "OOC in the log tab" toggle — instead keeps OOC in the strip. If the active tab
	// was OOC while OOC is a box, or it has been torn into its own floating panel (torntabs.go), fall
	// back to the IC log so the docked area still shows something.
	oocAsTab := a.d.Prefs.Load().LegacyDevTheme || a.d.Prefs.Load().OOCInLogTab
	if (!oocAsTab && a.chatlog.logTab == logTabOOC) || a.classic.tabTorn(a.chatlog.logTab) || a.classic.tabHidden(a.chatlog.logTab) {
		a.chatlog.logTab = logTabLog
	}
	// The docked strip skips any torn-off tab and compacts the rest (0-alloc stack array).
	docked, numLogTabs := a.classic.dockedLogTabs(oocAsTab)
	tab := (r.W - volBtnW) / numLogTabs
	for i := int32(0); i < numLogTabs; i++ {
		bw := tab
		if i == numLogTabs-1 {
			bw = (r.W - volBtnW) - (numLogTabs-1)*tab // last tab takes the remainder before the Vol toggle
		}
		if c.Button(sdl.Rect{X: r.X + i*tab, Y: r.Y, W: bw, H: btnH}, docked[i].label) {
			a.chatlog.logTab = docked[i].id
		}
	}
	volBtn := sdl.Rect{X: r.X + r.W - volBtnW, Y: r.Y, W: volBtnW, H: btnH}
	if c.Button(volBtn, "Vol") {
		a.musicTab.volStripOn = !a.musicTab.volStripOn
		{
			pv1 := a.musicTab.volStripOn
			a.d.Prefs.Update(func(sn *config.Settings) { // persist so it survives a restart
				sn.VolStripOn = pv1
			})
		}
	}
	if a.musicTab.volStripOn {
		c.Border(volBtn, ColAccent) // active cue
	}
	c.Tooltip(volBtn, "Show/hide volume sliders on screen (chat stays usable)")
	innerY := r.Y + btnH + logPanelRowGapPx
	if a.musicTab.volStripOn {
		// Just the two slider rows — the streaming checkbox moved to Settings →
		// Audio only (the popover was getting cluttered).
		a.drawVolumeStrip(sdl.Rect{X: r.X + logPanelInsetPx, Y: innerY, W: r.W - 2*logPanelInsetPx, H: volStripRowsH})
		innerY += volStripRowsH + logPanelRowGapPx
	}
	inner := sdl.Rect{X: r.X + logPanelInsetPx, Y: innerY, W: r.W - 2*logPanelInsetPx, H: r.Y + r.H - innerY - logPanelInsetPx}
	// Ctrl+wheel (fine) or wheel-button-held (fast) anywhere on the panel
	// resizes the log/OOC/list text; plain wheel keeps scrolling the active
	// list. Taking the wheel here stops the inner lists' zoom from double-stepping.
	// The Music tab tunes its OWN scale (musicPct) so you can shrink long track
	// titles without shrinking the IC log.
	scale := &a.logPct
	switch a.chatlog.logTab {
	case logTabMusic:
		scale = &a.musicPct // the Music tab tunes its own scale
	case logTabAreas:
		scale = &a.areaPct // the Areas tab tunes its own scale (the inner drawAreaList zoomWheel then no-ops via wheelTaken — no double-step)
	case logTabPlayers:
		scale = &a.playerPct // the Players tab tunes its own scale — without this case the panel handler took Ctrl+wheel into logPct and the inner drawPlayerList zoomWheel never fired (wheelTaken)
	case logTabOOC:
		scale = &a.oocPct // the OOC tab resizes OOC text, independent of the IC log
	}
	a.zoomWheel(r, scale, config.MinLogScalePercent, config.MaxLogScalePercent)
	switch a.chatlog.logTab {
	case logTabMusic:
		a.drawMusicList(inner, false, false, false)
		return
	case logTabAreas:
		a.drawAreaList(inner, false)
		return
	case logTabPlayers:
		a.drawPlayerList(inner)
		return
	case logTabOOC:
		// The OOC tab is a COMPLETE OOC chat: scrollback + its own input + the OOC name. The
		// bottom OOC bar is a SECOND, always-visible input (no name) for a hybrid setup — type
		// from either; both share the same OOC draft (a.oocInput).
		a.drawOOCPanel(inner, true)
		return
	case logTabNotes:
		a.drawNotesTab(inner)
		return
	case logTabFriends:
		a.drawFriendsTab(inner)
		return
	}
	// IC log tab: search box + Copy/TXT/HTML row, then the colored
	// scrollback (AO text colors preserved per entry). The row is sized to
	// the CHROME font: textField vertically centres its text, so a custom
	// whole-UI font taller than the old fixed 24px box spilled its glyphs
	// BELOW the field, straight onto the first log line 4px under it
	// (playtest, reported by Tifera: the log overlapped the search bar). With the
	// stock font the max() keeps the row at exactly 24px — byte-identical.
	rowY := inner.Y
	rowH := logSearchRowH(int32(c.font.Height()))
	// "Filter" toggles the advanced filter sub-panel (showname + pos, #129).
	// Reserved at the row's left edge so the search field and export buttons
	// keep their existing right-side geometry.
	const filterBtnW = int32(48)
	filterBtn := sdl.Rect{X: inner.X, Y: rowY, W: filterBtnW, H: rowH}
	if c.Button(filterBtn, "Filter") {
		a.chatlog.logFilterOpen = !a.chatlog.logFilterOpen
	}
	if a.chatlog.logFilterOpen {
		c.Border(filterBtn, ColAccent) // active cue, matching the Vol toggle
	}
	c.Tooltip(filterBtn, "Show/hide advanced filters (only showname / position)")
	searchX := inner.X + filterBtnW + 4
	searchW := inner.W - filterBtnW - 4 - 3*logExportBtnPitch - 12
	// A layout-edited slot can be too narrow for the export buttons: give the
	// search field the rest of the row instead of drawing it at a degenerate
	// width (the placeholder text has no box to live in at W <= 0).
	showExports := searchW >= logSearchMinW
	if !showExports {
		searchW = inner.W - (filterBtnW + 4)
	}
	a.chatlog.logSearch, _ = c.TextField("logsearch", sdl.Rect{X: searchX, Y: rowY, W: searchW, H: rowH}, a.chatlog.logSearch, "Search log...")
	if showExports {
		bx := searchX + searchW + 4
		if c.Button(sdl.Rect{X: bx, Y: rowY, W: logExportBtnW, H: rowH}, "Copy") {
			a.copyICLog()
		}
		if c.Button(sdl.Rect{X: bx + logExportBtnPitch, Y: rowY, W: logExportBtnW, H: rowH}, "TXT") {
			a.exportICLog(false)
		}
		if c.Button(sdl.Rect{X: bx + 2*logExportBtnPitch, Y: rowY, W: logExportBtnW, H: rowH}, "HTML") {
			a.exportICLog(true)
		}
	}
	listY := rowY + rowH + logSearchRowGap
	// Advanced filters (#129): a second row of two fields — "Showname" (only that
	// speaker) and "Pos" (only that position) — shown when Filter is expanded.
	// Both are inclusive and stack on top of the text search above.
	if a.chatlog.logFilterOpen {
		frH := rowH
		half := (inner.W - 4) / 2
		a.chatlog.logFilterShowname, _ = c.TextField("logfilter_showname", sdl.Rect{X: inner.X, Y: listY, W: half, H: frH}, a.chatlog.logFilterShowname, "Showname…")
		a.chatlog.logFilterPos, _ = c.TextField("logfilter_pos", sdl.Rect{X: inner.X + half + 4, Y: listY, W: inner.W - half - 4, H: frH}, a.chatlog.logFilterPos, "Pos (e.g. wit)…")
		// Colour filter (#49): a second row narrowing to entries that use the
		// picked colour (message-level or any inline span). "Any color" = off.
		a.chatlog.logFilterColor, _ = c.Dropdown("logfilter_color", sdl.Rect{X: inner.X, Y: listY + frH + logSearchRowGap, W: inner.W, H: frH}, logColorFilterChoices, a.chatlog.logFilterColor)
		listY += 2*frH + 2*logSearchRowGap
	}
	// false: this is the classic docked Log tab, AsyncAO chrome outside any theme's
	// design canvas (#21 label 16).
	a.drawICLogList(sdl.Rect{X: inner.X, Y: listY, W: inner.W, H: inner.H - (listY - inner.Y)}, false)
}

const (
	// logPanelInsetPx is the classic docked log panel's body inset inside its own
	// accent border: the volume strip and the active tab's content must clear the
	// outline. This is why drawICLogList / drawOOCLogList / drawMusicList / … carry NO
	// internal padding — the classic path insets before it calls them, and the themed
	// path does its own (themedLogInsetPx). Historical value, kept exactly.
	logPanelInsetPx = int32(4)
	// logPanelRowGapPx is the vertical gap between the log panel's stacked rows:
	// tab strip → optional volume strip → body.
	logPanelRowGapPx = int32(4)
	// logSearchRowMinH is the search/export row's historical height — the
	// floor, so the stock chrome font keeps the exact old geometry.
	logSearchRowMinH = int32(24)
	// logSearchRowPadV is the vertical breathing room around the chrome
	// font inside the search field (glyphs must never leave the box).
	logSearchRowPadV = int32(8)
	// logSearchRowGap separates the row from the log list below it.
	logSearchRowGap = int32(4)
	// logSearchMinW is the narrowest useful search field; below it the
	// export buttons yield the row (a layout-edited skinny log slot).
	logSearchMinW = int32(60)
	// logExportBtnW/Pitch are the Copy/TXT/HTML button width and spacing.
	logExportBtnW     = int32(50)
	logExportBtnPitch = int32(52)
)

// icBarUnderStage is the #8 default layout: the IC input bar sits DIRECTLY under the
// stage (icBarTop, the classic AO spot so it's obvious where you talk IC), and the
// control-button block sits fH + a small gap below it (defY). Pure, so the "input is
// under the viewport, above the controls" invariant is unit-pinnable.
func icBarUnderStage(vp sdl.Rect, fH int32) (icBarTop, defY int32) {
	icBarTop = vp.Y + vp.H + pad
	defY = icBarTop + fH + 8 // gap below the input row
	return
}

func (a *App) drawICControls(w, h int32, vp sdl.Rect) {
	c := a.ctx
	// #8 (ZeitHeld): the IC INPUT BAR sits DIRECTLY under the stage — the classic AO
	// spot — so it's obvious where you talk IC; the control buttons, judge strip and
	// emote grid follow BELOW it. fH (the input-row height) is needed up here to place
	// the control block below the bar.
	fH := a.inputFieldH()
	icBarTop, defY := icBarUnderStage(vp, fH) // IC bar under the stage; control block below it

	// Control-button slot ("controls"): the two button rows — shouts / pair / layout
	// knobs (row 1) and the utility buttons (row 2, which wraps) — move together as one
	// block. Un-edited, the content width is w-2*pad, so the wrap structure — and
	// therefore the block's row count and height — matches the classic layout exactly
	// (clusterX == pad, y == defY, dy == 0 ⇒ every rect, every wrap edge identical:
	ctrlOv, ctrlEdited := a.classic.classicOv[slotControls]
	var ctrlRect sdl.Rect
	if ctrlEdited {
		ctrlRect = a.classic.anchoredRect(slotControls, ctrlOv, w, h) // window pin honoured
	}
	clusterX, y, dy, clusterRight := controlsBlockOrigin(ctrlRect, ctrlEdited, w, defY)

	// Row 1: shouts, pairing, and the live layout knobs (both hideable).
	// Extracted to drawICShoutRow (rects by value, no closures — the row stays
	// alloc-free). It ARMS a.icShout now; nothing is returned to a send.
	a.drawICShoutRow(clusterX, y, w, h)

	// Row 2: utility buttons (their own row so nothing overlaps at any
	// viewport scale or window width). Split into the legacy-dev-theme and the
	// grouped-default variants; each returns the (wrapped) row bottom y2 and a
	// `done` flag set when a Disconnect click already fired requestDisconnect —
	// in which case the frame short-circuits exactly as the inline `return` did.
	y2 := y + btnH + 4
	var done bool
	if a.d.Prefs.Load().LegacyDevTheme {
		y2, done = a.drawICUtilityRowLegacy(clusterX, y2, clusterRight, w, h)
	} else {
		y2, done = a.drawICUtilityRowGrouped(clusterX, y2, clusterRight, w, h)
	}
	if done {
		return // Disconnect requested — skip the rest of this frame (was an inline return)
	}

	// The control-button block ends here. Register it for the editor (its height is
	// content-driven, so measure it now). The grab box spans the LIVE content width —
	// the wrap edge the rows actually used — so the side handles sit where the
	// buttons end after a width resize. def keeps the default full width; its height
	// reflects the live row count (recomputing the default wrap would mean laying the
	// rows out twice — reset just recomputes for real next frame).
	if a.classic.classicEdit {
		a.classic.regSlot(slotControls,
			sdl.Rect{X: clusterX, Y: y, W: clusterRight - clusterX, H: y2 + btnH - y},
			sdl.Rect{X: pad, Y: defY, W: w - 2*pad, H: y2 - dy + btnH - defY})
	}

	// Judge strip (JD grant, or the judge stand when pos-dependent) + the emote grid now
	// follow the control block, which sits BELOW the IC bar (#8). They anchor to the
	// block's UN-MOVED bottom (y2 - dy). The IC bar itself anchors to icBarTop (under the
	// stage), independent of this chain, so the judge strip never pushes the input around.
	postY := y2 - dy + btnH + 6
	if a.judgeVisible() {
		postY += a.drawJudgeRow(pad, postY)
	}

	// IC input row (height follows the Box knob), led by the AO2 text
	// color selector: a swatch previews the active wire color (MS
	// text_color 0–9), the dropdown names it (AO2's color dropdown). The
	// showname box OVERRIDES the Settings showname for the session.
	// stage width (vp.W) DIRECTLY under the stage (icBarTop, #8) — the first thing below
	icBar := sdl.Rect{X: pad, Y: icBarTop, W: vp.W, H: fH}
	rowY := icBar.Y
	// Colour swatch + dropdown — an individually movable slot (#4a). The selector also
	// offers the extended AsyncAO colours (#98) and the two "fun colour" modes (#79): they
	// sit after the palette so they're picked like any colour instead of being buried in
	// Settings. icColorSelected drives the active row + swatch; applyICColorPick routes
	// the pick — both shared with the themed row so the two layouts can't drift.
	colorBox := a.classic.slotRect(slotICColor, sdl.Rect{X: icBar.X, Y: rowY, W: 32 + colorSelectW, H: fH}, w, h)
	a.drawICColorStrip(colorBox) // swatch + colour dropdown (rect by value — alloc-free)
	// The rest of the IC bar (showname → Immediate → Additive → SFX → emoji → FX →
	// text input → muted chip) is one sequential cursor chain, kept together in
	// drawICInputRow. It returns whether Enter was pressed. icBar is passed by
	// value (alloc-free); the chain flows from the DEFAULT positions, never an
	// override, so freeing one slot never cascades the rest (unchanged behaviour).
	// ENTER is the only sender. The shout row ARMS (courtroom.cpp:6048-6127) and
	if a.drawICInputRow(icBar, rowY, w, h, fH) && !a.font.serverNoticeDlg.open {
		a.sendIC()
	}

	// Emote row — a movable/resizable slot (the grid pages within whatever rect it
	// gets, so drag/resize is free). slotRect stays INSIDE the hidden guard so the
	// editor never registers a handle for a grid that isn't drawn. The default is
	// byte-identical to before, so an un-edited courtroom is pixel-identical.
	emoteY := postY // #8: the emote grid follows the control block + judge strip, below the IC bar
	if !a.classic.panelHidden(panelEmotes) {
		emoteDef := sdl.Rect{X: pad, Y: emoteY, W: w - 2*pad, H: h - emoteY - 30}
		a.drawEmoteRow(a.classic.slotRect(slotEmotes, emoteDef, w, h), vp)
	}
	a.drawSpritePreviewFallback(false)

	// OOC row at the very bottom: a FULL-width OOC chat INPUT (no name box — the OOC name
	// lives in the OOC box/tab now; it used to be duplicated here, which is the redundancy
	// a tester flagged). Shown whenever OOC is a tab (Legacy theme, or the opt-in "OOC in the
	// log tab" toggle), as a SECOND always-visible input alongside the tab's own — the hybrid.
	// In the new-default OOC BOX the input lives inside the box, so this bar is dropped.
	oocY := h - fH - 4
	// …and it comes BACK as the fallback when the right column is hidden, because
	// the OOC box lives inside that column (drawCleanRightColumn, guarded by
	// panelLog above). Without this clause the box-by-default layout would leave a
	// user who hides the right column with NO OOC input anywhere — no OOC chat and
	// no way to run a slash command. The invariant is one live OOC input in every
	// state, and only an explicit "hide OOC chat" may take it away.
	oocBoxUnreachable := a.classic.panelHidden(panelLog)
	if !a.classic.panelHidden(panelOOC) && (a.d.Prefs.Load().LegacyDevTheme || a.d.Prefs.Load().OOCInLogTab || oocBoxUnreachable) {
		// The bottom OOC bar is its own movable + resizable slot ("oocbar"): its default spans
		// the bottom row, the row centred within the slot height. Registered only while it
		// actually draws, so the editor offers a handle for it only when OOC is a tab.
		oocBarDef := sdl.Rect{X: pad, Y: oocY, W: w - 3*pad, H: fH}
		oocBar := a.classic.slotRect(slotOOCBar, oocBarDef, w, h)
		oocRowY := oocBar.Y
		if oocBar.H > fH {
			oocRowY = oocBar.Y + (oocBar.H-fH)/2
		}
		oocPrimary, oocEmoji := a.icFieldFonts(a.compose.oocInput) // #M5: emoji/unicode in the OOC bar too
		var sendOOC bool
		a.compose.oocInput, sendOOC = c.TextFieldEmoji("ooc", sdl.Rect{X: oocBar.X, Y: oocRowY, W: oocBar.W, H: fH}, a.compose.oocInput, "OOC chat…  (set your OOC name in the OOC tab)", oocPrimary, oocEmoji)
		if sendOOC {
			a.submitOOC() // shared OOC send: uses your OOC name (or the server default) + clears
		}
		a.recallOOC() // Up/Down recall recently-sent OOC lines when the bar is focused
		// Ctrl+wheel over the OOC row resizes the OOC text (independent of the IC log).
		if c.ctrlHeld && c.wheelY != 0 && c.hovering(sdl.Rect{X: oocBar.X, Y: oocRowY, W: oocBar.W, H: fH}) {
			a.oocPct = clampInt(a.oocPct+int(c.wheelY)*config.ScaleStepPercent,
				config.MinLogScalePercent, config.MaxLogScalePercent)
			a.saveLayout()
		}
	}
	// The shared single-line toast (warnLine) + the DND badge share this row.
	// Missing-asset reports left this channel in #27 (they own a menu-bar chip
	// now — assetmisschip.go); what remains here is ordinary transient feedback.
	// When DND is on, clip the (left-aligned) toast so it can't run under the
	// right-aligned badge — both stay readable. DND off draws as before.
	toastW := w - 2*pad
	dndMsg := ""
	var dndW int32
	if a.dndOn {
		dndMsg = "● Do Not Disturb — alerts muted (click to undo)"
		dndW = c.TextWidth(dndMsg)
		if gap := dndW + 16; gap < toastW {
			toastW -= gap
		} else {
			toastW = 0
		}
	}
	if a.warnActive() {
		c.LabelClipped(pad, oocY-20, toastW, a.warnLine, ColDanger)
	}
	// Do Not Disturb badge: a persistent reminder while alerts are muted, so a
	// silenced callword never reads as "callwords broken". Click it to turn DND
	// off without opening Settings.
	if a.dndOn {
		bx := w - pad - dndW
		c.Label(bx, oocY-20, dndMsg, ColTierYellow)
		if c.clicked && c.hovering(sdl.Rect{X: bx, Y: oocY - 22, W: dndW, H: 18}) {
			a.setDND(false)
			a.warnLine = "Do Not Disturb off."
			a.warnAt = time.Now()
		}
	}

}

// drawICShoutRow draws control-block Row 1 — the shout buttons (Hold It / Objection
// / Take That + the 2.10 custom interjection), the Pair toggle and the live layout
// knobs — from the block origin (clusterX, y). clusterX / y arrive by value and the
// local x cursor never escapes, so the row stays allocation-free (moved verbatim
// from drawICControls).
// standing in for AO2's "*_selected" sprite swap (courtroom.cpp:6062 etc.). It used
// to return a modifier the caller fed straight into sendIC, which is why a click
func (a *App) drawICShoutRow(clusterX, y, w, h int32) {
	c := a.ctx
	x := clusterX
	if !a.classic.panelHidden(panelShouts) {
		shoutW := int32(96)
		// Each shout is individually movable in the layout editor (literal slot keys
		// keep the row alloc-free; the struct is range-only so it stays on the stack).
		shouts := []struct {
			key   string
			label string
			mod   int
		}{
			{"ctrl.holdit", "Hold It!", protocol.ShoutHoldIt},
			{"ctrl.objection", "Objection!", protocol.ShoutObjection},
			{"ctrl.takethat", "Take That!", protocol.ShoutTakeThat},
		}
		for _, s := range shouts {
			if a.classic.panelHidden(s.key) {
				continue // individually hideable (the row compacts); the group toggle still hides all
			}
			// slotRect ONCE and reuse it, rather than movableButton plus a second
			// lookup: the armed overlay has to land on the rect the button actually
			// DREW at (the layout editor can have moved it), and asking twice repeats
			// the editor's slot bookkeeping for nothing. This is movableButton inlined.
			shoutRect := a.classic.slotRect(s.key, sdl.Rect{X: x, Y: y, W: shoutW, H: btnH}, w, h)
			if c.Button(shoutRect, s.label) {
				a.toggleShout(s.mod)
			}
			a.drawArmedOverlay(shoutRect, a.compose.icShout == s.mod)
			x += shoutW + 6
		}
		// Custom interjection (2.10): the button ARMS the active pick;
		// the ▾ cycler steps base custom → each named [Shouts] entry.
		// Only for characters that actually ship one (hasCustomShout).
		if a.link.sess.Features.Has(protocol.FeatureCustomObjections) && a.hasCustomShout() {
			label := a.customShoutLabel()
			bw := c.TextWidth(label) + 16
			customRect := sdl.Rect{X: x, Y: y, W: bw, H: btnH}
			if c.Button(customRect, label) {
				a.toggleShout(protocol.ShoutCustom)
			}
			a.drawArmedOverlay(customRect, a.compose.icShout == protocol.ShoutCustom)
			x += bw + 4
			if len(a.compose.customShouts) > 0 {
				if c.Button(sdl.Rect{X: x, Y: y, W: 26, H: btnH}, "▾") {
					// −1 (base) → 0 → … → len−1 → −1
					a.compose.customIdx++
					if a.compose.customIdx >= len(a.compose.customShouts) {
						a.compose.customIdx = -1
					}
				}
				x += 32
			}
		}
	}
	if !a.classic.panelHidden("ctrl.pair") { // hideable: hiding compacts, like ctrlSlot
		if a.classic.movableButton(&a.env, "ctrl.pair", sdl.Rect{X: x, Y: y, W: 70, H: btnH}, "Pair...", w, h) {
			a.pairbox.showPair = !a.pairbox.showPair
		}
		x += 80
	}
	if !a.classic.panelHidden(panelKnobs) && !a.d.Prefs.Load().DragLayout { // drag-resize mode hides the +/− knobs
		if a.d.Prefs.Load().ViewportExactW == 0 { // exact-px sizing shadows vpPct, so the View knob would no-op — hide it (set the size in Settings instead)
			x = a.scaleControl(x, y, "View", &a.vpPct, config.ViewportStepPercent, config.MinViewportPercent, config.MaxViewportPercent)
		}
		x = a.scaleControl(x, y, "Text", &a.chatPct, config.ScaleStepPercent, config.MinChatScalePercent, config.MaxChatScalePercent)
		x = a.scaleControl(x, y, "MsgBox", &a.boxPct, config.ScaleStepPercent, config.MinChatBoxPercent, config.MaxChatBoxPercent)
		x = a.scaleControl(x, y, "Log", &a.logPct, config.ScaleStepPercent, config.MinLogScalePercent, config.MaxLogScalePercent)
		_ = a.scaleControl(x, y, "Input", &a.inputPct, config.ScaleStepPercent, config.MinInputPercent, config.MaxInputPercent)
	}
}

// drawICUtilityRowLegacy draws control-block Row 2 in the legacy-dev-theme layout
// (the flat, ungrouped button run). It threads the row cursor y2 (which wraps to a
// fresh row when the trailing buttons wouldn't fit) in and out by value, and returns
// done=true when a Disconnect click already fired requestDisconnect so the caller
// short-circuits the frame exactly as the old inline `return` did (moved verbatim).
func (a *App) drawICUtilityRowLegacy(clusterX, y2, clusterRight, w, h int32) (int32, bool) {
	c := a.ctx
	x := clusterX
	if c.Button(sdl.Rect{X: x, Y: y2, W: 100, H: btnH}, "Character") {
		// Back to char select; the session stays, the server re-picks via
		// CC → PV and EventCharPicked rebuilds the courtroom.
		a.screen = ScreenCharSelect
	}
	x += 106
	// Wardrobe (iniswap): accent + tooltip so it's easy to spot.
	wr := sdl.Rect{X: x, Y: y2, W: 90, H: btnH}
	if c.Button(wr, "Wardrobe") {
		a.openIniswap()
	}
	c.Border(wr, ColAccent)
	c.Tooltip(wr, "Wardrobe / iniswap — swap your character's sprites & emotes")
	x += 96
	if c.Button(sdl.Rect{X: x, Y: y2, W: 100, H: btnH}, "Background") {
		a.openBgPicker()
	}
	x += 106
	if c.Button(sdl.Rect{X: x, Y: y2, W: 90, H: btnH}, "Settings") {
		a.prevScreen = ScreenCourtroom
		a.screen = ScreenSettings
	}
	x += 96
	if c.Button(sdl.Rect{X: x, Y: y2, W: 80, H: btnH}, "About") {
		a.prevScreen = ScreenCourtroom
		a.screen = ScreenAbout
	}
	x += 86
	if c.Button(sdl.Rect{X: x, Y: y2, W: 110, H: btnH}, "Disconnect") {
		a.requestDisconnect() // confirm first unless instant-disconnect is set
		return y2, true
	}
	x += 116
	evLabel := "Evidence"
	if a.evid.evidPresent {
		evLabel = "Evidence ●" // armed: next IC message presents it
	}
	if c.Button(sdl.Rect{X: x, Y: y2, W: 100, H: btnH}, evLabel) {
		a.toggleEvidence()
	}
	x += 106
	if c.Button(sdl.Rect{X: x, Y: y2, W: 80, H: btnH}, "Mods...") {
		a.courtxtra.showModcall = true
	}
	x += 86
	if c.Button(sdl.Rect{X: x, Y: y2, W: 70, H: btnH}, "Login...") {
		a.openLoginDialog()
	}
	x += 76
	// "UI..." retired (A1): show/hide now lives on the bottom-right toolbox
	// (Hide-UI chip → pinned per-piece panel) and hotkeyUIChrome.
	x = a.drawPosSelect(x, y2, btnH)
	// "Hotkeys" (#96) + "Restyle" (#103/#104) are the trailing convenience buttons,
	// appended after Pos so no existing button shifts. At a narrow window width the
	// long Row 2 would push the PAIR off the right edge, so wrap them down to a
	// fresh row when they wouldn't fit. Everything below keys off y2 (icY = y2 +
	// btnH + …), so bumping it in place drops the IC area and judge strip with it.
	const keysW, styleW, btnGap int32 = 90, 84, 6
	if x+keysW+btnGap+styleW > clusterRight {
		y2 += btnH + 4
		x = clusterX
	}
	keysR := sdl.Rect{X: x, Y: y2, W: keysW, H: btnH}
	if c.Button(keysR, "Hotkeys") {
		a.openHotkeyCheatSheet()
	}
	c.Tooltip(keysR, "Show all your hotkeys & custom binds (also F1)")
	x += keysW + btnGap
	styleR := sdl.Rect{X: x, Y: y2, W: styleW, H: btnH}
	if c.Button(styleR, "Restyle") {
		a.openSpriteStyle()
	}
	c.Tooltip(styleR, "Recolour / glow your character on the fly — other AsyncAO players see it")
	x += styleW + btnGap

	// Mod / CM launchers, in the button row so they never float over the emote
	// grid. Wrap to a fresh row as a group if they wouldn't fit. ("Edit Layout"
	// retired here (A1): it's the Edit chip on the bottom-right toolbox now.)
	modW, cmW := int32(0), int32(0)
	if a.amIMod() {
		modW = 54
	}
	if a.srvMeta.amICMNow {
		cmW = 46
	}
	// Only wrap when the group actually draws something (else an empty Mod/CM row
	// would push the IC area down for a plain user with no role).
	if modW+cmW > 0 && x+modW+cmW+btnGap > clusterRight {
		y2 += btnH + 4
		x = clusterX
	}
	if modW > 0 {
		mR := sdl.Rect{X: x, Y: y2, W: modW, H: btnH}
		if c.Button(mR, "Mod") {
			a.toggleModDash()
		}
		c.Border(mR, ColDanger)
		c.Tooltip(mR, "Moderation tools — server-aware ban / kick")
		x += modW + btnGap
	}
	if cmW > 0 {
		cR := sdl.Rect{X: x, Y: y2, W: cmW, H: btnH}
		if c.Button(cR, "CM") {
			a.toggleCMPanel()
		}
		c.Border(cR, chipCMColor)
		c.Tooltip(cR, "CM area controls — lock / kick-from-area")
		x += cmW + btnGap
	}
	// Group Chat / DMs launcher — a MAIN button (default ON; Settings → Chat to hide).
	// Appended last so it can't shift the buttons before it; skipped (no x advance) off.
	if a.d.Prefs.Load().GroupChatButton && !a.classic.panelHidden("ctrl.groupchat") {
		const gcW = int32(96)
		if x+gcW > clusterRight {
			y2 += btnH + 4
			x = clusterX
		}
		gcR := sdl.Rect{X: x, Y: y2, W: gcW, H: btnH}
		if c.Button(gcR, "Group Chat") {
			a.msgs.toggleMessages()
		}
		c.Border(gcR, ColAccent)
		c.Tooltip(gcR, "Private DMs & group chats with other AsyncAO players (also Extras → Group Chat)")
		x += gcW + btnGap
	}
	// Voice button — only in a voice-enabled area (VS_CAPS / Nyathena); appears
	// when you enter a VC room, hidden otherwise. Appended; no x advance when off.
	if a.voiceOfferable() && !a.classic.panelHidden("ctrl.voice") {
		const vcW = int32(80)
		if x+vcW > clusterRight {
			y2 += btnH + 4
			x = clusterX
		}
		vcLabel, vcBord := a.voiceButtonState()
		vcR := sdl.Rect{X: x, Y: y2, W: vcW, H: btnH}
		if c.Button(vcR, vcLabel) {
			a.toggleVoice()
		}
		c.Border(vcR, vcBord)
		c.Tooltip(vcR, "Voice chat — this area supports it. Join to talk (Nyathena). Red dot = your mic is live.")
		// No cursor advance: this is the row's last block and nothing reads x
		// after it (staticcheck SA4006). A button appended below adds its own.
	}
	return y2, false
}

// drawICUtilityRowGrouped draws control-block Row 2 in the grouped-default layout
// (buttons clustered by purpose — Character · Scene · Moderation · System — with a
// wider gap between groups and Disconnect set apart at the end). Like the legacy
// variant it threads the wrapping row cursor y2 by value and returns done=true when
// a Disconnect click already fired requestDisconnect (moved verbatim).
func (a *App) drawICUtilityRowGrouped(clusterX, y2, clusterRight, w, h int32) (int32, bool) {
	c := a.ctx
	x := clusterX
	// New default: the same buttons grouped by purpose (Character · Scene · Moderation · System),
	// a wider gap between groups, the leave button set apart at the end. Inline (no closures) so
	// the row stays alloc-free.
	const gGap = int32(16) // gap between groups
	evLabel := "Evidence"
	if a.evid.evidPresent {
		evLabel = "Evidence ●"
	}
	// — Character — (each button hideable via UI… → Buttons; hidden ones compact away)
	if r, ok := a.classic.ctrlSlot(&x, y2, 100, 106, w, h, "ctrl.character"); ok {
		if c.Button(r, "Character") {
			a.screen = ScreenCharSelect
		}
	}
	if r, ok := a.classic.ctrlSlot(&x, y2, 90, 96, w, h, "ctrl.wardrobe"); ok {
		if c.Button(r, "Wardrobe") {
			a.openIniswap()
		}
		c.Border(r, ColAccent)
		c.Tooltip(r, "Wardrobe / iniswap — swap your character's sprites & emotes")
	}
	if r, ok := a.classic.ctrlSlot(&x, y2, 84, 84+gGap, w, h, "ctrl.restyle"); ok {
		if c.Button(r, "Restyle") {
			a.openSpriteStyle()
		}
		c.Tooltip(r, "Recolour / glow your character on the fly — other AsyncAO players see it")
	}
	// — Scene —
	if r, ok := a.classic.ctrlSlot(&x, y2, 100, 106, w, h, "ctrl.background"); ok {
		if c.Button(r, "Background") {
			a.openBgPicker()
		}
	}
	if r, ok := a.classic.ctrlSlot(&x, y2, 100, 106, w, h, "ctrl.evidence"); ok {
		if c.Button(r, evLabel) {
			a.toggleEvidence()
		}
	}
	x = a.drawPosSelect(x, y2, btnH)
	x += gGap
	// — Moderation —
	if r, ok := a.classic.ctrlSlot(&x, y2, 80, 86, w, h, "ctrl.mods"); ok {
		if c.Button(r, "Mods...") {
			a.courtxtra.showModcall = true
		}
	}
	if a.amIMod() {
		mR := a.classic.slotRect("ctrl.mod", sdl.Rect{X: x, Y: y2, W: 54, H: btnH}, w, h)
		if c.Button(mR, "Mod") {
			a.toggleModDash()
		}
		c.Border(mR, ColDanger)
		c.Tooltip(mR, "Moderation tools — server-aware ban / kick")
		x += 60
	}
	if a.srvMeta.amICMNow {
		cR := a.classic.slotRect("ctrl.cm", sdl.Rect{X: x, Y: y2, W: 46, H: btnH}, w, h)
		if c.Button(cR, "CM") {
			a.toggleCMPanel()
		}
		c.Border(cR, chipCMColor)
		c.Tooltip(cR, "CM area controls — lock / kick-from-area")
		x += 52
	}
	x += gGap
	// — System — wrap to a fresh row as a block if it would run off the right edge.
	// ("UI..." + "Edit Layout" retired (A1): show/hide + the editor live on the
	// bottom-right toolbox now, so the System block is Settings/Hotkeys/About/Login.)
	if x+90+96+86+76 > clusterRight {
		y2 += btnH + 4
		x = clusterX
	}
	if r, ok := a.classic.ctrlSlot(&x, y2, 90, 96, w, h, "ctrl.settings"); ok {
		if c.Button(r, "Settings") {
			a.prevScreen = ScreenCourtroom
			a.screen = ScreenSettings
		}
	}
	if r, ok := a.classic.ctrlSlot(&x, y2, 90, 96, w, h, "ctrl.hotkeys"); ok {
		if c.Button(r, "Hotkeys") {
			a.openHotkeyCheatSheet()
		}
		c.Tooltip(r, "Show all your hotkeys & custom binds (also F1)")
	}
	if r, ok := a.classic.ctrlSlot(&x, y2, 80, 86, w, h, "ctrl.about"); ok {
		if c.Button(r, "About") {
			a.prevScreen = ScreenCourtroom
			a.screen = ScreenAbout
		}
	}
	// Login / account button: once you've saved an account for this server it shows
	// your username (left-click views your profile via /account); otherwise it's the
	// plain "Login..." that opens the dialog. Right-click always opens the dialog (log
	// in / switch / re-login). Account name is per-server (a.serverKey), so it's
	// correct across multi-server tabs.
	acct := strings.TrimSpace(a.d.Prefs.Load().ServerWarmInfoFor(a.srvMeta.serverKey).LoginUser)
	loginLabel, loginW := "Login...", int32(70)
	if acct != "" {
		loginLabel, loginW = acct, c.TextWidth(acct)+18
	}
	if loginR, ok := a.classic.ctrlSlot(&x, y2, loginW, loginW+6, w, h, "ctrl.login"); ok {
		if c.Button(loginR, loginLabel) {
			if acct != "" {
				a.link.sess.SendOOC(a.oocNameOrDefault(), "/account") // view your profile
			} else {
				a.openLoginDialog()
			}
		}
		if acct != "" {
			if c.rightClicked && c.hovering(loginR) {
				a.openLoginDialog() // re-login / switch account
			} else if c.hovering(loginR) { // build the hint string only on hover (0-alloc otherwise)
				c.Tooltip(loginR, "Logged in as "+acct+" — click: view profile (/account) · right-click: log in / switch")
			}
		}
	}
	// Group Chat / DMs launcher — a MAIN button (default ON; Settings → Chat to hide).
	// APPENDED at the end of the functional buttons so it can never shift the ones
	// before it; when off it's skipped entirely (no x advance), so the row is
	// byte-identical to before. A movable slot, so Edit Layout can reposition it.
	if a.d.Prefs.Load().GroupChatButton && !a.classic.panelHidden("ctrl.groupchat") {
		const gcW = int32(96)
		if x+gcW > clusterRight {
			y2 += btnH + 4
			x = clusterX
		}
		gcR := a.classic.slotRect("ctrl.groupchat", sdl.Rect{X: x, Y: y2, W: gcW, H: btnH}, w, h)
		if c.Button(gcR, "Group Chat") {
			a.msgs.toggleMessages()
		}
		c.Border(gcR, ColAccent)
		c.Tooltip(gcR, "Private DMs & group chats with other AsyncAO players (also Extras → Group Chat)")
		x += gcW + 6
	}
	// Voice button — appears ONLY in an area that advertises voice (VS_CAPS, i.e.
	// Nyathena), so it shows up when you move into a VC-supported room and hides
	// elsewhere. Appended like the others; skipped (no x advance) when unavailable.
	if a.voiceOfferable() && !a.classic.panelHidden("ctrl.voice") {
		const vcW = int32(80)
		if x+vcW > clusterRight {
			y2 += btnH + 4
			x = clusterX
		}
		vcLabel, vcBord := a.voiceButtonState()
		vcR := a.classic.slotRect("ctrl.voice", sdl.Rect{X: x, Y: y2, W: vcW, H: btnH}, w, h)
		if c.Button(vcR, vcLabel) {
			a.toggleVoice()
		}
		c.Border(vcR, vcBord)
		c.Tooltip(vcR, "Voice chat — this area supports it. Join to talk (Nyathena). Red dot = your mic is live.")
		x += vcW + 6
	}
	// — Leave — set apart at the end.
	x += gGap
	if x+110 > clusterRight {
		y2 += btnH + 4
		x = clusterX
	}
	if !a.classic.panelHidden("ctrl.disconnect") { // hideable: Esc still leaves the server
		if a.classic.movableButton(&a.env, "ctrl.disconnect", sdl.Rect{X: x, Y: y2, W: 110, H: btnH}, "Disconnect", w, h) {
			a.requestDisconnect()
			return y2, true
		}
		x += 116
	}
	return y2, false
}

// drawICColorStrip draws the IC text-colour selector (the swatch previewing the
// active wire colour + the naming dropdown) inside its resolved slot rect. colorBox
// arrives by value; icColorSelected drives the active row + swatch and
// applyICColorPick routes the pick — both shared with the themed row so the two
// layouts can't drift (moved verbatim from drawICControls).
func (a *App) drawICColorStrip(colorBox sdl.Rect) {
	c := a.ctx
	// The pieces fill the SLOT rect: a resize genuinely widens the
	// dropdown / grows the row height instead of being silently ignored.
	swatch := sdl.Rect{X: colorBox.X, Y: colorBox.Y, W: 26, H: colorBox.H}
	icSel, sw := a.icColorSelected()
	c.Fill(swatch, sw)
	c.Border(swatch, ColPanelHi)
	a.lookUI.icSwatchRect = swatch // the free-hex wheel anchors here
	if a.compose.icCustomOn && c.clicked && c.hovering(swatch) {
		a.lookUI.showICColorWheel = !a.lookUI.showICColorWheel // re-open to adjust (re-picking "Custom…" in the dropdown doesn't fire changed)
	}
	colorDDW := colorBox.W - 32
	if colorDDW < 60 {
		colorDDW = 60 // floor: the dropdown stays clickable however small the slot is dragged
	}
	// Freeze the IC field's selection BEFORE the dropdown so a picked colour can
	// wrap it (§3.8 select-and-colour, folded into the dropdown to match AO2's own
	// on_text_color_changed). The open-click unfocuses the field later this frame
	// (the strip draws before the field), so by the pick frame the live selection
	// is gone — captureICColorSel snapshots it while it still exists.
	a.captureICColorSel()
	if next, changed := c.Dropdown(icColorDDID, sdl.Rect{X: colorBox.X + 32, Y: colorBox.Y, W: colorDDW, H: colorBox.H}, icColorChoices, icSel); changed {
		a.applyICColorPick(next)
	}
}

// icOptionalDraws decides whether one width-guarded optional IC-bar button (Pre,
// SFX, emoji, FX) draws this frame. A hidden piece never draws (hidden beats
// everything). Otherwise it draws when the narrow-bar width guard passes OR the
// user saved a slot override for it OR the classic editor is armed — so a button
// the crammed row would drop is still reachable: an override forces it in (the
// movable-slot promise), and edit mode always shows it so a dropped button can be
// grabbed and pulled out to somewhere with room. With no override and not editing
// this collapses to exactly the historical `!hidden && guardOK`, so an un-edited
// narrow bar stays byte-identical. Pure func (value params, no closure) so the
// drop discipline is unit-testable off the SDL path.
func icOptionalDraws(guardOK, hasOverride, editing, hidden bool) bool {
	return !hidden && (guardOK || hasOverride || editing)
}

// icBarButtonFits is the pure narrow-bar width guard for one optional IC-bar button
// drawn left-to-right: given the bar's total width, the width already consumed by
// everything to this button's left, this button's full demand (its own width PLUS the
// gap that follows it), and the tail reserve the input floor needs, it reports whether
// the button still leaves room. Because the row draws in PRIORITY order (Pre, FX, then
// the SFX dropdown, then emoji) and each button advances the cursor only when it fits,
// the LATER buttons (SFX, emoji) are the first to fail this guard on a narrow bar —
// which is exactly the intended sacrifice order. Factored out (value params, no closure)
func icBarButtonFits(barW, used, demand, tailReserve int32) bool {
	return barW-used-demand >= tailReserve
}

// IC-bar optional-button widths (package-level so the row and its layout tests share
// one source of truth — rule 9). preW was trimmed from 60 (redesign) so the
// default-visible Pre + Text-FX pair fits the 720p classic bar with margin: at the
// 1280×720 default the bar is 666px and 372px is consumed before the optionals, leaving
// 666-372-minICInputW(150) = 144px; Pre(preW+6=58) + FX(fxBtnW+4=78) = 136 <= 144, an
// 8px margin. See drawICInputRow's drop-order note.
const (
	minICInputW  = 150 // the IC text input never shrinks below this (the tail reserve / floor)
	shownameBoxW = 140 // the per-session showname box width
	immedW       = 96  // "Immediate" toggle width (fits the full label without truncating)
	icAddW       = 84  // "Additive" toggle width (2.8 servers only; +icAddW+6 to the pre-optional band)
	// preW fits the "Pre" checkbox (16px box + 6px gap + ~26px "Pre" chrome-font label ≈
	// 48px visual) with breathing room, and keeps the Pre+FX pair inside the 720p bar.
	preW   = 52
	sfxDDW = 92 // SFX dropdown width
	// effectsDDW sizes the AO2 screen-effect picker (ui_effects_dropdown). Sized
	// like sfxDDW because it holds the same class of value (a short effect name);
	// AO2's own stock rect is 89x22 (ao2defaultrects.go), so this is that width
	// rounded to the classic row's scale. Drawn LAST of the optional controls
	// before the emoji button, so it yields to Pre/FX/SFX on a narrow bar and only
	// the emoji button yields before it. It draws at ALL only when the roster is
	// non-empty and you are not spectating (AO2 courtroom.cpp:5586-5598), so a
	// server/theme with no effects.ini costs the bar nothing.
	effectsDDW = 92
	// sfxCompactW is the collapsed SFX picker: a fixed-width button shown where the
	// full sfxDDW dropdown no longer fits, so the feature never silently VANISHES on
	// a narrow bar (users concluded it had been removed). It opens the SAME choice
	// list (the SFX Browser). Labelled "SFX" (the chrome font renders it reliably,
	// unlike a ♪ glyph). It occupies the same drop-discipline slot as the full
	// dropdown — after Flip, before emoji — so only WHAT renders changes at the
	// collapse width, never the survival order.
	sfxCompactW = 40
	// icFlipW sizes the "Flip" checkbox slot the SAME way the other IC-bar checkboxes size
	// theirs — box(16) + gap(6) + the label's chrome-font width — with a matching sliver of
	// slack so the label never kisses the slot edge (icAddW=84 for "Additive", immedW=96 for
	// "Immediate"; per-char those leave ~1–2px past the glyphs). "Flip" is 4 chars ≈ 32px, so
	// 16+6+32 = 54 was flush to the last pixel and read as squished next to its neighbour; 60
	// restores the same breathing room the wider labels get. Flip draws unconditionally (no width
	// guard) whenever the server advertises flipping — matching AO2-Client's ui_flip which also
	// has no width guard (courtroom.cpp:1629-1636). It sits after Pre+FX but before SFX/emoji in
	// the cursor chain; at very narrow bars SFX/emoji may drop to make room, but Flip itself
	// never drops.
	icFlipW = 60
)

// drawICInputRow draws the IC bar's input chain — showname box (+ saved-name picker),
// the Immediate and Additive toggles, then the Pre + Text-FX core buttons and the SFX
// picker + emoji button (drop-order note inside), the IC text field itself and the muted
// chip — flowing left-to-right from the DEFAULT positions so freeing one slot in the
// editor never cascades the rest. icBar arrives by value; it returns whether Enter was
// pressed so the caller keeps the send decision visible (moved verbatim from drawICControls).
func (a *App) drawICInputRow(icBar sdl.Rect, rowY, w, h, fH int32) (send bool) {
	c := a.ctx
	// shownameBoxW / immedW are package-level consts now (shared with the layout tests).
	nameX := icBar.X + 32 + colorSelectW + 6 // DEFAULT spot; downstream (immedX) flows from here
	namePlaceholder := a.d.Prefs.Load().Showname
	if namePlaceholder == "" {
		namePlaceholder = "Showname"
	}
	a.ensureNameOpts()
	// Showname box (+ saved-name picker) — an individually movable slot (#4a). Draws at
	// the slot; the row cursor below flows from the DEFAULT nameX so moving it never
	// cascades the rest.
	nameBox := a.classic.slotRect(slotICShowname, sdl.Rect{X: nameX, Y: rowY, W: shownameBoxW, H: fH}, w, h)
	// fix ("the showname box is not resizable — it has the ui for it but
	// it does nothing"): the field fills the SLOT'S live width/height, not the
	// fixed default, so the editor's resize handles actually take effect.
	snW, snDD := nameBox.W, int32(0)
	if len(a.budget.nameOpts) > 1 { // a tiny ▾ saved-name picker, fitted INSIDE the box width so nothing downstream shifts
		snDD = 22
		snW -= snDD + 2
	}
	if snW < 40 {
		snW = 40 // floor: never collapse the field into an unclickable sliver
	}
	// CJK / non-Latin shownames must render as real glyphs while you TYPE them (a
	// Japanese showname is first-class), not the chrome font's tofu — the same
	// TextFieldEmoji + icFieldFonts treatment the IC input below uses. Plain-ASCII
	// names keep the byte-identical single-font fast path (icFieldFonts is nil for
	// ASCII). The placeholder is a saved showname, which can itself be CJK.
	snPrimary, snEmoji := a.icFieldFonts(a.compose.shownameOverride)
	if a.compose.shownameOverride == "" {
		snPrimary, snEmoji = a.icFieldFonts(namePlaceholder) // route a CJK placeholder too
	}
	a.compose.shownameOverride, _ = c.TextFieldEmoji("icshownameov", sdl.Rect{X: nameBox.X, Y: nameBox.Y, W: snW, H: nameBox.H}, a.compose.shownameOverride, namePlaceholder, snPrimary, snEmoji)
	if name := a.pickNameDropdown("snpick", sdl.Rect{X: nameBox.X + snW + 2, Y: nameBox.Y, W: snDD, H: nameBox.H}); name != "" {
		a.compose.shownameOverride = name
	}
	// Immediate (AO non-interrupting preanim): the preanim plays without
	// holding back the text. Session toggle; rides the next message via
	// OutgoingMS.Immediate. Vertically centered against the fH-tall inputs.
	// (immedW is a package-level const, shared with the layout tests.)
	immedX := nameX + shownameBoxW + 6
	// Slice 1 of the movable IC bar: the Immediate toggle can be pulled out into its
	// own spot in the classic editor. Its default rides the bar (un-edited / whole-bar
	// move stays pixel-identical), and everything downstream keeps flowing from the
	// DEFAULT immedX below — never the override — so freeing Immediate doesn't cascade
	// the rest of the row. slotRect is alloc-free off the edit path.
	immedDef := sdl.Rect{X: immedX, Y: rowY, W: immedW, H: fH}
	immedBox := a.classic.slotRect(slotICImmediate, immedDef, w, h)
	a.compose.icImmediate = c.Checkbox(immedBox.X, immedBox.Y+(immedBox.H-16)/2, "Immediate", a.compose.icImmediate)
	c.Tooltip(immedBox, "Immediate: the preanim plays without holding back the text (non-interrupting preanim)")
	icX := immedX + immedW + 6 // downstream flows from the DEFAULT position, not the override
	// Additive (#14, 2.8): the next message APPENDS to your last one (narration RP).
	// Shown only when the server advertises additive AND the master pref is on (AO2
	// shows ui_additive whenever the server advertises it, courtroom.cpp:1638-1644).
	// When hidden the toggle is forced off so a stale check can't ride a message.
	if a.link.sess != nil && a.link.sess.Features.Has(protocol.FeatureAdditive) && a.d.Prefs.Load().AdditiveText {
		// icAddW ("Additive") is a package-level const, shared with the layout tests.
		// Movable slot (#4a), same wrap-not-extract rule as Immediate above: draws through
		// slotRect so the editor can pull it out into its own spot, but the row cursor still
		// advances by the DEFAULT width below — so an un-edited bar is pixel-identical and
		// moving Additive never cascades the rest. slotRect is alloc-free off the edit path.
		addDef := sdl.Rect{X: icX, Y: rowY, W: icAddW, H: fH}
		addBox := a.classic.slotRect(slotICAdditive, addDef, w, h)
		// bounceOnToggle: ui_additive → on_additive_clicked → focus_ic_input
		// (courtroom.cpp:504, :6572). The leading-space half of that handler lives at
		// the send site (AdditiveWantsLeadingSpace), not here.
		a.compose.icAdditive = a.bounceOnToggle(c.Checkbox(addBox.X, addBox.Y+(addBox.H-16)/2, "Additive", a.compose.icAdditive), a.compose.icAdditive)
		c.Tooltip(addBox, "Additive: this message adds to your last one instead of replacing it (2.8 narration-style RP).")
		icX += icAddW + 6 // downstream flows from the DEFAULT position, not the override
	} else {
		a.compose.icAdditive = false
	}
	icCounterOn := a.d.Prefs.Load().MessageCounter
	// The IC input must always keep at least minICInputW. Each optional IC-bar button is
	// placed ONLY if it still leaves that room, so a narrow bar drops them in REVERSE draw
	// order — emoji first, then SFX, then FX, then Pre — instead of pushing the text input
	// off the edge. Regression guard: an earlier extra button collapsed the input to zero
	// width for users with a narrower stage ("the IC bar disappeared"). Widths key off the
	// slot (icBar.W), so the same guard holds after the bar is moved/resized. Inlined (no
	// closure) to keep this per-frame row allocation-free.
	tailReserve := int32(minICInputW)
	// "Pre" toggle (AO2-Client ui_pre): send the selected emote's preanimation.
	// AUTO-FOLLOWS each emote pick (selectEmote), and the user can override until
	// the next pick. Unchecked forces idle even when the emote defines a preanim
	// (NormalizeOutgoingEmoteMod at the send site). Placed right after Immediate/
	// Additive but INSIDE the narrow-bar drop discipline (the tailReserve guard),
	// so on a tight bar it drops instead of shoving the IC input off the edge —
	// Immediate/Additive draw unconditionally, so an always-drawn Pre beside them
	// would have regressed that guard. Movable slot (#4a), same wrap-not-extract
	// rule: draws through slotRect, the row cursor advances by the DEFAULT width so
	// an un-edited bar is pixel-identical and moving it never cascades the rest.
	_, preOv := a.classic.classicOv[slotICPre]
	preGuardOK := icBarButtonFits(icBar.W, icX-icBar.X, preW+6, tailReserve)
	if icOptionalDraws(preGuardOK, preOv, a.classic.classicEdit, a.classic.panelHidden(slotICPre)) {
		preBox := a.classic.slotRect(slotICPre, sdl.Rect{X: icX, Y: rowY, W: preW, H: fH}, w, h)
		// bounceOnToggle: ui_pre is wired straight to focus_ic_input (courtroom.cpp:502).
		a.compose.icPreanim = a.bounceOnToggle(c.Checkbox(preBox.X, preBox.Y+(preBox.H-16)/2, "Pre", a.compose.icPreanim), a.compose.icPreanim)
		c.Tooltip(preBox, "Pre: play this emote's pre-animation before the line. Follows each emote you pick; uncheck to skip an emote's intro.")
		icX += preW + 6 // downstream flows from the DEFAULT position, not the override
	}
	// #M5: dedicated Text FX cycle button (Off → Shake → Wave → Rainbow) — movable (#4a).
	_, fxOv := a.classic.classicOv[slotICFx]
	fxGuardOK := icBarButtonFits(icBar.W, icX-icBar.X, fxBtnW+4, tailReserve)
	if icOptionalDraws(fxGuardOK, fxOv, a.classic.classicEdit, false) {
		a.fxButton(a.classic.slotRect(slotICFx, sdl.Rect{X: icX, Y: rowY, W: fxBtnW, H: fH}, w, h))
		icX += fxBtnW + 4
	}
	// "Flip" toggle (AO2-Client ui_flip): mirror your character's emotes for this and
	// following messages. AO2 shows ui_flip on the IC bar whenever the server advertises
	// the FLIPPING feature (courtroom.cpp:1629-1636, tooltip courtroom.cpp:1087). This is
	// ONE bool, TWO views: it mirrors a.pairFlip (the Pair panel's "Flip my sprite"
	// checkbox), so whichever view draws last just rewrites the same field with the same
	// value — Checkbox returns the current value when unclicked, so the two can't drift
	// (single source of truth: a.pairFlip; no new state, no handler). The pair-panel
	// checkbox stays unconditional; this one is feature-gated because AO2 hides ui_flip on
	// non-flipping servers. Placed here (after Pre+FX, before SFX/emoji) in a movable slot
	// (#4a). pairFlip is SESSION state that already parks/resets per tab,
	// so — unlike icAdditive in the themed row — it needs NO forced-off reset when the feature
	// vanishes: the field is real pair state, and a stale flip simply stops being SHOWN.
	flipInChain := a.link.sess != nil && a.link.sess.Features.Has(protocol.FeatureFlipping)
	if flipInChain && !a.classic.panelHidden(slotICFlip) {
		flipBox := a.classic.slotRect(slotICFlip, sdl.Rect{X: icX, Y: rowY, W: icFlipW, H: fH}, w, h)
		// bounceOnToggle: ui_flip → focus_ic_input (courtroom.cpp:503). Only the IC-bar
		// view bounces; the Pair panel's mirror of the same bool is not an IC-bar
		// control, and a redraw carrying the value the other view just wrote compares
		// equal here, so the two views cannot bounce each other.
		a.pairbox.pairFlip = a.bounceOnToggle(c.Checkbox(flipBox.X, flipBox.Y+(flipBox.H-16)/2, "Flip", a.pairbox.pairFlip), a.pairbox.pairFlip)
		c.Tooltip(flipBox, "Flip: mirror your character's emotes. Same setting as the Pair panel's flip toggle.")
		icX += icFlipW + 6 // downstream flows from the DEFAULT position, not the override
	}
	// SFX picker (AO2-style): pick a sound to ride your NEXT message — overrides the
	// emote's own sound until set back to "auto". Picking one previews it. Drawn AFTER
	// Pre/FX (redesign), so on a narrow bar it yields BEFORE them — SFX is no
	// longer a survives-longest core button; Pre and FX outrank it now.
	a.ensureSFXChoices()
	// Override / editor win over the drop (same rule as Pre above). SFX has no
	// panelHidden gate in the classic row (it never had one) — keep it that way: pass
	// hidden=false so this stays a pure width/override/edit decision.
	_, sfxOv := a.classic.classicOv[slotICSFX]
	sfxGuardOK := icBarButtonFits(icBar.W, icX-icBar.X, sfxDDW+4, tailReserve)
	sfxCompactGuardOK := icBarButtonFits(icBar.W, icX-icBar.X, sfxCompactW+4, tailReserve)
	switch {
	case icOptionalDraws(sfxGuardOK, sfxOv, a.classic.classicEdit, false):
		sfxRect := a.classic.slotRect(slotICSFX, sdl.Rect{X: icX, Y: rowY, W: sfxDDW, H: fH}, w, h) // movable (#4a)
		if next, changed := c.Dropdown("sfxdd", sfxRect, a.emotepick.sfxChoices, a.emotepick.sfxChoiceIdx); changed {
			a.applySFXPick(next) // pick + audition + focus bounce (icsfx.go; courtroom.cpp:5498-5502)
		}
		a.sfxRowRightClickPreview() // right-click auditions a row without picking it
		c.TooltipAfter("sfxdd-tip", sfxRect, "Sound for your NEXT message — 'auto' uses the emote's own sound, or pick one to override. Picking previews it; right-click a row to hear it without picking. Extras → SFX Browser for favourites & any sound by name.")
		icX += sfxDDW + 4
	case sfxCompactGuardOK:
		// Compact fallback: the full dropdown didn't fit but this narrow button does.
		// It opens the SFX Browser (the same choice list the dropdown offers). A '•'
		// dot marks an active override so the collapsed control still signals "not auto".
		compactRect := sdl.Rect{X: icX, Y: rowY, W: sfxCompactW, H: fH}
		label := "SFX"
		if a.emotepick.sfxChoiceIdx > 0 {
			label = "SFX•" // an override is active (not the "auto" default)
		}
		if c.Button(compactRect, label) {
			a.toggleSfxBrowser()
		}
		c.TooltipAfter("sfxcompact-tip", compactRect, "SFX picker (collapsed to fit the bar): opens the SFX Browser to pick the sound for your next message. Widen the window for the inline dropdown.")
		icX += sfxCompactW + 4
	}
	// effects_dropdown (AO2 ui_effects_dropdown, courtroom.cpp:968): the screen
	// effect that rides your NEXT message. Hidden while spectating and when the
	// roster is empty, exactly as AO2 hides it (:5586-5598) — which is also why it
	// needs no panelHidden gate or override escape hatch: on a server with no
	// effects it simply is not there, and on one with effects it is the only way to
	// reach them. Not a movable classic slot (yet): it draws in the row's flow,
	// which keeps it out of the classic slot table entirely.
	if a.overlayEffectsVisible() && icBarButtonFits(icBar.W, icX-icBar.X, effectsDDW+4, tailReserve) {
		a.drawEffectsPicker(sdl.Rect{X: icX, Y: rowY, W: effectsDDW, H: fH})
		icX += effectsDDW + 4
	}
	// #M2 S1: emoji picker button on the IC bar's left edge — movable (#4a) and
	// hideable (playtest: some players don't want it at all). Drawn LAST of the optional
	// buttons (redesign), so it's the FIRST to yield on a narrow bar. Override /
	// editor win over the drop (same rule as Pre); hidden still hides.
	_, emojiOv := a.classic.classicOv[slotICEmoji]
	emojiGuardOK := icBarButtonFits(icBar.W, icX-icBar.X, fH+4, tailReserve)
	if icOptionalDraws(emojiGuardOK, emojiOv, a.classic.classicEdit, a.classic.panelHidden(slotICEmoji)) {
		if a.drawEmojiBarButton(a.classic.slotRect(slotICEmoji, sdl.Rect{X: icX, Y: rowY, W: fH, H: fH}, w, h)) {
			a.emoji.showEmojiPicker = !a.emoji.showEmojiPicker
		}
		icX += fH + 4
	}
	// The #2 React BUTTON was removed by request (playtest: unused) — incoming
	// reaction floats still render (and Hide-reactions still hides them), the
	// IC bar just doesn't spend a slot on sending one. (FX moved up above SFX in the
	// redesign so it outranks SFX/emoji on a narrow bar.)
	icW := icBar.W - (icX - icBar.X)
	if icW < minICInputW {
		icW = minICInputW // defensive floor: never collapse the input, even on a tiny stage
	}
	// The IC text input itself is a movable + resizable slot (#4a) — default fills the rest
	// of the bar; the message counter now draws INSIDE this box's right edge (drawMsgCounter),
	// so it needs no tail reserve; an override repositions/resizes the field and the counter
	// rides along inside it.
	icBox := a.classic.slotRect(slotICInput, sdl.Rect{X: icX, Y: rowY, W: icW, H: fH}, w, h)
	icPrimary, icEmoji := a.icFieldFonts(a.compose.icInput) // #M5: show typed emoji/unicode, not tofu
	// Reserve the counter + muted-chip band BEFORE the field draws, so the typed
	// line clips short of them instead of running underneath (the overdrawn-counter
	// report). No-op when both are off/absent.
	c.ReserveFieldTail(icFieldID, a.icFieldTailReserve(icCounterOn))
	a.compose.icInput, send = c.TextFieldEmoji(icFieldID, icBox, a.compose.icInput, "Talk in-character here…  (/pair <id>, /unpair, /offset <x> [y], /pos <side>)", icPrimary, icEmoji)
	a.recallIC() // #8: Up/Down recall recently-sent lines when the IC field is focused
	a.drawMsgCounter(icBox, icCounterOn)
	// Muted (#13): the server disabled our IC speech (MU). Draw a persistent chip
	// over the input's right edge so a muted player sees WHY their sends do
	// nothing; sendIC refuses the send and keeps the typed line intact.
	if a.link.sess != nil && a.link.sess.Muted {
		chipW := a.mutedChipW() // the ONE metric; icMutedBandW reserves off the same number
		if chipW < icBox.W {    // only when it fits inside the field
			chip := sdl.Rect{X: icBox.X + icBox.W - chipW - 2, Y: icBox.Y + 2, W: chipW, H: icBox.H - 4}
			c.Fill(chip, sdl.Color{R: 120, G: 30, B: 30, A: 210})
			c.Label(chip.X+6, chip.Y+(chip.H-14)/2, mutedChipLabel, ColDanger)
		}
	}
	return send
}
