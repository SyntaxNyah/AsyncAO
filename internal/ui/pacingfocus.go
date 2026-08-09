package ui

// The PACER'S FOCUS DECISION — one concern, one file.
//
// Every rate the frame pacer picks hangs off ONE boolean the main loop reads from
// the window (`WINDOW_INPUT_FOCUS`) and hands to five App methods: FramePace,
// HardCapBudget, SkipFrame, NextWakeDelay and RenderSuppressed. Between them they
// own the unfocused trickle, the 0-fps background ceiling, the static park and the
// wake schedule — four separate places an unfocused window slows down.
//
// The user's "keep full speed while unfocused" option (a second monitor, capture
// software recording the client while you work elsewhere) therefore lands HERE, as
// an override on that one boolean, and nowhere else. Two things it deliberately is
// NOT:
//
//   - NOT a check inside wantsFullRate. That predicate answers "is something on
//     screen genuinely moving", it is consulted by SkipFrame as well as FramePace,
//     and the census doctrine ([[v1-55-2-test30-preview-cap-latch]]: never key
//     SkipFrame on bare state) says a preference must not masquerade as a draw-site
//     census. A pref answering "yes, something is animating" would hold the loop at
//     the full-input tier forever and defeat every OTHER cap at once.
//   - NOT a restoration of the old unfocused-follows-animation behaviour, which
//     FramePace's unfocused branch already re-litigated once. That branch stays
//     exactly as it is; the pref is the sanctioned route past it, and with the pref
//     off nothing below changes a single frame.
//
// Because the override is applied at the entry points, the user's ACTIVE cap still
// rules an unfocused window — HardCapBudget's focused arm is the active FPS cap,
// which is the honest reading of "treat it exactly like a focused window".
//
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
	return a.d.Prefs != nil && a.d.Prefs.UnfocusedFullRateOn()
}
