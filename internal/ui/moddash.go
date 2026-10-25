package ui

import (
	"strconv"
	"strings"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
)

// CM / mod dashboard (#130): a STANDALONE panel (its own thing — never bloats the player list)

// detectedSoftware resolves the server software for the dashboard's command syntax: the user's
// override if they set one, else auto-detected from the ID-packet software string captured on
// join (Session.Software).
func (a *App) detectedSoftware() courtroom.ServerSoftware {
	if a.link.cmSoftwareOverride != courtroom.SoftwareUnknown {
		return a.link.cmSoftwareOverride
	}
	if a.link.sess == nil {
		return courtroom.SoftwareUnknown
	}
	return courtroom.DetectSoftware(a.link.sess.Software)
}

// amIMod reports whether we're mod-authed — the AUTH wire flag the server sets on /login.
func (a *App) amIMod() bool {
	return a.link.sess != nil && a.link.sess.ModGranted
}

// myAreaInfo returns the live ARUP state for our current area (ok=false when unknown). AreaInfo
// is parallel to the area-name list, so we match our area's name to its index. We use myAreaName()
func (a *App) myAreaInfo() (courtroom.AreaInfo, bool) {
	if a.link.sess == nil {
		return courtroom.AreaInfo{}, false
	}
	name := a.myAreaName()
	for i := range a.link.sess.Areas {
		if a.link.sess.Areas[i] == name && i < len(a.link.sess.AreaInfo) {
			return a.link.sess.AreaInfo[i], true
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
	if a.link.sess != nil && a.link.sess.PlayerID > 0 && strings.Contains(cm, "("+strconv.Itoa(a.link.sess.PlayerID)+")") {
		return true
	}
	// Looser: our showname / character / OOC name as a substring (covers the servers that name the
	// CM by one of those instead of the uid).
	for _, id := range []string{a.effectiveShowname(), a.myCharName(), a.compose.oocName} {
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
