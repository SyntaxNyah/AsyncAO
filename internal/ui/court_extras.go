package ui

// Court extras: everything the 2.6–2.8 protocol layers on top of plain IC
// chat — penalty bars (HP), testimony/verdict splashes (RT), server clocks
// (TI), judge controls (JD), modcalls (ZZ), evidence (LE/PE/DE/EE), case
// announcements (CASEA), the SD position dropdown — plus the user-facing
// hide-this-chrome panel. Semantics cite AO2-Client courtroom.cpp /
// packet_distribution.cpp throughout; the session reducer owns the state,
// this file owns pixels and sends.

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/network"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
)

const (
	// hpBarWidthPct sizes each penalty bar as a % of viewport width.
	hpBarWidthPct = 28
	// splashWidthPct sizes the WT/CE/verdict splash as a % of viewport width.
	splashWidthPct = 80
	// badgeWidthPct sizes the looping "Testimony" recording badge.
	badgeWidthPct = 18
	// evidPopupWidthPct sizes the presented-evidence popup.
	evidPopupWidthPct = 22
	// splashStaticDuration holds a STATIC splash image (or the text
	// fallback) on screen; animated splashes run their own length
	// (AO2-Client plays the animation once — wtce_max_time caps frames).
	splashStaticDuration = 1500 * time.Millisecond
	// evidenceShowDuration keeps the presented-evidence popup visible,
	// approximating AOEvidenceDisplay's message-length linger.
	evidenceShowDuration = 4 * time.Second
	// hpBarSegments is the pip count of the procedural fallback bar.
	hpBarSegments = courtroom.HPBarMax
)

// Hideable chrome ids (persisted via Prefs.HiddenPanels).
const (
	panelShouts    = "shouts"
	panelKnobs     = "knobs"
	panelEmotes    = "emotes"
	panelLog       = "log"
	panelOOC       = "ooc"
	panelHP        = "hp"
	panelTimers    = "timers"
	panelTestimony = "testimony"
	panelJudge     = "judge"
)

// hideablePanels drives the UI popup: id + human label, draw order.
var hideablePanels = []struct{ id, label string }{
	{panelShouts, "Shout buttons (Hold It / Objection / Take That)"},
	{panelKnobs, "Layout knobs (View/Text/MsgBox/Log/Input)"},
	{panelEmotes, "Emote buttons"},
	{panelLog, "Right column (log/music/areas/OOC tabs)"},
	{panelOOC, "Bottom OOC row"},
	{panelHP, "Penalty bars"},
	{panelTimers, "Server timers"},
	{panelTestimony, "Testimony recording badge"},
	{panelJudge, "Judge controls (even when granted)"},
}

// panelHidden reports whether the user hid a chrome region.
func (a *App) panelHidden(id string) bool { return a.hidden[id] }

// setPanelHidden flips one region and persists the full set.
func (a *App) setPanelHidden(id string, hide bool) {
	if hide {
		a.hidden[id] = true
	} else {
		delete(a.hidden, id)
	}
	ids := make([]string, 0, len(a.hidden))
	for k := range a.hidden {
		ids = append(ids, k)
	}
	sort.Strings(ids) // stable JSON, stable diffs
	a.d.Prefs.SetHiddenPanels(ids)
}

// --- sounds -----------------------------------------------------------------

// playThemeSFX streams the theme-configured sound for key (picked up at
// theme apply from courtroom_sounds.ini / penalty.ini, with stock AO2
// fallbacks). Missing keys stay silent — AO2 with a sparse INI does too.
func (a *App) playThemeSFX(key string) {
	if a.room == nil {
		return
	}
	if name := a.themeSounds[key]; name != "" {
		a.d.Audio.PlaySFX(a.urls.SFX(name), 0) // AssetType: SFX (court sound)
	}
}

// --- WTCE splashes + testimony badge -----------------------------------------

// wtceFallbackText is the no-theme banner text per splash stem.
var wtceFallbackText = map[string]string{
	"witnesstestimony": "WITNESS TESTIMONY",
	"crossexamination": "CROSS EXAMINATION",
	"notguilty":        "NOT GUILTY",
	"guilty":           "GUILTY",
}

// handleWTCE drives splash + badge + sfx per courtroom.cpp handle_wtce.
func (a *App) handleWTCE(name string, variant int) {
	switch name {
	case "testimony1":
		if variant == 1 {
			// 2.9 "end recording" marker: only the badge stops.
			a.testimonyOn = false
			return
		}
		a.testimonyOn = true
		a.startSplash("witnesstestimony")
		a.playThemeSFX("witness_testimony")
	case "testimony2":
		a.testimonyOn = false
		a.startSplash("crossexamination")
		a.playThemeSFX("cross_examination")
	case "judgeruling":
		a.testimonyOn = false
		if variant == 1 {
			a.startSplash("guilty")
			a.playThemeSFX("guilty")
		} else {
			a.startSplash("notguilty")
			a.playThemeSFX("not_guilty")
		}
	default:
		// Fully custom WTCE: image and sfx share the packet string
		// (handle_wtce's else branch). No preloaded texture → text banner.
		a.startSplash(name)
		a.d.Audio.PlaySFX(a.urls.SFX(name), 0) // AssetType: SFX (custom wtce)
	}
}

func (a *App) startSplash(stem string) {
	a.wtceName, a.wtceAt = stem, time.Now()
}

// pageFrameAt maps elapsed time onto a one-shot animation's frame index;
// done reports playback completion. Static pages hold for
// splashStaticDuration.
func pageFrameAt(page *render.TexturePage, elapsed time.Duration) (idx int, done bool) {
	if !page.Animated || len(page.Frames) < 2 {
		return 0, elapsed >= splashStaticDuration
	}
	var total time.Duration
	for _, d := range page.Delays {
		total += d
	}
	if total <= 0 {
		return 0, elapsed >= splashStaticDuration
	}
	if elapsed >= total {
		return len(page.Frames) - 1, true
	}
	var acc time.Duration
	for i, d := range page.Delays {
		acc += d
		if elapsed < acc {
			return i, false
		}
	}
	return len(page.Frames) - 1, true
}

// pageFrameLoop maps elapsed time onto a looping animation's frame index.
func pageFrameLoop(page *render.TexturePage, elapsed time.Duration) int {
	if !page.Animated || len(page.Frames) < 2 {
		return 0
	}
	var total time.Duration
	for _, d := range page.Delays {
		total += d
	}
	if total <= 0 {
		return 0
	}
	elapsed %= total
	var acc time.Duration
	for i, d := range page.Delays {
		acc += d
		if elapsed < acc {
			return i
		}
	}
	return 0
}

// --- viewport overlays ---------------------------------------------------------

// drawCourtOverlays paints the live court state over the viewport: penalty
// bars, server clocks, the testimony badge, the presented-evidence popup,
// and the WT/CE/verdict splash (top-most). lay positions the bars at their
// design rects when the theme defines them (nil = classic corners).
func (a *App) drawCourtOverlays(vp sdl.Rect, lay *themeLayoutCache) {
	c := a.ctx
	now := time.Now()

	barH := int32(0)
	if !a.panelHidden(panelHP) {
		defR, okDef := sdl.Rect{}, false
		proR, okPro := sdl.Rect{}, false
		if lay != nil {
			defR, okDef = lay.rect("defense_bar")
			proR, okPro = lay.rect("prosecution_bar")
		}
		if okDef {
			a.drawHPBarRect(defR, true, a.sess.HPDef)
		} else {
			barH = a.drawHPBar(vp, true, a.sess.HPDef)
		}
		if okPro {
			a.drawHPBarRect(proR, false, a.sess.HPPro)
		} else if h2 := a.drawHPBar(vp, false, a.sess.HPPro); h2 > barH {
			barH = h2
		}
	}

	// Server clocks: centered chips along the viewport top (between the
	// bars), drawn only while the server marked them visible.
	if !a.panelHidden(panelTimers) {
		var chips []string
		for id := 0; id < courtroom.TimerCount; id++ {
			t := &a.sess.Timers[id]
			if !t.Visible {
				continue
			}
			rem := t.Remaining(now)
			chips = append(chips, fmt.Sprintf("T%d %02d:%02d", id+1,
				int(rem.Minutes()), int(rem.Seconds())%60))
		}
		var totalW int32
		for _, label := range chips {
			totalW += c.TextWidth(label) + 12 + 6
		}
		tx := vp.X + (vp.W-totalW)/2
		for _, label := range chips {
			w := c.TextWidth(label) + 12
			r := sdl.Rect{X: tx, Y: vp.Y + 4, W: w, H: 20}
			c.Fill(r, sdl.Color{R: 0, G: 0, B: 0, A: 185})
			c.Border(r, ColPanelHi)
			c.Label(r.X+6, r.Y+3, label, ColText)
			tx += w + 6
		}
	}

	// Testimony recording badge under the defense bar (loops while the
	// server is recording — RT testimony1 starts it, testimony1#1 ends).
	if a.testimonyOn && !a.panelHidden(panelTestimony) {
		by := vp.Y + 6 + barH + 4
		if page, ok := a.themePage("testimony"); ok {
			w := vp.W * badgeWidthPct / 100
			h := w * page.H / page.W
			dst := sdl.Rect{X: vp.X + 6, Y: by, W: w, H: h}
			_ = c.Ren.Copy(page.Frames[pageFrameLoop(page, time.Since(a.wtceAt))], nil, &dst)
		} else {
			r := sdl.Rect{X: vp.X + 6, Y: by, W: c.TextWidth("● Testimony") + 12, H: 20}
			c.Fill(r, sdl.Color{R: 120, G: 0, B: 0, A: 200})
			c.Label(r.X+6, r.Y+3, "● Testimony", ColText)
		}
	}

	// Presented-evidence popup (display_evidence_image): right side, gone
	// after evidenceShowDuration.
	if a.evShowImg != "" {
		if time.Since(a.evShowAt) >= evidenceShowDuration {
			a.evShowImg = ""
		} else if page, ok := a.d.Store.Get(a.urls.Evidence(a.evShowImg)); ok && len(page.Frames) > 0 {
			w := vp.W * evidPopupWidthPct / 100
			h := w * page.H / page.W
			dst := sdl.Rect{X: vp.X + vp.W - w - 8, Y: vp.Y + vp.H/4, W: w, H: h}
			_ = c.Ren.Copy(page.Frames[0], nil, &dst)
			c.Border(dst, ColAccent)
		}
	}

	// WT/CE/verdict splash, top-most overlay.
	if a.wtceName != "" {
		elapsed := time.Since(a.wtceAt)
		if page, ok := a.themePage(a.wtceName); ok {
			idx, done := pageFrameAt(page, elapsed)
			if done {
				a.wtceName = ""
			} else {
				w := vp.W * splashWidthPct / 100
				h := w * page.H / page.W
				dst := sdl.Rect{X: vp.X + (vp.W-w)/2, Y: vp.Y + (vp.H-h)/2, W: w, H: h}
				_ = c.Ren.Copy(page.Frames[idx], nil, &dst)
			}
		} else if elapsed >= splashStaticDuration {
			a.wtceName = ""
		} else {
			text := wtceFallbackText[a.wtceName]
			if text == "" {
				text = strings.ToUpper(a.wtceName)
			}
			banner := sdl.Rect{X: vp.X, Y: vp.Y + vp.H/2 - 24, W: vp.W, H: 48}
			c.Fill(banner, sdl.Color{R: 0, G: 0, B: 0, A: 210})
			c.Heading(banner.X+(banner.W-c.TextWidth(text)*2)/2, banner.Y+10, text, ColAccent)
		}
	}
}

// drawHPBar draws one penalty bar at the classic corner placement and
// returns the drawn height (the themed path positions via design rects).
func (a *App) drawHPBar(vp sdl.Rect, def bool, val int) int32 {
	w := vp.W * hpBarWidthPct / 100
	x := vp.X + vp.W - w - 6 // prosecution right
	if def {
		x = vp.X + 6 // defense left
	}
	h := int32(12)
	if page, ok := a.themePage(a.hpBarStem(def, val)); ok {
		h = w * page.H / page.W
	}
	a.drawHPBarRect(sdl.Rect{X: x, Y: vp.Y + 6, W: w, H: h}, def, val)
	return h
}

func (a *App) hpBarStem(def bool, val int) string {
	if def {
		return "defensebar" + strconv.Itoa(val)
	}
	return "prosecutionbar" + strconv.Itoa(val)
}

// drawHPBarRect draws one penalty bar into an exact rect: theme art
// (defensebar<N>/prosecutionbar<N>, the images set_hp_bar swaps) stretched
// like AO2 stretches them, or a procedural pip strip.
func (a *App) drawHPBarRect(bar sdl.Rect, def bool, val int) {
	c := a.ctx
	if page, ok := a.themePage(a.hpBarStem(def, val)); ok {
		_ = c.Ren.Copy(a.themeFrame(page), nil, &bar)
		return
	}
	// Procedural fallback: pip strip, blue defense / red prosecution.
	c.Fill(bar, sdl.Color{R: 0, G: 0, B: 0, A: 185})
	fill := sdl.Color{R: 200, G: 40, B: 40, A: 235}
	if def {
		fill = sdl.Color{R: 50, G: 110, B: 230, A: 235}
	}
	segW := (bar.W - 2) / hpBarSegments
	for i := 0; i < val && i < hpBarSegments; i++ {
		seg := sdl.Rect{X: bar.X + 1 + int32(i)*segW, Y: bar.Y + 1, W: segW - 1, H: bar.H - 2}
		c.Fill(seg, fill)
	}
	c.Border(bar, ColPanelHi)
}

// --- judge controls -------------------------------------------------------------

// judgeVisible mirrors show_judge_controls: the JD state wins; pos-dependent
// falls back to "are we on the judge stand" (get_pos_is_judge).
func (a *App) judgeVisible() bool {
	if a.panelHidden(panelJudge) {
		return false
	}
	switch a.sess.Judge {
	case courtroom.JudgeShow:
		return true
	case courtroom.JudgeHide:
		return false
	default:
		return a.mySide() == "jud"
	}
}

// drawJudgeRow renders the judge button strip and returns its height
// (0 when nothing drew). Buttons mirror courtroom.cpp's judge handlers.
func (a *App) drawJudgeRow(x, y int32) int32 {
	c := a.ctx
	put := func(label string) bool {
		w := c.TextWidth(label) + 16
		hit := c.Button(sdl.Rect{X: x, Y: y, W: w, H: btnH}, label)
		x += w + 6
		return hit
	}
	if put("Witness Testimony") {
		a.sess.SendWTCE("testimony1", 0)
	}
	if put("Cross Examination") {
		a.sess.SendWTCE("testimony2", 0)
	}
	if put("Not Guilty") {
		a.sess.SendWTCE("judgeruling", 0)
	}
	if put("Guilty") {
		a.sess.SendWTCE("judgeruling", 1)
	}
	if put("Def -") {
		a.sess.SendHP(1, a.sess.HPDef-1)
	}
	if put("Def +") {
		a.sess.SendHP(1, a.sess.HPDef+1)
	}
	if put("Pro -") {
		a.sess.SendHP(2, a.sess.HPPro-1)
	}
	if put("Pro +") {
		a.sess.SendHP(2, a.sess.HPPro+1)
	}
	return btnH + 4
}

// prewarmServer warms what this server needed last visit (favorite
// pre-warm): the last-used character's icon + core sprites and the
// last-seen background's standard position parts — all LOW priority
// through the normal pool, shed freely under real load.
func (a *App) prewarmServer() {
	info := a.d.Prefs.ServerWarmInfoFor(a.serverKey)
	if info.Char != "" {
		a.d.Manager.Prefetch(a.urls.CharIcon(info.Char), assets.AssetTypeCharIcon, network.PriorityLow) // AssetType: CharIcon (pre-warm)
		idle := a.urls.Emote(info.Char, "normal", courtroom.EmoteIdle)
		talk := a.urls.Emote(info.Char, "normal", courtroom.EmoteTalk)
		bare := a.urls.EmoteBare(info.Char, "normal")
		a.d.Manager.PrefetchWithFallback(idle, bare, assets.AssetTypeCharSprite, network.PriorityLow) // AssetType: CharSprite (pre-warm)
		a.d.Manager.PrefetchWithFallback(talk, bare, assets.AssetTypeCharSprite, network.PriorityLow) // AssetType: CharSprite (pre-warm)
	}
	if info.Background != "" {
		// wit/def/pro/jud cover the courtroom core (standardPositions
		// leads with exactly those four).
		for _, pos := range standardPositions[:4] {
			bgPart, deskPart := courtroom.PositionScene(pos)
			a.d.Manager.Prefetch(a.urls.Background(info.Background, bgPart), assets.AssetTypeBackground, network.PriorityLow)    // AssetType: Background (pre-warm)
			a.d.Manager.Prefetch(a.urls.Background(info.Background, deskPart), assets.AssetTypeDeskOverlay, network.PriorityLow) // AssetType: DeskOverlay (pre-warm)
		}
	}
}

// speculateEmote warms everything the picked emote will need the moment
// the user hits enter: idle/talk loops, the preanim, and its char.ini SFX.
// LOW priority — shed-able speculation, never a render-path cost (§10).
func (a *App) speculateEmote(me string, e *courtroom.Emote) {
	idle := a.urls.Emote(me, e.Anim, courtroom.EmoteIdle)
	talk := a.urls.Emote(me, e.Anim, courtroom.EmoteTalk)
	bare := a.urls.EmoteBare(me, e.Anim)
	a.d.Manager.PrefetchWithFallback(idle, bare, assets.AssetTypeCharSprite, network.PriorityLow) // AssetType: CharSprite (picked emote idle)
	a.d.Manager.PrefetchWithFallback(talk, bare, assets.AssetTypeCharSprite, network.PriorityLow) // AssetType: CharSprite (picked emote talk)
	if e.Preanim != "" && e.Preanim != "-" {
		a.d.Manager.Prefetch(a.urls.Emote(me, e.Preanim, courtroom.EmotePreanim), assets.AssetTypeCharSprite, network.PriorityLow) // AssetType: CharSprite (picked preanim)
	}
	if e.SFXName != "" && e.SFXName != "0" && e.SFXName != "1" {
		a.d.Manager.Prefetch(a.urls.SFX(e.SFXName), assets.AssetTypeSFX, network.PriorityLow) // AssetType: SFX (picked emote)
	}
}

// hasCustomShout reports whether the current character ships a custom
// interjection. AO2-Client shows ui_custom_objection only when the char
// folder holds custom art (courtroom.cpp set_widgets file_exists checks);
// a streaming client can't stat the folder, so the discoverable evidence
// is char.ini: a custom_name rename or named [Shouts] entries. Without
// either, the button hides (playtest: it highlighted on hover for chars
// with no custom shout at all).
func (a *App) hasCustomShout() bool {
	return len(a.customShouts) > 0 || a.customName != ""
}

// customShoutLabel is the active custom-shout button text: a named
// [Shouts] pick, the char.ini custom_name rename, or plain "Custom!".
func (a *App) customShoutLabel() string {
	if a.customIdx >= 0 && a.customIdx < len(a.customShouts) {
		return a.customShouts[a.customIdx].Name + "!"
	}
	if a.customName != "" {
		return a.customName + "!"
	}
	return "Custom!"
}

// --- position cycler -------------------------------------------------------------

// standardPositions is the classic AO side set used until an SD packet
// supplies the server's own list.
var standardPositions = []string{"wit", "def", "pro", "jud", "hld", "hlp", "jur", "sea"}

func (a *App) posChoices() []string {
	if a.sess != nil && len(a.sess.PosList) > 0 {
		return a.sess.PosList
	}
	return standardPositions
}

// posSelectW fits the widest standard side ("Pos" label drawn separately).
const posSelectW = 64

// colorSelectW fits the widest color name ("Orange") in the IC selector.
const colorSelectW = 86

// drawPosSelect renders the side selector as a real dropdown (AO2's
// ui_pos_dropdown; playtest asked for dropdowns over cyclers) at the given
// height. Returns the next x. The Ctrl+P hotkey still cycles (cyclePos).
func (a *App) drawPosSelect(x, y, h int32) int32 {
	c := a.ctx
	c.Label(x, y+(h-int32(c.font.Height()))/2, "Pos", ColTextDim)
	x += c.TextWidth("Pos") + 6
	choices := a.posChoices()
	cur := 0
	for i, p := range choices {
		if p == a.mySide() {
			cur = i
			break
		}
	}
	if next, changed := c.Dropdown("posdd", sdl.Rect{X: x, Y: y, W: posSelectW, H: h}, choices, cur); changed {
		a.sidePref = choices[next]
	}
	return x + posSelectW + 6
}

// --- casing ----------------------------------------------------------------------

// sendCasingPrefs pushes the persisted SETCASE subscription on join (the
// tsuserver family keeps it per connection).
func (a *App) sendCasingPrefs() {
	if a.sess == nil {
		return
	}
	enabled, roles := a.d.Prefs.Casing()
	if !enabled || !a.sess.Features.Has(protocol.FeatureCasingAlerts) {
		return
	}
	a.sess.SetCasingPrefs(
		roles&courtroom.CaseRoleDef != 0,
		roles&courtroom.CaseRolePro != 0,
		roles&courtroom.CaseRoleJudge != 0,
		roles&courtroom.CaseRoleJury != 0,
		roles&courtroom.CaseRoleSteno != 0,
	)
}

// --- evidence ---------------------------------------------------------------------

// noteEvidencePresented mirrors display_evidence_image + the "has presented
// evidence" IC log line for incoming messages carrying an evidence id
// (wire ids are shifted by 1; 0 = none per legacy standards).
func (a *App) noteEvidencePresented(msg *protocol.ChatMessage) {
	id := msg.EvidenceID
	if a.sess == nil || id <= 0 || id > len(a.sess.Evidence) {
		return
	}
	item := a.sess.Evidence[id-1]
	name := msg.Showname
	if name == "" {
		name = msg.CharName
	}
	a.pushIC(name+" presented evidence: "+item.Name, 0, a.friendMessage(a.serverKey, msg))
	a.evShowImg = item.Image
	a.evShowAt = time.Now()
	a.d.Manager.PrefetchExact(a.urls.Evidence(item.Image), assets.AssetTypeMisc, network.PriorityHigh) // AssetType: Misc (evidence image, exact URL)
}

// demandEvidence paces thumbnail fetches for the evidence grid: one exact
// fetch per item per retry interval while the panel is open.
func (a *App) demandEvidence(idx int, url string) {
	if len(a.evidAsk) != len(a.sess.Evidence) {
		a.evidAsk = make([]time.Time, len(a.sess.Evidence))
	}
	if idx < 0 || idx >= len(a.evidAsk) || time.Since(a.evidAsk[idx]) < charIconRetryInterval {
		return
	}
	a.evidAsk[idx] = time.Now()
	a.d.Manager.PrefetchExact(url, assets.AssetTypeMisc, network.PriorityLow) // AssetType: Misc (evidence thumbnail, exact URL)
}

// drawEvidencePanel is the modal evidence browser/editor: a thumbnail grid
// of the server's LE list, an inspector for the selection, present-arming,
// and the PE/DE/EE editor ops.
func (a *App) drawEvidencePanel(w, h int32) {
	c := a.ctx
	panel := sdl.Rect{X: w / 8, Y: h / 8, W: w * 3 / 4, H: h * 3 / 4}
	c.Fill(panel, ColPanel)
	c.Border(panel, ColAccent)
	c.Heading(panel.X+pad, panel.Y+pad, fmt.Sprintf("Evidence (%d)", len(a.sess.Evidence)), ColText)
	if c.Button(sdl.Rect{X: panel.X + panel.W - 80 - pad, Y: panel.Y + pad, W: 80, H: btnH}, "Close") {
		a.showEvid = false
		return
	}
	if c.Button(sdl.Rect{X: panel.X + panel.W - 170 - pad, Y: panel.Y + pad, W: 84, H: btnH}, "Add new") {
		a.evidEditing, a.evidIdx = true, -1
		a.evidName, a.evidDesc, a.evidImage = "", "", "empty.png"
	}

	// Editor mode replaces the grid: 3 fields + save/cancel.
	if a.evidEditing {
		fy := panel.Y + pad + 40
		c.Label(panel.X+pad, fy+4, "Name:", ColText)
		a.evidName, _ = c.TextField("evname", sdl.Rect{X: panel.X + pad + 90, Y: fy, W: panel.W - 2*pad - 90, H: fieldH}, a.evidName, "Evidence name")
		fy += 32
		c.Label(panel.X+pad, fy+4, "Description:", ColText)
		a.evidDesc, _ = c.TextField("evdesc", sdl.Rect{X: panel.X + pad + 90, Y: fy, W: panel.W - 2*pad - 90, H: fieldH}, a.evidDesc, "What it proves")
		fy += 32
		c.Label(panel.X+pad, fy+4, "Image file:", ColText)
		a.evidImage, _ = c.TextField("evimg", sdl.Rect{X: panel.X + pad + 90, Y: fy, W: panel.W - 2*pad - 90, H: fieldH}, a.evidImage, "knife.png (base/evidence/)")
		fy += 40
		if c.Button(sdl.Rect{X: panel.X + pad, Y: fy, W: 90, H: btnH}, "Save") {
			name, desc, img := strings.TrimSpace(a.evidName), strings.TrimSpace(a.evidDesc), strings.TrimSpace(a.evidImage)
			if name != "" {
				if a.evidIdx < 0 {
					a.sess.AddEvidence(name, desc, img)
				} else {
					a.sess.EditEvidence(a.evidIdx, name, desc, img)
				}
				a.evidEditing = false
			}
		}
		if c.Button(sdl.Rect{X: panel.X + pad + 100, Y: fy, W: 90, H: btnH}, "Cancel") {
			a.evidEditing = false
		}
		return
	}

	// Thumbnail grid (left 60%) + inspector (right 40%).
	const cell, cellGap = int32(72), int32(8)
	gridW := panel.W * 6 / 10
	grid := sdl.Rect{X: panel.X + pad, Y: panel.Y + pad + 40, W: gridW - 2*pad, H: panel.H - 2*pad - 40}
	cols := grid.W / (cell + cellGap)
	if cols < 1 {
		cols = 1
	}
	rows := (int32(len(a.sess.Evidence)) + cols - 1) / cols
	contentH := rows * (cell + cellGap + 14)
	track := sdl.Rect{X: grid.X + grid.W - scrollBarW, Y: grid.Y, W: scrollBarW, H: grid.H}
	a.evidScroll = c.VScrollbar("evidscroll", track, a.evidScroll, contentH, grid.H)
	for i := range a.sess.Evidence {
		item := &a.sess.Evidence[i]
		col, row := int32(i)%cols, int32(i)/cols
		cx := grid.X + col*(cell+cellGap)
		cy := grid.Y + row*(cell+cellGap+14) - a.evidScroll
		if cy+cell < grid.Y || cy > grid.Y+grid.H {
			continue
		}
		r := sdl.Rect{X: cx, Y: cy, W: cell, H: cell}
		if i == a.evidIdx {
			c.Fill(sdl.Rect{X: r.X - 2, Y: r.Y - 2, W: r.W + 4, H: r.H + 4}, ColAccent)
		}
		url := a.urls.Evidence(item.Image)
		if page, ok := a.d.Store.Get(url); ok && len(page.Frames) > 0 {
			_ = c.Ren.Copy(page.Frames[0], nil, &r)
		} else {
			c.Fill(r, ColPanelHi)
			a.demandEvidence(i, url)
		}
		c.Border(r, ColPanelHi)
		c.LabelClipped(cx, cy+cell+2, cell, item.Name, ColTextDim)
		if c.hovering(r) && c.clicked {
			a.evidIdx = i
		}
	}

	// Inspector.
	ix := panel.X + gridW + pad
	iw := panel.W - gridW - 2*pad
	iy := panel.Y + pad + 40
	if a.evidIdx < 0 || a.evidIdx >= len(a.sess.Evidence) {
		c.Label(ix, iy, "Select an item.", ColTextDim)
		return
	}
	sel := &a.sess.Evidence[a.evidIdx]
	c.Label(ix, iy, sel.Name, ColAccent)
	iy += 22
	for _, line := range c.WrapText(sel.Description, iw, maxDescLines) {
		c.LabelClipped(ix, iy, iw, line, ColText)
		iy += 18
	}
	c.LabelClipped(ix, iy, iw, "image: "+sel.Image, ColTextDim)
	iy += 28
	presentLabel := "Present with next message"
	if a.evidPresent {
		presentLabel = "Presenting — click to cancel"
	}
	if c.Button(sdl.Rect{X: ix, Y: iy, W: iw, H: btnH}, presentLabel) {
		a.evidPresent = !a.evidPresent
	}
	iy += btnH + 6
	if c.Button(sdl.Rect{X: ix, Y: iy, W: (iw - 6) / 2, H: btnH}, "Edit") {
		a.evidEditing = true
		a.evidName, a.evidDesc, a.evidImage = sel.Name, sel.Description, sel.Image
	}
	if c.Button(sdl.Rect{X: ix + (iw-6)/2 + 6, Y: iy, W: (iw - 6) / 2, H: btnH}, "Delete") {
		a.sess.DeleteEvidence(a.evidIdx)
		a.evidPresent = false
	}
	iy += btnH + 6
	// Pin to the case notebook: name + description in one quoted line.
	if c.Button(sdl.Rect{X: ix, Y: iy, W: iw, H: btnH}, "Pin to notebook") {
		a.pinNote("[evidence] " + sel.Name + ": " + sel.Description)
	}
}

// --- modcall dialog ---------------------------------------------------------------

// drawModcallDialog collects the reason and fires ZZ (servers without
// modcall_reason get the bare packet — Session.CallMod handles both).
func (a *App) drawModcallDialog(w, h int32) {
	c := a.ctx
	panel := sdl.Rect{X: w/2 - 230, Y: h/2 - 70, W: 460, H: 140}
	c.Fill(panel, ColPanel)
	c.Border(panel, ColDanger)
	c.Label(panel.X+pad, panel.Y+10, "Call a moderator — reason:", ColText)
	a.modReason, _ = c.TextField("modreason", sdl.Rect{X: panel.X + pad, Y: panel.Y + 36, W: panel.W - 2*pad, H: fieldH}, a.modReason, "What needs attention?")
	by := panel.Y + panel.H - btnH - 12
	if c.Button(sdl.Rect{X: panel.X + pad, Y: by, W: 110, H: btnH}, "Call mod") {
		a.sess.CallMod(strings.TrimSpace(a.modReason))
		a.pushOOC("CLIENT: Moderator called.")
		a.showModcall, a.modReason = false, ""
	}
	if c.Button(sdl.Rect{X: panel.X + pad + 120, Y: by, W: 90, H: btnH}, "Cancel") {
		a.showModcall, a.modReason = false, ""
	}
}

// --- hide-chrome popup --------------------------------------------------------------

// drawUICfgPanel toggles visibility of courtroom chrome regions; the set
// persists across sessions (Prefs.HiddenPanels).
func (a *App) drawUICfgPanel(w, h int32) {
	c := a.ctx
	panelH := int32(len(hideablePanels))*26 + 70
	panel := sdl.Rect{X: w/2 - 280, Y: h/2 - panelH/2, W: 560, H: panelH}
	c.Fill(panel, ColPanel)
	c.Border(panel, ColAccent)
	c.Heading(panel.X+pad, panel.Y+pad, "Hide UI pieces", ColText)
	y := panel.Y + pad + 34
	for _, p := range hideablePanels {
		hidden := a.panelHidden(p.id)
		if next := c.Checkbox(panel.X+pad, y, "Hide "+p.label, hidden); next != hidden {
			a.setPanelHidden(p.id, next)
		}
		y += 26
	}
	// Theater: the logical extreme of hiding chrome — stage only,
	// borderless, Esc (or the hotkey) exits.
	if c.Button(sdl.Rect{X: panel.X + pad, Y: panel.Y + panel.H - btnH - 10, W: 210, H: btnH}, "Theater mode (Esc exits)") {
		a.showUICfg = false
		a.setTheater(true)
	}
	// Live layout editor (themed layouts only — classic uses the knobs).
	if a.themeLay.valid && a.d.Prefs.ThemeLayoutEnabled() {
		if c.Button(sdl.Rect{X: panel.X + pad + 220, Y: panel.Y + panel.H - btnH - 10, W: 170, H: btnH}, "Edit layout (drag)") {
			a.startLayoutEdit()
		}
	}
	if c.Button(sdl.Rect{X: panel.X + panel.W - 90 - pad, Y: panel.Y + panel.H - btnH - 10, W: 90, H: btnH}, "Done") {
		a.showUICfg = false
	}
}
