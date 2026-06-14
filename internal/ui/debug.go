package ui

import (
	"fmt"
	"runtime"
	"time"

	"github.com/veandco/go-sdl2/sdl"
)

// drawDebugOverlay paints the bounded failure log plus a one-line session
// health readout over whatever screen is active (Settings → "Debug
// overlay"). It exists to answer two questions without a debugger attached:
// "what failed?" (the ring: missing assets, theme misses, dropped/unknown
// packets, disconnect reasons) and "is the server itself misbehaving?"
// (phase stuck before ready + a rising last-packet age = server-side hang).
//
// This is an opt-in diagnostics path: the fmt allocations here are accepted
// and never run while the toggle is off (the zero-alloc render gates bench
// with it off).
func (a *App) drawDebugOverlay(w, h int32) {
	c := a.ctx
	const (
		lineH    = 16 // matches the small UI font row height
		panelPad = 4
	)
	lines := a.debugLog
	if len(lines) > debugOverlayLines {
		lines = lines[len(lines)-debugOverlayLines:]
	}
	// Header + lines, anchored bottom-left; 55% width leaves the right
	// column (logs/music) readable underneath.
	panelH := int32(len(lines)+2)*lineH + 2*panelPad // 2 header lines (health + diag)
	panel := sdl.Rect{X: 0, Y: h - panelH, W: w * 55 / 100, H: panelH}
	c.Fill(panel, sdl.Color{R: 0, G: 0, B: 0, A: 200})
	y := panel.Y + panelPad
	c.LabelClipped(panel.X+6, y, panel.W-12, a.debugHealthLine(), ColAccent)
	y += lineH
	c.LabelClipped(panel.X+6, y, panel.W-12, a.debugDiagLine(), ColAccent)
	y += lineH
	for _, ln := range lines {
		c.LabelClipped(panel.X+6, y, panel.W-12, ln, ColTextDim)
		y += lineH
	}
}

// debugHealthLine summarizes the live session: handshake phase, the server
// software string it announced, and how stale the incoming packet stream
// is. A buggy/hung server shows up as the phase stuck before "ready" with
// the last-packet age climbing.
func (a *App) debugHealthLine() string {
	if a.sess == nil {
		return "debug · no session (lobby) · log " +
			fmt.Sprintf("%d/%d", len(a.debugLog), debugLogCap)
	}
	age := "none yet"
	if !a.lastPktAt.IsZero() {
		age = fmt.Sprintf("%q %.0fs ago", a.lastPktHdr, time.Since(a.lastPktAt).Seconds())
	}
	software := a.sess.Software
	if software == "" {
		software = "(unannounced)"
	}
	return fmt.Sprintf("debug · phase %s · server %s · last pkt %s · log %d/%d",
		a.sess.Phase(), software, age, len(a.debugLog), debugLogCap)
}

// debugDiagLine is the second readout: live structural counts that catch leaks
// and stuck state at a glance — open tabs, the area we're in, the IC play-queue
// depth (a backlog that won't drain), the IC/OOC scrollback sizes, and the
// goroutine count (a steady climb is the classic leak tell). Cheap fields only;
// computed solely while the opt-in overlay is open.
func (a *App) debugDiagLine() string {
	area := a.curArea
	if area == "" {
		area = "—"
	}
	queue := 0
	if a.room != nil {
		queue = a.room.QueueLen()
	}
	return fmt.Sprintf("diag · tabs %d · area %s · queue %d · ic %d · ooc %d · goroutines %d",
		len(a.tabs), area, queue, len(a.icLog), len(a.oocLog), runtime.NumGoroutine())
}
