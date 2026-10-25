package ui

import "time"

// timerState owns timer state.
type timerState struct {
	// Local alarm/timer (#97, opt-in): a personal countdown distinct from the
	// server courtroom timers. App-global (one timer across all tabs, like DND).
	showTimer       bool
	timerEndAt      time.Time     // when the running countdown fires (zero = not running)
	timerPausedLeft time.Duration // frozen remainder while paused (0 = not paused)
	timerSetSec     int           // configured duration, seeded from prefs on first open
	timerRepeat     bool          // restart from the same duration on fire
	timerSeeded     bool          // timerSetSec loaded from prefs once
}
