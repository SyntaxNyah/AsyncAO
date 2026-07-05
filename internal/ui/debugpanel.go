package ui

import (
	"fmt"
	"runtime"
	"time"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/cache"
)

// Debug panel (Extras → Debug): the comprehensive, interactive diagnostics view
// the user asked for — server software, session health, the live packet
// inspector (#333), performance + asset/prefetch stats, and the failure log, in
// one sectioned floating window (non-blocking, drag/resizable like the mod
// dashboard). It DELIBERATELY reuses the existing readouts rather than cloning
// them: the F3 perf HUD and the Settings "Debug overlay" stay as passive glances
// (frame graph / failure ring); this panel is the interactive superset. All the
// numbers are read on the render thread; the fmt draw here is a diagnostics path,
// so its allocations are accepted (same policy as the perf HUD / debug overlay)
// and never run while the panel is closed.

const (
	debugPanelW    = int32(580)
	debugPanelH    = int32(470)
	debugPanelMinW = int32(460)
	debugPanelMinH = int32(320)
	debugPanelIn   = int32(16)
	// debugRowH is one text row in the scrollable Packets / Log lists.
	debugRowH = int32(16)
)

// debugSections are the panel's tabs, indexed by a.debugSection.
var debugSections = []string{"Session", "Packets", "Perf", "Cache", "Log"}

// toggleDebugPanel opens / closes the Debug panel (Extras → Debug, the F8 key,
// or Settings → Power user → Diagnostics).
func (a *App) toggleDebugPanel() { a.showDebugPanel = !a.showDebugPanel }

// debugPanelRect is the Debug panel's floating-window rect (floatwin.go).
func (a *App) debugPanelRect(w, h int32) sdl.Rect {
	return a.debugWin.rect(debugPanelW, debugPanelH, debugPanelMinW, debugPanelMinH, w, h)
}

func (a *App) drawDebugPanel(w, h int32, pressed *bool) {
	c := a.ctx
	if c.escPressed {
		a.showDebugPanel = false
		return
	}
	panel := a.debugPanelRect(w, h)
	// Compositor census: the panel's live readouts (packet ages, ping, the
	// frame graph) repaint via an own-rect tick at diagTickBudget — this
	// records where that tick must clip (WalkNeeded reads it).
	a.drawnDebugPanelRect = panel
	pw, ph := panel.W, panel.H
	c.Fill(panel, ColPanel)
	c.Border(panel, ColAccent)
	c.Fill(sdl.Rect{X: panel.X, Y: panel.Y, W: pw, H: floatTitleH}, ColPanelHi) // title bar / drag handle
	a.floatWinDrag(&a.debugWin, sdl.Rect{X: panel.X, Y: panel.Y, W: pw - 84 - debugPanelIn, H: floatTitleH}, pressed)
	grip := sdl.Rect{X: panel.X + pw - floatGripSz, Y: panel.Y + ph - floatGripSz, W: floatGripSz, H: floatGripSz}
	a.floatWinResize(&a.debugWin, grip, panel, debugPanelMinW, debugPanelMinH, pressed)
	a.drawResizeGrip(grip)
	x := panel.X + debugPanelIn

	c.Heading(x, panel.Y+12, "Debug", ColText)
	if c.Button(sdl.Rect{X: panel.X + pw - debugPanelIn - 74, Y: panel.Y + 12, W: 74, H: btnH}, "Close") {
		a.showDebugPanel = false
		return
	}

	// Section switcher — the active tab is outlined.
	sy := panel.Y + 46
	bx := x
	for i, name := range debugSections {
		bw := c.TextWidth(name) + 22
		r := sdl.Rect{X: bx, Y: sy, W: bw, H: btnH}
		if c.Button(r, name) {
			a.debugSection = i
		}
		if a.debugSection == i {
			c.Border(r, ColAccent)
		}
		bx += bw + 8
	}

	top := sy + btnH + 10
	body := sdl.Rect{X: x, Y: top, W: pw - 2*debugPanelIn, H: panel.Y + ph - top - debugPanelIn}
	switch a.debugSection {
	case 1:
		a.drawDebugPackets(body)
	case 2:
		a.drawDebugPerf(body)
	case 3:
		a.drawDebugCache(body)
	case 4:
		a.drawDebugLogSection(body)
	default:
		a.drawDebugSession(body)
	}
}

// drawDebugSession shows the connection at a glance: server-software family (the
// mod dashboard keys its command syntax off this), the existing health + diag
// readouts, the live WebSocket ping, and the raw conn packet counters.
func (a *App) drawDebugSession(r sdl.Rect) {
	c := a.ctx
	y := r.Y
	line := func(s string, col sdl.Color) {
		c.LabelClipped(r.X, y, r.W, s, col)
		y += 19
	}
	if a.sess == nil {
		line("No session — you're in the lobby.", ColTextDim)
		return
	}
	sw := a.detectedSoftware()
	raw := a.sess.Software
	if raw == "" {
		raw = "(unannounced)"
	}
	line("Software: "+sw.String()+"   ["+raw+"]", ColAccent)
	line(a.debugHealthLine(), ColText) // phase · server · last pkt · log fill
	line(a.debugDiagLine(), ColText)   // tabs · area · queue · ic · ooc · goroutines

	rtt := a.pingRTT.Load()
	switch {
	case rtt > 0:
		line(fmt.Sprintf("Ping (WS round-trip): %d ms", rtt/int64(time.Millisecond)), ColText)
	case a.d.Prefs.PingChipOn():
		line("Ping: measuring…", ColTextDim)
	default:
		line("Ping: off (enable the connection chip in Settings to measure it)", ColTextDim)
	}
	if a.conn != nil {
		st := a.conn.Stats()
		line(fmt.Sprintf("Conn packets: %d sent · %d received", st.Sent, st.Received), ColTextDim)
	}
}

// drawDebugPackets renders the packet inspector (#333): the in/out totals for
// the active connection plus a scrollable, newest-first list of recent packets
// (direction, header, field count, approx wire size, age).
func (a *App) drawDebugPackets(r sdl.Rect) {
	c := a.ctx
	c.LabelClipped(r.X, r.Y, r.W, fmt.Sprintf("Active connection — %d in · %d out (%d total)",
		a.pkts.inTotal, a.pkts.outTotal, a.pkts.total), ColAccent)
	listR := sdl.Rect{X: r.X, Y: r.Y + 22, W: r.W, H: r.Y + r.H - (r.Y + 22)}
	c.Border(listR, ColPanelHi)

	a.debugPktBuf = a.pkts.recent(a.debugPktBuf, pktLogCap) // reused slice → alloc-free after warmup
	if len(a.debugPktBuf) == 0 {
		c.LabelClipped(listR.X+6, listR.Y+6, listR.W-12, "No packets recorded yet on this connection.", ColTextDim)
		return
	}
	if !c.ctrlHeld {
		a.debugPktScroll -= c.WheelIn(listR) * scrollStepPx
	}
	contentH := int32(len(a.debugPktBuf)) * debugRowH
	track := sdl.Rect{X: listR.X + listR.W - scrollBarW, Y: listR.Y, W: scrollBarW, H: listR.H}
	a.debugPktScroll = c.VScrollbar("debugpkts", track, a.debugPktScroll, contentH, listR.H)
	clipPrev, clipHad := c.pushClip(listR)
	defer c.popClip(clipPrev, clipHad)
	rowW := listR.W - scrollBarW - 12
	rowY := listR.Y - a.debugPktScroll
	now := a.now()
	for i := range a.debugPktBuf {
		if rowY > listR.Y+listR.H {
			break
		}
		if rowY >= listR.Y-debugRowH {
			rec := a.debugPktBuf[i]
			dir, col := "IN ", ColText
			if rec.out {
				dir, col = "OUT", ColAccent
			}
			c.LabelClipped(listR.X+6, rowY+1, rowW,
				fmt.Sprintf("%s  %-4s  %2df  %4dB   %5.1fs", dir, rec.hdr, rec.fields, rec.size, now.Sub(rec.at).Seconds()), col)
		}
		rowY += debugRowH
	}
}

// drawDebugPerf shows frame timing (avg / worst / fps + a compact bar graph) and
// the 1 Hz profiler's heap / GC / asset-cache numbers — the "performance" and
// "prefetches" the user asked for (cache hit rate, network probes, cached 404s).
func (a *App) drawDebugPerf(r sdl.Rect) {
	c := a.ctx
	avg, worst, fps := a.frameStats()
	y := r.Y
	c.LabelClipped(r.X, y, r.W, fmt.Sprintf("Frame: %.2f ms avg · %.1f ms worst · %.0f fps", avg, worst, fps), ColText)
	y += 22
	graph := sdl.Rect{X: r.X, Y: y, W: r.W, H: 46}
	c.Border(graph, ColPanelHi)
	a.drawFrameBars(graph)
	y += graph.H + 12

	if a.d.Profiler != nil {
		if s := a.d.Profiler.Latest(); s != nil {
			heapMiB := float64(s.HeapBytes) / (1 << 20)
			col := ColText
			if s.HeapBytes > perfBudgetBytes*3/4 {
				col = ColTierYellow
			}
			if s.HeapBytes > perfBudgetBytes {
				col = ColDanger
			}
			c.LabelClipped(r.X, y, r.W, fmt.Sprintf("Heap: %.1f / %d MiB   ·   GC pause p99: %s", heapMiB, perfBudgetBytes>>20, s.GCPauseP99), col)
			y += 19
			c.LabelClipped(r.X, y, r.W, fmt.Sprintf("Assets — cache hit %.0f%%   ·   network probes %d   ·   cached 404s %d",
				s.CacheHitRate*100, s.Probes, s.Cached404s), ColText)
			y += 19
		} else {
			c.LabelClipped(r.X, y, r.W, "Profiler warming up (first 1 Hz sample)…", ColTextDim)
			y += 19
		}
	}
	c.LabelClipped(r.X, y, r.W, fmt.Sprintf("Goroutines: %d", runtime.NumGoroutine()), ColTextDim)
	y += 19
	c.LabelClipped(r.X, y, r.W, fmt.Sprintf("Walks (drawnFps): %d · presents (presFps): %d — low over steady = compositor working", a.drawnFPS, a.presFPS), ColText)
	y += 22

	// The damage X-ray (damageoverlay.go): session-only toggle, like the
	// panel itself — a diagnostic, so deliberately no prefs plumbing.
	// Checkbox returns the NEW value (not "was clicked") — assign, never
	// `if Checkbox { flip }`: that re-flips on every un-clicked walk and
	// self-cancels the toggle within one diag tick (the test12 hotfix).
	a.dmgOvOn = c.Checkbox(r.X, y, "Show damage regions (selective rendering X-ray)", a.dmgOvOn)
	y += 22
	if a.dmgOvOn {
		c.LabelClipped(r.X, y, r.W, "red = full frame · blue stage · green chatbox · cyan log · yellow field · violet diag · amber hover · white = clip", ColTextDim)
	}
}

// drawDebugCache renders the three-tier asset cache inspector (#164): where
// assets are coming from this session (T1 / T2 / disk / network breakdown) plus
// each tier's live fill, hit rate and eviction/error counters. Every number is a
// cached atomic snapshot (no directory walk — DiskStats reads counters, not the
// disk), so it is safe to draw each frame the panel is open. Nil-guarded so a
// minimal (test) App with no pipeline wired still renders without panicking.
func (a *App) drawDebugCache(r sdl.Rect) {
	c := a.ctx
	y := r.Y
	line := func(s string, col sdl.Color) {
		c.LabelClipped(r.X, y, r.W, s, col)
		y += 19
	}

	// Source breakdown: which tier served each demand this session (the streaming
	// win is a high T1/T2/disk share and a low network count).
	if a.d.Manager != nil {
		ms := a.d.Manager.Stats()
		line("Asset sources this session:", ColAccent)
		line(fmt.Sprintf("  T1 %d · T2 %d · disk %d · network %d · missing %d",
			ms.T1Hits, ms.T2Hits, ms.DiskHits, ms.NetFetches, ms.Missing), ColText)
	}
	// T1 — decoded textures (render side).
	if a.d.Store != nil {
		line("T1 decoded textures — "+memTierLine(a.d.Store.Stats()), ColText)
	}
	// T2 raw bytes + T3 on-disk cache (both owned by the asset manager).
	if a.d.Manager != nil {
		line("T2 raw bytes — "+memTierLine(a.d.Manager.T2Stats()), ColText)
		d := a.d.Manager.DiskStats()
		line(fmt.Sprintf("T3 disk cache — hit %d · miss %d · writes %d · dropped %d · errors %d",
			d.Hits, d.Misses, d.Writes, d.Dropped, d.WriteErrors), ColText)
	}
	// Resolver — learned per-host formats (the zero-fallback streaming premise).
	if a.d.Resolver != nil {
		rs := a.d.Resolver.Stats()
		line(fmt.Sprintf("Learned formats — hit %d · miss %d (%s)",
			rs.LearnedHits, rs.LearnedMisses, hitRatePct(rs.LearnedHits, rs.LearnedMisses)), ColTextDim)
	}
	// Network client — total probes + cached 404s (never re-probed inside TTL).
	if a.d.Client != nil {
		ns := a.d.Client.Stats()
		line(fmt.Sprintf("Network — %d requests · %d cached 404s", ns.Requests, ns.Cached404s), ColTextDim)
	}
}

// memTierLine formats a byte-budgeted memory tier (T1/T2) for the cache
// inspector: entry count, MiB used vs budget, hit rate and evictions.
func memTierLine(s cache.MemoryStats) string {
	return fmt.Sprintf("%d items · %.1f/%d MiB · hit %s · evict %d",
		s.Entries, float64(s.Bytes)/(1<<20), s.Budget>>20, hitRatePct(s.Hits, s.Misses), s.Evictions)
}

// hitRatePct renders hits/(hits+misses) as a percentage, or "n/a" before any
// traffic (so an idle tier doesn't read as a 0% miss storm).
func hitRatePct(hits, misses int64) string {
	total := hits + misses
	if total <= 0 {
		return "n/a"
	}
	return fmt.Sprintf("%.0f%%", float64(hits)*100/float64(total))
}

// drawFrameBars draws the frame-time bar graph into r (oldest → newest, green
// under the 60 fps budget, amber to the 33 ms scale, red above). Shares the perf
// HUD's ring + thresholds so the two views can't disagree.
func (a *App) drawFrameBars(r sdl.Rect) {
	c := a.ctx
	barW := r.W / int32(perfHUDFrames)
	if barW < 1 {
		barW = 1
	}
	for i := 0; i < perfHUDFrames; i++ {
		dt := a.frameDts[(a.frameDtIdx+i)%perfHUDFrames]
		hPx := int32(float32(r.H) * dt / perfHUDScaleMs)
		if hPx > r.H {
			hPx = r.H
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
		c.Fill(sdl.Rect{X: r.X + int32(i)*barW, Y: r.Y + r.H - hPx, W: barW, H: hPx}, col)
	}
}

// frameStats reduces the frame-time ring to avg / worst / fps (shared by the
// perf section; the F3 HUD computes the same numbers inline in its graph loop).
func (a *App) frameStats() (avg, worst, fps float32) {
	var sum float32
	for i := 0; i < perfHUDFrames; i++ {
		dt := a.frameDts[i]
		sum += dt
		if dt > worst {
			worst = dt
		}
	}
	avg = sum / perfHUDFrames
	if avg > 0 {
		fps = 1000 / avg
	}
	return
}

// drawDebugLogSection renders the bounded failure-log ring (newest first) —
// the same lines the Settings "Debug overlay" shows, hosted here so the panel is
// a one-stop view.
func (a *App) drawDebugLogSection(r sdl.Rect) {
	c := a.ctx
	c.Border(r, ColPanelHi)
	if len(a.debugLog) == 0 {
		c.LabelClipped(r.X+6, r.Y+6, r.W-12, "No failures logged this session.", ColTextDim)
		return
	}
	if !c.ctrlHeld {
		a.debugLogScroll -= c.WheelIn(r) * scrollStepPx
	}
	contentH := int32(len(a.debugLog)) * debugRowH
	track := sdl.Rect{X: r.X + r.W - scrollBarW, Y: r.Y, W: scrollBarW, H: r.H}
	a.debugLogScroll = c.VScrollbar("debuglog", track, a.debugLogScroll, contentH, r.H)
	clipPrev, clipHad := c.pushClip(r)
	defer c.popClip(clipPrev, clipHad)
	rowW := r.W - scrollBarW - 12
	rowY := r.Y - a.debugLogScroll
	for i := len(a.debugLog) - 1; i >= 0; i-- { // newest first
		if rowY > r.Y+r.H {
			break
		}
		if rowY >= r.Y-debugRowH {
			c.LabelClipped(r.X+6, rowY+1, rowW, a.debugLog[i], ColTextDim)
		}
		rowY += debugRowH
	}
}
