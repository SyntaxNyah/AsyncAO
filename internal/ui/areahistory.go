package ui

// Area history (M3): the areas you've recently passed through, newest first
// (index 0 = your current area), driven by your own PR/PU area. The Areas tab
// shows the rest as one-click "jump back" chips — the companion to follow.

// areaHistoryCap bounds the recent-areas list (hard rule #4: no unbounded slices).
const areaHistoryCap = 12

// pushAreaHistory move-to-fronts name in the MRU list, deduped and capped. A
// no-op when name is empty or already current (index 0), so it allocates only on
// a real area change — never per frame. Pure.
func pushAreaHistory(hist []string, name string) []string {
	if name == "" || (len(hist) > 0 && hist[0] == name) {
		return hist
	}
	out := make([]string, 0, areaHistoryCap)
	out = append(out, name)
	for _, h := range hist {
		if h != name && len(out) < areaHistoryCap {
			out = append(out, h)
		}
	}
	return out
}

// recordAreaHistory notes our current area (from the PR/PU roster) into the MRU
// history. Called on each EventPlayersUpdated; the dedup guard makes it free
// while we stay put. No-op without PR/PU (older servers report no self area).
func (a *App) recordAreaHistory() {
	if a.sess == nil {
		return
	}
	id, ok := a.sess.PlayerArea(a.sess.PlayerID)
	if !ok || id < 0 || id >= len(a.sess.Areas) {
		return
	}
	a.areaHistory = pushAreaHistory(a.areaHistory, a.sess.Areas[id])
}
