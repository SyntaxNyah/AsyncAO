package ui

import "time"

// courtExtrasState owns courtxtra state.
type courtExtrasState struct {
	// --- court extras (HP / WTCE / modcall / evidence) ---
	wtceName    string    // active splash stem ("" = none)
	wtceAt      time.Time // splash start (frame stepping + expiry)
	testimonyOn bool      // persistent "Testimony" recording badge (RT 2.9)
	hpPrev      [2]int    // last drawn HP per bar — penalty sfx direction
	showModcall bool      // modcall reason dialog
}
