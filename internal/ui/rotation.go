package ui

// Per-element rotation for texture-backed, non-interactive chrome: themed HP bars, buttons, backdrops, chatbox skins
// and the classic HP bar. The layout editor's R-key sets an angle, persisted per element and drawn with CopyEx.
// Invariant: angle 0 takes the original Copy path, so an unedited courtroom is unchanged.
// Interactive rotated panels are not supported (they would need inverse-rotated hit-testing).

import (
	"strconv"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
)

// classicSlotRotation resolves a classic slot's rotation in degrees with one map probe on the App-local snapshot.
// 0 means the plain Copy path.
func (a *App) classicSlotRotation(slot string) float64 {
	b, ok := a.classicRot[slot]
	if !ok || b == 0 {
		return 0
	}
	return config.RotationByteToDeg(b)
}

// classicRotHPBar keys the classic HP-bar rotation. The HP bar is not a draggable slot, so only an applied
// LayoutProfile sets these; defense and prosecution persist under separate keys.
const (
	classicRotHPBarDef = "hpbar:def"
	classicRotHPBarPro = "hpbar:pro"
)

// classicSlotRotatable reports whether a classic editor slot is texture-backed chrome CopyEx can rotate.
// None is today (the HP bar isn't a slot; the classic chatbox skin is out of scope), so the R-key reports n/a.
// a.classicRot is fed only by an applied LayoutProfile.
func classicSlotRotatable(slot string) bool {
	_ = slot
	return false
}

// themedKeyRotatable reports whether a themed editor key has resident theme art that CopyEx can rotate.
// Without art the flat fallback ignores the angle, so rotation would persist a byte with no visible effect.
// The classic chatbox skin (screens.go) is not rotatable; only the themed skin is (theme_layout.go).
func (a *App) themedKeyRotatable(key string) bool {
	switch key {
	case "defense_bar", "prosecution_bar":
		// The HP art swaps by penalty value; any resident bar frame counts.
		def := key == "defense_bar"
		_, ok := a.themePage(a.hpBarStem(def, hpBarSegments))
		return ok
	case themeChatboxKey:
		_, ok := a.themePage(themeStemChatbox)
		return ok
	default:
		_, ok := a.themePage(themeBtnStem(key)) // same stem as drawThemeButton, memoized (app.go)
		return ok
	}
}

// themeChatboxKey is the themed editor's key for the chatbox rect.
const themeChatboxKey = "ao2_chatbox"

// badgeAngle resolves a plumbing-only overlay's rotation from the themed layout cache (nil lay for classic is 0).
// These overlays have no editor keys today, so it returns 0.
func badgeAngle(lay *themeLayoutCache, key string) float64 {
	if lay == nil {
		return 0
	}
	return lay.angle(key)
}

// themedRotationDeg resolves a themed key's rotation in degrees from the baked layout cache, never from prefs.
func (a *App) themedRotationDeg(key string) float64 {
	if a.themeLay.ang == nil {
		return 0
	}
	b, ok := a.themeLay.ang[key]
	if !ok || b == 0 {
		return 0
	}
	return config.RotationByteToDeg(b)
}

// nextRotationByte advances a rotation byte for an R-keypress: coarse 0/90/180/270, or RotStepFineDeg with Shift.
// Fine steps recompute from degrees so quantization error doesn't accumulate; a full circle may not land exactly on 0.
func nextRotationByte(cur uint8, fine bool) uint8 {
	if fine {
		return config.RotationDegToByte(int(config.RotationByteToDeg(cur)) + config.RotStepFineDeg)
	}
	return config.NextCoarseRotation(cur)
}

// rotationChipLabel is the editor banner's "Rot N°" readout, empty for angle 0.
func rotationChipLabel(b uint8) string {
	if b == 0 {
		return ""
	}
	return "Rot " + strconv.Itoa(int(config.RotationByteToDeg(b)+0.5)) + "°"
}

// cycleSlotRotation advances the hovered classic slot's rotation (R-key). No classic slot is rotatable today,
// so it reports n/a. Not undoable, like the anchor pin; persists through SetClassicRotation directly.
func (a *App) cycleSlotRotation(name string, fine bool) {
	if name == "" {
		return
	}
	if !classicSlotRotatable(name) {
		a.pushDebug("layout: " + classicSlotLabel(name) + " can't be rotated (not texture-backed chrome)")
		return
	}
	// Rotation needs an existing override; without one there is nothing to rotate.
	if _, ok := a.classicOv[name]; !ok {
		return
	}
	next := nextRotationByte(a.classicRot[name], fine)
	if a.classicRot == nil {
		a.classicRot = make(map[string]uint8, classicSlotRegCap)
	}
	if next == 0 {
		delete(a.classicRot, name)
		a.d.Prefs.ClearClassicRotation(name)
	} else {
		a.classicRot[name] = next
		a.d.Prefs.SetClassicRotation(name, next)
	}
	a.pushDebug("layout: " + classicSlotLabel(name) + " rotated to " + strconv.Itoa(int(config.RotationByteToDeg(next)+0.5)) + "°")
}

// cycleThemeRotation advances the hovered themed key's rotation (R-key), only on keys themedKeyRotatable accepts.
// Not undoable; persists through SetThemeRectRotation and invalidates the layout cache so the new angle is rebuilt.
func (a *App) cycleThemeRotation(themeName, key string, fine bool) {
	if themeName == "" || key == "" {
		return
	}
	if !a.themedKeyRotatable(key) {
		a.pushDebug("layout edit: " + key + " can't be rotated (no theme art — draws flat)")
		return
	}
	cur := uint8(0)
	if a.themeLay.ang != nil {
		cur = a.themeLay.ang[key]
	}
	next := nextRotationByte(cur, fine)
	if next == 0 {
		a.d.Prefs.ClearThemeRectRotation(themeName, key)
	} else {
		a.d.Prefs.SetThemeRectRotation(themeName, key, next)
	}
	a.invalidateThemeCanvases() // re-bake ang so the new angle reaches the draw path
	a.pushDebug("layout edit: " + key + " rotated to " + strconv.Itoa(int(config.RotationByteToDeg(next)+0.5)) + "°")
}
