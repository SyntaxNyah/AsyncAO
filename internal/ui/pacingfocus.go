package ui

// Focus override for the frame pacer. The main loop hands one focus boolean to FramePace, HardCapBudget, SkipFrame,
// NextWakeDelay and RenderSuppressed; the "keep full speed while unfocused" preference overrides it here, once.
// It is not a check inside wantsFullRate: SkipFrame consults that too, and a preference answering "something is
// animating" would hold the loop at full rate and defeat every other cap.
// The active FPS cap still rules an unfocused window (HardCapBudget's focused arm). Pure and allocation-free.

// pacingFocused is the focus state the pacer should use, given the window's real one. A nil Prefs keeps unfocused
// unfocused, like the other nil-Prefs guards on this path.
func (a *App) pacingFocused(focused bool) bool {
	if focused {
		return true
	}
	return a.d.Prefs != nil && a.d.Prefs.Load().UnfocusedFullRate
}
