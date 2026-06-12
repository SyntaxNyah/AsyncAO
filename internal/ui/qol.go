package ui

// Quality-of-life: configurable Ctrl-chord hotkeys, callword alerts, IC
// log search/copy/export, screenshots, music stop. Render thread except
// the explicitly off-thread file writes.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/veandco/go-sdl2/sdl"

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
func (a *App) checkCallwords(text string) {
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

// --- IC log export --------------------------------------------------------------------

// icLogFiltered returns the indices of log entries matching the search
// box ("" = all). Called per frame only while the Log tab is visible.
func (a *App) icLogFiltered() []int {
	out := make([]int, 0, len(a.icLog))
	q := strings.ToLower(strings.TrimSpace(a.logSearch))
	for i := range a.icLog {
		if q == "" || strings.Contains(strings.ToLower(a.icLog[i].text), q) {
			out = append(out, i)
		}
	}
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
