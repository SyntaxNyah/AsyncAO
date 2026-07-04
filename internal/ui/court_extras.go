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
	panelExtras    = "extras"
	// Individual right-column tabs — hide ones you never use (e.g. Friends/Notes). A
	// hidden tab leaves the docked strip AND isn't drawn even if it was torn out.
	panelTabMusic   = "tab.music"
	panelTabAreas   = "tab.areas"
	panelTabPlayers = "tab.players"
	panelTabNotes   = "tab.notes"
	panelTabFriends = "tab.friends"
	// Players-list per-row actions. These once gated inline row BUTTONS; the
	// actions now live in the per-row "…" / right-click menu (rostermenu.go)
	// and each id hides its menu ENTRY instead — same prefs, same popup grid.
	// The Friend action keeps its own Settings toggle (FriendButtonShown) —
	// no duplicate id here.
	rosterBtnPair    = "roster.pair"
	rosterBtnUID     = "roster.uid"
	rosterBtnIPID    = "roster.ipid"
	rosterBtnIgnore  = "roster.ignore"
	rosterBtnProfile = "roster.profile"
)

// hideableRosterButtons drives the "Players-list row buttons" grid in the UI
// popup (tick to hide — same semantics as the control-button grid).
var hideableRosterButtons = []struct{ id, label string }{
	{rosterBtnPair, "Pair"},
	{rosterBtnUID, "UID copy"},
	{rosterBtnIPID, "IPID copy (mods)"},
	{rosterBtnIgnore, "Ignore"},
	{rosterBtnProfile, "Profile"},
}

// hideablePanels drives the UI popup AND the editor toolbox (#27): id + human label
// (the dialog's checkbox text) + a SHORT chip label (the toolbox), draw order.
var hideablePanels = []struct{ id, label, short string }{
	{panelShouts, "Shout buttons (Hold It / Objection / Take That)", "Shouts"},
	{panelKnobs, "Layout knobs (View/Text/MsgBox/Log/Input)", "Knobs"},
	{panelEmotes, "Emote buttons", "Emotes"},
	{panelLog, "Right column (log/music/areas/OOC tabs)", "Right column"},
	{panelTabMusic, "Music tab", "Music tab"},
	{panelTabAreas, "Areas tab", "Areas tab"},
	{panelTabPlayers, "Players tab", "Players tab"},
	{panelTabNotes, "Notes tab", "Notes tab"},
	{panelTabFriends, "Friends tab", "Friends tab"},
	{panelOOC, "Bottom OOC row", "OOC bar"},
	{panelHP, "Penalty bars", "Penalty bars"},
	{panelTimers, "Server timers", "Timers"},
	{panelTestimony, "Testimony recording badge", "Testimony"},
	{panelJudge, "Judge controls (even when granted)", "Judge"},
	{panelExtras, "Extras button (AsyncAO features menu — themed mode; the 'x' hotkey still opens it)", "Extras btn"},
}

// hideableButtons drives the "Control buttons" grid in the UI popup — the
// customizable courtroom toolbar (screens.go ctrlSlot). Each id is a ctrl.*
// slot key; ticking one hides that button and the row compacts with no gap.
// "UI…" itself is deliberately absent (it's the way back to this popup) and so
// are the contextual buttons (Mod/CM/Pos/Group/Voice/Disconnect) that already
// appear and disappear on their own.
var hideableButtons = []struct{ id, label string }{
	{"ctrl.character", "Character"},
	{"ctrl.wardrobe", "Wardrobe"},
	{"ctrl.restyle", "Restyle"},
	{"ctrl.background", "Background"},
	{"ctrl.evidence", "Evidence"},
	{"ctrl.mods", "Mods"},
	{"ctrl.settings", "Settings"},
	{"ctrl.editlayout", "Edit Layout"},
	{"ctrl.hotkeys", "Hotkeys"},
	{"ctrl.about", "About"},
	{"ctrl.login", "Login"},
	// v1.50.5 (Nightingale: "your hide menus are missing quite a few — I can't
	// hide the voice chat or gc"): the once-"contextual" buttons are hideable
	// too. Each still self-gates (Voice only in VC areas, GC behind its pref);
	// hiding wins over both. Mod/CM stay auto-only (moderation affordances).
	{"ctrl.pair", "Pair"},
	{"ctrl.pos", "Pos selector"},
	{"ctrl.groupchat", "Group Chat"},
	{"ctrl.voice", "Voice chat"},
	{"ctrl.disconnect", "Disconnect (Esc still leaves)"},
	{"ctrl.randchar", "Rand char (emote grid)"},
	{"ctrl.favsfilter", "★ Favs filter (emote grid)"},
	// Individually-hideable shouts (the "Shout buttons" panel toggle above
	// still hides the whole row) and the IC bar's emoji button.
	{"ctrl.holdit", "Hold It! shout"},
	{"ctrl.objection", "Objection! shout"},
	{"ctrl.takethat", "Take That! shout"},
	{slotICEmoji, "Emoji button (IC bar)"},
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
// fallbacks). Missing keys stay silent (AO2 with a sparse INI does too).
// Callword/friend alerts deliberately don't go through here — they use the
// built-in ping so a sparse theme can never silence them.
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

// timerChipLabels returns the visible server clocks' "Tn mm:ss" labels for the
// overlay, into a REUSED backing slice and memoized per displayed second — so a
// stable (e.g. paused) clock allocates nothing on the always-on courtroom draw,
// and a running one rebuilds at most once per second instead of every frame.
func (a *App) timerChipLabels(now time.Time) []string {
	chips := a.timerChips[:0]
	for id := 0; id < courtroom.TimerCount; id++ {
		t := &a.sess.Timers[id]
		if !t.Visible {
			continue
		}
		secs := int(t.Remaining(now).Seconds())
		if secs < 0 {
			secs = 0
		}
		// A built label is never "" → that doubles as the "not yet built" check.
		if a.timerLabels[id] == "" || a.timerLabelSecs[id] != secs {
			a.timerLabels[id] = fmt.Sprintf("T%d %02d:%02d", id+1, secs/60, secs%60)
			a.timerLabelSecs[id] = secs
		}
		chips = append(chips, a.timerLabels[id])
	}
	a.timerChips = chips // keep the grown backing for next frame's reuse
	return chips
}

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
		chips := a.timerChipLabels(now)
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
		a.drawTimerChip(vp) // #97 local alarm countdown — same visibility group as the server clocks
	}

	// Testimony recording badge under the defense bar (loops while the
	// server is recording — RT testimony1 starts it, testimony1#1 ends).
	if a.testimonyOn && !a.panelHidden(panelTestimony) {
		by := vp.Y + 6 + barH + 4
		if page, ok := a.themePage("testimony"); ok {
			if len(page.Frames) > 1 {
				a.frameAnimChrome = true // the badge loops: keep frames coming through the static skip
			}
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
		// Splash in flight (animated page OR the timed fallback banner): both
		// step/expire on the draw clock, so the static skip must stand down.
		a.frameAnimChrome = true
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

// hpBarStems holds the precomputed theme-art key for every penalty value
// ([0] = defense, [1] = prosecution), so the per-frame HP draw looks a key up
// instead of allocating one by string concatenation. val is 0..HPBarMax (the
// session clamps it there). Built once at init; the always-on courtroom draw
// calls hpBarStem up to 4×/frame, so this keeps that path off the heap.
var hpBarStems = func() [2][hpBarSegments + 1]string {
	var t [2][hpBarSegments + 1]string
	for v := 0; v <= hpBarSegments; v++ {
		t[0][v] = "defensebar" + strconv.Itoa(v)
		t[1][v] = "prosecutionbar" + strconv.Itoa(v)
	}
	return t
}()

func (a *App) hpBarStem(def bool, val int) string {
	// Out of the precomputed range (a server sent an odd value): fall back to
	// the concat — rare/never given the session clamp, and it keeps the table
	// bounded.
	if val < 0 || val > hpBarSegments {
		if def {
			return "defensebar" + strconv.Itoa(val)
		}
		return "prosecutionbar" + strconv.Itoa(val)
	}
	if def {
		return hpBarStems[0][val]
	}
	return hpBarStems[1][val]
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
		a.d.Manager.PrefetchChain(idle, a.urls.EmoteAlts(info.Char, "normal", courtroom.EmoteIdle), assets.AssetTypeCharSprite, network.PriorityLow) // AssetType: CharSprite (pre-warm)
		a.d.Manager.PrefetchChain(talk, a.urls.EmoteAlts(info.Char, "normal", courtroom.EmoteTalk), assets.AssetTypeCharSprite, network.PriorityLow) // AssetType: CharSprite (pre-warm)
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
	a.d.Manager.PrefetchChain(idle, a.urls.EmoteAlts(me, e.Anim, courtroom.EmoteIdle), assets.AssetTypeCharSprite, network.PriorityLow) // AssetType: CharSprite (picked emote idle)
	a.d.Manager.PrefetchChain(talk, a.urls.EmoteAlts(me, e.Anim, courtroom.EmoteTalk), assets.AssetTypeCharSprite, network.PriorityLow) // AssetType: CharSprite (picked emote talk)
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

// applySide sets OUR side and, when we're connected, immediately tells the server
// with the /pos OOC command — the exact path a playtester confirmed moves them
// instantly. AO2-Client's pos dropdown is local-only: the chosen side merely rides
// the NEXT IC message (on_pos_dropdown_changed sends nothing to the server), so on a
// tsuserver-family server picking a position felt inert — nothing visibly happened
// until you spoke. Firing /pos here mirrors typing it in the OOC box, so the move
// (and the server's SP/BG echo that repaints the scene) lands the moment you pick;
// a.sidePref still rides later IC messages exactly as before. A deliberate, minimal
// deviation from AO2-Client. User-initiated only (dropdown / Ctrl+P cycle / IC-box
// "/pos") — never per frame, so the render loop and its alloc gate are untouched —
// and the same-side guard makes a re-pick of the current side a no-op (no resend).
func (a *App) applySide(side string) {
	side = strings.ToLower(strings.TrimSpace(side))
	if side == "" || side == a.sidePref {
		return
	}
	a.sidePref = side
	if a.sess != nil {
		a.sess.SendOOC(a.oocNameOrDefault(), "/pos "+side)
	}
}

// posSelectW fits the widest standard side ("Pos" label drawn separately).
const posSelectW = 64

// colorSelectW fits the widest color name ("Orange") in the IC selector.
const colorSelectW = 86

// Pos-dropdown thumbnail geometry: open rows show a 4:3 stage thumbnail of each
// position's background down the left, so you can SEE the placement (and which
// positions this background actually has art for) instead of guessing from "wit
// / def / pro …" codes. 48×36 = 4:3.
const (
	posThumbW       = 48
	posThumbRowH    = 40
	posSelectThumbW = 96 // closed Pos control width when it carries a bg thumbnail (thumb + side code + chevron)
)

// drawPosSelect renders the side selector as a real dropdown (AO2's
// ui_pos_dropdown; playtest asked for dropdowns over cyclers) at the given
// height. Returns the next x. The Ctrl+P hotkey still cycles (cyclePos). When a
// background is loaded, each open row carries a bg thumbnail (fetched on demand
// only while the list is open — one probe per position, within the streaming
// design); without one it falls back to the plain text dropdown.
func (a *App) drawPosSelect(x, y, h int32) int32 {
	c := a.ctx
	labelW := c.TextWidth("Pos") + 6
	// v1.50.5 (Nightingale: "everything is movable EXCEPT the /pos thing"): the
	// whole Pos control (label + dropdown) is a movable+resizable "ctrl.pos"
	// slot — and hideable like any control button. The row cursor advances by
	// the DEFAULT width either way (wrap-not-extract), so moving or hiding it
	// never cascades the rest of the row.
	ddDefW := int32(posSelectW)
	if a.sess != nil && a.sess.Background != "" {
		// Wider control so the CLOSED box fits the current position's bg thumbnail
		// + the side code + the chevron ("show the thing that's already selected").
		ddDefW = int32(posSelectThumbW)
	}
	adv := labelW + ddDefW + 6
	if a.panelHidden("ctrl.pos") {
		return x // hidden: the row compacts, exactly like ctrlSlot
	}
	r := a.slotRect("ctrl.pos", sdl.Rect{X: x, Y: y, W: labelW + ddDefW, H: h}, a.winW, a.winH)
	c.Label(r.X, r.Y+(r.H-int32(c.font.Height()))/2, "Pos", ColTextDim)
	ddW := r.W - labelW
	if ddW < 60 {
		ddW = 60 // floor: the dropdown stays clickable however small the slot is dragged
	}
	ctrl := sdl.Rect{X: r.X + labelW, Y: r.Y, W: ddW, H: r.H}
	choices := a.posChoices()
	cur := 0
	for i, p := range choices {
		if p == a.mySide() {
			cur = i
			break
		}
	}
	bg := ""
	if a.sess != nil {
		bg = a.sess.Background
	}
	if bg == "" { // no background yet → nothing to thumbnail; plain text dropdown
		if next, changed := c.Dropdown("posdd", ctrl, choices, cur); changed {
			a.applySide(choices[next]) // also /pos the server so the move is instant, not next-message
		}
		return x + adv
	}
	if a.posBgKey != bg { // bg changed: drop the now-wrong thumbnails (cachedPage keys by index, not URL)
		a.posBgPages, a.posBgKey = nil, bg
	}
	if a.posThumbFn == nil { // bind once: a stable fn (reads the live bg/choices), so the per-frame Pos selector never allocates a closure
		a.posThumbFn = a.posThumbDraw
	}
	if next, changed := c.DropdownThumbs("posdd", ctrl, choices, cur, posThumbRowH, posThumbW, a.posThumbFn); changed {
		a.applySide(choices[next]) // also /pos the server so the move is instant, not next-message
	}
	return x + adv
}

// posThumbDraw paints the i-th position's background thumbnail into tr — used by
// both the open dropdown rows and the closed control's "currently selected" chip.
// Stored once as a.posThumbFn (not a per-frame closure), and reads the live
// bg/choices so it's always current. On the render thread (FinishFrame or the
// closed-control draw), so the on-demand cachedPage / paced fetch is safe.
func (a *App) posThumbDraw(i int, tr sdl.Rect) {
	c := a.ctx
	choices := a.posChoices()
	bg := ""
	if a.sess != nil {
		bg = a.sess.Background
	}
	if i < 0 || i >= len(choices) || bg == "" {
		c.Fill(tr, sdl.Color{R: 10, G: 10, B: 14, A: 255})
		c.Border(tr, ColPanelHi)
		return
	}
	bgPart, _ := courtroom.PositionScene(choices[i])
	base := a.urls.Background(bg, bgPart)
	if page, ok := a.cachedPage(&a.posBgPages, &a.posBgGen, len(choices), i, base); ok && len(page.Frames) > 0 {
		_ = c.Ren.Copy(page.Frames[0], nil, &tr)
	} else {
		c.Fill(tr, sdl.Color{R: 10, G: 10, B: 14, A: 255})
		a.demandAsset(&a.posBgAsk, len(choices), i, base, assets.AssetTypeBackground) // AssetType: Background (pos thumb)
	}
	c.Border(tr, ColPanelHi)
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
	fr, fc := a.friendMessage(a.serverKey, msg)
	a.pushIC(name+" presented evidence: "+item.Name, 0, fr, fc, "") // system line — no speaker tint
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

// evidPanelDefW etc. size the floating Evidence window; the min keeps the grid +
// inspector usable.
const (
	evidPanelDefW = 560
	evidPanelDefH = 420
	evidPanelMinW = 360
	evidPanelMinH = 240
)

// evidPanelRect is the Evidence window's screen rect. Its FIRST-OPEN default tucks
// into the top-LEFT over the stage (not centred), so the IC input (bottom) and the log
// (right column) stay visible — you can keep talking and follow the conversation while
// you browse evidence (#5). Once dragged/resized (placed) the floatWin geometry wins.
func (a *App) evidPanelRect(w, h int32) sdl.Rect {
	if !a.evidWin.placed {
		dw := clampI32(evidPanelDefW, evidPanelMinW, w-2*floatWinMargin)
		dh := clampI32(evidPanelDefH, evidPanelMinH, h-2*floatWinMargin)
		return sdl.Rect{X: floatWinMargin, Y: floatTitleH, W: dw, H: dh}
	}
	return a.evidWin.rect(evidPanelDefW, evidPanelDefH, evidPanelMinW, evidPanelMinH, w, h)
}

// drawEvidencePanel is the evidence browser/editor — a NON-BLOCKING floating window
// (floatWin: drag the title bar, resize the bottom-right grip) so the courtroom stays
// live behind it: you can keep talking, follow the chat and pre-write a message while
// you browse or arm evidence (#5, Crystalwarrior). A thumbnail grid of the server's LE
// list, an inspector for the selection, present-arming, and the PE/DE/EE editor ops.
func (a *App) drawEvidencePanel(w, h int32, pressed *bool) {
	c := a.ctx
	r := a.evidPanelRect(w, h)
	c.Fill(r, ColPanel)
	c.Border(r, ColAccent)
	// Title bar / drag handle + close + "Add new" + a bottom-right resize grip.
	c.Fill(sdl.Rect{X: r.X, Y: r.Y, W: r.W, H: floatTitleH}, ColPanelHi)
	c.Heading(r.X+pad, r.Y+6, fmt.Sprintf("Evidence (%d)", len(a.sess.Evidence)), ColText)
	closeB := sdl.Rect{X: r.X + r.W - 80 - pad, Y: r.Y + 3, W: 80, H: btnH}
	if c.Button(closeB, "Close") {
		a.showEvid = false
		return
	}
	addB := sdl.Rect{X: closeB.X - 90, Y: r.Y + 3, W: 84, H: btnH}
	if c.Button(addB, "Add new") {
		a.evidEditing, a.evidIdx = true, -1
		a.evidName, a.evidDesc, a.evidImage = "", "", "empty.png"
	}
	a.floatWinDrag(&a.evidWin, sdl.Rect{X: r.X, Y: r.Y, W: addB.X - r.X - 4, H: floatTitleH}, pressed)
	grip := sdl.Rect{X: r.X + r.W - floatGripSz, Y: r.Y + r.H - floatGripSz, W: floatGripSz, H: floatGripSz}
	a.floatWinResize(&a.evidWin, grip, r, evidPanelMinW, evidPanelMinH, pressed)
	a.drawResizeGrip(grip)
	contentTop := r.Y + floatTitleH + 8

	// Editor mode replaces the grid: 3 fields + save/cancel.
	if a.evidEditing {
		fy := contentTop
		c.Label(r.X+pad, fy+4, "Name:", ColText)
		a.evidName, _ = c.TextField("evname", sdl.Rect{X: r.X + pad + 90, Y: fy, W: r.W - 2*pad - 90, H: fieldH}, a.evidName, "Evidence name")
		fy += 32
		c.Label(r.X+pad, fy+4, "Description:", ColText)
		a.evidDesc, _ = c.TextField("evdesc", sdl.Rect{X: r.X + pad + 90, Y: fy, W: r.W - 2*pad - 90, H: fieldH}, a.evidDesc, "What it proves")
		fy += 32
		c.Label(r.X+pad, fy+4, "Image file:", ColText)
		a.evidImage, _ = c.TextField("evimg", sdl.Rect{X: r.X + pad + 90, Y: fy, W: r.W - 2*pad - 90, H: fieldH}, a.evidImage, "knife.png (base/evidence/)")
		fy += 40
		if c.Button(sdl.Rect{X: r.X + pad, Y: fy, W: 90, H: btnH}, "Save") {
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
		if c.Button(sdl.Rect{X: r.X + pad + 100, Y: fy, W: 90, H: btnH}, "Cancel") {
			a.evidEditing = false
		}
		return
	}

	// Thumbnail grid (left 60%) + inspector (right 40%).
	const cell, cellGap = int32(72), int32(8)
	gridW := r.W * 6 / 10
	grid := sdl.Rect{X: r.X + pad, Y: contentTop, W: gridW - 2*pad, H: r.Y + r.H - contentTop - pad}
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
		rc := sdl.Rect{X: cx, Y: cy, W: cell, H: cell}
		if i == a.evidIdx {
			c.Fill(sdl.Rect{X: rc.X - 2, Y: rc.Y - 2, W: rc.W + 4, H: rc.H + 4}, ColAccent)
		}
		url := a.urls.Evidence(item.Image)
		if page, ok := a.d.Store.Get(url); ok && len(page.Frames) > 0 {
			_ = c.Ren.Copy(page.Frames[0], nil, &rc)
		} else {
			c.Fill(rc, ColPanelHi)
			a.demandEvidence(i, url)
		}
		c.Border(rc, ColPanelHi)
		c.LabelClipped(cx, cy+cell+2, cell, item.Name, ColTextDim)
		if c.hovering(rc) && c.clicked {
			a.evidIdx = i
		}
	}

	// Inspector.
	ix := r.X + gridW + pad
	iw := r.W - gridW - 2*pad
	iy := contentTop
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

// --- modcall panel ----------------------------------------------------------------

const (
	modcallPanelDefW = 460
	modcallPanelDefH = 150
	modcallPanelMinW = 300
	modcallPanelMinH = 120
)

// modcallPanelRect is the Call-Mod window's screen rect. First-open tucks top-left
// (like the evidence panel) so the IC input and log stay clear; once dragged or
// resized (placed) the floatWin geometry wins.
func (a *App) modcallPanelRect(w, h int32) sdl.Rect {
	if !a.modcallWin.placed {
		dw := clampI32(modcallPanelDefW, modcallPanelMinW, w-2*floatWinMargin)
		dh := clampI32(modcallPanelDefH, modcallPanelMinH, h-2*floatWinMargin)
		return sdl.Rect{X: floatWinMargin, Y: floatTitleH, W: dw, H: dh}
	}
	return a.modcallWin.rect(modcallPanelDefW, modcallPanelDefH, modcallPanelMinW, modcallPanelMinH, w, h)
}

// drawModcallPanel collects the reason and fires ZZ — a NON-BLOCKING floating
// window (floatWin: drag the title bar, resize the bottom-right grip) so the
// courtroom stays live behind it: you can keep talking and watching while the
// call is open. Servers without modcall_reason get the bare packet (Session.CallMod
// handles both).
func (a *App) drawModcallPanel(w, h int32, pressed *bool) {
	c := a.ctx
	r := a.modcallPanelRect(w, h)
	c.Fill(r, ColPanel)
	c.Border(r, ColDanger)
	c.Fill(sdl.Rect{X: r.X, Y: r.Y, W: r.W, H: floatTitleH}, ColPanelHi)
	c.Heading(r.X+pad, r.Y+6, "Call a moderator", ColText)
	closeB := sdl.Rect{X: r.X + r.W - 80 - pad, Y: r.Y + 3, W: 80, H: btnH}
	if c.Button(closeB, "Close") {
		a.showModcall, a.modReason = false, ""
		return
	}
	a.floatWinDrag(&a.modcallWin, sdl.Rect{X: r.X, Y: r.Y, W: closeB.X - r.X - 4, H: floatTitleH}, pressed)
	grip := sdl.Rect{X: r.X + r.W - floatGripSz, Y: r.Y + r.H - floatGripSz, W: floatGripSz, H: floatGripSz}
	a.floatWinResize(&a.modcallWin, grip, r, modcallPanelMinW, modcallPanelMinH, pressed)
	a.drawResizeGrip(grip)

	c.Label(r.X+pad, r.Y+floatTitleH+8, "Reason (some servers make it optional):", ColText)
	a.modReason, _ = c.TextField("modreason", sdl.Rect{X: r.X + pad, Y: r.Y + floatTitleH + 32, W: r.W - 2*pad, H: fieldH}, a.modReason, "What needs attention?")
	if c.Button(sdl.Rect{X: r.X + pad, Y: r.Y + r.H - btnH - 12, W: 110, H: btnH}, "Call mod") {
		a.sess.CallMod(strings.TrimSpace(a.modReason))
		a.pushOOC("CLIENT: Moderator called.", "")
		a.showModcall, a.modReason = false, ""
	}
}

// --- hide-chrome popup --------------------------------------------------------------

// drawUICfgPanel toggles visibility of courtroom chrome regions; the set
// persists across sessions (Prefs.HiddenPanels).
func (a *App) drawUICfgPanel(w, h int32) {
	c := a.ctx
	const cfgRow = int32(26) // checkbox row pitch (matches the chrome list)
	const btnCols = int32(3) // the control-button grid is 3 wide
	// The control-button grid only applies to the new-default toolbar: the
	// legacy/themed row (drawICControls' LegacyDevThemeOn branch) draws fixed
	// inline buttons that don't consult the hidden set, so showing the grid
	// there would be a dead toggle. It — and its height — appears only when the
	// new-default layout is active.
	showBtnGrid := !a.d.Prefs.LegacyDevThemeOn()
	gridBlock := int32(0)
	if showBtnGrid {
		btnRows := (int32(len(hideableButtons)) + btnCols - 1) / btnCols
		gridBlock = 30 + btnRows*cfgRow // 30 = sub-heading
	}
	rosterRows := (int32(len(hideableRosterButtons)) + btnCols - 1) / btnCols
	rosterBlock := 30 + rosterRows*cfgRow // Players-list row buttons (always shown)
	// pad+34 heading · chrome list · [grid block] · roster block · btnH+18 bottom row
	panelH := pad + 34 + int32(len(hideablePanels))*cfgRow + gridBlock + rosterBlock + btnH + 18
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
		y += cfgRow
	}
	// Control buttons — the customizable toolbar row. Tick to HIDE a button
	// (same checked = hidden semantics as the chrome list above); the row
	// compacts with no gap. "UI…" stays put so this popup is always reachable.
	if showBtnGrid {
		c.Label(panel.X+pad, y+4, "Control buttons (tick to hide from the toolbar row):", ColTextDim)
		y += 30
		colW := (panel.W - 2*pad) / btnCols
		for i, b := range hideableButtons {
			cx := panel.X + pad + int32(i)%btnCols*colW
			cy := y + int32(i)/btnCols*cfgRow
			hidden := a.panelHidden(b.id)
			if next := c.Checkbox(cx, cy, b.label, hidden); next != hidden {
				a.setPanelHidden(b.id, next)
			}
		}
		y += (int32(len(hideableButtons)) + btnCols - 1) / btnCols * cfgRow
	}
	// Players-list row actions — these now live in the per-row "…" /
	// right-click menu (rostermenu.go); ticking one removes that menu entry.
	c.Label(panel.X+pad, y+4, "Players-list row menu actions (tick to hide):", ColTextDim)
	y += 30
	rColW := (panel.W - 2*pad) / btnCols
	for i, b := range hideableRosterButtons {
		cx := panel.X + pad + int32(i)%btnCols*rColW
		cy := y + int32(i)/btnCols*cfgRow
		hidden := a.panelHidden(b.id)
		if next := c.Checkbox(cx, cy, b.label, hidden); next != hidden {
			a.setPanelHidden(b.id, next)
		}
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
