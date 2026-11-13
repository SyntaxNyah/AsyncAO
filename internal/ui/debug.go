package ui

import (
	"fmt"
	"runtime"
	"strconv"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

// drawDebugOverlay paints the bounded failure log plus a one-line session health readout over the active screen.
// It answers "what failed?" (the ring) and "is the server misbehaving?" (phase stuck before ready with a rising
// last-packet age). It is opt-in, so its fmt allocations are accepted and never run while the toggle is off.
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
	// Header and lines anchor bottom-left; 55% width leaves the right column readable.
	panelH := int32(len(lines)+3)*lineH + 2*panelPad // 3 header lines (health + diag + cold-load)
	panel := sdl.Rect{X: 0, Y: h - panelH, W: w * 55 / 100, H: panelH}
	c.Fill(panel, sdl.Color{R: 0, G: 0, B: 0, A: 200})
	y := panel.Y + panelPad
	c.LabelClipped(panel.X+6, y, panel.W-12, a.debugHealthLine(), ColAccent)
	y += lineH
	c.LabelClipped(panel.X+6, y, panel.W-12, a.debugDiagLine(), ColAccent)
	y += lineH
	c.LabelClipped(panel.X+6, y, panel.W-12, a.debugColdLoadLine(), ColAccent)
	y += lineH
	for _, e := range lines {
		c.LabelClipped(panel.X+6, y, panel.W-12, e.text, ColTextDim)
		y += lineH
	}
}

// debugHealthLine summarizes the live session: handshake phase, announced server software, and the age of the
// last incoming packet.
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
	wire := "fanta"
	if a.conn != nil && a.conn.JSONMode() {
		wire = "json"
	}
	return fmt.Sprintf("debug · phase %s · server %s · wire %s · last pkt %s · log %d/%d",
		a.sess.Phase(), software, wire, age, len(a.debugLog), debugLogCap)
}

// debugDiagLine is the second readout: structural counts that reveal leaks and stuck state (open tabs, area, IC
// play-queue depth, scrollback sizes, goroutine count). Cheap fields only.
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

// debugColdLoadLine is the cold-load readout: per-stage averages of an uncached asset (network fetch, decode and
// downscale, GPU upload); the largest is the bottleneck. A stage with no samples reads "â€”".
func (a *App) debugColdLoadLine() string {
	fetch, decode := a.d.Manager.ColdLoadStats()
	up := a.d.Store.AvgUpload()
	ms := func(d time.Duration) string {
		if d <= 0 {
			return "—"
		}
		return strconv.FormatFloat(float64(d)/float64(time.Millisecond), 'f', 1, 64) + "ms"
	}
	return "cold-load · fetch " + ms(fetch) + " · decode " + ms(decode) + " · upload " + ms(up) +
		"  (EWMAs; the big one is the bottleneck)"
}
