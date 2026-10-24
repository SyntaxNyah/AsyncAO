package ui

import "time"

// demandClockState owns demand state.
type demandClockState struct {
	// scenery self-heal stamps (healScenery pacing)
	bgAskBase   string
	bgAskAt     time.Time
	deskAskBase string
	deskAskAt   time.Time
	spkAskBase  string // speaker sprite heal pacing
	spkAskAt    time.Time
	pairAskBase string // pair sprite heal pacing
	pairAskAt   time.Time
}
