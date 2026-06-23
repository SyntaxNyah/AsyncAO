package ui

import (
	"strings"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
)

// Auto-status (#M1): flip your presence status from trigger words you type in IC (e.g.
// "brb" → AFK). Configurable per status in Settings; OFF by default. The match runs on
// the SEND path (not the render loop), so its small ToLower/Fields cost is fine — and a
// trigger word rides the same message, so others see the change immediately (it fixes
// the "status only updates when you next speak" gap for announced changes).

// matchAutoStatus scans an outgoing IC message for a configured trigger word and returns
// the status to switch to. Whole-word, case-insensitive; the last matching word wins (so
// a "back" later in the line clears an earlier "brb"). Pure + testable.
func matchAutoStatus(text string, pref config.AutoStatusPref) (courtroom.Status, bool) {
	if !pref.Enabled {
		return courtroom.StatusNone, false
	}
	lookup := map[string]courtroom.Status{}
	addAutoStatusWords(lookup, pref.ClearWords, courtroom.StatusNone)
	addAutoStatusWords(lookup, pref.AFKWords, courtroom.StatusAFK)
	addAutoStatusWords(lookup, pref.BusyWords, courtroom.StatusBusy)
	addAutoStatusWords(lookup, pref.WritingWords, courtroom.StatusWriting)
	addAutoStatusWords(lookup, pref.LFRPWords, courtroom.StatusLFRP)
	if len(lookup) == 0 {
		return courtroom.StatusNone, false
	}
	status, matched := courtroom.StatusNone, false
	for _, tok := range strings.Fields(strings.ToLower(text)) {
		tok = strings.Trim(tok, ".,!?;:\"'()[]")
		if s, ok := lookup[tok]; ok {
			status, matched = s, true
		}
	}
	return status, matched
}

func addAutoStatusWords(m map[string]courtroom.Status, csv string, s courtroom.Status) {
	for _, w := range strings.Split(csv, ",") {
		if w = strings.ToLower(strings.TrimSpace(w)); w != "" {
			m[w] = s
		}
	}
}

// applyAutoStatus flips myStatus from a trigger word in the outgoing message (#M1).
func (a *App) applyAutoStatus(text string) {
	if s, ok := matchAutoStatus(text, a.d.Prefs.AutoStatus()); ok {
		a.myStatus = s
	}
}

// drawAutoStatusSettings draws the "Auto-status" editor: an enable toggle plus a
// comma-separated trigger-word field per status. Settings-only; never a hot path.
func (a *App) drawAutoStatusSettings(y, w int32) int32 {
	c := a.ctx
	as := a.d.Prefs.AutoStatus()
	if next := c.Checkbox(pad, y, "Auto-set my status from words I type in IC (e.g. \"brb\" → AFK)", as.Enabled); next != as.Enabled {
		as.Enabled = next
		a.d.Prefs.SetAutoStatus(as)
	}
	y += 24
	if !as.Enabled {
		c.Label(pad, y, "When on, an IC message containing one of your words flips your status; standard clients are unaffected.", ColTextDim)
		return y + 22
	}
	c.Label(pad, y, "Comma-separated trigger words per status. The word rides the message, so others see it right away.", ColTextDim)
	y += 24
	changed := false
	field := func(id, label, val string) string {
		c.Label(pad, y+5, label, ColText)
		n, _ := c.TextField(id, sdl.Rect{X: pad + 90, Y: y, W: 280, H: fieldH}, val, "words, comma-separated")
		y += 30
		if n != val {
			changed = true
		}
		return n
	}
	as.AFKWords = field("as_afk", "AFK", as.AFKWords)
	as.BusyWords = field("as_busy", "Busy", as.BusyWords)
	as.WritingWords = field("as_writing", "Writing", as.WritingWords)
	as.LFRPWords = field("as_lfrp", "LFRP", as.LFRPWords)
	as.ClearWords = field("as_clear", "Clear", as.ClearWords)
	if changed {
		a.d.Prefs.SetAutoStatus(as)
	}
	return y + 4
}
