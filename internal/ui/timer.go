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

// Timer panel geometry. The old modal was a fixed 400×270 centred block; as a
// floatWin it keeps that as its default size and can be resized down to the
// controls' own footprint.
const (
	timerPanelDefW = int32(400)
	timerPanelDefH = int32(300)
	timerPanelMinW = int32(320) // the four 62px-pitch preset buttons still fit at this width
	timerPanelMinH = int32(266) // readout + both sliders + presets + Repeat + the transport row
	// timerValueColW reserves the right-hand column the slider's numeric readout is
	// drawn in, so the responsive track width below can never run under it.
	timerValueColW = int32(62)
)

// timerPanelRect is the panel's screen rect, seeded from its persisted layout slot
// on first open and clamped on-screen thereafter (the modcallPanelRect idiom).
func (a *App) timerPanelRect(w, h int32) sdl.Rect {
	if r, ok := a.seedPanelFromSlot(&a.timerWin, slotPanelTimer, timerPanelDefW, timerPanelDefH, timerPanelMinW, timerPanelMinH, w, h); ok {
		return r
	}
	return a.timerWin.rect(timerPanelDefW, timerPanelDefH, timerPanelMinW, timerPanelMinH, w, h)
}

// drawTimerPanel is the countdown's setup surface: a large remaining readout,
// Minutes/Seconds sliders + quick presets while idle, and Start/Pause/Resume/Reset
// + Repeat. Opened from the Extras "Timer" entry.
//
// It was a return-to-top modal, and that is issue #31: opening a personal
// countdown blanked the courtroom, the logs, the music list and every button —
// drawCourtroomModals ends the pass, so nothing under it draws at all. A timer is
// the LAST thing that should take the screen; you set it precisely so you can
// watch the scene against it. It is a non-blocking floatWin now (drag the title
// bar, resize the bottom-right grip) like the Evidence and Call Mod boxes.
func (a *App) drawTimerPanel(w, h int32, pressed *bool) {
	c := a.ctx
	wasActive := a.timerWin.dragging || a.timerWin.resizing // detect the drag/resize-end frame for slot persistence
	panel := a.timerPanelRect(w, h)
	c.Fill(panel, ColPanel)
	c.Border(panel, ColAccent)
	c.Fill(sdl.Rect{X: panel.X, Y: panel.Y, W: panel.W, H: floatTitleH}, ColPanelHi)
	c.Heading(panel.X+pad, panel.Y+6, "Timer", ColText)
	closeB := sdl.Rect{X: panel.X + panel.W - 30, Y: panel.Y + 3, W: 24, H: btnH}
	if c.Button(closeB, "X") {
		a.showTimer = false
		return
	}
	a.floatWinDrag(&a.timerWin, sdl.Rect{X: panel.X, Y: panel.Y, W: closeB.X - panel.X - 4, H: floatTitleH}, pressed)
	grip := sdl.Rect{X: panel.X + panel.W - floatGripSz, Y: panel.Y + panel.H - floatGripSz, W: floatGripSz, H: floatGripSz}
	a.floatWinResize(&a.timerWin, grip, panel, timerPanelMinW, timerPanelMinH, pressed)
	a.drawResizeGrip(grip)
	if wasActive && !a.timerWin.dragging && !a.timerWin.resizing { // drag/resize just ended → remember where
		a.persistPanelSlot(slotPanelTimer, panel, w, h)
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
		// The tracks follow the panel width now that it is resizable — a fixed 200 px
		// track with its value label pinned 278 px in would have run straight out of a
		// panel dragged down to timerPanelMinW.
		mins := int32(a.timerSetSec / 60)
		secs := int32(a.timerSetSec % 60)
		trackX := panel.X + pad + 70
		trackW := panel.W - pad - 70 - timerValueColW
		ry := panel.Y + 86
		c.Label(panel.X+pad, ry+3, "Minutes", ColTextDim)
		nm := c.Slider("tmin", sdl.Rect{X: trackX, Y: ry, W: trackW, H: 18}, mins, 99)
		c.Label(trackX+trackW+8, ry+3, fmt.Sprintf("%d", nm), ColText)
		ry += 28
		c.Label(panel.X+pad, ry+3, "Seconds", ColTextDim)
		ns := c.Slider("tsec", sdl.Rect{X: trackX, Y: ry, W: trackW, H: 18}, secs, 59)
		c.Label(trackX+trackW+8, ry+3, fmt.Sprintf("%d", ns), ColText)
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
	// Repeat toggle (both states), persisted so it's remembered next session. Sits on
	// its own row ABOVE the transport buttons rather than beside them: on the bottom
	// row it collided with the resize grip the moment the panel was dragged narrow.
	if rep := c.Checkbox(panel.X+pad, by-24, "Repeat", a.timerRepeat); rep != a.timerRepeat {
		a.timerRepeat = rep
		a.d.Prefs.SetTimerRepeat(rep)
	}
}
