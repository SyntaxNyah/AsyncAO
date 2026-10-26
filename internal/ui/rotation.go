package ui

// Per-element ROTATION for texture-backed, non-interactive chrome (A4 — the
// "cheap tier" of the layout-rotation revamp). A user rotates a themed HP bar,
// button, backdrop or chatbox skin (and, when a theme ships classic chrome art,
// the classic HP bar) through the layout editor's R-key; the angle persists per
// element (config.ThemeRectRotations / config.ClassicRotations) and the draw
// path blits it with SDL's CopyEx.

import (
	"strconv"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
)

// classicSlotRotation resolves a classic slot's rotation angle in degrees for
// the draw path. It is a single map probe on the App-local snapshot (loaded once
// in ensureClassicOv) — O(1), no prefs read — and returns 0 (the plain-Copy
// fast path) when the slot has no rotation, which is every slot on a settled,
// unedited frame. Kept tiny and SDL-free so the angle==0 routing is unit-testable.
func (classic1 *classicState) classicSlotRotation(slot string) float64 {
	b, ok := classic1.classicRot[slot]
	if !ok || b == 0 {
		return 0
	}
	return config.RotationByteToDeg(b)
}

// classicRotHPBar keys the classic HP-bar rotation. The classic HP bar is NOT a
// draggable slot (it is drawn at the viewport corners via drawHPBar, gated by
// panelHP — there is no slotHP to hover), so this angle is only ever set by an
// applied LayoutProfile that carries it, never by the classic R-key. Defense and
// prosecution share the encoding but persist under distinct keys so a user can
// tilt one without the other.
const (
	classicRotHPBarDef = "hpbar:def"
	classicRotHPBarPro = "hpbar:pro"
)

// classicSlotRotatable reports whether a classic editor slot maps to
// texture-backed chrome the CopyEx path can rotate (architect ruling 5). NO
// classic SLOT qualifies today: the only texture-backed classic chrome is the HP
// bar, which is not a draggable slot (see classicRotHPBar), and the classic
// chatbox skin is deliberately out of scope (ruling 5 scopes chatbox rotation to
// the themed skin). So this returns false for every slot and the classic R-key
// reports "n/a" — an honest, ruling-6-compliant result, not a silent no-op.
func classicSlotRotatable(slot string) bool {
	_ = slot
	return false
}

// themedKeyRotatable reports whether a themed editor key actually maps to
// texture-backed chrome that the CopyEx path can rotate (architect ruling 5 —
// the editor must only OFFER rotation on pieces that rotate). It keys off
// RESIDENT theme art: an HP bar / chatbox / button with no theme image draws via
// the flat Fill/Border fallback, which ignores the angle, so offering rotation
// there would persist a byte with no visible effect. The classic chatbox skin
// (screens.go) is deliberately NOT in the rotatable set — ruling 5 scopes
// chatbox rotation to the THEMED skin (theme_layout.go).
func (a *App) themedKeyRotatable(key string) bool {
	switch key {
	case "defense_bar", "prosecution_bar":
		// The HP art swaps by penalty value; any resident bar frame counts (the
		// full-health stem is the always-present default).
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

// themeChatboxKey is the themed editor's key for the chatbox rect (the skin
// stretches to lay.rect(themeChatboxKey)).
const themeChatboxKey = "ao2_chatbox"

// badgeAngle resolves a plumbing-only overlay's rotation from the themed layout
// cache, nil-safe for the classic path (lay==nil → 0). These overlays (testimony
// badge, evidence popup, WT/CE splash) are not editor-hoverable keys today, so
// the cache carries no angle for them and this returns 0 — the branch is present
// only to keep the CopyEx path bolt-on-ready (a later slice can add editor keys
// and these would rotate for free).
func badgeAngle(lay *themeLayoutCache, key string) float64 {
	if lay == nil {
		return 0
	}
	return lay.angle(key)
}

// themedRotationDeg resolves a themed key's rotation in degrees from the live
// layout cache (angles are baked into themeLayoutCache.ang at rebuild time, so
// this is a plain map probe — never a prefs read on the draw path). 0 (absent or
// explicit) routes through the plain Copy path.
func (a *App) themedRotationDeg(key string) float64 {
	if a.skin.themeLay.ang == nil {
		return 0
	}
	b, ok := a.skin.themeLay.ang[key]
	if !ok || b == 0 {
		return 0
	}
	return config.RotationByteToDeg(b)
}

// nextRotationByte advances a rotation byte for an editor R-keypress: the coarse
// cycle (0/90/180/270) by default, or the fine RotStepFineDeg step when Shift is
// held. The fine step recomputes from degrees each press (byte → deg → +step →
// byte) rather than nudging the byte directly, so quantization error doesn't
// accumulate — the visible step is ~15° ± 1° and a full circle of fine presses
// doesn't land exactly back on 0 (documented, accepted, matches SpriteStyle).
func nextRotationByte(cur uint8, fine bool) uint8 {
	if fine {
		return config.RotationDegToByte(int(config.RotationByteToDeg(cur)) + config.RotStepFineDeg)
	}
	return config.NextCoarseRotation(cur)
}

// rotationChipLabel is the editor banner's "Rot N°" readout for a nonzero angle
// (empty when the angle is 0 — the chip only shows when the hovered piece is
// actually rotated). Rounds the byte to whole degrees for display.
func rotationChipLabel(b uint8) string {
	if b == 0 {
		return ""
	}
	return "Rot " + strconv.Itoa(int(config.RotationByteToDeg(b)+0.5)) + "°"
}

// cycleSlotRotation advances the hovered CLASSIC slot's rotation (R-key). Because
// no classic slot is texture-backed today (classicSlotRotatable), this reports an
// "n/a" hint rather than silently doing nothing — the R-key still exists for
// symmetry with the themed editor and forward-compatibility. Non-undoable, like
// the anchor pin (classiclayout.go:598 — pins aren't in the undo history either);
// persistence rides the SetClassicRotation setter directly (user-initiated, no
// SaveNow), matching cycleSlotAnchor.
func (classic1 *classicState) cycleSlotRotation(name string, fine bool) {
	if name == "" {
		return
	}
	if !classicSlotRotatable(name) {
		classic1.env.debug.Push("layout: " + classicSlotLabel(name) + " can't be rotated (not texture-backed chrome)")
		return
	}
	// Rotation re-bases like the anchor: it needs an override to hang off, so mint
	// one from the current rect if the slot has none (unreachable while no classic
	// slot is rotatable, but kept correct for the forward-compat path).
	if _, ok := classic1.classicOv[name]; !ok {
		return
	}
	next := nextRotationByte(classic1.classicRot[name], fine)
	if classic1.classicRot == nil {
		classic1.classicRot = make(map[string]uint8, classicSlotRegCap)
	}
	if next == 0 {
		delete(classic1.classicRot, name)
		classic1.env.d.Prefs.ClearClassicRotation(name)
	} else {
		classic1.classicRot[name] = next
		classic1.env.d.Prefs.SetClassicRotation(name, next)
	}
	classic1.env.debug.Push("layout: " + classicSlotLabel(name) + " rotated to " + strconv.Itoa(int(config.RotationByteToDeg(next)+0.5)) + "°")
}

// cycleThemeRotation advances the hovered THEMED key's rotation (R-key). Offered
// only on keys that actually rotate (themedKeyRotatable); a press on a
// non-rotatable key reports "n/a". Non-undoable (mirrors the classic decision and
// the theme editor's own no-undo-for-metadata precedent); persistence rides
// SetThemeRectRotation directly, and the layout cache is invalidated so the new
// angle reaches the draw path on the next rebuild (like a drag@theme_layout).
func (a *App) cycleThemeRotation(themeName, key string, fine bool) {
	if themeName == "" || key == "" {
		return
	}
	if !a.themedKeyRotatable(key) {
		a.debug.Push("layout edit: " + key + " can't be rotated (no theme art — draws flat)")
		return
	}
	cur := uint8(0)
	if a.skin.themeLay.ang != nil {
		cur = a.skin.themeLay.ang[key]
	}
	next := nextRotationByte(cur, fine)
	if next == 0 {
		a.d.Prefs.ClearThemeRectRotation(themeName, key)
	} else {
		a.d.Prefs.SetThemeRectRotation(themeName, key, next)
	}
	a.invalidateThemeCanvases() // re-bake ang so the new angle reaches the draw path
	a.debug.Push("layout edit: " + key + " rotated to " + strconv.Itoa(int(config.RotationByteToDeg(next)+0.5)) + "°")
}
