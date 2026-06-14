package ui

// Quality-of-life: configurable Ctrl-chord hotkeys, callword alerts, IC
// log search/copy/export, screenshots, music stop. Render thread except
// the explicitly off-thread file writes.

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/veandco/go-sdl2/sdl"
	"github.com/veandco/go-sdl2/ttf"

	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
)

// Hotkey actions (persisted overrides in Prefs.Hotkeys; all fire on
// Ctrl+<key>).
const (
	hotkeyHoldIt     = "holdit"
	hotkeyObjection  = "objection"
	hotkeyTakeThat   = "takethat"
	hotkeyCustom     = "custom"
	hotkeyPosCycle   = "pos_cycle"
	hotkeyMusicStop  = "music_stop"
	hotkeyLogJump    = "log_jump"
	hotkeyScreenshot = "screenshot"
	hotkeyTheater    = "theater"
	hotkeyLogin      = "login"
	hotkeyMuteSFX    = "mute_sfx"
	hotkeyQuickSwap  = "quick_swap"
	hotkeyEmoteCycle = "emote_cycle"
	hotkeyPinNote    = "pin_note"
	hotkeyFriendHi   = "friend_toggle"
)

// hotkeyDefs drives both dispatch and the Settings rows: id, label, and
// the default key (per the original request: shouts on Ctrl+1..4).
var hotkeyDefs = []struct {
	id, label, def string
}{
	{hotkeyHoldIt, "Hold It! shout", "1"},
	{hotkeyObjection, "Objection! shout", "2"},
	{hotkeyTakeThat, "Take That! shout", "3"},
	{hotkeyCustom, "Custom shout", "4"},
	{hotkeyPosCycle, "Cycle position", "p"},
	{hotkeyMusicStop, "Stop music", "m"},
	{hotkeyLogJump, "Jump logs to newest", "l"},
	{hotkeyScreenshot, "Screenshot", "s"},
	{hotkeyTheater, "Theater mode", "t"},
	{hotkeyLogin, "Server login (saved creds)", "g"},
	{hotkeyMuteSFX, "Mute sound effects", "k"},
	{hotkeyQuickSwap, "Quick-swap character (cycle wardrobe)", "j"},
	{hotkeyEmoteCycle, "Cycle emote (next)", "e"},
	{hotkeyPinNote, "Pin hovered log line to notes", "n"},
	{hotkeyFriendHi, "Toggle friend highlights", "u"},
}

// hotkeyFor resolves an action's key name (pref override or default).
func (a *App) hotkeyFor(action string) string {
	if key := a.d.Prefs.Hotkey(action); key != "" {
		return strings.ToLower(key)
	}
	for _, def := range hotkeyDefs {
		if def.id == action {
			return def.def
		}
	}
	return ""
}

// refreshCharKeys re-reads this server's character keybinds into the
// per-frame lookup caches (connect + bind edits only — never per frame).
func (a *App) refreshCharKeys() {
	a.charKeys = a.d.Prefs.CharKeyBinds(a.serverKey)
	if len(a.charKeys) == 0 {
		a.charKeysRev = nil
		return
	}
	a.charKeysRev = make(map[string]string, len(a.charKeys))
	for k, ch := range a.charKeys {
		a.charKeysRev[ch] = k
	}
}

// charKeyFor reports the key bound to a character on this server ("" =
// none) — the wardrobe badge lookup.
func (a *App) charKeyFor(char string) string { return a.charKeysRev[char] }

// pollCharBind completes an armed wardrobe key-capture: the next plain
// keypress binds key → character on this server; Esc cancels.
func (a *App) pollCharBind() {
	if a.bindingFor == "" {
		return
	}
	c := a.ctx
	if c.escPressed {
		a.bindingFor = ""
		return
	}
	if c.keyPressed == 0 {
		return
	}
	key := strings.ToLower(sdl.GetKeyName(c.keyPressed))
	a.d.Prefs.SetCharKeyBind(a.serverKey, key, a.bindingFor)
	a.pushDebug(fmt.Sprintf("key %q now wears %s (this server only)", key, a.bindingFor))
	a.bindingFor = ""
	a.refreshCharKeys()
}

// handleCharKeys wears a bound character on a bare keypress — only with
// no text field focused, no capture armed, and no Ctrl chord in flight,
// so typing can never trigger a swap.
func (a *App) handleCharKeys() {
	c := a.ctx
	if c.keyPressed == 0 || c.focusID != "" || a.bindingFor != "" || c.ctrlHeld {
		return
	}
	if name := a.charKeys[strings.ToLower(sdl.GetKeyName(c.keyPressed))]; name != "" {
		a.wearFromMenu(name)
	}
}

// handleEmoteKeys picks the emote in number-key position N on the CURRENT
// page (keys 1-9), but only with no text field focused (so typing a number
// never switches emotes), no Ctrl chord in flight, and only when the digit
// isn't a deliberate character keybind — the same fence as handleCharKeys.
// Picking focuses the IC input, matching a click.
func (a *App) handleEmoteKeys() {
	c := a.ctx
	if c.keyPressed < sdl.K_1 || c.keyPressed > sdl.K_9 || c.focusID != "" || c.ctrlHeld || a.emotePerPage <= 0 {
		return
	}
	if a.charKeys[strings.ToLower(sdl.GetKeyName(c.keyPressed))] != "" {
		return // a character keybind owns this digit
	}
	a.selectEmote(a.emotePage*a.emotePerPage + int(c.keyPressed-sdl.K_1))
}

// handleHotkeys consumes this frame's Ctrl chord on the courtroom screen
// (and dispatches macro keybinds, then character keybinds — macros win
// a key conflict since they were bound deliberately).
func (a *App) handleHotkeys() {
	if !a.handleMacroKeys() && !a.handleJukeboxKeys() {
		a.handleCharKeys()
		a.handleEmoteKeys()
	}
	key := a.ctx.hotkey
	if key == 0 || a.sess == nil {
		return
	}
	name := strings.ToLower(sdl.GetKeyName(key))
	switch name {
	case a.hotkeyFor(hotkeyHoldIt):
		a.sendIC(protocol.ShoutHoldIt)
	case a.hotkeyFor(hotkeyObjection):
		a.sendIC(protocol.ShoutObjection)
	case a.hotkeyFor(hotkeyTakeThat):
		a.sendIC(protocol.ShoutTakeThat)
	case a.hotkeyFor(hotkeyCustom):
		a.sendIC(protocol.ShoutCustom)
	case a.hotkeyFor(hotkeyPosCycle):
		a.cyclePos()
	case a.hotkeyFor(hotkeyMusicStop):
		a.stopMusic()
	case a.hotkeyFor(hotkeyLogJump):
		a.jumpLogs()
	case a.hotkeyFor(hotkeyScreenshot):
		a.captureScreenshot()
	case a.hotkeyFor(hotkeyTheater):
		a.setTheater(!a.theaterOn)
	case a.hotkeyFor(hotkeyLogin):
		a.loginNow()
	case a.hotkeyFor(hotkeyMuteSFX):
		a.toggleSFXMute()
	case a.hotkeyFor(hotkeyQuickSwap):
		a.quickSwapNext()
	case a.hotkeyFor(hotkeyEmoteCycle):
		a.cycleEmote(1)
	case a.hotkeyFor(hotkeyFriendHi):
		on := !a.d.Prefs.FriendHighlightOn()
		a.d.Prefs.SetFriendHighlight(on)
		a.warnLine = "Friend highlights off"
		if on {
			a.warnLine = "Friend highlights on"
		}
		a.warnAt = time.Now()
	}
}

// quickSwapNext wears the next character in this server's wardrobe ring (the
// starred set, in the same alphabetical order the wardrobe menu shows). It
// advances from whatever's worn now — so the cycle stays intuitive even after
// a manual pick — and wraps. Empty wardrobe = a hint, no swap. The wardrobe is
// per server, so the ring is too.
func (a *App) quickSwapNext() {
	ring, _, _ := mergeWardrobe(a.d.Prefs.WardrobeList(a.serverKey), nil)
	if len(ring) == 0 {
		a.warnLine = "Quick-swap: no wardrobe characters yet — star some with ★ in the Wardrobe"
		a.warnAt = time.Now()
		return
	}
	next := 0 // current char not in the ring (or nothing worn) → start at the first
	for i, n := range ring {
		if strings.EqualFold(n, a.iniChar) {
			next = (i + 1) % len(ring)
			break
		}
	}
	a.wearFromMenu(ring[next])
	a.warnLine = fmt.Sprintf("Quick-swap: %s (%d/%d)", ring[next], next+1, len(ring))
	a.warnAt = time.Now()
}

// toggleSFXMute flips a session-only SFX mute and re-applies volumes. The
// music/blip channels are untouched; the saved SFX volume is preserved (mute
// is not persisted — it's a quick "shush" for one session).
func (a *App) toggleSFXMute() {
	a.sfxMuted = !a.sfxMuted
	a.applyAudioVolumes()
	a.warnLine = "SFX unmuted"
	if a.sfxMuted {
		a.warnLine = "SFX muted (press the Mute SFX hotkey again to restore)"
	}
	a.warnAt = time.Now()
}

// duckMusicPercent is how loud music plays (as a % of its set volume) while a
// message is on stage, when music ducking is on.
const duckMusicPercent = 35

// applyAudioVolumes pushes the saved volumes to the audio system, applying the
// session SFX mute (SFX → 0) and music ducking (music → duckMusicPercent)
// when active. Call it wherever those states change so they're always honored.
func (a *App) applyAudioVolumes() {
	music, sfx, blip := a.d.Prefs.AudioVolumes()
	if a.sfxMuted {
		sfx = 0
	}
	if a.musicDucked {
		music = music * duckMusicPercent / 100
	}
	a.d.Audio.SetVolumes(music, sfx, blip)
}

// drawHotkeyCheatSheet overlays the current bindings (F1 toggles it on any
// screen). It lists the configurable Ctrl-chord actions (resolved to the
// user's keys) plus the fixed function keys. Only drawn while open, so it
// costs nothing closed — allocations here are fine (opt-in overlay, like the
// perf HUD).
func (a *App) drawHotkeyCheatSheet(w, h int32) {
	c := a.ctx
	const rowH = 22
	rows := int32(len(hotkeyDefs) + 4)
	pw, ph := int32(460), rows*rowH+44
	panel := sdl.Rect{X: (w - pw) / 2, Y: (h - ph) / 2, W: pw, H: ph}
	c.Fill(panel, sdl.Color{R: 12, G: 12, B: 18, A: 242})
	c.Border(panel, ColAccent)
	c.Heading(panel.X+pad, panel.Y+6, "Hotkeys", ColText)
	c.Label(panel.X+pw-120, panel.Y+10, "F1 to close", ColTextDim)
	y := panel.Y + 36
	for _, def := range hotkeyDefs {
		c.Label(panel.X+pad, y, "Ctrl+"+strings.ToUpper(a.hotkeyFor(def.id)), ColAccent)
		c.Label(panel.X+pad+96, y, def.label, ColText)
		y += rowH
	}
	for _, fx := range [...]struct{ key, label string }{
		{"F1", "show this hotkey list"},
		{"F3", "performance HUD"},
		{"Esc", "close menus / exit theater"},
		{"Ctrl+wheel", "resize text · zoom the stage"},
	} {
		c.Label(panel.X+pad, y, fx.key, ColAccent)
		c.Label(panel.X+pad+96, y, fx.label, ColText)
		y += rowH
	}
}

// theaterHint is the only chrome theater mode draws.
const theaterHint = "Esc exits theater"

// drawTheater is the borderless viewport-only courtroom: the stage
// letterboxed to AO's 4:3 in a black surround, the chat overlay (it IS
// the show), hotkeys, and nothing else. Esc or the theater hotkey exits.
func (a *App) drawTheater(w, h int32) {
	c := a.ctx
	c.Fill(sdl.Rect{X: 0, Y: 0, W: w, H: h}, sdl.Color{A: 255})
	vpW, vpH := w, w*3/4
	if vpH > h {
		vpH = h
		vpW = vpH * 4 / 3
	}
	vp := sdl.Rect{X: (w - vpW) / 2, Y: (h - vpH) / 2, W: vpW, H: vpH}
	a.renderViewportZoomed(vp)
	chatBandH := vp.H / 4 * int32(a.boxPct) / DefaultScalePct
	a.handleViewportZoom(vp, c.mouseY >= vp.Y+vp.H-chatBandH)
	if a.vpZoom <= 1 {
		a.handleSpriteDrag(vp)
	}
	a.handleHotkeys()
	a.drawChatOverlay(vp)
	a.drawCourtOverlays(vp, nil) // splashes/HP still play — part of the show
	c.Label(w-c.TextWidth(theaterHint)-8, 6, theaterHint, ColTextDim)
	if c.escPressed {
		a.setTheater(false)
	}
}

// cyclePos advances our side through posChoices (the SD list when sent).
func (a *App) cyclePos() {
	choices := a.posChoices()
	cur := a.mySide()
	next := choices[0]
	for i, p := range choices {
		if p == cur {
			next = choices[(i+1)%len(choices)]
			break
		}
	}
	a.sidePref = next
}

// stopMusic asks the server to stop the area music — AO has no stop
// packet, so AO2-Client requests a fake track: "~stop.mp3", or the first
// extension-less list entry (a category header the server recognizes) on
// servers without it (courtroom.cpp music_stop).
func (a *App) stopMusic() {
	if a.sess == nil {
		return
	}
	const fakeStopSong = "~stop.mp3"
	track := fakeStopSong
	found := false
	for _, song := range a.sess.Music {
		if song == fakeStopSong {
			found = true
			break
		}
	}
	if !found {
		for _, song := range a.sess.Music {
			if !strings.Contains(song, ".") {
				track = song
				break
			}
		}
	}
	a.sess.RequestMusic(track)
}

// jumpLogs snaps every scrollback to its newest line.
func (a *App) jumpLogs() {
	const snapToTail = 1 << 30 // clamped by each scrollbar
	a.icScroll = snapToTail
	a.oocScroll = snapToTail
	a.icStick, a.oocStick = true, true // jumping to newest re-arms follow
}

// --- callwords ----------------------------------------------------------------------

// checkCallwords flashes + pings when a configured highlight word appears
// in IC/OOC traffic (AO2-Client callwords: get_court_sfx("word_call")).
// Streamer mode suppresses both — an on-stream name ping is exactly the
// leak the toggle exists to prevent.
func (a *App) checkCallwords(text string) {
	if a.d.Prefs.StreamerMode() {
		return
	}
	words := a.d.Prefs.CallWords()
	if len(words) == 0 || text == "" {
		return
	}
	lower := strings.ToLower(text)
	for _, w := range words {
		if strings.Contains(lower, w) {
			if f := a.d.Prefs.CallwordSoundPath(); f != "" {
				a.d.Audio.PlayFile(f) // custom callword sound
			} else {
				a.playThemeSFX("word_call")
			}
			a.ctx.FlashWindow()
			return
		}
	}
}

// --- screenshots ----------------------------------------------------------------------

// captureScreenshot reads the back buffer (render thread — required for
// ReadPixels) and writes the BMP off-thread next to the exe under
// screenshots\ (§17.2: file I/O never blocks the render thread).
func (a *App) captureScreenshot() {
	w, h, err := a.ctx.Ren.GetOutputSize()
	if err != nil {
		a.pushDebug("screenshot: " + err.Error())
		return
	}
	surf, err := sdl.CreateRGBSurfaceWithFormat(0, w, h, 32, uint32(sdl.PIXELFORMAT_ABGR8888))
	if err != nil {
		a.pushDebug("screenshot: " + err.Error())
		return
	}
	if err := a.ctx.Ren.ReadPixels(nil, surf.Format.Format, surf.Data(), int(surf.Pitch)); err != nil {
		surf.Free()
		a.pushDebug("screenshot: " + err.Error())
		return
	}
	stamp := time.Now().Format("20060102-150405")
	go func() {
		defer surf.Free()
		exe, err := os.Executable()
		if err != nil {
			return
		}
		dir := filepath.Join(filepath.Dir(exe), "screenshots")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return
		}
		path := filepath.Join(dir, "asyncao-"+stamp+".bmp")
		// SaveBMP touches only this surface — no renderer state, safe
		// off the render thread.
		_ = surf.SaveBMP(path)
	}()
	a.pushDebug("screenshot saved: screenshots\\asyncao-" + stamp + ".bmp")
}

// --- streamer mode ----------------------------------------------------------------------

// streamerIPRe matches IPv4-looking tokens (modcall broadcasts on some
// servers embed caller IPs).
var streamerIPRe = regexp.MustCompile(`\b\d{1,3}(?:\.\d{1,3}){3}\b`)

// streamerMaskLine redacts one OOC display line: the sender prefix
// becomes ???, IP-like tokens block out. Display-time only — the raw log
// is untouched, so switching the mode off restores everything.
func streamerMaskLine(line string) string {
	if name, rest, found := strings.Cut(line, ": "); found && len(name) <= streamerNameMax {
		line = "???: " + rest
		_ = name
	}
	return streamerIPRe.ReplaceAllString(line, "█.█.█.█")
}

// streamerNameMax bounds what counts as a "name:" prefix — a colon deep
// inside a sentence is message text, not a sender.
const streamerNameMax = 32

// --- OOC wrapping ----------------------------------------------------------------------

// oocWrapMaxLinesPerEntry bounds one entry's wrapped output (a hostile
// MOTD cannot balloon the list).
const oocWrapMaxLinesPerEntry = 24

// oocWrapped returns the OOC log as display lines: long entries (MOTDs)
// word-wrap to the list width and embedded newlines split, instead of the
// old 120-char truncation. Streamer mode masks sender names and IPs here
// (display only). Cached against (log seq, width, font scale, mask) —
// rebuilds happen on new messages or resizes, never per frame.
func (a *App) oocWrapped(width int32) []string {
	streamer := a.d.Prefs.StreamerMode()
	if a.oocWrap != nil && a.oocWrapSeq == a.oocSeq && a.oocWrapGen == a.ctx.fontChainGen &&
		a.oocWrapW == width && a.oocWrapPct == a.logPct && a.oocWrapMask == streamer {
		return a.oocWrap
	}
	out := a.oocWrap[:0]
	name := a.oocWrapName[:0] // parallel to out: speaker on each entry's first display line
	for i, entry := range a.oocLog {
		sp := ""
		if i < len(a.oocSpeakers) {
			sp = a.oocSpeakers[i]
		}
		if streamer {
			entry = streamerMaskLine(entry)
		}
		entryFirst := true // the speaker tints only the entry's FIRST display line
		for _, para := range strings.Split(entry, "\n") {
			// Per-paragraph font pick: wraps measure with the font that
			// will draw the line (the CJK chain rule).
			font := a.ctx.LogFontFor(a.logPct, para)
			lines := wrapToWidth(font, strings.TrimRight(para, "\r"), width, oocWrapMaxLinesPerEntry)
			if len(lines) == 0 {
				out = append(out, "") // blank MOTD spacer lines survive
				name = append(name, "")
				continue
			}
			for _, ln := range lines {
				out = append(out, ln)
				if entryFirst {
					name = append(name, sp)
					entryFirst = false
				} else {
					name = append(name, "")
				}
			}
		}
	}
	if out == nil {
		out = []string{}
	}
	a.oocWrap, a.oocWrapName = out, name
	a.oocWrapSeq, a.oocWrapW, a.oocWrapPct, a.oocWrapMask = a.oocSeq, width, a.logPct, streamer
	a.oocWrapGen = a.ctx.fontChainGen
	return out
}

// icWrapLine is one display row of the wrapped IC log: a text slice plus
// its source entry (the row inherits that entry's AO color).
type icWrapLine struct {
	text  string
	entry int // index into icLog
}

// icWrapMaxLinesPerEntry bounds one entry's wrapped rows. IC messages cap
// at 256 chars on the wire, so 16 rows survives even huge fonts in a
// narrow column.
const icWrapMaxLinesPerEntry = 16

// icWrapped returns the filtered IC log as display rows wrapped to the
// list width (playtest: lines clipped at the right edge). Cached against
// (log seq, query, width, font scale) — rebuilds on new messages,
// searches, and resizes, never per frame.
func (a *App) icWrapped(width int32) []icWrapLine {
	if a.icWrap != nil && a.icWrapSeq == a.icLogSeq && a.icWrapQuery == a.logSearch &&
		a.icWrapW == width && a.icWrapPct == a.logPct && a.icWrapGen == a.ctx.fontChainGen {
		return a.icWrap
	}
	out := a.icWrap[:0]
	for _, i := range a.icLogFiltered() {
		// Wrap with the font that will draw the entry (CJK chain rule).
		font := a.ctx.LogFontFor(a.logPct, a.icLog[i].text)
		for _, ln := range wrapToWidth(font, a.icLog[i].text, width, icWrapMaxLinesPerEntry) {
			out = append(out, icWrapLine{text: ln, entry: i})
		}
	}
	if out == nil {
		out = []icWrapLine{} // non-nil marks the cache as populated
	}
	a.icWrap, a.icWrapSeq, a.icWrapQuery, a.icWrapW, a.icWrapPct, a.icWrapGen =
		out, a.icLogSeq, a.logSearch, width, a.logPct, a.ctx.fontChainGen
	return out
}

// wrapToWidth greedily word-wraps text for an arbitrary font (the kit's
// WrapText memo is chrome-font only; this measures with the actual log
// font, and only runs on cache rebuilds). Words wider than the column
// hard-split so a long URL can't force a 1-line overflow. A nil font
// (headless tests) measures at a rough 8 px/char.
func wrapToWidth(font *ttf.Font, text string, maxW int32, maxLines int) []string {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	var lines []string
	var line strings.Builder
	flush := func() {
		if line.Len() > 0 {
			lines = append(lines, line.String())
			line.Reset()
		}
	}
	width := func(s string) int32 {
		if font == nil {
			return int32(len(s) * 8)
		}
		w, _, err := font.SizeUTF8(s)
		if err != nil {
			return int32(len(s) * 8)
		}
		return int32(w)
	}
	for _, word := range strings.Fields(text) {
		// Hard-split single words wider than the column.
		for width(word) > maxW && len(word) > 1 {
			cut := len(word) / 2
			for cut > 1 && width(word[:cut]) > maxW {
				cut /= 2
			}
			flush()
			lines = append(lines, word[:cut])
			word = word[cut:]
			if len(lines) >= maxLines {
				return lines
			}
		}
		candidate := word
		if line.Len() > 0 {
			candidate = line.String() + " " + word
		}
		if width(candidate) <= maxW {
			line.Reset()
			line.WriteString(candidate)
			continue
		}
		flush()
		if len(lines) >= maxLines {
			return lines
		}
		line.WriteString(word)
	}
	flush()
	if len(lines) > maxLines {
		lines = lines[:maxLines]
	}
	return lines
}

// --- IC log export --------------------------------------------------------------------

// icLogFiltered returns the indices of log entries matching the search
// box ("" = all), cached against (log seq, query): while the log tab is
// visible the per-frame cost is two comparisons instead of a 1024-line
// scan plus a slice allocation.
func (a *App) icLogFiltered() []int {
	if a.icFilter != nil && a.icFilterSeq == a.icLogSeq && a.icFilterQuery == a.logSearch {
		return a.icFilter
	}
	out := a.icFilter[:0]
	q := strings.ToLower(strings.TrimSpace(a.logSearch))
	for i := range a.icLog {
		if q == "" || strings.Contains(strings.ToLower(a.icLog[i].text), q) {
			out = append(out, i)
		}
	}
	if out == nil {
		out = []int{} // non-nil marks the cache as populated
	}
	a.icFilter, a.icFilterSeq, a.icFilterQuery = out, a.icLogSeq, a.logSearch
	return out
}

// copyICLog puts the (filtered) log on the clipboard.
func (a *App) copyICLog() {
	idx := a.icLogFiltered()
	var b strings.Builder
	for _, i := range idx {
		b.WriteString(a.icLog[i].text)
		b.WriteString("\r\n")
	}
	_ = sdl.SetClipboardText(b.String())
	a.pushDebug(fmt.Sprintf("copied %d log lines to clipboard", len(idx)))
}

// exportICLog writes the full log next to the exe as TXT or HTML (colors
// preserved in HTML via the AO palette). Snapshot on the render thread,
// file I/O off it.
func (a *App) exportICLog(asHTML bool) {
	entries := make([]icEntry, len(a.icLog))
	copy(entries, a.icLog)
	server := strings.Map(func(r rune) rune {
		if strings.ContainsRune(`\/:*?"<>| `, r) {
			return '-'
		}
		return r
	}, a.serverName)
	stamp := time.Now().Format("20060102-150405")
	go func() {
		exe, err := os.Executable()
		if err != nil {
			return
		}
		dir := filepath.Join(filepath.Dir(exe), "logs")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return
		}
		var b strings.Builder
		ext := ".txt"
		if asHTML {
			ext = ".html"
			b.WriteString("<!doctype html><meta charset=\"utf-8\"><title>AsyncAO IC log</title>\n")
			b.WriteString("<body style=\"background:#18181c;color:#ebebeb;font-family:monospace\">\n")
			for _, e := range entries {
				col := icHTMLColor(e.color)
				b.WriteString("<div style=\"color:" + col + "\">" + htmlEscape(e.text) + "</div>\n")
			}
			b.WriteString("</body>\n")
		} else {
			for _, e := range entries {
				b.WriteString(e.text)
				b.WriteString("\r\n")
			}
		}
		path := filepath.Join(dir, "ic-"+server+"-"+stamp+ext)
		if err := os.WriteFile(path, []byte(b.String()), 0o644); err == nil {
			select {
			case settings.ioRes <- "IC log exported: " + path:
			default:
			}
		}
	}()
	a.pushDebug("exporting IC log (" + stamp + ")")
}

// icHTMLColor maps an AO text color index onto a CSS color matching the
// in-client palette (render.TextColor).
func icHTMLColor(index int) string {
	col := render.TextColor(index)
	return fmt.Sprintf("#%02x%02x%02x", col.R, col.G, col.B)
}

func htmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}
