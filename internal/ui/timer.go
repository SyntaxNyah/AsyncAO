package ui

import (
	"fmt"
	"time"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
)

// Local alarm/timer (#97): an opt-in personal countdown — set a duration, then
// Start / Pause / Reset (optionally Repeat) — that pings, flashes the window and
// shows an on-screen banner when it reaches zero, so RPers and casers feel a bit
// of urgency. It is entirely distinct from the SERVER courtroom timers
// (sess.TimerCount / the centered clock chips). Off by default and zero-footprint
// until used: pollTimer returns on the first compare while idle, the on-stage
// chip only draws while a countdown is live, and nothing here touches the render
// hot path. App-global (one personal timer across all tabs, like Do-Not-Disturb).

// timerRunning reports a live countdown (not idle, not paused).
func (a *App) timerRunning() bool { return !a.timerEndAt.IsZero() }

// timerActive reports a countdown that's running OR paused (so the chip shows).
func (a *App) timerActive() bool { return a.timerRunning() || a.timerPausedLeft > 0 }

// timerRemaining is the time left: until timerEndAt while running, the frozen
// remainder while paused, else the configured duration (idle preview).
func (a *App) timerRemaining() time.Duration {
	switch {
	case a.timerRunning():
		if d := time.Until(a.timerEndAt); d > 0 {
			return d
		}
		return 0
	case a.timerPausedLeft > 0:
		return a.timerPausedLeft
	default:
		return time.Duration(a.timerSetSec) * time.Second
	}
}

// openTimer seeds the configured duration from prefs once, then shows the panel.
func (a *App) openTimer() {
	if !a.timerSeeded {
		a.timerSetSec = a.d.Prefs.TimerSecondsValue()
		a.timerRepeat = a.d.Prefs.TimerRepeatOn()
		a.timerSeeded = true
	}
	a.showTimer = true
}

// startTimer begins the countdown from the configured duration and remembers it.
func (a *App) startTimer() {
	sec := a.timerSetSec
	if sec < config.TimerMinSeconds {
		sec = config.TimerMinSeconds
	}
	a.timerSetSec = sec
	a.d.Prefs.SetTimerSeconds(sec)
	a.timerEndAt = time.Now().Add(time.Duration(sec) * time.Second)
	a.timerPausedLeft = 0
}

// pauseTimer freezes a running countdown at its current remainder.
func (a *App) pauseTimer() {
	if !a.timerRunning() {
		return
	}
	a.timerPausedLeft = a.timerRemaining()
	if a.timerPausedLeft <= 0 {
		a.timerPausedLeft = time.Second // never freeze at a stopped zero
	}
	a.timerEndAt = time.Time{}
}

// resumeTimer restarts a paused countdown from the frozen remainder.
func (a *App) resumeTimer() {
	if a.timerPausedLeft <= 0 {
		return
	}
	a.timerEndAt = time.Now().Add(a.timerPausedLeft)
	a.timerPausedLeft = 0
}

// resetTimer stops the countdown back to idle (the configured duration shows).
func (a *App) resetTimer() {
	a.timerEndAt = time.Time{}
	a.timerPausedLeft = 0
}

// pollTimer fires the alarm when a running countdown reaches zero. One time
// compare per frame while running; an immediate return (zero cost) when idle or
// paused. Called from the main poll loop.
func (a *App) pollTimer() {
	if a.timerEndAt.IsZero() {
		return
	}
	if time.Now().Before(a.timerEndAt) {
		return
	}
	a.fireTimer()
}

// fireTimer raises the alarm: built-in ping + window flash + on-screen banner,
// then repeats from the same duration if Repeat is on, else stops. User-initiated
// (you started it), so unlike callwords it is NOT gated by Do-Not-Disturb or
// streamer mode. Nil-guards keep it callable from tests with a bare App.
func (a *App) fireTimer() {
	a.warnLine = "Timer finished!"
	a.warnAt = time.Now()
	if a.ctx != nil {
		a.ctx.FlashWindow()
	}
	if a.d.Audio != nil {
		a.d.Audio.PlayAlert()
	}
	if a.timerRepeat && a.timerSetSec > 0 {
		a.timerEndAt = time.Now().Add(time.Duration(a.timerSetSec) * time.Second)
	} else {
		a.timerEndAt = time.Time{}
	}
}

// formatTimer renders a duration as MM:SS (or H:MM:SS past an hour), rounded to
// whole seconds and floored at zero.
func formatTimer(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	total := int(d.Round(time.Second) / time.Second)
	h, m, s := total/3600, (total%3600)/60, total%60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%02d:%02d", m, s)
}

// drawTimerChip draws the live countdown over the stage (top-right) while a timer
// is running or paused, so the urgency is always visible — independent of whether
// the setup panel is open. Drawn from drawCourtOverlays (both layouts); a no-op
// when no timer is active, so it costs nothing when unused.
func (a *App) drawTimerChip(vp sdl.Rect) {
	if !a.timerActive() {
		return
	}
	c := a.ctx
	rem := a.timerRemaining()
	label := "Timer " + formatTimer(rem)
	if a.timerPausedLeft > 0 {
		label += " (paused)"
	}
	w := c.TextWidth(label) + 12
	r := sdl.Rect{X: vp.X + vp.W - w - 6, Y: vp.Y + 4, W: w, H: 20}
	c.Fill(r, sdl.Color{R: 0, G: 0, B: 0, A: 185})
	col := ColAccent
	if a.timerRunning() && rem <= 10*time.Second { // final stretch: blink red for urgency
		col = ColDanger
		if (time.Now().UnixMilli()/400)%2 == 0 {
			col = ColText
		}
	}
	c.Border(r, col)
	c.Label(r.X+6, r.Y+3, label, col)
}

// drawTimerPanel is the setup modal: a large remaining readout, Minutes/Seconds
// sliders + quick presets while idle, and Start/Pause/Resume/Reset + Repeat. Shown
// from the Extras "Timer" entry; reached via drawCourtroomModals like other popups.
func (a *App) drawTimerPanel(w, h int32) {
	c := a.ctx
	panel := sdl.Rect{X: w/2 - 200, Y: h/2 - 135, W: 400, H: 270}
	c.Fill(panel, ColPanel)
	c.Border(panel, ColAccent)
	c.Heading(panel.X+pad, panel.Y+10, "Timer", ColText)
	if c.Button(sdl.Rect{X: panel.X + panel.W - 34, Y: panel.Y + 8, W: 26, H: 24}, "X") {
		a.showTimer = false
	}

	// Big remaining / configured readout, plus a paused tag.
	read := formatTimer(a.timerRemaining())
	if a.timerPausedLeft > 0 {
		read += "  (paused)"
	}
	c.Heading(panel.X+pad, panel.Y+44, read, ColAccent)

	by := panel.Y + panel.H - btnH - 14
	if a.timerActive() {
		// Running or paused: transport controls only (the duration is locked in).
		if a.timerRunning() {
			if c.Button(sdl.Rect{X: panel.X + pad, Y: by, W: 110, H: btnH}, "Pause") {
				a.pauseTimer()
			}
		} else {
			if c.Button(sdl.Rect{X: panel.X + pad, Y: by, W: 110, H: btnH}, "Resume") {
				a.resumeTimer()
			}
		}
		if c.Button(sdl.Rect{X: panel.X + pad + 120, Y: by, W: 100, H: btnH}, "Reset") {
			a.resetTimer()
		}
	} else {
		// Idle: pick a duration via sliders + presets, then Start.
		mins := int32(a.timerSetSec / 60)
		secs := int32(a.timerSetSec % 60)
		ry := panel.Y + 86
		c.Label(panel.X+pad, ry+3, "Minutes", ColTextDim)
		nm := c.Slider("tmin", sdl.Rect{X: panel.X + pad + 70, Y: ry, W: 200, H: 18}, mins, 99)
		c.Label(panel.X+pad+278, ry+3, fmt.Sprintf("%d", nm), ColText)
		ry += 28
		c.Label(panel.X+pad, ry+3, "Seconds", ColTextDim)
		ns := c.Slider("tsec", sdl.Rect{X: panel.X + pad + 70, Y: ry, W: 200, H: 18}, secs, 59)
		c.Label(panel.X+pad+278, ry+3, fmt.Sprintf("%d", ns), ColText)
		if nm != mins || ns != secs {
			a.timerSetSec = int(nm)*60 + int(ns)
		}
		// Quick presets.
		ry += 28
		px := panel.X + pad
		for _, p := range [...]struct {
			label string
			sec   int
		}{{"1m", 60}, {"3m", 180}, {"5m", 300}, {"10m", 600}} {
			if c.Button(sdl.Rect{X: px, Y: ry, W: 56, H: btnH}, p.label) {
				a.timerSetSec = p.sec
			}
			px += 62
		}
		if c.Button(sdl.Rect{X: panel.X + pad, Y: by, W: 110, H: btnH}, "Start") {
			a.startTimer()
		}
	}
	// Repeat toggle (both states), persisted so it's remembered next session.
	if rep := c.Checkbox(panel.X+pad+240, by+4, "Repeat", a.timerRepeat); rep != a.timerRepeat {
		a.timerRepeat = rep
		a.d.Prefs.SetTimerRepeat(rep)
	}
}
