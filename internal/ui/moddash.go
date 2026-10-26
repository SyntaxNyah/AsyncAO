package ui

import (
	"strconv"
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
// is parallel to the area-name list, so we match our area's name to its index. We use myAreaName()
// — the server-pushed area of OUR uid (a.sess.PlayerArea(PlayerID)), falling back to the
// click-driven curArea — NOT curArea directly: a freshly-joined CM hasn't clicked an area, so
// curArea would be empty/stale and the CM section would never appear.
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

// amICM reports whether the ARUP CM column for our area names us, so the CM menu surfaces once a
// /cm takes effect on the wire. "FREE" / blank means no CM. Servers spell that column differently
// — Athena/Nyathena write "<char> (<uid>)" (server.go sendCMArup), others use the showname or OOC
// name — so this is a best-effort match of ANY of our identities. The UID (a.sess.PlayerID, our
// own client id) is the STRONGEST signal where present: it's matched in Athena's parened form
// "(<uid>)" exactly, before the looser substring fallbacks. Best-effort by design — it only ever
// reveals harmless room controls, never anything destructive.
func (a *App) amICM() bool {
	info, ok := a.myAreaInfo()
	if !ok {
		return false
	}
	cm := strings.ToLower(strings.TrimSpace(info.CM))
	if cm == "" || cm == "free" || cm == "-" {
		return false
	}
	// Exact: our uid in the Athena/Nyathena "(<uid>)" form (no false positive — the parens fence
	// it off from other numbers like a longer uid or a name's digits).
	if a.sess != nil && a.sess.PlayerID > 0 && strings.Contains(cm, "("+strconv.Itoa(a.sess.PlayerID)+")") {
		return true
	}
	// Looser: our showname / character / OOC name as a substring (covers the servers that name the
	// CM by one of those instead of the uid).
	for _, id := range []string{a.effectiveShowname(), a.myCharName(), a.oocName} {
		if id = strings.ToLower(strings.TrimSpace(id)); id != "" && strings.Contains(cm, id) {
			return true
		}
	}
	return false
}

// dashSoftwareKnown reports whether the dashboard can build correct commands (an unknown
// software disables the action buttons until the user picks one via the override).
func (a *App) dashSoftwareKnown() bool {
	return a.detectedSoftware() != courtroom.SoftwareUnknown
}
