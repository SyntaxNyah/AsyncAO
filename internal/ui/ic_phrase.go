package ui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/veandco/go-sdl2/sdl"
)

// IC quick-phrases: bind a bare key to a canned IC line ("E → Happy Pride Month")
// so pressing it makes your CHARACTER say that line in IC — the IC counterpart to
// the OOC-only macros. Same shape as the showname keybinds (showname_bind.go): a
// global key→text map, cached per connect, dispatched on a bare keypress, and a
// Settings key-capture to set one.

// refreshICPhraseKeys re-reads the global key→IC-phrase binds into the per-frame
// lookup cache (connect + bind edits only, never per frame).
func (a *App) refreshICPhraseKeys() {
	a.icPhraseKeys = a.d.Prefs.ICPhraseBinds()
}

// handleICPhraseKeys sends a bound IC phrase on a bare keypress in the courtroom —
// only with no text field focused, no capture armed, and no Ctrl chord, so typing
// never fires one. Returns true when it consumed the key (so the character/emote
// binds don't also fire on the same press). Mirrors handleShownameKeys.
func (a *App) handleICPhraseKeys() bool {
	c := a.ctx
	if c.keyPressed == 0 || c.focusID != "" || a.bindingFor != "" || a.shownameBindFor != "" ||
		a.icPhraseBindFor != "" || a.jukeBindFor != "" || a.macroBind >= 0 || c.ctrlHeld {
		return false
	}
	phrase, ok := a.icPhraseKeys[strings.ToLower(sdl.GetKeyName(c.keyPressed))]
	if !ok || a.sess == nil {
		return false
	}
	a.sendICPhrase(phrase)
	return true
}

// sendICPhrase sends a canned line through the normal IC pipeline (current
// character / emote / colour) WITHOUT disturbing whatever is in the IC draft: it
// swaps the input, sends, then restores the draft (sendIC clears the input on a
// successful send). A "/command" phrase runs as the command, same as typing it.
func (a *App) sendICPhrase(phrase string) {
	if a.sess == nil || a.sess.MyCharID < 0 || strings.TrimSpace(phrase) == "" {
		return
	}
	draft := a.icInput
	a.icInput = phrase
	a.sendIC(0)
	a.icInput = draft
}

// pollICPhraseBind completes an armed Settings key-capture: the next plain keypress
// binds key → the typed IC phrase; Esc cancels. Runs every frame from the main
// poll loop so it captures even while Settings is up. Mirrors pollShownameBind.
func (a *App) pollICPhraseBind() {
	if a.icPhraseBindFor == "" {
		return
	}
	c := a.ctx
	if c.escPressed {
		a.icPhraseBindFor = ""
		return
	}
	if c.keyPressed == 0 {
		return
	}
	key := strings.ToLower(sdl.GetKeyName(c.keyPressed))
	a.d.Prefs.SetICPhraseKey(key, a.icPhraseBindFor)
	a.pushDebug(fmt.Sprintf("key %q now sends IC: %q", key, a.icPhraseBindFor))
	a.icPhraseBindFor = ""
	a.refreshICPhraseKeys()
}

// drawICPhraseSettings renders the IC quick-phrase list + editor (Settings →
// Controls). Returns the next y. Mirrors drawMacroSettings, but binds one key to
// one canned IC line.
func (a *App) drawICPhraseSettings(y, w int32) int32 {
	c := a.ctx
	pad := a.formX
	_ = w // laid out by formX/formW; w param kept for the call signature
	binds := a.d.Prefs.ICPhraseBinds()
	c.Label(pad, y+4, fmt.Sprintf("IC quick-phrases (%d) — a bound key makes your CHARACTER say the line in IC:", len(binds)), ColText)
	y += 26

	keys := make([]string, 0, len(binds))
	for k := range binds {
		keys = append(keys, k)
	}
	sort.Strings(keys) // stable display order
	for _, k := range keys {
		c.LabelClipped(pad+12, y+3, a.formW-90, clampLine(fmt.Sprintf("[%s]  %s", strings.ToUpper(k), binds[k])), ColTextDim)
		if c.Button(sdl.Rect{X: a.formX + a.formW - 56, Y: y, W: 50, H: 22}, "✕") {
			a.d.Prefs.SetICPhraseKey(k, "") // empty clears the bind
			a.refreshICPhraseKeys()
		}
		y += 24
	}

	// Editor: the phrase + a key-capture button (the next key binds to this line).
	settings.icPhrase, _ = c.TextField("icphrase", sdl.Rect{X: pad, Y: y, W: a.formW - 130, H: fieldH}, settings.icPhrase, "what your character says, e.g. Happy Pride Month")
	keyLabel := "Bind key"
	if a.icPhraseBindFor != "" {
		keyLabel = "press..."
	}
	if c.Button(sdl.Rect{X: a.formX + a.formW - 124, Y: y, W: 118, H: btnH}, keyLabel) {
		if p := strings.TrimSpace(settings.icPhrase); p != "" {
			a.icPhraseBindFor = p // arm: pollICPhraseBind binds the next key to this line
			a.ctx.focusID = ""
			settings.icPhrase = ""
			settings.statusLine = "Press a key to bind that IC phrase (Esc cancels)."
		}
	}
	y += 30
	c.Label(pad, y, "Fires with no text box focused, as your character with your current emote. A /command phrase runs the command.", ColTextDim)
	return y + 24
}
