package ui

import (
	"strings"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
)

// CM / mod dashboard (#130): a STANDALONE panel (its own thing — never bloats the player list)
// with server-software-aware moderation. This file holds the wire-signal detection that drives
// it: the server software (ID packet), whether we're mod (AUTH), and whether we're our area's CM
// (ARUP CM column). All read on demand (panel draw / events), never per frame — the panel is
// opt-in (closed by default = zero cost).

// detectedSoftware resolves the server software for the dashboard's command syntax: the user's
// override if they set one, else auto-detected from the ID-packet software string captured on
// join (Session.Software).
func (a *App) detectedSoftware() courtroom.ServerSoftware {
	if a.cmSoftwareOverride != courtroom.SoftwareUnknown {
		return a.cmSoftwareOverride
	}
	if a.sess == nil {
		return courtroom.SoftwareUnknown
	}
	return courtroom.DetectSoftware(a.sess.Software)
}

// amIMod reports whether we're mod-authed — the AUTH wire flag the server sets on /login.
func (a *App) amIMod() bool {
	return a.sess != nil && a.sess.ModGranted
}

// myAreaInfo returns the live ARUP state for our current area (ok=false when unknown). AreaInfo
// is parallel to the area-name list, so we match our current area's name to its index.
func (a *App) myAreaInfo() (courtroom.AreaInfo, bool) {
	if a.sess == nil {
		return courtroom.AreaInfo{}, false
	}
	for i := range a.sess.Areas {
		if a.sess.Areas[i] == a.curArea && i < len(a.sess.AreaInfo) {
			return a.sess.AreaInfo[i], true
		}
	}
	return courtroom.AreaInfo{}, false
}

// amICM reports whether the ARUP CM column for our area names us — a best-effort identity match
// (showname or character), so the CM menu surfaces once a /cm takes effect on the wire. "FREE" /
// blank means no CM.
func (a *App) amICM() bool {
	info, ok := a.myAreaInfo()
	if !ok {
		return false
	}
	cm := strings.ToLower(strings.TrimSpace(info.CM))
	if cm == "" || cm == "free" || cm == "-" {
		return false
	}
	if name := strings.ToLower(strings.TrimSpace(a.effectiveShowname())); name != "" && strings.Contains(cm, name) {
		return true
	}
	if char := strings.ToLower(strings.TrimSpace(a.myCharName())); char != "" && strings.Contains(cm, char) {
		return true
	}
	return false
}

// dashSoftwareKnown reports whether the dashboard can build correct commands (an unknown
// software disables the action buttons until the user picks one via the override).
func (a *App) dashSoftwareKnown() bool {
	return a.detectedSoftware() != courtroom.SoftwareUnknown
}
