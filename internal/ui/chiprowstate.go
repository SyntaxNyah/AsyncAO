package ui

import "github.com/SyntaxNyah/AsyncAO/internal/courtroom"

// chipRowState owns chipUI state.
type chipRowState struct {
	// showAreaWheel expands the area-colour row's inline colour wheel (Settings →
	// Area list) — session-only chrome, like the other settings expanders.
	showAreaWheel bool
	// Server-clock chip memo: the "Tn mm:ss" labels are rebuilt only when their
	// displayed second changes, into a reused scratch slice — so a visible (esp.
	// paused) clock costs nothing on the always-on courtroom draw.
	timerChips     []string
	timerLabels    [courtroom.TimerCount]string
	timerLabelSecs [courtroom.TimerCount]int
}
