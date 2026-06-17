package ui

import (
	"strconv"
	"time"
)

// Follow-a-player (M3): trail a player across areas. The PR/PU roster carries
// every player's live area (PU type 3), so following just watches the followed
// UID's area on each EventPlayersUpdated and auto-jumps when it differs from ours
// — a mod tailing a suspect, or catching up to a friend.

// followJumpDebounce bounds auto-jumps so a burst of PR/PU updates (or a jump
// that hasn't reflected back as our own area yet) can't spam area transfers.
const followJumpDebounce = 2 * time.Second

// followTarget decides where a follower should jump: the followed player's area
// NAME, when it differs from the follower's own area and resolves to a real area.
// ok=false means stay put (same area, unknown id, or an out-of-range area). Pure.
func followTarget(theirArea, myArea int, areas []string) (string, bool) {
	if theirArea == myArea || theirArea < 0 || theirArea >= len(areas) {
		return "", false
	}
	if name := areas[theirArea]; name != "" {
		return name, true
	}
	return "", false
}

// toggleFollow starts or stops following a player by UID. Starting catches up to
// them at once if they're already in another area.
func (a *App) toggleFollow(uid string) {
	if a.followUID == uid {
		a.followUID = ""
		a.warnLine = "Stopped following"
		a.warnAt = a.now()
		return
	}
	a.followUID = uid
	a.warnLine = clampLine("Following UID " + uid)
	a.warnAt = a.now()
	a.maybeFollowJump()
}

// maybeFollowJump auto-jumps to the followed player's area when it differs from
// ours. Called on each PR/PU update (EventPlayersUpdated); debounced. No-op when
// not following, the player is gone, or we're already together.
func (a *App) maybeFollowJump() {
	if a.followUID == "" || a.sess == nil {
		return
	}
	if !a.d.Prefs.FollowEnabledOn() {
		a.followUID = "" // feature is off (opt-in): stop trailing
		return
	}
	uid, err := strconv.Atoi(a.followUID)
	if err != nil {
		a.followUID = "" // malformed; stop trailing a ghost
		return
	}
	theirArea, ok := a.sess.PlayerArea(uid)
	if !ok {
		return // they've left, or no PR/PU yet — keep following in case they return
	}
	myArea, _ := a.sess.PlayerArea(a.sess.PlayerID)
	name, ok := followTarget(theirArea, myArea, a.sess.Areas)
	if !ok {
		return
	}
	if a.now().Sub(a.lastFollowJump) < followJumpDebounce {
		return
	}
	a.lastFollowJump = a.now()
	a.jumpToArea(name)
	a.warnLine = clampLine("Following → " + name)
	a.warnAt = a.now()
}
