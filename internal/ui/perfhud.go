package ui

// Perf HUD (F3, any screen): live frame-time graph plus the 1 Hz
// profiler's numbers — heap vs the 256 MiB budget, GC pause p99, cache
// hit rate, network probes, cached 404s. The sampler already existed
// (internal/metrics, --debug logged it); this renders it. Diagnostics
// path: per-frame allocations here are acceptable, same policy as the
// debug overlay.

import (
	"fmt"

	"github.com/veandco/go-sdl2/sdl"
)

const (
	// perfHUDFrames is the frame-time ring (≈2 s at 60 fps).
	perfHUDFrames = 120
	// perfHUDBarW is one frame's bar width in the graph.
	perfHUDBarW = 2
	// perfHUDGraphH is the graph height; perfHUDScaleMs is the dt that
	// reaches the top (33 ms ≈ a 30 fps frame — anything taller clips).
	perfHUDGraphH  = 48
	perfHUDScaleMs = 33.0
	// perfBudgetBytes mirrors main's GOMEMLIMIT budget (spec §13): the
	// HUD shows heap pressure against it.
	perfBudgetBytes = 256 << 20
	// frameBudgetMs is the 60 fps line drawn across the graph.
	frameBudgetMs = 16.7
)

// recordFrameDt feeds the ring (called every Frame with the real dt).
func (a *App) recordFrameDt(dtMs float32) {
	a.frameDts[a.frameDtIdx] = dtMs
	a.frameDtIdx = (a.frameDtIdx + 1) % perfHUDFrames
}

// drawPerfHUD renders the overlay (top-left, under the tab strip).
func (a *App) drawPerfHUD(w, h int32) {
	c := a.ctx
	const x0, y0 = 8, 30
	graphW := int32(perfHUDFrames * perfHUDBarW)
	panel := sdl.Rect{X: x0 - 4, Y: y0 - 4, W: graphW + 8, H: perfHUDGraphH + 96}
	c.Fill(panel, sdl.Color{R: 0, G: 0, B: 0, A: 200})
	c.Border(panel, ColPanelHi)

	// Frame-time graph: oldest → newest left to right; green under the
	// 60 fps budget, amber to 33 ms, red above.
	var sum, worst float32
	for i := 0; i < perfHUDFrames; i++ {
		dt := a.frameDts[(a.frameDtIdx+i)%perfHUDFrames]
		sum += dt
		if dt > worst {
			worst = dt
		}
		hPx := int32(float32(perfHUDGraphH) * dt / perfHUDScaleMs)
		if hPx > perfHUDGraphH {
			hPx = perfHUDGraphH
		}
		if hPx < 1 {
			hPx = 1
		}
		col := ColTierGreen
		switch {
		case dt > perfHUDScaleMs:
			col = ColDanger
		case dt > frameBudgetMs:
			col = ColTierYellow
		}
		c.Fill(sdl.Rect{X: x0 + int32(i*perfHUDBarW), Y: y0 + perfHUDGraphH - hPx, W: perfHUDBarW, H: hPx}, col)
	}
	// The 60 fps line.
	budgetFrac := float32(frameBudgetMs) / float32(perfHUDScaleMs) // variable: constant conversion would be lossy
	lineY := y0 + perfHUDGraphH - int32(float32(perfHUDGraphH)*budgetFrac)
	c.Fill(sdl.Rect{X: x0, Y: lineY, W: graphW, H: 1}, ColTextDim)

	avg := sum / perfHUDFrames
	ty := int32(y0 + perfHUDGraphH + 6)
	fps := float32(0)
	if avg > 0 {
		fps = 1000 / avg
	}
	c.Label(x0, ty, fmt.Sprintf("frame %5.2fms avg (%4.0f fps)  worst %5.1fms", avg, fps, worst), ColText)
	ty += 18

	// The 1 Hz sampler's numbers (nil before its first tick / in tests).
	if a.d.Profiler != nil {
		if s := a.d.Profiler.Latest(); s != nil {
			heapMiB := float64(s.HeapBytes) / (1 << 20)
			budMiB := float64(perfBudgetBytes) / (1 << 20)
			heapCol := ColText
			if s.HeapBytes > perfBudgetBytes*3/4 {
				heapCol = ColTierYellow
			}
			if s.HeapBytes > perfBudgetBytes {
				heapCol = ColDanger
			}
			c.Label(x0, ty, fmt.Sprintf("heap %5.1f / %.0f MiB   gc p99 %s", heapMiB, budMiB, s.GCPauseP99), heapCol)
			ty += 18
			c.Label(x0, ty, fmt.Sprintf("cache hit %3.0f%%   probes %d   cached 404s %d",
				s.CacheHitRate*100, s.Probes, s.Cached404s), ColText)
			ty += 18
		}
	}
	c.Label(x0, ty, "F3 hides", ColTextDim)
}
