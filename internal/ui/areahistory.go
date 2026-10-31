package ui

// Area history: the areas you've recently passed through, newest first (index 0 = your current area), from your own
// PR/PU area. The Areas tab shows the rest as one-click "jump back" chips.

// areaHistoryCap bounds the recent-areas list (hard rule 4).
const areaHistoryCap = 12

// pushAreaHistory moves name to the front of the MRU list, deduped and capped. A no-op when name is empty or already
// current, so it allocates only on a real area change. Pure.
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

// recordAreaHistory notes our current area (from the PR/PU roster) into the history on each EventPlayersUpdated; a
// no-op without PR/PU (older servers report no self area).
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
