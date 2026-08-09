package ui

import (
	"fmt"
	"strings"

	"github.com/veandco/go-sdl2/sdl"
)

// Showname keybinds (M6): bind a key to a specific saved showname preset, then
// press it in the courtroom to swap your showname to it — the per-preset
// companion to Ctrl+H (random) and Ctrl+B (cycle). Global, persisted, armed from
// Settings → General. Mirrors the wardrobe character-keybind machinery, but
// server-agnostic (shownames are global, not per-server).

// refreshShownameKeys re-reads the global showname binds into the per-frame
// lookup caches (startup + bind edits only — never per frame).
func (a *App) refreshShownameKeys() {
	a.shownameKeys = a.d.Prefs.ShownameKeyBinds()
	if len(a.shownameKeys) == 0 {
		a.shownameKeysRev = nil
		return
	}
	a.shownameKeysRev = make(map[string]string, len(a.shownameKeys))
	for k, sn := range a.shownameKeys {
		a.shownameKeysRev[strings.ToLower(sn)] = k
	}
}

// shownameKeyFor reports the key bound to a showname ("" = none) — the Settings
// preset-list badge lookup.
func (a *App) shownameKeyFor(showname string) string {
	return a.shownameKeysRev[strings.ToLower(showname)]
}

// pollShownameBind completes an armed Settings key-capture: the next plain
// keypress binds key → the chosen showname; Esc cancels. Runs every frame from
// the main poll loop, so it captures even while the Settings screen is up.
func (a *App) pollShownameBind() {
	if a.shownameBindFor == "" {
		return
	}
	c := a.ctx
	if c.escPressed {
		a.shownameBindFor = ""
		return
	}
	if c.keyPressed == 0 {
		return
	}
	key := strings.ToLower(sdl.GetKeyName(c.keyPressed))
	a.d.Prefs.SetShownameKeyBind(key, a.shownameBindFor)
	a.pushDebug(fmt.Sprintf("key %q now sets showname %q", key, a.shownameBindFor))
	a.shownameBindFor = ""
	a.refreshShownameKeys()
}

// handleShownameKeys applies a bound showname on Alt+<bound key> in the courtroom
// — the shared bareBindKey gate every plain-key namespace moved onto (qol.go), so
// typing the same letter can never swap it. Returns true when it consumed the
// key, so the character/emote keybinds don't also fire on the same press.
func (a *App) handleShownameKeys() bool {
	k := a.bareBindKey()
	if k == 0 {
		return false
	}
	sn, ok := a.shownameKeys[strings.ToLower(sdl.GetKeyName(k))]
	if !ok {
		return false
	}
	a.shownameOverride = sn // the in-courtroom override effectiveShowname reads
	a.warnLine = clampLine("Showname → " + sn)
	a.warnAt = a.now()
	return true
}
