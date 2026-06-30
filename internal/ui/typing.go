package ui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
)

// Typing indicator (#3): the opt-in, OFF-by-default "X is typing…" between AsyncAO
// users. The wire is courtroom/typingwire.go (an OOC carrier + a zero-width frame); this
// is the UI half — throttled SEND while you compose, RECEIVE + suppress at the OOC seam,
// and the on-screen caption. With the pref off the client sends ZERO pulses and shows
// nothing (TestTypingPrefGatesSend pins the send half).

const (
	// typingResendInterval caps pulses to one per this window while you actively type —
	// the flood-safety throttle (advisor: ~1 packet / 5 s) so a server's OOC rate limit
	// can't mute you for it.
	typingResendInterval = 5 * time.Second
	// typingActiveWindow stops pulsing this long after your last keystroke, so a stale
	// non-empty draft you walked away from doesn't pulse forever.
	typingActiveWindow = 6 * time.Second
	// typingExpiry drops a peer's caption this long after their last pulse — a bit more
	// than the resend interval so it doesn't flicker off between pulses.
	typingExpiry = 7 * time.Second
	// typingMaxPeers bounds the remembered typers (rule §17.4).
	typingMaxPeers = 64
)

// typingMaybeSend emits at most one throttled typing pulse per frame while you're
// actively composing an IC line, IF the opt-in indicator is on. With the pref off it
// sends nothing. The change-tracker (icInputLast/icTypedAt) runs regardless so toggling
// the pref mid-compose doesn't spuriously fire on the next keystroke.
func (a *App) typingMaybeSend() {
	if a.sess == nil || a.room == nil {
		return
	}
	now := a.now()
	if a.shouldSendTypingPulse(now) {
		a.lastTypingSent = now
		a.sess.SendOOC(a.oocNameOrDefault(), courtroom.EncodeTypingMarker())
	}
}

// shouldSendTypingPulse is the pure decision behind typingMaybeSend (no SDL / no send),
// so the gating is unit-tested. It also advances the keystroke tracker as a side effect
// (icInputLast/icTypedAt), which runs even with the pref off so toggling on mid-compose
// doesn't immediately fire on a stale draft. Returns false with the pref OFF — the
// zero-traffic guarantee.
func (a *App) shouldSendTypingPulse(now time.Time) bool {
	if a.icInput != a.icInputLast { // a keystroke / paste / recall changed the draft
		a.icInputLast = a.icInput
		a.icTypedAt = now
	}
	if !a.d.Prefs.TypingIndicatorOn() {
		return false // OFF ⇒ zero typing traffic (the guarantee that matters)
	}
	if strings.TrimSpace(a.icInput) == "" || now.Sub(a.icTypedAt) > typingActiveWindow {
		return false // nothing drafted, or you stopped typing a while ago
	}
	return now.Sub(a.lastTypingSent) >= typingResendInterval // else throttle
}

// notePeerTyping records that `name` is typing right now, bounded. Called from the OOC
// seam when a typing frame arrives (the line itself is suppressed there). The server
// echoes our own pulse back, so our own name is ignored.
func (a *App) notePeerTyping(name string) {
	name = strings.TrimSpace(name)
	if name == "" || name == a.oocNameOrDefault() {
		return
	}
	if a.typingPeers == nil {
		a.typingPeers = map[string]time.Time{}
	}
	if _, known := a.typingPeers[name]; !known && len(a.typingPeers) >= typingMaxPeers {
		return
	}
	a.typingPeers[name] = a.now()
}

// activeTypers returns the names currently shown as typing (within typingExpiry),
// sorted, pruning expired entries. nil when none or the feature is off.
func (a *App) activeTypers() []string {
	if !a.d.Prefs.TypingIndicatorOn() || len(a.typingPeers) == 0 {
		return nil
	}
	now := a.now()
	var live []string
	for name, at := range a.typingPeers {
		if now.Sub(at) > typingExpiry {
			delete(a.typingPeers, name)
			continue
		}
		live = append(live, name)
	}
	sort.Strings(live)
	return live
}

// typingLine formats active typers into a short caption, capping the named list so a
// busy room stays one line. Pure — unit-tested.
func typingLine(names []string) string {
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0] + " is typing…"
	case 2:
		return names[0] + " and " + names[1] + " are typing…"
	case 3:
		return names[0] + ", " + names[1] + " and " + names[2] + " are typing…"
	default:
		return fmt.Sprintf("%s, %s and %d others are typing…", names[0], names[1], len(names)-2)
	}
}

// drawTypingCaption paints the "… is typing…" pill just above the IC input box, on a
// short dark strip so it reads over anything behind. No-op when nobody's typing.
func (a *App) drawTypingCaption(icBox sdl.Rect) {
	line := typingLine(a.activeTypers())
	if line == "" {
		return
	}
	c := a.ctx
	tw := c.TextWidth(line) + 12
	if tw > icBox.W {
		tw = icBox.W
	}
	r := sdl.Rect{X: icBox.X, Y: icBox.Y - 18, W: tw, H: 16}
	c.Fill(r, sdl.Color{R: 0, G: 0, B: 0, A: 180})
	c.LabelClipped(r.X+6, r.Y+1, r.W-10, line, ColAccent)
}
