package ui

import (
	"fmt"
	"strings"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

// Custom style presets: named "mood" bundles of your sprite style and text colour (plus an emote, applied by name),
// created from the Sprite Style box, applied in one click and bound to Alt+<key> (bareBindKey), the per-preset
// companion to the showname keybinds. All local: applying one just sets your existing style, colour and emote.

// applyStylePreset applies a saved mood: sprite style and text colour always, the emote by name only when the current
// character has one (best effort, skipped outside a session). Mutually exclusive with the extended-colour selection.
func (a *App) applyStylePreset(p config.StylePreset) {
	a.d.Prefs.SetSpriteStyle(p.Style)
	a.icColor = p.Color
	a.icExtColor = 0
	a.icCustomOn = false // a preset picks a palette colour — the free hex turns off
	if p.Emote != "" {
		for i := range a.emotes {
			if strings.EqualFold(a.emotes[i].Anim, p.Emote) {
				a.emoteIdx = i
				a.icPreanim = emoteHasPreanim(&a.emotes[i]) // "Pre" auto-follows the pick (AO2-Client ui_pre)
				a.followPinnedPreview(a.activeCharName(), &a.emotes[i])
				break
			}
		}
	}
	a.warnLine = clampLine("Style → " + p.Name)
	a.warnAt = a.now()
}

// saveStylePreset snapshots the current style, text colour and selected emote's name into a named preset (overwriting
// one of the same name). A blank name is ignored.
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
		Style: a.d.Prefs.Load().MySpriteStyle,
		Color: a.icColor,
		Emote: emote,
	})
}

// drawStylePresets paints the presets section near the top of the Sprite Style box: a Save row (name + button) and one
// row per saved mood (Apply, bind a key, delete). It returns the y below the last row for the sections that follow.
// styleBoxRect already counts these rows into the box height, and the body scrolls.
func (a *App) drawStylePresets(c *Ctx, x, y, w int32) int32 {
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
	for i, pr := range a.d.Prefs.Load().StylePresets() {
		if c.Button(sdl.Rect{X: x, Y: y, W: applyW, H: 22}, pr.Name) {
			a.applyStylePreset(pr)
		}
		// The chord, not the letter: handleStylePresetKeys reads bareBindKey
		// (qol.go), so this preset applies on Alt+<key>. "..." is armed.
		keyLabel := bindChipLabel(pr.Key, a.stylePresetBindFor == pr.Name, "...", "key")
		if c.Button(sdl.Rect{X: x + applyW + 4, Y: y, W: bindW, H: 22}, keyLabel) {
			a.stylePresetBindFor = pr.Name // arm the key-capture (pollStylePresetBind finishes it)
		}
		if c.Button(sdl.Rect{X: x + applyW + bindW + 8, Y: y, W: delW, H: 22}, "x") {
			a.d.Prefs.DeleteStylePreset(i)
			break // the list changed; the rest redraws next frame
		}
		y += 26
	}
	return y
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
	for i, pr := range a.d.Prefs.Load().StylePresets() {
		if pr.Name == a.stylePresetBindFor {
			a.d.Prefs.SetStylePresetKey(i, key)
			a.pushDebug(fmt.Sprintf("key %q now applies style %q", key, pr.Name))
			break
		}
	}
	a.stylePresetBindFor = ""
}

// handleStylePresetKeys applies a key-bound preset on Alt+<bound key> in the
// courtroom — the shared bareBindKey gate every plain-key namespace moved onto
// (qol.go), so typing the same letter can never swap it. Returns true when it
// consumed the key.
func (a *App) handleStylePresetKeys() bool {
	k := a.bareBindKey()
	if k == 0 {
		return false
	}
	if pr, ok := a.d.Prefs.Load().StylePresetForKey(strings.ToLower(sdl.GetKeyName(k))); ok {
		a.applyStylePreset(pr)
		return true
	}
	return false
}
