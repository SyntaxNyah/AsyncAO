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

// handleHotkeys consumes this frame's Ctrl chord on the courtroom screen.
func (a *App) handleHotkeys() {
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
			a.playThemeSFX("word_call")
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
	if a.oocWrap != nil && a.oocWrapSeq == a.oocSeq &&
		a.oocWrapW == width && a.oocWrapPct == a.logPct && a.oocWrapMask == streamer {
		return a.oocWrap
	}
	font := a.ctx.LogFont(a.logPct)
	out := a.oocWrap[:0]
	for _, entry := range a.oocLog {
		if streamer {
			entry = streamerMaskLine(entry)
		}
		for _, para := range strings.Split(entry, "\n") {
			lines := wrapToWidth(font, strings.TrimRight(para, "\r"), width, oocWrapMaxLinesPerEntry)
			if len(lines) == 0 {
				out = append(out, "") // blank MOTD spacer lines survive
				continue
			}
			out = append(out, lines...)
		}
	}
	if out == nil {
		out = []string{}
	}
	a.oocWrap, a.oocWrapSeq, a.oocWrapW, a.oocWrapPct, a.oocWrapMask = out, a.oocSeq, width, a.logPct, streamer
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
