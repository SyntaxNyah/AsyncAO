package ui

// Theme layout engine: when the active theme ships AO2's
// courtroom_design.ini geometry ("courtroom" + "viewport" rects at
// minimum), the courtroom adopts it wholesale — every widget the theme
// positions draws at its design rect (scaled to the window, letterboxed),
// with the theme's own button art. Elements the theme doesn't define fall
// back per-widget; themes without the geometry keep the classic layout
// entirely. That is what "applying a theme" means to an AO player.

import (
	"math"
	"time"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
)

// AO2 themes "hide" elements by flinging them off the design space
// (11037 by convention, on either axis); anything starting past the
// design bounds is treated as absent. Qt also CLIPPED overhanging
// children at the fixed window edge — themes rely on that, so scaled
// rects clamp into the courtroom area instead of drawing into the void.
const (
	// minThemedElementPx rejects degenerate post-scale rects (a 3 px
	// "button" is sloppy theme data, not a widget).
	minThemedElementPx = 6
	// logMergeOverlapFrac: when the IC and OOC log rects overlap this
	// much, the theme meant them as one toggled widget (AO2's ooc_toggle)
	// — we draw them tabbed in the shared rect.
	logMergeOverlapFrac = 0.6
)

// clampRectInto shifts r inside bounds, shrinking only when r is larger
// than bounds on an axis. Qt amputated these at the window edge; shifting
// keeps the whole widget visible and usable. ok=false when r never
// intersected bounds at all (treated as hidden).
func clampRectInto(r, bounds sdl.Rect) (sdl.Rect, bool) {
	if _, hits := r.Intersect(&bounds); !hits {
		return sdl.Rect{}, false
	}
	if r.W > bounds.W {
		r.W = bounds.W
	}
	if r.H > bounds.H {
		r.H = bounds.H
	}
	if r.X < bounds.X {
		r.X = bounds.X
	}
	if r.Y < bounds.Y {
		r.Y = bounds.Y
	}
	if r.X+r.W > bounds.X+bounds.W {
		r.X = bounds.X + bounds.W - r.W
	}
	if r.Y+r.H > bounds.Y+bounds.H {
		r.Y = bounds.Y + bounds.H - r.H
	}
	return r, true
}

// rectOverlapFrac reports how much of the smaller rect lies inside the
// other (0..1).
func rectOverlapFrac(a, b sdl.Rect) float64 {
	sect, hits := a.Intersect(&b)
	if !hits {
		return 0
	}
	areaA := int64(a.W) * int64(a.H)
	areaB := int64(b.W) * int64(b.H)
	small := areaA
	if areaB < small {
		small = areaB
	}
	if small <= 0 {
		return 0
	}
	return float64(int64(sect.W)*int64(sect.H)) / float64(small)
}

// themeLayoutCache holds the window-scaled design rects; recomputed only
// when the window size or the theme changes (render loop reads are plain
// map probes on pre-scaled rects).
type themeLayoutCache struct {
	valid          bool
	winW, winH     int32
	designW        int32
	scaleX, scaleY float64 // per-axis: equal for Letterbox/Crop, independent for Stretch
	offX, offY     int32
	fit            int                 // ThemeFit mode it was built for (rebuild on change)
	r              map[string]sdl.Rect // scaled; absolute except showname/message (chatbox-relative)
}

// themeLayout returns the cache for the current window, rebuilding on
// window resize or theme swap (pollThemeApply invalidates).
func (a *App) themeLayout(w, h int32) *themeLayoutCache {
	lay := &a.themeLay
	fit := a.d.Prefs.ThemeFitMode()
	if lay.valid && lay.winW == w && lay.winH == h && lay.fit == fit {
		return lay
	}
	*lay = themeLayoutCache{winW: w, winH: h, fit: fit}
	court, okCourt := a.themeRects["courtroom"]
	_, okVP := a.themeRects["viewport"]
	if !okCourt || !okVP || court.W <= 0 || court.H <= 0 {
		return lay
	}
	lay.designW = int32(court.W)
	// Per-axis scale by fit mode: Stretch fills both axes independently (no
	// bars, slight distortion); Crop scales UP uniformly and lets the overflow
	// run off-screen; Letterbox scales DOWN uniformly with centered bars.
	sx, sy := float64(w)/float64(court.W), float64(h)/float64(court.H)
	switch fit {
	case config.ThemeFitStretch:
		lay.scaleX, lay.scaleY = sx, sy
	case config.ThemeFitCrop:
		s := math.Max(sx, sy)
		lay.scaleX, lay.scaleY = s, s
	case config.ThemeFitCustom:
		s := math.Min(sx, sy) * float64(a.d.Prefs.ThemeZoom()) / 100 // manual zoom over the fit
		lay.scaleX, lay.scaleY = s, s
	default: // ThemeFitLetterbox
		s := math.Min(sx, sy)
		lay.scaleX, lay.scaleY = s, s
	}
	lay.offX = (w - int32(float64(court.W)*lay.scaleX)) / 2
	lay.offY = (h - int32(float64(court.H)*lay.scaleY)) / 2
	if fit == config.ThemeFitCustom { // pan to crop where you like
		px, py := a.d.Prefs.ThemePan()
		lay.offX += int32(px) * w / 100
		lay.offY += int32(py) * h / 100
	}
	courtArea := sdl.Rect{
		X: lay.offX, Y: lay.offY,
		W: int32(float64(court.W) * lay.scaleX),
		H: int32(float64(court.H) * lay.scaleY),
	}
	lay.r = make(map[string]sdl.Rect, len(a.themeRects))
	for key, r := range a.themeRects {
		if r.X > court.W || r.Y > court.H {
			continue // off-design = hidden (the 11037 convention, both axes)
		}
		sr := sdl.Rect{
			X: int32(float64(r.X) * lay.scaleX),
			Y: int32(float64(r.Y) * lay.scaleY),
			W: int32(float64(r.W) * lay.scaleX),
			H: int32(float64(r.H) * lay.scaleY),
		}
		// showname/message are children of the chatbox in AO2 — keep them
		// chatbox-relative; everything else becomes window-absolute and
		// clamps into the stage like Qt's window edge clipped it.
		if key != "showname" && key != "message" {
			sr.X += lay.offX
			sr.Y += lay.offY
			clamped, ok := clampRectInto(sr, courtArea)
			if !ok {
				continue // never touched the stage: hidden
			}
			sr = clamped
			if sr.W < minThemedElementPx || sr.H < minThemedElementPx {
				continue // sloppy theme data, not a usable widget
			}
		}
		lay.r[key] = sr
	}
	lay.valid = true
	return lay
}

// rect fetches a usable scaled rect (absent when the theme hides or omits
// it — hidden/overhang filtering happened at build time).
func (l *themeLayoutCache) rect(key string) (sdl.Rect, bool) {
	r, ok := l.r[key]
	if !ok || r.W <= 0 || r.H <= 0 {
		return sdl.Rect{}, false
	}
	return r, true
}

// drawScreenBackdrop paints a theme screen background (lobbybackground /
// charselect_background) stretched to the window, or the flat client
// color when the theme ships none.
func (a *App) drawScreenBackdrop(w, h int32, stem string) {
	c := a.ctx
	dst := sdl.Rect{X: 0, Y: 0, W: w, H: h}
	// The lobby/server list defaults to the plain client backdrop: a busy AO2
	// lobbybackground (built for AO2's own list) often renders our server list
	// unreadable. Untick "plain lobby" in Settings → Theme to use the theme's.
	if stem == "lobbybackground" && a.d.Prefs.PlainLobbyOn() {
		c.Fill(dst, ColBackground)
		return
	}
	if page, ok := a.themePage(stem); ok {
		_ = c.Ren.Copy(a.themeFrame(page), nil, &dst)
		return
	}
	c.Fill(dst, ColBackground)
}

// drawThemeButton draws one themed widget: the theme's art when resident
// (hover = accent border), the kit's chip button otherwise. Reports clicks.
func (a *App) drawThemeButton(key, label string, r sdl.Rect) bool {
	c := a.ctx
	if page, ok := a.themePage(themeBtnPrefix + key); ok {
		_ = c.Ren.Copy(a.themeFrame(page), nil, &r)
		hov := c.hovering(r)
		if hov {
			c.Border(r, ColAccent)
		}
		return hov && c.clicked
	}
	return c.Button(r, label)
}

// drawThemedSFXPicker draws the per-message SFX dropdown at rect — shared by the themed
// IC row's crammed and theme-placed (asyncao_ic_sfx, #4b) paths so they can't drift.
func (a *App) drawThemedSFXPicker(rect sdl.Rect) {
	c := a.ctx
	if next, changed := c.Dropdown("sfxdd", rect, a.sfxChoices, a.sfxChoiceIdx); changed {
		a.sfxChoiceIdx = next
		if next > 0 && next < len(a.sfxChoices) {
			a.d.Audio.PlaySFX(a.urls.SFX(a.sfxChoices[next]), 0) // preview the picked sound
		}
	}
	c.TooltipAfter("sfxdd-tip", rect, "Sound for your NEXT message — 'auto' uses the emote's own sound, or pick one to override. Extras → SFX Browser for favourites & any sound by name.")
}

// drawCourtroomThemed is the design-driven courtroom. Geometry comes from
// the theme; behavior is shared with the classic path (same state, same
// send/poll helpers, same modals).
func (a *App) drawCourtroomThemed(w, h int32, lay *themeLayoutCache) {
	c := a.ctx
	// Layout edit mode fences every widget below (they draw, but stay
	// inert); the editor overlay paints and interacts at the very end.
	a.layoutEditFence()
	a.themedExtrasHint() // one-time-per-session: point players at the Extras box

	// Stage: letterbox fill, then the theme's window art over the design
	// area (this alone makes the whole screen read as "themed").
	c.Fill(sdl.Rect{X: 0, Y: 0, W: w, H: h}, ColBackground)
	court := sdl.Rect{
		X: lay.offX, Y: lay.offY,
		W: w - 2*lay.offX, H: h - 2*lay.offY,
	}
	if page, ok := a.themePage("courtroombackground"); ok {
		_ = c.Ren.Copy(a.themeFrame(page), nil, &court)
	} else {
		c.Fill(court, ColPanel)
	}

	vp, _ := lay.rect("viewport")
	c.Fill(vp, sdl.Color{R: 0, G: 0, B: 0, A: 255})
	a.renderViewportZoomed(vp)
	inChat := false
	if box, ok := lay.rect("ao2_chatbox"); ok {
		inChat = c.hovering(box)
	}
	a.handleViewportZoom(vp, inChat)
	if a.vpZoom <= 1 {
		a.handleSpriteDrag(vp)
		a.handleSpriteHide(vp) // right-click → hide-sprite confirm (default ON)
	}
	a.handleHotkeys()
	if a.rehearsal {
		c.Label(vp.X+8, vp.Y+8, rehearsalBadge, ColTierYellow)
	}

	// Chatbox at its design rect; classic overlay when the theme has none.
	if box, ok := lay.rect("ao2_chatbox"); ok {
		a.drawThemedChatBox(box, lay)
	} else {
		a.drawChatOverlay(vp, false, 0, 0) // themed layout owns its chatbox geometry
	}
	a.drawCourtOverlays(vp, lay)
	a.drawReactionFloats(vp) // #2: emoji reactions rising over the stage (0-alloc when none)

	// Modal popups: same shared list as the classic path, so the two can't
	// drift (the bg picker once drew in classic but was missing here).
	if a.drawCourtroomModals(w, h) {
		return
	}

	// Logs: IC at ic_chatlog; OOC log prefers server_chatlog (AO2's joint
	// log) and falls back to ms_chatlog. Themes that stack the two on the
	// same rect meant them as one toggled widget (AO2's ooc_toggle) —
	// detect the overlap and draw them tabbed instead of on top of each
	// other.
	icRect, okIC := lay.rect("ic_chatlog")
	oocRect, okOOC := lay.rect("server_chatlog")
	if !okOOC {
		oocRect, okOOC = lay.rect("ms_chatlog")
	}
	merged := okIC && okOOC && rectOverlapFrac(icRect, oocRect) >= logMergeOverlapFrac
	if merged && !a.panelHidden(panelLog) {
		tab := sdl.Rect{X: icRect.X, Y: icRect.Y, W: 44, H: 22}
		if c.Button(tab, "IC") {
			a.logTab = logTabLog
		}
		tab.X += 48
		if c.Button(tab, "OOC") {
			a.logTab = logTabOOC
		}
		tab.X += 48
		tab.W = 56
		if c.Button(tab, "Notes") {
			a.logTab = logTabNotes
		}
		inner := sdl.Rect{X: icRect.X, Y: icRect.Y + 26, W: icRect.W, H: icRect.H - 26}
		switch a.logTab {
		case logTabOOC:
			a.drawOOCLogList(inner)
		case logTabNotes:
			a.drawNotesTab(inner)
		default:
			a.drawICLogList(inner)
		}
	} else if okIC && !a.panelHidden(panelLog) {
		a.drawICLogList(icRect)
	}
	if okOOC && !merged && !a.panelHidden(panelOOC) {
		a.drawOOCLogList(oocRect)
	}
	// OOC inputs draw wherever the theme put them — independent of how
	// the log rects resolved (merged tabs still need a send box).
	if !a.panelHidden(panelOOC) {
		if in, ok := lay.rect("ooc_chat_message"); ok {
			var send bool
			a.oocInput, send = c.TextField("ooc", in, a.oocInput, "OOC chat...")
			if send {
				a.submitOOC()
			}
			a.recallOOC() // Up/Down recall recently-sent OOC lines when the themed field is focused
		}
		if nameR, ok := lay.rect("ooc_chat_name"); ok {
			a.ensureNameOpts()
			dd := int32(0)
			if len(a.nameOpts) > 1 && nameR.W > 60 { // tiny ▾ picker, fitted inside the theme's box
				dd = 20
				nameR.W -= dd + 2
			}
			// TAB-LOCAL (like the classic OOC box): the saved default is the
			// Settings → Identity field only, so a name typed here can't
			// follow you into other tabs.
			a.oocName, _ = c.TextField("oocname", nameR, a.oocName, "OOC name")
			if name := a.pickNameDropdown("oocpick", sdl.Rect{X: nameR.X + nameR.W + 2, Y: nameR.Y, W: dd, H: nameR.H}); name != "" {
				a.oocName = name
			}
		}
	}

	// Music / Areas / Players share the music_list rect (AO2 toggles them; we chip).
	if r, ok := lay.rect("music_list"); ok && !a.panelHidden(panelLog) {
		toggle := sdl.Rect{X: r.X, Y: r.Y, W: 60, H: 22}
		if c.Button(toggle, "Music") {
			a.logTab = logTabMusic
		}
		toggle.X += 64
		if c.Button(toggle, "Areas") {
			a.logTab = logTabAreas
		}
		toggle.X += 64
		if c.Button(sdl.Rect{X: toggle.X, Y: toggle.Y, W: 96, H: toggle.H}, "Player List") {
			a.logTab = logTabPlayers
		}
		inner := sdl.Rect{X: r.X, Y: r.Y + 26, W: r.W, H: r.H - 26}
		switch a.logTab {
		case logTabAreas:
			a.drawAreaList(inner)
		case logTabPlayers:
			a.drawPlayerList(inner)
		default:
			a.drawMusicList(inner)
		}
	}

	// IC input row: color swatch + name dropdown + the message field at
	// its design rect (the dropdown's open list auto-widens past 64px). Shares
	// the classic row's full colour list + helpers (palette, extended #98,
	// Rainbow/Random) so the two layouts stay in lock-step.
	if in, ok := lay.rect("ao2_ic_chat_message"); ok {
		icSel, sw := a.icColorSelected()
		const themedColorW = 64
		// Colour swatch + dropdown: at its OWN theme rect (asyncao_ic_color, #4b) if the
		// theme places it there, else crammed at the message rect's left edge (classic).
		colorR, ownColor := lay.rect("asyncao_ic_color")
		lead := int32(0)
		if !ownColor {
			colorR = sdl.Rect{X: in.X, Y: in.Y, W: 14 + themedColorW, H: in.H}
			lead = 14 + themedColorW + 4 // crammed: the field starts after the colour
		}
		swatch := sdl.Rect{X: colorR.X, Y: colorR.Y, W: 12, H: colorR.H}
		c.Fill(swatch, sw)
		c.Border(swatch, ColPanelHi)
		a.icSwatchRect = swatch // the free-hex wheel anchors here (v1.52.0)
		if a.icCustomOn && c.clicked && c.hovering(swatch) {
			a.showICColorWheel = !a.showICColorWheel // re-open to adjust (same as the classic row)
		}
		if next, changed := c.Dropdown("colordd", sdl.Rect{X: colorR.X + 14, Y: colorR.Y, W: themedColorW, H: colorR.H}, icColorChoices, icSel); changed {
			a.applyICColorChoice(next)
		}
		fieldX, fieldW := in.X+lead, in.W-lead
		// Immediate toggle: at its OWN theme rect (asyncao_ic_immediate, #4b) if placed,
		// else crammed into the field (only when the message rect is wide enough after it).
		const (
			themedImmedW    = 96  // space reserved for the "Immediate" checkbox
			themedImmedKeep = 120 // min field width to still host the checkbox
		)
		// The classic theme IC bar doesn't host the #14 Additive toggle, so force it
		// off here — otherwise a check left on in the default layout could silently
		// ride a message sent from this layout.
		a.icAdditive = false
		if ir, ownImmed := lay.rect("asyncao_ic_immediate"); ownImmed {
			a.icImmediate = c.Checkbox(ir.X, ir.Y+(ir.H-16)/2, "Immediate", a.icImmediate)
			c.Tooltip(ir, "Immediate: the preanim plays without holding back the text")
		} else if fieldW > themedImmedW+themedImmedKeep {
			a.icImmediate = c.Checkbox(fieldX, in.Y+(in.H-16)/2, "Immediate", a.icImmediate)
			c.Tooltip(sdl.Rect{X: fieldX, Y: in.Y, W: themedImmedW, H: in.H}, "Immediate: the preanim plays without holding back the text")
			fieldX += themedImmedW
			fieldW -= themedImmedW
		}
		field := sdl.Rect{X: fieldX, Y: in.Y, W: fieldW, H: in.H}
		// SFX picker (AO2-style): a sound for your NEXT message, overriding the emote's
		// own until set back to "auto". Picking one previews it. First so it survives a
		// narrow field longest.
		a.ensureSFXChoices()
		if sr, ownSFX := lay.rect("asyncao_ic_sfx"); ownSFX { // own theme rect (#4b)
			a.drawThemedSFXPicker(sr)
		} else if field.W > 92+120 {
			a.drawThemedSFXPicker(sdl.Rect{X: field.X, Y: field.Y, W: 92, H: field.H})
			field.X += 92 + 4
			field.W -= 92 + 4
		}
		// emoji / FX / React buttons: each at its OWN theme rect (asyncao_ic_emoji / _fx /
		// _react, #4b) if the theme places it there, else crammed into the field
		// left-to-right (only when there's room — #M2 S1 / #M5 / #2).
		if a.panelHidden(slotICEmoji) {
			// hideable in both layouts (playtest: some players don't want it)
		} else if er, ownEmoji := lay.rect("asyncao_ic_emoji"); ownEmoji {
			if a.drawEmojiBarButton(er) {
				a.showEmojiPicker = !a.showEmojiPicker
			}
		} else if field.W > field.H+120 {
			if a.drawEmojiBarButton(sdl.Rect{X: field.X, Y: field.Y, W: field.H, H: field.H}) {
				a.showEmojiPicker = !a.showEmojiPicker
			}
			field.X += field.H + 4
			field.W -= field.H + 4
		}
		if fr, ownFX := lay.rect("asyncao_ic_fx"); ownFX {
			a.fxButton(fr)
		} else if field.W > fxBtnW+120 {
			a.fxButton(sdl.Rect{X: field.X, Y: field.Y, W: fxBtnW, H: field.H})
			field.X += fxBtnW + 4
			field.W -= fxBtnW + 4
		}
		// The #2 React BUTTON was removed by request (playtest: unused); the
		// asyncao_ic_react theme key is now a no-op for compatibility.
		var send bool
		icCounterOn := a.d.Prefs.MessageCounterOn()
		if icCounterOn {
			field.W -= msgCounterReserve
		}
		icPrimary, icEmoji := a.icFieldFonts(a.icInput) // #M5: show typed emoji/unicode, not tofu
		a.icInput, send = c.TextFieldEmoji("ic", field, a.icInput, "Talk in-character here…", icPrimary, icEmoji)
		a.recallIC() // #8: Up/Down recall recently-sent lines when the IC field is focused
		a.drawMsgCounter(field, icCounterOn)
		if send {
			a.sendIC(0)
		}
	}
	if nameR, ok := lay.rect("ao2_ic_chat_name"); ok {
		// Session override box (matches the classic layout): blank falls
		// back to — and shows — the persisted Settings showname.
		namePlaceholder := a.d.Prefs.SavedShowname()
		if namePlaceholder == "" {
			namePlaceholder = "Showname"
		}
		a.ensureNameOpts()
		dd := int32(0)
		if len(a.nameOpts) > 1 && nameR.W > 60 { // tiny ▾ saved-name picker, fitted inside the theme's box
			dd = 20
			nameR.W -= dd + 2
		}
		a.shownameOverride, _ = c.TextField("icshownameov", nameR, a.shownameOverride, namePlaceholder)
		if name := a.pickNameDropdown("snpick", sdl.Rect{X: nameR.X + nameR.W + 2, Y: nameR.Y, W: dd, H: nameR.H}); name != "" {
			a.shownameOverride = name
		}
	}

	// Shouts at their design rects, themed art when shipped.
	if !a.panelHidden(panelShouts) {
		shouts := []struct {
			key, label string
			mod        int
		}{
			{"hold_it", "Hold It!", protocol.ShoutHoldIt},
			{"objection", "Objection!", protocol.ShoutObjection},
			{"take_that", "Take That!", protocol.ShoutTakeThat},
		}
		for _, s := range shouts {
			if r, ok := lay.rect(s.key); ok {
				if a.drawThemeButton(s.key, s.label, r) {
					a.sendIC(s.mod)
				}
			}
		}
		if a.sess.Features.Has(protocol.FeatureCustomObjections) && a.hasCustomShout() {
			if r, ok := lay.rect("custom_objection"); ok {
				if a.drawThemeButton("custom_objection", a.customShoutLabel(), r) {
					a.sendIC(protocol.ShoutCustom)
				}
				if len(a.customShouts) > 0 {
					cyc := sdl.Rect{X: r.X + r.W + 2, Y: r.Y, W: 18, H: r.H}
					if c.Button(cyc, "▾") {
						a.customIdx++
						if a.customIdx >= len(a.customShouts) {
							a.customIdx = -1
						}
					}
				}
			}
		}
	}

	// Judge strip (grant- or pos-gated exactly like classic).
	if a.judgeVisible() {
		judge := []struct {
			key, label string
			run        func()
		}{
			{"witness_testimony", "WT", func() { a.sess.SendWTCE("testimony1", 0) }},
			{"cross_examination", "CE", func() { a.sess.SendWTCE("testimony2", 0) }},
			{"not_guilty", "NG", func() { a.sess.SendWTCE("judgeruling", 0) }},
			{"guilty", "G", func() { a.sess.SendWTCE("judgeruling", 1) }},
			{"defense_minus", "D-", func() { a.sess.SendHP(1, a.sess.HPDef-1) }},
			{"defense_plus", "D+", func() { a.sess.SendHP(1, a.sess.HPDef+1) }},
			{"prosecution_minus", "P-", func() { a.sess.SendHP(2, a.sess.HPPro-1) }},
			{"prosecution_plus", "P+", func() { a.sess.SendHP(2, a.sess.HPPro+1) }},
		}
		for _, j := range judge {
			if r, ok := lay.rect(j.key); ok {
				if a.drawThemeButton(j.key, j.label, r) {
					j.run()
				}
			}
		}
	}

	// Pos / pair / modcall / evidence at their design homes.
	if r, ok := lay.rect("pos_dropdown"); ok {
		// A real dropdown at the theme's pos_dropdown rect (AO2 parity).
		choices := a.posChoices()
		cur := 0
		for i, p := range choices {
			if p == a.mySide() {
				cur = i
				break
			}
		}
		if next, changed := c.Dropdown("posdd", r, choices, cur); changed {
			a.applySide(choices[next]) // also /pos the server so the move is instant, not next-message
		}
	}
	if r, ok := lay.rect("pair_button"); ok {
		if a.drawThemeButton("pair_button", "Pair", r) {
			a.showPair = !a.showPair
		}
	}
	if r, ok := lay.rect("call_mod"); ok {
		if a.drawThemeButton("call_mod", "Call Mod", r) {
			a.showModcall = true
		}
	}
	evLabel := "Evidence"
	if a.evidPresent {
		evLabel = "Evidence ●"
	}
	if r, ok := lay.rect("evidence_button"); ok {
		if a.drawThemeButton("evidence_button", evLabel, r) {
			a.showEvid = true
		}
	}

	// Emote grid at the theme's metrics, with page arrows.
	if r, ok := lay.rect("emotes"); ok && !a.panelHidden(panelEmotes) {
		a.drawEmoteGridThemed(r, lay, vp)
	}

	// Extras: ONE compact button (bottom-left) opening the box of every AsyncAO
	// feature this AO2 theme has no slot for. Hideable; the hotkey opens it too.
	a.drawThemedExtrasButton(w, h)

	if a.warnActive() {
		c.LabelClipped(pad, h-44, w-2*pad, a.warnLine, ColDanger)
	}

	// Layout editor overlay: last, above everything it edits.
	if a.layoutEdit {
		a.drawLayoutEditor(w, h, lay)
	}
}

// drawThemedChatBox is the design-rect chatbox: the skin stretches to the
// ao2_chatbox rect, showname/message sit at their chatbox-relative design
// rects (AO2 child-widget semantics).
func (a *App) drawThemedChatBox(box sdl.Rect, lay *themeLayoutCache) {
	c := a.ctx
	sc := &a.room.Scene
	// Blankpost hides the whole chatbox (frame + showname + text) so only the
	// sprite shows; the second clause is the unchanged idle/no-message case.
	if sc.IsBlankPost || (sc.MessageText == "" && sc.ShownameText == "") {
		return
	}
	skinned := false
	if page, ok := a.themePage(themeStemChatbox); ok {
		_ = c.Ren.Copy(a.themeFrame(page), nil, &box)
		skinned = true
	}
	if !skinned {
		bg := sdl.Color{R: 16, G: 16, B: 24, A: 215}
		if col, ok := a.partPanel(partChatbox); ok { // per-part tint (v1.52.0): custom base, opacity kept
			bg = sdl.Color{R: col.R, G: col.G, B: col.B, A: bg.A}
		}
		if a.d.Prefs.ChatboxTintOn() && sc.ShownameText != "" { // #14 per-character tint
			bg = chatboxTintFor(sc.ShownameText, bg)
		}
		c.Fill(box, bg)
		c.Border(box, ColAccent)
	}

	nameX, nameY := box.X+8, box.Y+4
	nameW := box.W - 16
	if r, ok := lay.rect("showname"); ok {
		nameX, nameY = box.X+r.X, box.Y+r.Y
		nameW = r.W
	}
	msgX, msgY := box.X+8, box.Y+26
	wrapW := box.W - 16
	if r, ok := lay.rect("message"); ok {
		msgX, msgY = box.X+r.X, box.Y+r.Y
		wrapW = r.W
	}

	nameCol := ColAccent
	if skinned && a.themeHasName {
		nameCol = a.themeNameCol
	}
	if a.d.Prefs.NameColorsOn() { // per-speaker name colour wins over accent/theme
		nameCol = nameColor(sc.ShownameText, float64(a.d.Prefs.NameColorSat())/100, float64(a.d.Prefs.NameColorVal())/100)
	}
	// Clipped: a long showname must never spill past the theme's box. ChatFontFor (not
	// the fixed chrome font) so a non-Latin name resolves to a covering face; emoji-aware
	// so a colour-emoji name renders the glyphs, not tofu.
	a.labelEmoji(c.ChatFontFor(DefaultScalePct, sc.ShownameText), c.EmojiFont(DefaultScalePct), nameX, nameY, nameW, sc.ShownameText, nameCol)

	a.ensureChatRaster(wrapW, skinned)
	// Text selection works on the themed chatbox too (drag a range / dbl-click
	// a word / triple-click all; Ctrl+C / right-click copies) — same handler
	// as the classic overlay, fed this layout's message rect.
	textRect := sdl.Rect{X: msgX, Y: msgY, W: wrapW, H: box.Y + box.H - msgY}
	a.handleChatSelect(textRect, sc)
	if a.msAnim != nil || a.msRaster != nil {
		_ = c.Ren.SetClipRect(&box)
		if a.chatSelActive { // selection highlight, UNDER the text so it reads through
			a.drawChatSelHighlight(msgX, msgY, wrapW, sc)
		}
		if a.msAnim != nil { // #M5 animated message
			reduce := a.d.Prefs.ReduceMotion()
			if a.msAnim.Animates(reduce) {
				a.NoteAnimating() // clock-driven text FX must not freeze at idle=0 — same census as the classic chatbox (screens.go)
			}
			a.msAnim.Draw(c.Ren, a.glyphCache, a.msAnimFont, a.d.Viewport.AnimClock(), sc.VisibleRunes, msgX, msgY, reduce)
		} else {
			a.msRaster.Draw(c.Ren, sc.VisibleRunes, msgX, msgY)
		}
		_ = c.Ren.SetClipRect(nil)
	}
	a.chatZoomWheel(box)
}

// drawEmoteGridThemed lays the emote buttons out on the theme's grid
// (emote_button_size/spacing scaled), paging with emote_left/right.
func (a *App) drawEmoteGridThemed(r sdl.Rect, lay *themeLayoutCache, vp sdl.Rect) {
	c := a.ctx
	if a.charINIBusy {
		c.Label(r.X, r.Y, "Loading emotes...", ColTextDim)
		return
	}
	a.refreshEmoteView() // favourite set + visible-index list (#77)
	vis := a.emoteVisible
	cellW := int32(float64(a.themeEmoteCell[0]) * lay.scaleX)
	cellH := int32(float64(a.themeEmoteCell[1]) * lay.scaleY)
	gapX := int32(float64(a.themeEmoteGap[0]) * lay.scaleX)
	gapY := int32(float64(a.themeEmoteGap[1]) * lay.scaleY)
	if cellW < 8 || cellH < 8 {
		cellW, cellH = 40, 40 // degenerate metrics: AO2 stock size
	}
	cols := (r.W + gapX) / (cellW + gapX)
	rows := (r.H + gapY) / (cellH + gapY)
	if cols < 1 {
		cols = 1
	}
	if rows < 1 {
		rows = 1
	}
	perPage := int(cols * rows)
	pages := (len(vis) + perPage - 1) / perPage
	if pages < 1 {
		pages = 1 // favs-only with nothing starred yet: one empty page
	}
	if a.emotePage >= pages {
		a.emotePage = 0
	}
	if pages > 1 { // mouse-wheel pages emotes (parity with the classic row)
		if d := c.WheelIn(r); d > 0 && a.emotePage > 0 {
			a.emotePage--
		} else if d < 0 && a.emotePage < pages-1 {
			a.emotePage++
		}
	}
	start := a.emotePage * perPage

	me := a.activeCharName()
	useImages := a.d.Prefs.EmoteButtonImagesEnabled()
	for slot := start; slot < len(vis) && slot < start+perPage; slot++ {
		i := vis[slot] // real index into a.emotes (favs-only filters which show)
		e := &a.emotes[i]
		n := int32(slot - start)
		btn := sdl.Rect{
			X: r.X + (n%cols)*(cellW+gapX),
			Y: r.Y + (n/cols)*(cellH+gapY),
			W: cellW, H: cellH,
		}
		selected := i == a.emoteIdx
		if selected {
			c.Fill(sdl.Rect{X: btn.X - 2, Y: btn.Y - 2, W: btn.W + 4, H: btn.H + 4}, ColAccent)
		}
		label := e.Comment
		if label == "" {
			label = e.Anim
		}
		var picked bool
		if useImages {
			picked = a.drawEmoteImageButton(btn, me, i, selected, label)
		} else {
			picked = c.Button(btn, label)
		}
		// The favourite ★ (drawn on top) wins the click over selecting the emote.
		if a.drawEmoteFavStar(btn, i) {
			// star toggled — swallow this cell's select for the frame
		} else if picked {
			a.emoteIdx = i
			a.speculateEmote(me, e)
			c.FocusField("ic") // AO2 focus_ic_input: pick emote, keep typing
		}
		// Same hover-preview behavior as the classic row: the preanim (scrubbed)
		// when the emote has one, else the talking sprite — warmed on demand.
		if c.HoverPreview("emote:"+e.Anim, btn) {
			a.previewEmote(me, e)
		}
	}
	// Favs-only with nothing starred yet: tell the user how to recover (the
	// toggle lives in Settings for themed layouts, which are pixel-precise).
	if len(vis) == 0 && a.d.Prefs.EmoteFavOnlyOn() {
		c.Label(r.X, r.Y+4, "No favourite emotes — turn off \"favourites only\" in Settings, then click an emote's ★.", ColTextDim)
	}

	if pages > 1 {
		if lr, ok := lay.rect("emote_left"); ok {
			if a.drawThemeButton("emote_left", "<", lr) && a.emotePage > 0 {
				a.emotePage--
			}
		}
		if rr, ok := lay.rect("emote_right"); ok {
			if a.drawThemeButton("emote_right", ">", rr) && a.emotePage < pages-1 {
				a.emotePage++
			}
		}
	}
	if a.previewBase != "" {
		// Clamp to the WINDOW, not the stage — the box is draggable anywhere
		// (playtest: "you can't move the preview out of the viewport, wth").
		a.drawSpritePreview(a.winW, a.winH, false)
		if c.clicked {
			a.previewBase = ""
		}
	}
}

// themedExtrasHint fires a one-time-per-session toast when a themed (AO2) layout
// is in use, pointing players at the Extras button/hotkey — legacy AO2 themes
// carry no element keys for AsyncAO's own features, so they'd otherwise be lost.
func (a *App) themedExtrasHint() {
	if a.themedHintShown {
		return
	}
	a.themedHintShown = true
	hint := "AO2 theme — Wardrobe, Jukebox & all AsyncAO extras are in the ★ Extras button (bottom-left)"
	if a.panelHidden(panelExtras) {
		hint = "AO2 theme — press [" + a.hotkeyFor(hotkeyExtras) + "] for AsyncAO extras (Wardrobe, Jukebox…)"
	}
	a.warnLine = clampLine(hint)
	a.warnAt = time.Now()
}

// drawThemedExtrasButton places the bottom-left affordances for themed mode:
// the "Extras" button — the one entry point to every AsyncAO feature an AO2
// theme has no slot for (the box of widgets) — plus a sibling "Hotkeys" button
// (#96). Two compact buttons pinned bottom-left so they barely touch the theme
// art; both hideable via panelExtras (the Extras hotkey still opens the box).
// The classic (non-themed) layout exposes all of this as real buttons already,
// so this exists for themed mode only.
func (a *App) drawThemedExtrasButton(w, h int32) {
	c := a.ctx
	if a.panelHidden(panelExtras) {
		return // hidden — the Extras hotkey still opens the box
	}
	const bh int32 = 24
	label := "★ Extras"
	bw := c.TextWidth(label) + 14
	r := sdl.Rect{X: 4, Y: h - bh - 2, W: bw, H: bh}
	if c.Button(r, label) {
		a.showWidgets = true
	}
	c.Border(r, ColAccent)
	c.Tooltip(r, "AsyncAO extras (Wardrobe, Jukebox, Background, Settings…) — hotkey: "+a.hotkeyFor(hotkeyExtras))

	// Sibling "Hotkeys" button (#96): one click to the cheat sheet of every
	// shortcut + your custom binds, so themed users needn't open Extras or
	// recall F1. Constant labels → the two TextWidth probes stay alloc-free.
	hkLabel := "Hotkeys"
	hk := sdl.Rect{X: r.X + r.W + 4, Y: r.Y, W: c.TextWidth(hkLabel) + 14, H: bh}
	if c.Button(hk, hkLabel) {
		a.openHotkeyCheatSheet()
	}
	c.Border(hk, ColPanelHi)
	c.Tooltip(hk, "Show all your hotkeys & custom binds (also F1)")

	// Sibling "Restyle" button (#103/#104): one click to the floating Sprite Style
	// box, so themed users can recolour/glow their character without opening Extras.
	stLabel := "Restyle"
	st := sdl.Rect{X: hk.X + hk.W + 4, Y: r.Y, W: c.TextWidth(stLabel) + 14, H: bh}
	if c.Button(st, stLabel) {
		a.openSpriteStyle()
	}
	c.Border(st, ColAccent)
	c.Tooltip(st, "Recolour / glow your character on the fly — other AsyncAO players see it")

	// Sibling "Edit Layout" button: the live layout editor was buried in Hide chrome and nobody
	// found it. Surface it right next to Extras — AO2 themes are exactly where it works. Drag and
	// resize every box live; saves per theme.
	elLabel := "Edit Layout"
	el := sdl.Rect{X: st.X + st.W + 4, Y: r.Y, W: c.TextWidth(elLabel) + 14, H: bh}
	if c.Button(el, elLabel) {
		a.openLayoutEditor()
	}
	c.Border(el, ColAccent)
	c.Tooltip(el, "Live layout editor — drag & resize every box (log, OOC, stage, buttons). Tab cycles overlapping; saves per theme.")

	// Mod / CM launchers (only while you hold the role), chained after Edit Layout — in the row, so
	// they never float over the emote sprites.
	prev := el
	if a.amIMod() {
		m := sdl.Rect{X: prev.X + prev.W + 4, Y: r.Y, W: c.TextWidth("Mod") + 14, H: bh}
		if c.Button(m, "Mod") {
			a.toggleModDash()
		}
		c.Border(m, ColDanger)
		c.Tooltip(m, "Moderation tools — server-aware ban / kick")
		prev = m
	}
	if a.amICMNow {
		cm := sdl.Rect{X: prev.X + prev.W + 4, Y: r.Y, W: c.TextWidth("CM") + 14, H: bh}
		if c.Button(cm, "CM") {
			a.toggleCMPanel()
		}
		c.Border(cm, chipCMColor)
		c.Tooltip(cm, "CM area controls — lock / kick-from-area")
	}
}

// drawWidgetsPanel moved to floatbox.go as the non-blocking drawFloatingExtras.
