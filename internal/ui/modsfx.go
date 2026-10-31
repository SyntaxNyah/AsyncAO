package ui

import (
	"strings"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/render"
)

// modSFXCooldown collapses a burst into one play: one /kick often produces two server OOC lines, and on the single
// alert channel the second would clip the first.
const modSFXCooldown = 1500 * time.Millisecond

// isServerOOCName reports whether an OOC line came from the server, the only origin scanned, so a player typing "ban"
// can't trigger a sound. Add here any other OOC name a server tags its mod replies with.
func isServerOOCName(name string) bool {
	n := strings.TrimSpace(name)
	return n == "" || strings.EqualFold(n, "server")
}

// classifyModAction picks the sound for a server OOC line; ban, kick, then mute, and the first match wins so one line
// plays at most one sound.
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

// scanModActionOOC plays a mod-action sound when a server OOC line announces a ban, kick or mute.
func (a *App) scanModActionOOC(name, text string) {
	if action, ok := classifyModAction(name, text); ok {
		a.playModActionSFX(action)
	}
}

// playModActionSFX plays an action's sound if enabled; deliberately not silenced by DND or streamer mode.
func (a *App) playModActionSFX(action render.ModAction) {
	var on bool
	var path string
	switch action {
	case render.ModBan:
		on, path = a.d.Prefs.Load().ModBanSFX, a.d.Prefs.Load().ModBanSoundFile
	case render.ModKick:
		on, path = a.d.Prefs.Load().ModKickSFX, a.d.Prefs.Load().ModKickSoundFile
	case render.ModMute:
		on, path = a.d.Prefs.Load().ModMuteSFX, a.d.Prefs.Load().ModMuteSoundFile
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
