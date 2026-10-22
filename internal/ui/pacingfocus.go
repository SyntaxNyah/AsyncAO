package ui

// The PACER'S FOCUS DECISION — one concern, one file.
// Because the override is applied at the entry points, the user's ACTIVE cap still
// rules an unfocused window — HardCapBudget's focused arm is the active FPS cap,
// which is the honest reading of "treat it exactly like a focused window".
// Pure and allocation-free; called a handful of times per pass.

// pacingFocused is the focus state the PACER should reason with, given the
// window's real one. Focused windows are unaffected (the common case returns
// immediately). A nil Prefs answers "unfocused stays unfocused", matching every
// other nil-Prefs guard on this path (SkipFrame's own EventDrivenLoopOn check) so
// a headless harness that never wired preferences paces exactly as before.
func (a *App) pacingFocused(focused bool) bool {
	if focused {
		return true
	}
	return a.d.Prefs != nil && a.d.Prefs.Load().UnfocusedFullRate
}
