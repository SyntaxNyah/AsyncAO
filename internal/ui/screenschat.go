package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/helper/value"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

// drawChatOverlay paints the message box (showname + spoken text) over the stage. movableBox (classic layout only) lets
// the layout editor drag the box; w,h give the override its window-fraction frame.
func (a *App) drawChatOverlay(vp sdl.Rect, movableBox bool, w, h int32) {
	c := a.ctx
	sc := a.renderScene() // live room, slideshow override, OR the replay scene (M16)
	// Blankpost hides the whole chatbox; the second clause is the idle case.
	if sc.IsBlankPost || (sc.MessageText == "" && sc.ShownameText == "") {
		return
	}
	// Box height follows the MsgBox knob only; text size (the Text knob) clips at the box edge.
	boxH := vp.H / 4 * int32(a.boxPct) / DefaultScalePct
	if max := vp.H * 3 / 5; boxH > max {
		boxH = max
	}
	box := sdl.Rect{X: vp.X, Y: vp.Y + vp.H - boxH, W: vp.W, H: boxH}
	// Movable chatbox (classic layout): a layout-editor override can place it anywhere; everything below is relative to
	// box.
	if movableBox {
		box = a.slotRect(slotChatbox, box, w, h)
	}
	// The speaker's own char.ini chat= box art wins over the theme skin (AO2 get_chat); until it is resident the
	// theme/flat box covers.
	var charSkin *sdl.Texture
	if sc.ChatSkinBase != "" {
		if page, ok := a.d.Store.Get(sc.ChatSkinBase); ok && len(page.Frames) > 0 {
			charSkin = page.Frames[0]
		}
	}
	// Grow the flat fallback panel upward to fit the whole wrapped message; authored skin art keeps its box.
	if _, hasSkin := a.themePage(themeStemChatbox); !hasSkin && charSkin == nil {
		// The theme's "message" point size sets the line height. FontLineSpacing, not font.Height(): the raster stacks
		// rows by line spacing (linespacing.go), so the glyph box would under-grow the panel and clip a long message.
		msgPct := a.messagePct()
		lineH := a.messageRowPitchLogical(msgPct, a.messageFontFor(msgPct, sc.MessageText))
		if g := grownChatBoxH(box.H, vp.H, int32(a.chatMsgLines(box.W-2*chatOverlayPadX, sc)), lineH); g > box.H {
			box.Y -= g - box.H
			box.H = g
			a.chatDiag.grew = true
		} else {
			a.chatDiag.grew = false
		}
	}
	// Skin priority: the speaker's art, the theme's, then the flat panel. Skin blits go via the Ctx scratch rect: &box
	// into cgo would heap-allocate box every frame.
	skinned := false
	if charSkin != nil {
		c.cgoRect = box
		_ = c.Ren.Copy(charSkin, nil, &c.cgoRect)
		skinned = true
	}
	if !skinned {
		if page, ok := a.themePage(themeStemChatbox); ok {
			// A blank showname takes the plate-less `chatblank` art (courtroom.cpp:3320-3331); same selection as the
			// themed path (chatboxfit.go).
			page, _ = a.chatboxSkinForShowname(sc.ShownameText, page)
			c.cgoRect = box
			_ = c.Ren.Copy(a.themeFrame(page), nil, &c.cgoRect)
			skinned = true
		}
	}
	if !skinned {
		// Panel opacity is user-tunable; the border stays solid for legibility.
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
	// Creator easter egg, shared with the themed chatbox path; drawn after the skin so the glow rings sit on top.
	a.drawChatEgg(box, sc.MessageText)
	// Theme text ink applies over any drawn skin, including a per-character one (AO2 get_chat still inks it with theme
	// colours). Only the flat fallback panel keeps the client's readable defaults.
	nameCol := ColAccent
	if skinned && sc.ChatSkinNameHas {
		nameCol = sdl.Color{R: sc.ChatSkinNameColor.R, G: sc.ChatSkinNameColor.G, B: sc.ChatSkinNameColor.B, A: 255}
	} else if skinned && a.themeHasName {
		nameCol = a.themeNameCol
	}
	if a.d.Prefs.Load().NameColors { // per-speaker name colour wins over accent/theme
		nameCol = nameColor(sc.ShownameText, float64(a.d.Prefs.Load().NameSat)/100, float64(a.d.Prefs.Load().NameVal)/100)
	}
	// Pick a covering face for the showname so non-Latin names don't render as tofu; the theme's showname size, family
	// and bold drive it.
	snFont := a.elemFontFor(elemShowname, DefaultScalePct, sc.ShownameText)
	snEmoji := a.elemEmoji(elemShowname, DefaultScalePct)
	// Not showname_align: this overlay has no theme-authored showname box to align across; the themed chatbox honours
	// it (chatboxfit.go).
	// The bold weight is rasterized into the glyphs (one device-exact draw); a second offset pass smeared the name at
	// 105%+ UI scale.
	snBold := a.d.Prefs.Load().BoldNamesOn() || a.elemBold(elemShowname)
	a.labelEmojiWeight(snFont, snEmoji, box.X+chatOverlayPadX, box.Y+chatOverlayNameY, box.W-2*chatOverlayPadX, sc.ShownameText, nameCol, snBold)

	wrapW := box.W - 2*chatOverlayPadX
	// messagePct, not the themed fold: this overlay is sized from the viewport, so there is no theme canvas scale to
	// keep a ratio to.
	a.ensureChatRaster(wrapW, skinned, a.messagePct()) // theme ink over any drawn skin; only the flat panel keeps the client default
	// Drag the message to highlight it, Ctrl+C / right-click to copy (webAO-style).
	textRect := sdl.Rect{X: box.X + chatOverlayPadX, Y: box.Y + chatBoxTopStrip, W: wrapW, H: box.H - chatBoxTopStrip}
	// The box follows its own crawl (courtroom.cpp:4514-4521); the offset is 0 for a message that fits.
	msgScroll := chatCrawlOffset(a.msRaster, sc.VisibleRunes, textRect.H)
	msgY := textRect.Y - msgScroll
	// The clip starts at the text origin, not the box top: a scrolled message draws above textRect.Y, where the
	// showname plate is. See chatcrawlscroll.go for the other edges and the animated painter.
	msgClip := chatCrawlClip(box, textRect.Y, a.msAnim)
	a.handleChatSelect(textRect, msgScroll, sc)
	// Snapshot the geometry for Debug -> Session.
	a.chatDiag.vpH, a.chatDiag.boxY, a.chatDiag.boxH = vp.H, box.Y, box.H
	a.chatDiag.textY, a.chatDiag.wrapW = textRect.Y, wrapW
	a.chatDiag.renderPct, a.chatDiag.devScale = c.RenderScalePct(), a.ctx.textDevPct
	a.chatDiag.visible = int32(sc.VisibleRunes)
	if a.msRaster != nil {
		a.chatDiag.lines = int32(a.msRaster.Lines())
		a.chatDiag.lineH = a.msRaster.LineH()
		a.chatDiag.total = int32(a.msRaster.TotalRunes())
	} else {
		a.chatDiag.lines, a.chatDiag.lineH, a.chatDiag.total = 0, 0, 0
	}
	if a.msAnim != nil || a.msRaster != nil {
		// Clip to the box below the name plate. Via the Ctx scratch rect: &msgClip into cgo would heap-allocate it
		// every frame.
		c.cgoRect = msgClip
		_ = c.Ren.SetClipRect(&c.cgoRect)
		if a.chatSelActive { // selection highlight, UNDER the text so it reads through
			a.drawChatSelHighlight(textRect.X, msgY, wrapW, sc) // scrolled with the text it marks
		}
		if a.msAnim != nil { // #M5 animated message (shake/wave/rainbow spans)
			// The master off-switch implies reduce-motion for text too: glyphs stay legible but stop moving.
			reduce := a.d.Prefs.Load().ReduceMotionOn || a.d.Prefs.Load().DisableEffects
			if a.msAnim.Animates(reduce) {
				// Keep frames coming through the static skip while clock-driven text FX are on screen; self-clears when
				// the message leaves. Gradient-only and reduce-motion render static and don't hold frames.
				a.NoteAnimating()
			}
			a.msAnim.Draw(c.Ren, a.glyphCache, a.msAnimFont, a.d.Viewport.AnimClock(), sc.VisibleRunes, textRect.X, msgY, reduce)
		} else {
			// DrawScaled so the revealed prefix blits device-exact at a scaled UI. Pass msgClip, the clip set above:
			// the 1:1 bracket must re-assert it, and box would make the name band a paint target again.
			a.msRaster.DrawScaled(c.Ren, sc.VisibleRunes, textRect.X, msgY, c.RenderScalePct(), &msgClip)
		}
		_ = c.Ren.SetClipRect(nil)
	} else if a.rasterFailedText != "" && sc.MessageText != "" {
		// Last resort when the raster could not be built: draw the plain wrapped label (no typewriter or glyph effects)
		// rather than a silently blank chatbox.
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

// handleChatSelect runs chatbox text selection and copy: drag selects a rune range, double-click a word, triple-click
// the whole message, a plain click clears. The animated path (msAnim) selects the whole message. Activating this clears
// the log selection so they never fight over Ctrl+C. Shared by the classic overlay and the themed chatbox. scrollY is
// the crawl-follow offset the text is drawn with (chatcrawlscroll.go); rune lookups must measure from it.
func (a *App) handleChatSelect(textRect sdl.Rect, scrollY int32, sc *courtroom.Scene) {
	c := a.ctx
	pressed := c.mouseDown && !a.chatSelPrevDown
	a.chatSelPrevDown = c.mouseDown
	inText := c.hovering(textRect)
	if pressed {
		if inText {
			a.chatSelDragging = true
			a.chatSelDownX, a.chatSelDownY = c.mouseX, c.mouseY
			if a.msRaster != nil { // anchor the range at the pressed boundary
				a.chatSelA = a.msRaster.RuneAt(c.mouseX-textRect.X, c.mouseY-textRect.Y+scrollY)
				a.chatSelB = a.chatSelA
			}
		} else {
			a.chatSelActive = false // a press elsewhere clears the highlight
		}
	}
	if a.chatSelDragging {
		if c.mouseDown {
			if a.msRaster != nil { // the held drag moves the range's head
				a.chatSelB = a.msRaster.RuneAt(c.mouseX-textRect.X, c.mouseY-textRect.Y+scrollY)
			}
			if absInt(int(c.mouseX-a.chatSelDownX))+absInt(int(c.mouseY-a.chatSelDownY)) > 3 {
				if !a.chatSelActive {
					a.chatSelActive = true // moved enough → a selection, not a click
					a.logSelActive = false // …and it owns the highlight now
				}
			}
		} else {
			a.chatSelDragging = false
			switch {
			case a.chatSelActive && a.msRaster != nil && a.chatSelA == a.chatSelB:
				a.chatSelActive = false // wobbled past the slop but landed on one boundary — not a selection
			case a.chatSelActive:
				c.clicked = false // a real drag isn't a click on whatever's under it
				c.focusID = ""    // unfocus so Ctrl+C copies the selection, not a still-focused field
			}
		}
	}
	// Double-click: the word under the cursor; triple-click: the whole message.
	if a.msRaster != nil && inText && (c.dblClick || c.tripleClick) && sc.MessageText != "" {
		runes := []rune(sc.MessageText)
		if c.tripleClick {
			a.chatSelA, a.chatSelB = 0, len(runes)
			c.tripleClick = false
		} else {
			idx := a.msRaster.RuneAt(c.mouseX-textRect.X, c.mouseY-textRect.Y+scrollY)
			a.chatSelA, a.chatSelB = wordBoundsAt(runes, idx)
			c.dblClick = false
		}
		if a.chatSelB > a.chatSelA {
			a.chatSelActive = true
			a.logSelActive = false
			c.clicked = false
			c.focusID = ""
		}
	}
	if a.chatSelActive && sc.MessageText != "" {
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

// chatSelText is the text the chatbox selection copies: the selected range, else the whole message.
func (a *App) chatSelText(sc *courtroom.Scene) string {
	if a.msRaster == nil {
		return sc.MessageText
	}
	runes := []rune(sc.MessageText)
	lo, hi := a.chatSelA, a.chatSelB
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

// drawChatSelHighlight fills the selected range behind the message, one band per wrapped line, clamped to revealed
// glyphs. The animated path has no per-rune raster, so the whole visible block highlights.
func (a *App) drawChatSelHighlight(x, y, wrapW int32, sc *courtroom.Scene) {
	c := a.ctx
	m := a.msRaster
	if m == nil { // animated message (msAnim): whole-block highlight
		// a.rasterScale, not messagePct(): it is the percent ensureChatRaster built msAnim at, and a themed chatbox
		// folds the canvas scale into it (chatboxfit.go). Step by the raster's row pitch or an unpainted stripe shows
		// between wrapped rows.
		lineH := a.msAnim.RowPitch() // the step Draw will actually use, override included
		c.Fill(sdl.Rect{X: x, Y: y, W: wrapW, H: int32(a.chatMsgLines(wrapW, sc)) * lineH}, a.highlightFill())
		return
	}
	lo, hi := a.chatSelA, a.chatSelB
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

// sfxAutoLabel is the SFX picker's "no override" entry: AO2's "Default" row (courtroom.cpp:5488-5493, 5680-5683).
const sfxAutoLabel = "SFX: auto"

// ensureSFXChoices rebuilds the SFX picker list when the character or its emote list changes: auto, then the
// character's distinct char.ini sounds ("0"/"1"/empty are AO silence).
func (a *App) ensureSFXChoices() {
	// Plain-field guard, not a concatenated key: building a string here every frame allocated.
	name := a.activeCharName()
	if a.sfxChoicesForName == name && a.sfxChoicesForCount == len(a.emotes) {
		return
	}
	a.sfxChoicesForName, a.sfxChoicesForCount = name, len(a.emotes)
	a.sfxChoices = append(a.sfxChoices[:0], sfxAutoLabel)
	for i := range a.emotes {
		s := a.emotes[i].SFXName
		if s == "" || s == "0" || s == "1" {
			continue
		}
		dup := false
		for _, ex := range a.sfxChoices {
			if ex == s {
				dup = true
				break
			}
		}
		if !dup {
			a.sfxChoices = append(a.sfxChoices, s)
		}
	}
	if a.sfxChoiceIdx >= len(a.sfxChoices) {
		a.sfxChoiceIdx = 0
	}
}

// chatMsgLines is the chatbox message's wrapped-line count: the raster's own, else an approximate re-wrap.
func (a *App) chatMsgLines(wrapW int32, sc *courtroom.Scene) int {
	if a.msRaster != nil {
		if n := a.msRaster.Lines(); n > 0 {
			return n
		}
	}
	if n := len(a.ctx.WrapText(sc.MessageText, wrapW, 0)); n > 0 {
		return n
	}
	return 1
}

// ensureChatRaster re-rasterizes the message when text, color, zoom, wrap width or skin presence changed. pct is the
// resolved message scale, passed in because the themed chatbox folds the theme canvas scale into it (chatboxfit.go);
// rasterScale remembers it, so a change re-rasters exactly once.
func (a *App) ensureChatRaster(wrapW int32, skinned bool, pct int) {
	sc := a.renderScene() // matches drawChatOverlay (live / slideshow / replay scene)
	effSig := effectsSig(sc.MessageEffects)
	// staleAnimChain: an effects line built before a lazily-loaded CJK/emoji font tier landed must re-raster against
	// the new chain.
	staleAnimChain := a.msAnim != nil && a.msAnim.ChainGen() != a.ctx.fontChainGen
	if !staleAnimChain && (a.msRaster != nil || a.msAnim != nil) && a.rasterRaw == sc.MessageRaw && a.rasterText == sc.MessageText && a.rasterColor == sc.TextColor &&
		a.rasterScale == pct && a.rasterW == wrapW && a.rasterSkinned == skinned && a.rasterEffSig == effSig && a.rasterCentered == sc.Centered &&
		a.rasterChatMsg == sc.ChatSkinMsgColor && a.rasterChatMsgHas == sc.ChatSkinMsgHas &&
		a.rasterDevPct == a.ctx.textDevPct { // #77: a UI-scale change re-rasterizes at the new device size
		return
	}
	if a.rasterRaw != sc.MessageRaw {
		a.chatSelActive = false // a new message — drop the stale chatbox highlight/selection
	}
	if a.msRaster != nil {
		a.msRaster.Destroy()
		a.msRaster = nil
	}
	a.msAnim = nil // AnimatedText owns no textures (the glyph cache does); just drop it
	a.msAnimFont = nil
	if sc.MessageText == "" {
		return
	}
	// A message with effect spans takes the per-glyph animated path; every other message keeps the MessageRaster fast
	// path.
	if len(sc.MessageEffects) > 0 {
		if a.glyphCache == nil {
			a.glyphCache = render.NewGlyphCache(glyphCacheCap)
		}
		anim, font := renderAnimated(a, sc, wrapW, skinned, pct)
		anim.Warm(a.ctx.Ren, a.glyphCache, font) // render all glyphs up front → 0-alloc draws
		a.msAnim = anim
		a.msAnimFont = font
	} else {
		raster, err := renderRaster(a, sc, wrapW, skinned, pct, false)
		if err != nil {
			// Arm the plain-label fallback so drawChatOverlay still shows the text, and log the SDL error.
			a.pushDebug("chatbox raster failed: " + err.Error() + " — drawing the message unrasterized")
			a.rasterFailedText = sc.MessageText
			return
		}
		a.msRaster = raster
		a.rasterFailedText = ""
	}
	a.rasterText = sc.MessageText
	a.rasterRaw = sc.MessageRaw
	a.rasterColor = sc.TextColor
	a.rasterScale = pct
	a.rasterW = wrapW
	a.rasterSkinned = skinned
	a.rasterChatMsg = sc.ChatSkinMsgColor
	a.rasterChatMsgHas = sc.ChatSkinMsgHas
	a.rasterEffSig = effSig
	a.rasterCentered = sc.Centered
	a.rasterDevPct = a.ctx.textDevPct // #77
}

// chatZoomWheel: Ctrl+wheel over the chatbox zooms the chat text, shared by both chatbox flavors.
func (a *App) chatZoomWheel(box sdl.Rect) {
	a.zoomWheel(box, &a.chatPct, config.MinChatScalePercent, config.MaxChatScalePercent)
}

// volStripRowsH is the height of the volume popover's two slider rows, shared with drawLogPanel's box height.
const volStripRowsH = int32(44)

// persistChannelMute saves one channel's click-to-mute toggle. id matches drawVolumeStrip's cell ids; the SFX channel
// is session-only and not persisted.
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

// drawVolumeStrip draws the on-screen volume row (Master + the channels + blip rate) at the top of the log panel.
func (a *App) drawVolumeStrip(r sdl.Rect) {
	c := a.ctx
	// Drive the effective (per-server when connected) volumes like the Settings and Extras sliders; the global prefs
	// are overridden by a connected server's audio profile.
	master, music, sfx, blip := a.effectiveVolumes()
	rate, onSpaces := a.d.Prefs.Load().BlipTyping()
	const cols = 5
	colW := r.W / cols
	// Two rows per column: "label NN%" (the label is the click-to-mute) over a full-width slider.
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
		v := value.Clamp(c.Slider("volstrip:"+id, track, val, hi), lo, hi)
		if tip != "" {
			c.Tooltip(track, tip)
		}
		return v
	}
	if nv := cell(0, "master", "Master", fmt.Sprintf("%d%%", master), "", int32(master), 0, 100, &a.masterMuted); int(nv) != master {
		a.setEffectiveVolumes(int(nv), music, sfx, blip)
	}
	if nv := cell(1, "music", "Music", fmt.Sprintf("%d%%", music), "", int32(music), 0, 100, &a.musicMuted); int(nv) != music {
		a.setEffectiveVolumes(master, int(nv), sfx, blip)
	}
	if nv := cell(2, "sfx", "SFX", fmt.Sprintf("%d%%", sfx), "", int32(sfx), 0, 100, &a.sfxMuted); int(nv) != sfx {
		a.setEffectiveVolumes(master, music, int(nv), blip)
	}
	if nv := cell(3, "blip", "Blips", fmt.Sprintf("%d%%", blip), "", int32(blip), 0, 100, &a.blipMuted); int(nv) != blip {
		a.setEffectiveVolumes(master, music, sfx, int(nv))
	}
	// Blip cadence: 1 blip per N letters; no mute.
	if nv := cell(4, "rate", "Rate", fmt.Sprintf("1/%d", rate), "Blip cadence: 1 blip every N letters (left = faster)", int32(rate), int32(config.MinBlipRate), int32(config.MaxBlipRate), nil); int(nv) != rate {
		a.d.Prefs.SetBlipTyping(int(nv), onSpaces)
		a.applyTimingToRoom()
	}
}

func (a *App) drawLogPanel(r sdl.Rect, vp sdl.Rect) {
	c := a.ctx
	c.Fill(r, a.partPanelOr(partLog, ColPanel)) // per-part tint: the log column can carry its own colour
	c.Border(r, ColAccent)                      // match the rest of the UI (buttons/OOC box/panels all border in the accent) — playtest: the log panel was the lone grey outline
	// A toggle at the right end drops a compact volume strip above the panel content; the tabs share the rest of the
	// row.
	const volBtnW = int32(36)
	// OOC is its own box by default, so the OOC tab is dropped; the Legacy theme or "OOC in the log tab" keeps it in
	// the strip. A hidden or torn-off active tab falls back to the IC log.
	oocAsTab := a.d.Prefs.Load().LegacyDevTheme || a.d.Prefs.Load().OOCInLogTab
	if (!oocAsTab && a.logTab == logTabOOC) || a.tabTorn(a.logTab) || a.tabHidden(a.logTab) {
		a.logTab = logTabLog
	}
	// The docked strip skips torn-off tabs and compacts the rest.
	docked, numLogTabs := a.dockedLogTabs(oocAsTab)
	tab := (r.W - volBtnW) / numLogTabs
	for i := int32(0); i < numLogTabs; i++ {
		bw := tab
		if i == numLogTabs-1 {
			bw = (r.W - volBtnW) - (numLogTabs-1)*tab // last tab takes the remainder before the Vol toggle
		}
		if c.Button(sdl.Rect{X: r.X + i*tab, Y: r.Y, W: bw, H: btnH}, docked[i].label) {
			a.logTab = docked[i].id
		}
	}
	volBtn := sdl.Rect{X: r.X + r.W - volBtnW, Y: r.Y, W: volBtnW, H: btnH}
	if c.Button(volBtn, "Vol") {
		a.volStripOn = !a.volStripOn
		{
			pv1 := a.volStripOn
			a.d.Prefs.Update(func(sn *config.Settings) { // persist so it survives a restart
				sn.VolStripOn = pv1
			})
		}
	}
	if a.volStripOn {
		c.Border(volBtn, ColAccent) // active cue
	}
	c.Tooltip(volBtn, "Show/hide volume sliders on screen (chat stays usable)")
	innerY := r.Y + btnH + logPanelRowGapPx
	if a.volStripOn {
		a.drawVolumeStrip(sdl.Rect{X: r.X + logPanelInsetPx, Y: innerY, W: r.W - 2*logPanelInsetPx, H: volStripRowsH})
		innerY += volStripRowsH + logPanelRowGapPx
	}
	inner := sdl.Rect{X: r.X + logPanelInsetPx, Y: innerY, W: r.W - 2*logPanelInsetPx, H: r.Y + r.H - innerY - logPanelInsetPx}
	// Ctrl+wheel anywhere on the panel resizes the active tab's text; taking the wheel here stops the inner lists
	// double-stepping. The Music tab has its own scale (musicPct).
	scale := &a.logPct
	switch a.logTab {
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
	switch a.logTab {
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
		// The OOC tab is a complete OOC chat; the bottom OOC bar is a second input sharing the same draft (a.oocInput).
		a.drawOOCPanel(inner, true)
		return
	case logTabNotes:
		a.drawNotesTab(inner)
		return
	case logTabFriends:
		a.drawFriendsTab(inner)
		return
	}
	// IC log tab: search box and Copy/TXT/HTML row, then the colored scrollback. The row height follows the chrome
	// font, since textField centres its text and a taller font would spill onto the first log line.
	rowY := inner.Y
	rowH := logSearchRowH(int32(c.font.Height()))
	// Filter toggles the showname/pos filter sub-panel, reserved at the row's left edge.
	const filterBtnW = int32(48)
	filterBtn := sdl.Rect{X: inner.X, Y: rowY, W: filterBtnW, H: rowH}
	if c.Button(filterBtn, "Filter") {
		a.logFilterOpen = !a.logFilterOpen
	}
	if a.logFilterOpen {
		c.Border(filterBtn, ColAccent) // active cue, matching the Vol toggle
	}
	c.Tooltip(filterBtn, "Show/hide advanced filters (only showname / position)")
	searchX := inner.X + filterBtnW + 4
	searchW := inner.W - filterBtnW - 4 - 3*logExportBtnPitch - 12
	// A layout-edited slot can be too narrow for the export buttons: give the search field the rest of the row.
	showExports := searchW >= logSearchMinW
	if !showExports {
		searchW = inner.W - (filterBtnW + 4)
	}
	a.logSearch, _ = c.TextField("logsearch", sdl.Rect{X: searchX, Y: rowY, W: searchW, H: rowH}, a.logSearch, "Search log...")
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
	// Advanced filters: showname and position fields, inclusive, stacked on the text search.
	if a.logFilterOpen {
		frH := rowH
		half := (inner.W - 4) / 2
		a.logFilterShowname, _ = c.TextField("logfilter_showname", sdl.Rect{X: inner.X, Y: listY, W: half, H: frH}, a.logFilterShowname, "Showname…")
		a.logFilterPos, _ = c.TextField("logfilter_pos", sdl.Rect{X: inner.X + half + 4, Y: listY, W: inner.W - half - 4, H: frH}, a.logFilterPos, "Pos (e.g. wit)…")
		// Colour filter: narrows to entries using the picked colour (message-level or inline span); "Any color" is off.
		a.logFilterColor, _ = c.Dropdown("logfilter_color", sdl.Rect{X: inner.X, Y: listY + frH + logSearchRowGap, W: inner.W, H: frH}, logColorFilterChoices, a.logFilterColor)
		listY += 2*frH + 2*logSearchRowGap
	}
	// false: the classic docked Log tab sits outside any theme design canvas.
	a.drawICLogList(sdl.Rect{X: inner.X, Y: listY, W: inner.W, H: inner.H - (listY - inner.Y)}, false)
}

const (
	// logPanelInsetPx is the body inset inside the panel's accent border. The classic path insets before calling the
	// list drawers, which carry no padding of their own; the themed path uses themedLogInsetPx.
	logPanelInsetPx = int32(4)
	// logPanelRowGapPx is the vertical gap between the log panel's stacked rows.
	logPanelRowGapPx = int32(4)
	// logSearchRowMinH is the search/export row's minimum height.
	logSearchRowMinH = int32(24)
	// logSearchRowPadV is the vertical padding around the chrome font in the search field.
	logSearchRowPadV = int32(8)
	// logSearchRowGap separates the row from the log list below it.
	logSearchRowGap = int32(4)
	// logSearchMinW is the narrowest useful search field; below it the export buttons yield the row.
	logSearchMinW = int32(60)
	// logExportBtnW/Pitch are the Copy/TXT/HTML button width and spacing.
	logExportBtnW     = int32(50)
	logExportBtnPitch = int32(52)
)

// icBarUnderStage places the IC input bar directly under the stage and the control-button block fH plus a gap below it.
func icBarUnderStage(vp sdl.Rect, fH int32) (icBarTop, defY int32) {
	icBarTop = vp.Y + vp.H + pad
	defY = icBarTop + fH + 8 // gap below the input row
	return
}

func (a *App) drawICControls(w, h int32, vp sdl.Rect) {
	c := a.ctx
	// The IC input bar sits directly under the stage (the classic AO spot); controls, judge strip and emote grid follow
	// below.
	fH := a.inputFieldH()
	icBarTop, defY := icBarUnderStage(vp, fH) // IC bar under the stage; control block below it

	// Control block ("controls" slot): both button rows move together; its width drives the wrap edge and its height is
	// content-driven. Un-edited, every rect and wrap edge matches the classic layout.
	ctrlOv, ctrlEdited := a.classicOv[slotControls]
	var ctrlRect sdl.Rect
	if ctrlEdited {
		ctrlRect = a.anchoredRect(slotControls, ctrlOv, w, h) // window pin honoured
	}
	clusterX, y, dy, clusterRight := controlsBlockOrigin(ctrlRect, ctrlEdited, w, defY)

	// Row 1: shouts, pairing and the layout knobs. Shouts arm a.icShout; nothing is returned to a send.
	a.drawICShoutRow(clusterX, y, w, h)

	// Row 2: utility buttons, wrapping as needed. Each variant returns the row bottom y2 and done=true if Disconnect
	// fired.
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

	// Register the block for the layout editor: the grab box spans the live content width, def the default width.
	if a.classicEdit {
		a.regSlot(slotControls,
			sdl.Rect{X: clusterX, Y: y, W: clusterRight - clusterX, H: y2 + btnH - y},
			sdl.Rect{X: pad, Y: defY, W: w - 2*pad, H: y2 - dy + btnH - defY})
	}

	// Judge strip and emote grid anchor to the control block's unmoved bottom (y2 - dy); the IC bar anchors to icBarTop
	// independently, so the judge strip never pushes it.
	postY := y2 - dy + btnH + 6
	if a.judgeVisible() {
		postY += a.drawJudgeRow(pad, postY)
	}

	// IC input row (height follows the Box knob), led by the AO2 text color selector. The showname box overrides the
	// Settings showname for the session. Every element below is its own movable slot; the bar is only the default row
	// geometry.
	icBar := sdl.Rect{X: pad, Y: icBarTop, W: vp.W, H: fH}
	rowY := icBar.Y
	// Colour swatch + dropdown (movable slot): also the extended AsyncAO colours and the fun-colour modes.
	// icColorSelected and applyICColorPick are shared with the themed row so the layouts can't drift.
	colorBox := a.slotRect(slotICColor, sdl.Rect{X: icBar.X, Y: rowY, W: 32 + colorSelectW, H: fH}, w, h)
	a.drawICColorStrip(colorBox) // swatch + colour dropdown (rect by value — alloc-free)
	// The rest of the IC bar is one sequential cursor chain in drawICInputRow, flowing from the default positions so
	// freeing one slot never cascades. Enter is the only sender: shouts arm (courtroom.cpp:6048-6127), sendIC reads
	// a.icShout.
	//
	// Sending is also gated on the notice box: the field reads Ctx.enter directly, which neither the handleHotkeys
	// guard nor fencePointer covers, so Enter under a modal would send a message the user never saw. The field still
	// takes the keystroke.
	if a.drawICInputRow(icBar, rowY, w, h, fH) && !a.serverNoticeDlg.open {
		a.sendIC()
	}

	// Emote row slot; slotRect stays inside the hidden guard so the editor registers no handle for an undrawn grid.
	emoteY := postY // #8: the emote grid follows the control block + judge strip, below the IC bar
	if !a.panelHidden(panelEmotes) {
		emoteDef := sdl.Rect{X: pad, Y: emoteY, W: w - 2*pad, H: h - emoteY - 30}
		a.drawEmoteRow(a.slotRect(slotEmotes, emoteDef, w, h), vp)
	}
	a.drawSpritePreviewFallback(false)

	// OOC row at the bottom: a full-width OOC input (no name box), shown when OOC is a tab, as a second always-visible
	// input.
	oocY := h - fH - 4
	// Also the fallback when the right column is hidden, since the OOC box lives in that column; only an explicit "hide
	// OOC chat" may leave no OOC input.
	oocBoxUnreachable := a.panelHidden(panelLog)
	if !a.panelHidden(panelOOC) && (a.d.Prefs.Load().LegacyDevTheme || a.d.Prefs.Load().OOCInLogTab || oocBoxUnreachable) {
		// The bottom OOC bar is its own movable slot ("oocbar"), registered only while it draws.
		oocBarDef := sdl.Rect{X: pad, Y: oocY, W: w - 3*pad, H: fH}
		oocBar := a.slotRect(slotOOCBar, oocBarDef, w, h)
		oocRowY := oocBar.Y
		if oocBar.H > fH {
			oocRowY = oocBar.Y + (oocBar.H-fH)/2
		}
		oocPrimary, oocEmoji := a.icFieldFonts(a.oocInput) // #M5: emoji/unicode in the OOC bar too
		var sendOOC bool
		a.oocInput, sendOOC = c.TextFieldEmoji("ooc", sdl.Rect{X: oocBar.X, Y: oocRowY, W: oocBar.W, H: fH}, a.oocInput, "OOC chat…  (set your OOC name in the OOC tab)", oocPrimary, oocEmoji)
		if sendOOC {
			a.submitOOC() // shared OOC send: uses your OOC name (or the server default) + clears
		}
		a.recallOOC() // Up/Down recall recently-sent OOC lines when the bar is focused
		if c.ctrlHeld && c.wheelY != 0 && c.hovering(sdl.Rect{X: oocBar.X, Y: oocRowY, W: oocBar.W, H: fH}) {
			a.oocPct = value.Clamp(a.oocPct+int(c.wheelY)*config.ScaleStepPercent,
				config.MinLogScalePercent, config.MaxLogScalePercent)
			a.saveLayout()
		}
	}
	// The warnLine toast and the DND badge share this row (missing-asset reports have their own chip,
	// assetmisschip.go). With DND on, clip the toast so it can't run under the badge.
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
	// DND badge: a reminder while alerts are muted; click to turn it off.
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

// drawICShoutRow draws control-block Row 1: the shout buttons (plus the 2.10 custom interjection), Pair and the layout
// knobs. Buttons arm (toggleShout, icarms.go); the armed one wears drawArmedOverlay, standing in for AO2's "*_selected"
// sprite (courtroom.cpp:6062).
func (a *App) drawICShoutRow(clusterX, y, w, h int32) {
	c := a.ctx
	x := clusterX
	if !a.panelHidden(panelShouts) {
		shoutW := int32(96)
		// Each shout is individually movable (literal slot keys keep the row alloc-free).
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
			if a.panelHidden(s.key) {
				continue // individually hideable (the row compacts); the group toggle still hides all
			}
			// slotRect once and reuse it: the armed overlay must land on the rect the button actually drew at.
			shoutRect := a.slotRect(s.key, sdl.Rect{X: x, Y: y, W: shoutW, H: btnH}, w, h)
			if c.Button(shoutRect, s.label) {
				a.toggleShout(s.mod)
			}
			a.drawArmedOverlay(shoutRect, a.icShout == s.mod)
			x += shoutW + 6
		}
		// Custom interjection (2.10): the button arms the active pick; the ▾ cycler steps base custom then each
		// [Shouts] entry. Only for characters that ship one.
		if a.sess.Features.Has(protocol.FeatureCustomObjections) && a.hasCustomShout() {
			label := a.customShoutLabel()
			bw := c.TextWidth(label) + 16
			customRect := sdl.Rect{X: x, Y: y, W: bw, H: btnH}
			if c.Button(customRect, label) {
				a.toggleShout(protocol.ShoutCustom)
			}
			a.drawArmedOverlay(customRect, a.icShout == protocol.ShoutCustom)
			x += bw + 4
			if len(a.customShouts) > 0 {
				if c.Button(sdl.Rect{X: x, Y: y, W: 26, H: btnH}, "▾") {
					// −1 (base) → 0 → … → len−1 → −1
					a.customIdx++
					if a.customIdx >= len(a.customShouts) {
						a.customIdx = -1
					}
				}
				x += 32
			}
		}
	}
	if !a.panelHidden("ctrl.pair") { // hideable: hiding compacts, like ctrlSlot
		if a.movableButton("ctrl.pair", sdl.Rect{X: x, Y: y, W: 70, H: btnH}, "Pair...", w, h) {
			a.showPair = !a.showPair
		}
		x += 80
	}
	if !a.panelHidden(panelKnobs) && !a.d.Prefs.Load().DragLayout { // drag-resize mode hides the +/− knobs
		if a.d.Prefs.Load().ViewportExactW == 0 { // exact-px sizing shadows vpPct, so the View knob would no-op — hide it (set the size in Settings instead)
			x = a.scaleControl(x, y, "View", &a.vpPct, config.ViewportStepPercent, config.MinViewportPercent, config.MaxViewportPercent)
		}
		x = a.scaleControl(x, y, "Text", &a.chatPct, config.ScaleStepPercent, config.MinChatScalePercent, config.MaxChatScalePercent)
		x = a.scaleControl(x, y, "MsgBox", &a.boxPct, config.ScaleStepPercent, config.MinChatBoxPercent, config.MaxChatBoxPercent)
		x = a.scaleControl(x, y, "Log", &a.logPct, config.ScaleStepPercent, config.MinLogScalePercent, config.MaxLogScalePercent)
		_ = a.scaleControl(x, y, "Input", &a.inputPct, config.ScaleStepPercent, config.MinInputPercent, config.MaxInputPercent)
	}
}

// drawICUtilityRowLegacy draws control-block Row 2 for the legacy-dev theme (flat button run). It returns the wrapped
// row cursor y2 and done=true if Disconnect fired.
func (a *App) drawICUtilityRowLegacy(clusterX, y2, clusterRight, w, h int32) (int32, bool) {
	c := a.ctx
	x := clusterX
	if c.Button(sdl.Rect{X: x, Y: y2, W: 100, H: btnH}, "Character") {
		// Back to char select; the server re-picks via CC -> PV and EventCharPicked rebuilds the courtroom.
		a.screen = ScreenCharSelect
	}
	x += 106
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
	if a.evidPresent {
		evLabel = "Evidence ●" // armed: next IC message presents it
	}
	if c.Button(sdl.Rect{X: x, Y: y2, W: 100, H: btnH}, evLabel) {
		a.toggleEvidence()
	}
	x += 106
	if c.Button(sdl.Rect{X: x, Y: y2, W: 80, H: btnH}, "Mods...") {
		a.showModcall = true
	}
	x += 86
	if c.Button(sdl.Rect{X: x, Y: y2, W: 70, H: btnH}, "Login...") {
		a.openLoginDialog()
	}
	x += 76
	x = a.drawPosSelect(x, y2, btnH)
	// Hotkeys and Restyle trail after Pos so no existing button shifts; they wrap to a fresh row when they won't fit,
	// and everything below keys off y2.
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

	// Mod / CM launchers wrap to a fresh row as a group if they won't fit.
	modW, cmW := int32(0), int32(0)
	if a.amIMod() {
		modW = 54
	}
	if a.amICMNow {
		cmW = 46
	}
	// Only wrap when the group draws something.
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
	// Group Chat / DMs launcher (Settings -> Chat to hide); appended last, no x advance when off.
	if a.d.Prefs.Load().GroupChatButton && !a.panelHidden("ctrl.groupchat") {
		const gcW = int32(96)
		if x+gcW > clusterRight {
			y2 += btnH + 4
			x = clusterX
		}
		gcR := sdl.Rect{X: x, Y: y2, W: gcW, H: btnH}
		if c.Button(gcR, "Group Chat") {
			a.toggleMessages()
		}
		c.Border(gcR, ColAccent)
		c.Tooltip(gcR, "Private DMs & group chats with other AsyncAO players (also Extras → Group Chat)")
		x += gcW + btnGap
	}
	// Voice button: only in a voice-enabled area (VS_CAPS / Nyathena); no x advance when off.
	if a.voiceOfferable() && !a.panelHidden("ctrl.voice") {
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
		// No cursor advance: this is the row's last block and nothing reads x after it (staticcheck SA4006).
	}
	return y2, false
}

// drawICUtilityRowGrouped draws control-block Row 2 grouped by purpose (Character, Scene, Moderation, System), with
// Disconnect set apart at the end. Returns like the legacy variant.
func (a *App) drawICUtilityRowGrouped(clusterX, y2, clusterRight, w, h int32) (int32, bool) {
	c := a.ctx
	x := clusterX
	// Buttons grouped by purpose with a wider gap between groups; inline (no closures) to stay alloc-free.
	const gGap = int32(16) // gap between groups
	evLabel := "Evidence"
	if a.evidPresent {
		evLabel = "Evidence ●"
	}
	// Character: each button is hideable and hidden ones compact away.
	if r, ok := a.ctrlSlot(&x, y2, 100, 106, w, h, "ctrl.character"); ok {
		if c.Button(r, "Character") {
			a.screen = ScreenCharSelect
		}
	}
	if r, ok := a.ctrlSlot(&x, y2, 90, 96, w, h, "ctrl.wardrobe"); ok {
		if c.Button(r, "Wardrobe") {
			a.openIniswap()
		}
		c.Border(r, ColAccent)
		c.Tooltip(r, "Wardrobe / iniswap — swap your character's sprites & emotes")
	}
	if r, ok := a.ctrlSlot(&x, y2, 84, 84+gGap, w, h, "ctrl.restyle"); ok {
		if c.Button(r, "Restyle") {
			a.openSpriteStyle()
		}
		c.Tooltip(r, "Recolour / glow your character on the fly — other AsyncAO players see it")
	}
	// — Scene —
	if r, ok := a.ctrlSlot(&x, y2, 100, 106, w, h, "ctrl.background"); ok {
		if c.Button(r, "Background") {
			a.openBgPicker()
		}
	}
	if r, ok := a.ctrlSlot(&x, y2, 100, 106, w, h, "ctrl.evidence"); ok {
		if c.Button(r, evLabel) {
			a.toggleEvidence()
		}
	}
	x = a.drawPosSelect(x, y2, btnH)
	x += gGap
	// — Moderation —
	if r, ok := a.ctrlSlot(&x, y2, 80, 86, w, h, "ctrl.mods"); ok {
		if c.Button(r, "Mods...") {
			a.showModcall = true
		}
	}
	if a.amIMod() {
		mR := a.slotRect("ctrl.mod", sdl.Rect{X: x, Y: y2, W: 54, H: btnH}, w, h)
		if c.Button(mR, "Mod") {
			a.toggleModDash()
		}
		c.Border(mR, ColDanger)
		c.Tooltip(mR, "Moderation tools — server-aware ban / kick")
		x += 60
	}
	if a.amICMNow {
		cR := a.slotRect("ctrl.cm", sdl.Rect{X: x, Y: y2, W: 46, H: btnH}, w, h)
		if c.Button(cR, "CM") {
			a.toggleCMPanel()
		}
		c.Border(cR, chipCMColor)
		c.Tooltip(cR, "CM area controls — lock / kick-from-area")
		x += 52
	}
	x += gGap
	// System: wraps to a fresh row as a block if it would run off the right edge.
	if x+90+96+86+76 > clusterRight {
		y2 += btnH + 4
		x = clusterX
	}
	if r, ok := a.ctrlSlot(&x, y2, 90, 96, w, h, "ctrl.settings"); ok {
		if c.Button(r, "Settings") {
			a.prevScreen = ScreenCourtroom
			a.screen = ScreenSettings
		}
	}
	if r, ok := a.ctrlSlot(&x, y2, 90, 96, w, h, "ctrl.hotkeys"); ok {
		if c.Button(r, "Hotkeys") {
			a.openHotkeyCheatSheet()
		}
		c.Tooltip(r, "Show all your hotkeys & custom binds (also F1)")
	}
	if r, ok := a.ctrlSlot(&x, y2, 80, 86, w, h, "ctrl.about"); ok {
		if c.Button(r, "About") {
			a.prevScreen = ScreenCourtroom
			a.screen = ScreenAbout
		}
	}
	// The login button shows the saved username (click: /account profile), else "Login..."; right-click always opens
	// the dialog. The account is per-server (a.serverKey).
	acct := strings.TrimSpace(a.d.Prefs.Load().ServerWarmInfoFor(a.serverKey).LoginUser)
	loginLabel, loginW := "Login...", int32(70)
	if acct != "" {
		loginLabel, loginW = acct, c.TextWidth(acct)+18
	}
	if loginR, ok := a.ctrlSlot(&x, y2, loginW, loginW+6, w, h, "ctrl.login"); ok {
		if c.Button(loginR, loginLabel) {
			if acct != "" {
				a.sess.SendOOC(a.oocNameOrDefault(), "/account") // view your profile
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
	// Group Chat / DMs launcher (Settings -> Chat to hide); appended last so it never shifts earlier buttons, no x
	// advance when off.
	if a.d.Prefs.Load().GroupChatButton && !a.panelHidden("ctrl.groupchat") {
		const gcW = int32(96)
		if x+gcW > clusterRight {
			y2 += btnH + 4
			x = clusterX
		}
		gcR := a.slotRect("ctrl.groupchat", sdl.Rect{X: x, Y: y2, W: gcW, H: btnH}, w, h)
		if c.Button(gcR, "Group Chat") {
			a.toggleMessages()
		}
		c.Border(gcR, ColAccent)
		c.Tooltip(gcR, "Private DMs & group chats with other AsyncAO players (also Extras → Group Chat)")
		x += gcW + 6
	}
	// Voice button: only in an area that advertises voice (VS_CAPS, i.e. Nyathena); no x advance when unavailable.
	if a.voiceOfferable() && !a.panelHidden("ctrl.voice") {
		const vcW = int32(80)
		if x+vcW > clusterRight {
			y2 += btnH + 4
			x = clusterX
		}
		vcLabel, vcBord := a.voiceButtonState()
		vcR := a.slotRect("ctrl.voice", sdl.Rect{X: x, Y: y2, W: vcW, H: btnH}, w, h)
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
	if !a.panelHidden("ctrl.disconnect") { // hideable: Esc still leaves the server
		if a.movableButton("ctrl.disconnect", sdl.Rect{X: x, Y: y2, W: 110, H: btnH}, "Disconnect", w, h) {
			a.requestDisconnect()
			return y2, true
		}
		x += 116
	}
	return y2, false
}

// drawICColorStrip draws the IC text-colour swatch and naming dropdown inside colorBox; the pick logic is shared with
// the themed row.
func (a *App) drawICColorStrip(colorBox sdl.Rect) {
	c := a.ctx
	// The pieces fill the slot rect so a resize takes effect.
	swatch := sdl.Rect{X: colorBox.X, Y: colorBox.Y, W: 26, H: colorBox.H}
	icSel, sw := a.icColorSelected()
	c.Fill(swatch, sw)
	c.Border(swatch, ColPanelHi)
	a.icSwatchRect = swatch // the free-hex wheel anchors here
	if a.icCustomOn && c.clicked && c.hovering(swatch) {
		a.showICColorWheel = !a.showICColorWheel // re-open to adjust (re-picking "Custom…" in the dropdown doesn't fire changed)
	}
	colorDDW := colorBox.W - 32
	if colorDDW < 60 {
		colorDDW = 60 // floor: the dropdown stays clickable however small the slot is dragged
	}
	// Freeze the IC field's selection before the dropdown so a picked colour can wrap it (AO2 on_text_color_changed):
	// the open-click unfocuses the field, so by the pick frame the live selection is gone.
	a.captureICColorSel()
	if next, changed := c.Dropdown(icColorDDID, sdl.Rect{X: colorBox.X + 32, Y: colorBox.Y, W: colorDDW, H: colorBox.H}, icColorChoices, icSel); changed {
		a.applyICColorPick(next)
	}
}

// icOptionalDraws reports whether a width-guarded optional IC-bar button draws: never when hidden, otherwise when the
// width guard passes, a slot override exists, or the editor is armed (so a dropped button can still be dragged out).
func icOptionalDraws(guardOK, hasOverride, editing, hidden bool) bool {
	return !hidden && (guardOK || hasOverride || editing)
}

// icBarButtonFits is the narrow-bar width guard: the button fits if tailReserve is still left after used and demand.
// The row draws in priority order, so the later buttons (SFX, emoji) are the first dropped.
func icBarButtonFits(barW, used, demand, tailReserve int32) bool {
	return barW-used-demand >= tailReserve
}

// IC-bar optional-button widths, shared with the layout tests. Pre + Text-FX must fit the 720p classic bar: 666 - 372 -
// minICInputW = 144 >= Pre (58) + FX (78).
const (
	minICInputW  = 150 // the IC text input never shrinks below this (the tail reserve / floor)
	shownameBoxW = 140 // the per-session showname box width
	immedW       = 96  // "Immediate" toggle width (fits the full label without truncating)
	icAddW       = 84  // "Additive" toggle width (2.8 servers only; +icAddW+6 to the pre-optional band)
	// preW fits the "Pre" checkbox and label and keeps the Pre+FX pair inside the 720p bar.
	preW   = 52
	sfxDDW = 92 // SFX dropdown width
	// effectsDDW sizes the AO2 screen-effect picker (stock rect 89x22, ao2defaultrects.go). Drawn last before emoji, so
	// it yields to Pre/FX/SFX on a narrow bar; it draws only with a non-empty roster and not spectating
	// (courtroom.cpp:5586-5598).
	effectsDDW = 92
	// sfxCompactW is the collapsed SFX picker button used where the full dropdown doesn't fit; it opens the same choice
	// list (SFX Browser) and keeps the dropdown's drop order.
	sfxCompactW = 40
	// icFlipW sizes the "Flip" checkbox: box(16) + gap(6) + label width + slack so the label doesn't touch the edge.
	// Flip draws with no width guard when the server advertises flipping (courtroom.cpp:1629-1636) and never drops.
	icFlipW = 60
)

// drawICInputRow draws the IC bar's input chain left to right from the default positions (showname, Immediate,
// Additive, Pre, FX, SFX, emoji, text field, muted chip). It returns whether Enter was pressed so the caller owns the
// send.
func (a *App) drawICInputRow(icBar sdl.Rect, rowY, w, h, fH int32) (send bool) {
	c := a.ctx
	nameX := icBar.X + 32 + colorSelectW + 6 // DEFAULT spot; downstream (immedX) flows from here
	namePlaceholder := a.d.Prefs.Load().Showname
	if namePlaceholder == "" {
		namePlaceholder = "Showname"
	}
	a.ensureNameOpts()
	// Showname box (+ saved-name picker), a movable slot; the cursor below flows from the default nameX so moving it
	// never cascades.
	nameBox := a.slotRect(slotICShowname, sdl.Rect{X: nameX, Y: rowY, W: shownameBoxW, H: fH}, w, h)
	// The field fills the slot's live width and height so the editor's resize handles work.
	snW, snDD := nameBox.W, int32(0)
	if len(a.nameOpts) > 1 { // a tiny ▾ saved-name picker, fitted INSIDE the box width so nothing downstream shifts
		snDD = 22
		snW -= snDD + 2
	}
	if snW < 40 {
		snW = 40 // floor: never collapse the field into an unclickable sliver
	}
	// CJK / non-Latin shownames must render real glyphs while typed (TextFieldEmoji + icFieldFonts, as the IC input
	// does); ASCII keeps the single-font fast path. The placeholder can itself be CJK.
	snPrimary, snEmoji := a.icFieldFonts(a.shownameOverride)
	if a.shownameOverride == "" {
		snPrimary, snEmoji = a.icFieldFonts(namePlaceholder) // route a CJK placeholder too
	}
	a.shownameOverride, _ = c.TextFieldEmoji("icshownameov", sdl.Rect{X: nameBox.X, Y: nameBox.Y, W: snW, H: nameBox.H}, a.shownameOverride, namePlaceholder, snPrimary, snEmoji)
	if name := a.pickNameDropdown("snpick", sdl.Rect{X: nameBox.X + snW + 2, Y: nameBox.Y, W: snDD, H: nameBox.H}); name != "" {
		a.shownameOverride = name
	}
	// Immediate (AO non-interrupting preanim): the preanim plays without holding back the text; session toggle sent via
	// OutgoingMS.Immediate.
	immedX := nameX + shownameBoxW + 6
	// The Immediate toggle is a movable slot; the row cursor flows from the default immedX, never the override, so
	// freeing it doesn't cascade.
	immedDef := sdl.Rect{X: immedX, Y: rowY, W: immedW, H: fH}
	immedBox := a.slotRect(slotICImmediate, immedDef, w, h)
	a.icImmediate = c.Checkbox(immedBox.X, immedBox.Y+(immedBox.H-16)/2, "Immediate", a.icImmediate)
	c.Tooltip(immedBox, "Immediate: the preanim plays without holding back the text (non-interrupting preanim)")
	icX := immedX + immedW + 6 // downstream flows from the DEFAULT position, not the override
	// Additive (2.8): the next message appends to your last one. Shown only when the server advertises it and the
	// master pref is on (courtroom.cpp:1638-1644); hiding forces it off so a stale check can't ride a message.
	if a.sess != nil && a.sess.Features.Has(protocol.FeatureAdditive) && a.d.Prefs.Load().AdditiveText {
		// Movable slot like Immediate: draws through slotRect, the cursor advances by the default width.
		addDef := sdl.Rect{X: icX, Y: rowY, W: icAddW, H: fH}
		addBox := a.slotRect(slotICAdditive, addDef, w, h)
		// bounceOnToggle: ui_additive -> on_additive_clicked -> focus_ic_input (courtroom.cpp:504, :6572). The
		// leading-space half is at the send site (AdditiveWantsLeadingSpace).
		a.icAdditive = a.bounceOnToggle(c.Checkbox(addBox.X, addBox.Y+(addBox.H-16)/2, "Additive", a.icAdditive), a.icAdditive)
		c.Tooltip(addBox, "Additive: this message adds to your last one instead of replacing it (2.8 narration-style RP).")
		icX += icAddW + 6 // downstream flows from the DEFAULT position, not the override
	} else {
		a.icAdditive = false
	}
	icCounterOn := a.d.Prefs.Load().MessageCounter
	// The IC input always keeps at least minICInputW: each optional button is placed only if it leaves that room, so a
	// narrow bar drops them in reverse draw order (emoji, SFX, FX, Pre) rather than pushing the input off the edge.
	// Widths key off icBar.W. Inlined (no closure) to stay alloc-free.
	//
	// Draw order is survival order: Pre, FX, SFX dropdown, emoji. On 2.8 additive servers at 720p the Additive toggle
	// can push everything past the bar; the classic editor override forces any dropped button back.
	//
	// The message counter draws inside the input's right edge (drawMsgCounter), so tailReserve is just minICInputW.
	tailReserve := int32(minICInputW)
	// Pre toggle (AO2 ui_pre): send the emote's preanimation. Auto-follows each emote pick (selectEmote); unchecked
	// forces idle (NormalizeOutgoingEmoteMod at the send site). It sits inside the narrow-bar drop discipline so it
	// can't shove the input off the edge. Movable slot; the cursor advances by the default width.
	//
	// Pre draws first so it outranks SFX/emoji: its guard prices only its own width plus the input floor. Inlined
	// arithmetic (no closure).
	// A saved override or an armed editor wins over the width drop (a dropped button never registers a slot, so it
	// couldn't be grabbed); hidden still hides. The cursor still advances by the default width so nothing cascades.
	_, preOv := a.classicOv[slotICPre]
	preGuardOK := icBarButtonFits(icBar.W, icX-icBar.X, preW+6, tailReserve)
	if icOptionalDraws(preGuardOK, preOv, a.classicEdit, a.panelHidden(slotICPre)) {
		preBox := a.slotRect(slotICPre, sdl.Rect{X: icX, Y: rowY, W: preW, H: fH}, w, h)
		// bounceOnToggle: ui_pre is wired straight to focus_ic_input (courtroom.cpp:502).
		a.icPreanim = a.bounceOnToggle(c.Checkbox(preBox.X, preBox.Y+(preBox.H-16)/2, "Pre", a.icPreanim), a.icPreanim)
		c.Tooltip(preBox, "Pre: play this emote's pre-animation before the line. Follows each emote you pick; uncheck to skip an emote's intro.")
		icX += preW + 6 // downstream flows from the DEFAULT position, not the override
	}
	// Text FX cycle button (Off, Shake, Wave, Rainbow), movable. A core button like Pre: drawn before SFX/emoji so they
	// yield first. Its guard prices fxBtnW+4 plus the input floor; no panelHidden gate in the classic row, so
	// hidden=false.
	_, fxOv := a.classicOv[slotICFx]
	fxGuardOK := icBarButtonFits(icBar.W, icX-icBar.X, fxBtnW+4, tailReserve)
	if icOptionalDraws(fxGuardOK, fxOv, a.classicEdit, false) {
		a.fxButton(a.slotRect(slotICFx, sdl.Rect{X: icX, Y: rowY, W: fxBtnW, H: fH}, w, h))
		icX += fxBtnW + 4
	}
	// Flip toggle (AO2 ui_flip): mirror your emotes. Shown whenever the server advertises FLIPPING
	// (courtroom.cpp:1629-1636, tooltip courtroom.cpp:1087). One bool, two views: it mirrors a.pairFlip (the Pair
	// panel checkbox), and Checkbox returns the current value when unclicked, so they can't drift. pairFlip is session
	// state parked per tab, so no forced-off reset is needed when the feature vanishes.
	//
	// No width guard: AO2 shows ui_flip regardless of bar width; at narrow bars the input shrinks past its floor
	// instead.
	flipInChain := a.sess != nil && a.sess.Features.Has(protocol.FeatureFlipping)
	if flipInChain && !a.panelHidden(slotICFlip) {
		flipBox := a.slotRect(slotICFlip, sdl.Rect{X: icX, Y: rowY, W: icFlipW, H: fH}, w, h)
		// bounceOnToggle: ui_flip -> focus_ic_input (courtroom.cpp:503). Only the IC-bar view bounces; the Pair panel
		// mirror writes an equal value, so the two views can't bounce each other.
		a.pairFlip = a.bounceOnToggle(c.Checkbox(flipBox.X, flipBox.Y+(flipBox.H-16)/2, "Flip", a.pairFlip), a.pairFlip)
		c.Tooltip(flipBox, "Flip: mirror your character's emotes. Same setting as the Pair panel's flip toggle.")
		icX += icFlipW + 6 // downstream flows from the DEFAULT position, not the override
	}
	// SFX picker (AO2-style): the picked sound rides your next message, overriding the emote's until set back to auto;
	// picking one previews it. Drawn after Pre/FX, so it yields before them.
	a.ensureSFXChoices()
	// Override/editor win over the drop (like Pre). No panelHidden gate: hidden=false keeps this a pure
	// width/override/edit decision.
	//
	// Two-form drop: the full sfxDDW dropdown when it fits (or an override/editor forces it), else a compact
	// fixed-width "SFX" button opening the same list; at minuscule widths SFX drops, and the sfxOv override still
	// forces it back.
	_, sfxOv := a.classicOv[slotICSFX]
	sfxGuardOK := icBarButtonFits(icBar.W, icX-icBar.X, sfxDDW+4, tailReserve)
	sfxCompactGuardOK := icBarButtonFits(icBar.W, icX-icBar.X, sfxCompactW+4, tailReserve)
	switch {
	case icOptionalDraws(sfxGuardOK, sfxOv, a.classicEdit, false):
		sfxRect := a.slotRect(slotICSFX, sdl.Rect{X: icX, Y: rowY, W: sfxDDW, H: fH}, w, h) // movable (#4a)
		if next, changed := c.Dropdown("sfxdd", sfxRect, a.sfxChoices, a.sfxChoiceIdx); changed {
			a.applySFXPick(next) // pick + audition + focus bounce (icsfx.go; courtroom.cpp:5498-5502)
		}
		a.sfxRowRightClickPreview() // right-click auditions a row without picking it
		c.TooltipAfter("sfxdd-tip", sfxRect, "Sound for your NEXT message — 'auto' uses the emote's own sound, or pick one to override. Picking previews it; right-click a row to hear it without picking. Extras → SFX Browser for favourites & any sound by name.")
		icX += sfxDDW + 4
	case sfxCompactGuardOK:
		// Compact fallback: opens the SFX Browser; a '•' marks an active override.
		compactRect := sdl.Rect{X: icX, Y: rowY, W: sfxCompactW, H: fH}
		label := "SFX"
		if a.sfxChoiceIdx > 0 {
			label = "SFX•" // an override is active (not the "auto" default)
		}
		if c.Button(compactRect, label) {
			a.toggleSfxBrowser()
		}
		c.TooltipAfter("sfxcompact-tip", compactRect, "SFX picker (collapsed to fit the bar): opens the SFX Browser to pick the sound for your next message. Widen the window for the inline dropdown.")
		icX += sfxCompactW + 4
	}
	// effects_dropdown (AO2 ui_effects_dropdown, courtroom.cpp:968): the screen effect that rides your next message.
	// Hidden while spectating or with an empty roster (:5586-5598), so it needs no panelHidden gate; it is not a
	// movable classic slot.
	if a.overlayEffectsVisible() && icBarButtonFits(icBar.W, icX-icBar.X, effectsDDW+4, tailReserve) {
		a.drawEffectsPicker(sdl.Rect{X: icX, Y: rowY, W: effectsDDW, H: fH})
		icX += effectsDDW + 4
	}
	// Emoji picker button on the IC bar's left edge: movable and hideable. Drawn last, so it is the first to yield.
	// Override/editor win over the drop.
	_, emojiOv := a.classicOv[slotICEmoji]
	emojiGuardOK := icBarButtonFits(icBar.W, icX-icBar.X, fH+4, tailReserve)
	if icOptionalDraws(emojiGuardOK, emojiOv, a.classicEdit, a.panelHidden(slotICEmoji)) {
		if a.drawEmojiBarButton(a.slotRect(slotICEmoji, sdl.Rect{X: icX, Y: rowY, W: fH, H: fH}, w, h)) {
			a.showEmojiPicker = !a.showEmojiPicker
		}
		icX += fH + 4
	}
	// No React button on the IC bar; incoming reaction floats still render (Hide-reactions hides them).
	icW := icBar.W - (icX - icBar.X)
	if icW < minICInputW {
		icW = minICInputW // defensive floor: never collapse the input, even on a tiny stage
	}
	// The IC text input is a movable, resizable slot filling the rest of the bar; the message counter draws inside its
	// right edge (drawMsgCounter).
	icBox := a.slotRect(slotICInput, sdl.Rect{X: icX, Y: rowY, W: icW, H: fH}, w, h)
	icPrimary, icEmoji := a.icFieldFonts(a.icInput) // #M5: show typed emoji/unicode, not tofu
	// Reserve the counter and muted-chip band before the field draws so typed text clips short of them.
	c.ReserveFieldTail(icFieldID, a.icFieldTailReserve(icCounterOn))
	a.icInput, send = c.TextFieldEmoji(icFieldID, icBox, a.icInput, "Talk in-character here…  (/pair <id>, /unpair, /offset <x> [y], /pos <side>)", icPrimary, icEmoji)
	a.recallIC() // #8: Up/Down recall recently-sent lines when the IC field is focused
	a.drawMsgCounter(icBox, icCounterOn)
	// Muted (MU): the server disabled our IC speech. Draw a chip over the input's right edge so the player sees why;
	// sendIC refuses the send and keeps the typed line.
	if a.sess != nil && a.sess.Muted {
		chipW := a.mutedChipW() // the ONE metric; icMutedBandW reserves off the same number
		if chipW < icBox.W {    // only when it fits inside the field
			chip := sdl.Rect{X: icBox.X + icBox.W - chipW - 2, Y: icBox.Y + 2, W: chipW, H: icBox.H - 4}
			c.Fill(chip, sdl.Color{R: 120, G: 30, B: 30, A: 210})
			c.Label(chip.X+6, chip.Y+(chip.H-14)/2, mutedChipLabel, ColDanger)
		}
	}
	return send
}
