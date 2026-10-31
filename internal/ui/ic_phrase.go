package ui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

// IC quick-phrases bind a key to a canned IC line. You press the bare letter to bind but it fires as Alt+<key>, which
// is why rows print bareBindLabel rather than the raw letter.

// refreshICPhraseKeys re-reads the binds into the lookup cache, on connect and bind edits only.
func (a *App) refreshICPhraseKeys() {
	a.icPhraseKeys = a.d.Prefs.Load().ICPhraseBinds()
}

// handleICPhraseKeys sends the phrase bound to Alt+<key>, returning true when it consumed the key so character and
// emote binds don't also fire.
func (a *App) handleICPhraseKeys() bool {
	k := a.bareBindKey()
	if k == 0 {
		return false
	}
	phrase, ok := a.icPhraseKeys[strings.ToLower(sdl.GetKeyName(k))]
	if !ok || a.sess == nil {
		return false
	}
	a.sendICPhrase(phrase)
	return true
}

// sendICPhrase sends a line through the normal IC path, restoring the draft afterwards.
// The phrase is the pending-echo snapshot, so its echo can't clear the restored draft.
func (a *App) sendICPhrase(phrase string) {
	if a.sess == nil || a.sess.MyCharID < 0 || strings.TrimSpace(phrase) == "" {
		return
	}
	draft := a.icInput
	a.icInput = phrase
	a.sendIC()
	a.icInput = draft
}

// pollICPhraseBind completes an armed key capture; it runs every frame so it works while Settings is up.
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

// drawICPhraseSettings draws the quick-phrase list and editor in Settings and returns the next y.
func (a *App) drawICPhraseSettings(y, w int32) int32 {
	c := a.ctx
	pad := a.formX
	_ = w // laid out by formX/formW; w param kept for the call signature
	binds := a.d.Prefs.Load().ICPhraseBinds()
	c.Label(pad, y+4, fmt.Sprintf("IC quick-phrases (%d) — a bound key makes your CHARACTER say the line in IC:", len(binds)), ColText)
	y += 26

	keys := make([]string, 0, len(binds))
	for k := range binds {
		keys = append(keys, k)
	}
	sort.Strings(keys) // stable display order
	for _, k := range keys {
		c.LabelClipped(pad+12, y+3, a.formW-90, clampLine(fmt.Sprintf("[%s]  %s", bareBindLabel(k), binds[k])), ColTextDim)
		if c.Button(sdl.Rect{X: a.formX + a.formW - 56, Y: y, W: 50, H: 22}, "✕") {
			a.d.Prefs.SetICPhraseKey(k, "") // empty clears the bind
			a.refreshICPhraseKeys()
		}
		y += 24
	}

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
	// An Alt chord is never text, so the phrase fires even while a text box has focus.
	c.Label(pad, y, "Fires on "+bareBindChordPrefix+"<key>, even while you are typing, as your character with your current emote. A /command phrase runs the command.", ColTextDim)
	return y + 24
}
