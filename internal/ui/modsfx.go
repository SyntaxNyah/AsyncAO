package ui

import (
	"strings"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/render"
)

// modSFXCooldown debounces the mod-command feedback sound (#60): one /kick often
// produces TWO server OOC lines (the actor's confirmation AND an area
// broadcast), and on the single reserved alert channel the second would just
// clip the first. A short per-action cooldown collapses such a burst — and any
// auto-reconnect re-ban loop — into one play.
const modSFXCooldown = 1500 * time.Millisecond

// isServerOOCName reports whether an OOC line came from the server itself — the
// only origin we scan for mod actions, so a player typing "ban" in chat can't
// trip it. NOTE: if a server tags its mod replies under a different OOC name,
// add it here (this is the one-line tuning point, not a user pref).
func isServerOOCName(name string) bool {
	n := strings.TrimSpace(name)
	return n == "" || strings.EqualFold(n, "server")
}

// classifyModAction decides which mod-action sound (if any) a server OOC line
// should fire. It is pure and table-tested (modsfx_test.go) — this function IS
// the spec for the keyword set, the "un-"/negation exclusions, the fixed
// precedence (ban → kick → mute, FIRST match wins, so one line plays at most
// one sound), and the server-origin gate. Keywords are deliberately hardcoded,
// not a user-editable pref: tuning to a server is a one-line edit right here.
func classifyModAction(name, text string) (render.ModAction, bool) {
	if !isServerOOCName(name) {
		return 0, false
	}
	s := strings.ToLower(text)
	switch {
	case strings.Contains(s, "ban") && !strings.Contains(s, "unban"):
		return render.ModBan, true
	case strings.Contains(s, "kick") && !strings.Contains(s, "unkick"):
		return render.ModKick, true
	case strings.Contains(s, "mut") && !strings.Contains(s, "unmut") && !strings.Contains(s, "not mut"):
		return render.ModMute, true
	}
	return 0, false
}

// scanModActionOOC fires a mod-action sound when a server OOC line announces a
// ban/kick/mute (covers your own /ban /kick /mute landing and actions you can
// see). The EventOOC seam calls this for every OOC line.
func (a *App) scanModActionOOC(name, text string) {
	if action, ok := classifyModAction(name, text); ok {
		a.playModActionSFX(action)
	}
}

// playModActionSFX plays the feedback sound for a mod action if that action's
// toggle is on, honouring a short per-action cooldown. Mod feedback is a DUTY
// signal, so — unlike callword/friend pings — it is intentionally NOT gated by
// DND or streamer mode.
func (a *App) playModActionSFX(action render.ModAction) {
	var on bool
	var path string
	switch action {
	case render.ModBan:
		on, path = a.d.Prefs.ModBanSFXOn(), a.d.Prefs.ModBanSoundPath()
	case render.ModKick:
		on, path = a.d.Prefs.ModKickSFXOn(), a.d.Prefs.ModKickSoundPath()
	case render.ModMute:
		on, path = a.d.Prefs.ModMuteSFXOn(), a.d.Prefs.ModMuteSoundPath()
	default:
		return
	}
	if !on {
		return
	}
	i := int(action)
	if i < 0 || i >= len(a.lastModSFX) {
		return
	}
	now := time.Now()
	if now.Sub(a.lastModSFX[i]) < modSFXCooldown {
		return // collapse the actor-confirmation + area-broadcast burst into one play
	}
	a.lastModSFX[i] = now
	a.d.Audio.PlayModAction(action, path)
}
