package ui

import "testing"

// An iniswap list fetch that lands after its tab was parked belongs to that tab.
// Dropping it left the tab's busy flag set forever, so its wardrobe list never
// fetched again and stayed on "loading".
func TestIniswapResultReachesItsParkedTab(t *testing.T) {
	const (
		parkedKey = "ws://parked.example/"
		activeKey = "ws://active.example/"
		listed    = "Phoenix"
	)
	a := testTabApp(t)
	a.iniRes = make(chan iniswapFetch, 1)
	a.tabs = []*courtTab{{state: sessionState{serverKey: parkedKey, iniBusy: true}}}
	a.serverKey = activeKey
	a.iniBusy = true // the active tab's own fetch is still in flight

	a.iniRes <- iniswapFetch{key: parkedKey, names: []string{listed}}
	a.pollIniswap()

	parked := a.tabs[0].state
	if parked.iniBusy {
		t.Fatal("the parked tab stays busy: its list can never fetch again")
	}
	if len(parked.iniServer) != 1 || parked.iniServer[0] != listed {
		t.Errorf("parked tab's server list = %v, want [%s]", parked.iniServer, listed)
	}
	if !a.iniBusy || a.iniServer != nil {
		t.Errorf("another tab's result touched the active tab (busy=%v list=%v)", a.iniBusy, a.iniServer)
	}
}
