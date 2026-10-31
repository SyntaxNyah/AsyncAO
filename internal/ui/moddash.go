package ui

import (
	"strconv"
	"strings"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
)

// Wire signals behind the CM and mod dashboard: server software (ID packet), mod status (AUTH), area CM (ARUP column).

// detectedSoftware resolves the server software: the user's override if set, else the ID-packet string from join.
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

// myAreaInfo returns the live ARUP state for our area (ok=false when unknown), matching myAreaName to the name list.
// It uses myAreaName, not curArea: a freshly joined CM has not clicked an area, so curArea would be empty or stale.
func (a *App) myAreaInfo() (courtroom.AreaInfo, bool) {
	if a.sess == nil {
		return courtroom.AreaInfo{}, false
	}
	name := a.myAreaName()
	for i := range a.sess.Areas {
		if a.sess.Areas[i] == name && i < len(a.sess.AreaInfo) {
			return a.sess.AreaInfo[i], true
		}
	}
	return courtroom.AreaInfo{}, false
}

// amICM reports, best effort, whether the ARUP CM column for our area names us; "FREE" or blank means no CM.
// Servers spell the column differently, so any of our identities may match; it only reveals harmless room controls.
func (a *App) amICM() bool {
	info, ok := a.myAreaInfo()
	if !ok {
		return false
	}
	cm := strings.ToLower(strings.TrimSpace(info.CM))
	if cm == "" || cm == "free" || cm == "-" {
		return false
	}
	// Exact: our uid in the Athena/Nyathena "(<uid>)" form; the parens stop a match inside a longer number.
	if a.sess != nil && a.sess.PlayerID > 0 && strings.Contains(cm, "("+strconv.Itoa(a.sess.PlayerID)+")") {
		return true
	}
	// Looser: our showname, character or OOC name as a substring, for servers that name the CM by one of those.
	for _, id := range []string{a.effectiveShowname(), a.myCharName(), a.oocName} {
		if id = strings.ToLower(strings.TrimSpace(id)); id != "" && strings.Contains(cm, id) {
			return true
		}
	}
	return false
}

// dashSoftwareKnown reports whether the dashboard can build correct commands; unknown disables the action buttons.
func (a *App) dashSoftwareKnown() bool {
	return a.detectedSoftware() != courtroom.SoftwareUnknown
}
