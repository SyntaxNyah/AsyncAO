package ui

import (
	"fmt"
	"strings"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
)

// Custom style presets (#126): named "mood" bundles of your own sprite style + text colour
// (+ an emote, applied by name), created from the Sprite Style box, applied in one click, and
// bound to a bare key for hands-free swapping — the per-preset companion to the showname
// keybinds (M6). All local: applying one just sets your existing style / colour / emote.

// applyStylePreset applies a saved mood. The sprite style + text colour always; the emote by
// NAME only when the current character actually has one (best-effort, skipped outside a
// session). Mutually exclusive with the extended-colour selection, like picking a palette
// colour by hand.
func (a *App) applyStylePreset(p config.StylePreset) {
	a.d.Prefs.SetSpriteStyle(p.Style)
	a.icColor = p.Color
	a.icExtColor = 0
	a.icCustomOn = false // a preset picks a palette colour — the free hex turns off
	if p.Emote != "" {
		for i := range a.emotes {
			if strings.EqualFold(a.emotes[i].Anim, p.Emote) {
				a.emoteIdx = i
				break
			}
		}
	}
	a.warnLine = clampLine("Style → " + p.Name)
	a.warnAt = a.now()
}

// saveStylePreset snapshots the current look — sprite style + text colour + the selected
// emote's name — into a named preset (overwriting one of the same name in place). A blank
// name is ignored.
func (a *App) saveStylePreset(name string) {
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	emote := ""
	if a.emoteIdx >= 0 && a.emoteIdx < len(a.emotes) {
		emote = a.emotes[a.emoteIdx].Anim
	}
	a.d.Prefs.AddStylePreset(config.StylePreset{
		Name:  name,
		Style: a.d.Prefs.SpriteStyle(),
		Color: a.icColor,
		Emote: emote,
	})
}

// drawStylePresets paints the #126 presets section at the bottom of the Sprite Style box: a
// Save row (name + button) and one row per saved mood — Apply (the wide button) · bind a key ·
// delete. The box height already reserved room for these rows (styleBoxRect).
func (a *App) drawStylePresets(c *Ctx, x, y, w int32) {
	c.LabelClipped(x, y, w, "Saved styles — Save current, click to apply, bind a key:", ColTextDim)
	y += 20
	const saveBtnW = int32(70)
	a.stylePresetNameInput, _ = c.TextField("stylePresetName", sdl.Rect{X: x, Y: y, W: w - saveBtnW - 4, H: 22}, a.stylePresetNameInput, "name this mood")
	if c.Button(sdl.Rect{X: x + w - saveBtnW, Y: y, W: saveBtnW, H: 22}, "Save") {
		a.saveStylePreset(a.stylePresetNameInput)
		a.stylePresetNameInput = ""
	}
	y += 28
	const bindW, delW = int32(48), int32(22)
	applyW := w - bindW - delW - 8
	for i, pr := range a.d.Prefs.StylePresets() {
		if c.Button(sdl.Rect{X: x, Y: y, W: applyW, H: 22}, pr.Name) {
			a.applyStylePreset(pr)
		}
		keyLabel := "key"
		if a.stylePresetBindFor == pr.Name {
			keyLabel = "..." // armed: press a key
		} else if pr.Key != "" {
			keyLabel = strings.ToUpper(pr.Key)
		}
		if c.Button(sdl.Rect{X: x + applyW + 4, Y: y, W: bindW, H: 22}, keyLabel) {
			a.stylePresetBindFor = pr.Name // arm the key-capture (pollStylePresetBind finishes it)
		}
		if c.Button(sdl.Rect{X: x + applyW + bindW + 8, Y: y, W: delW, H: 22}, "x") {
			a.d.Prefs.DeleteStylePreset(i)
			break // the list changed; the rest redraws next frame
		}
		y += 26
	}
}

// pollStylePresetBind completes an armed key-capture: the next plain keypress binds it to the
// chosen preset (by name); Esc cancels. Runs every frame from the main poll loop, mirroring
// pollShownameBind, so it captures even with the Style box open.
func (a *App) pollStylePresetBind() {
	if a.stylePresetBindFor == "" {
		return
	}
	c := a.ctx
	if c.escPressed {
		a.stylePresetBindFor = ""
		return
	}
	if c.keyPressed == 0 {
		return
	}
	key := strings.ToLower(sdl.GetKeyName(c.keyPressed))
	for i, pr := range a.d.Prefs.StylePresets() {
		if pr.Name == a.stylePresetBindFor {
			a.d.Prefs.SetStylePresetKey(i, key)
			a.pushDebug(fmt.Sprintf("key %q now applies style %q", key, pr.Name))
			break
		}
	}
	a.stylePresetBindFor = ""
}

// handleStylePresetKeys applies a key-bound preset on a bare keypress in the courtroom — the
// same guards as handleShownameKeys (no field focus, no capture armed, no Ctrl chord), so
// typing never swaps it. Returns true when it consumed the key.
func (a *App) handleStylePresetKeys() bool {
	c := a.ctx
	if c.keyPressed == 0 || c.focusID != "" || c.ctrlHeld ||
		a.bindingFor != "" || a.shownameBindFor != "" || a.stylePresetBindFor != "" ||
		a.jukeBindFor != "" || a.macroBind >= 0 {
		return false
	}
	if pr, ok := a.d.Prefs.StylePresetForKey(strings.ToLower(sdl.GetKeyName(c.keyPressed))); ok {
		a.applyStylePreset(pr)
		return true
	}
	return false
}
