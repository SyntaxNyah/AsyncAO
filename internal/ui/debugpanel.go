package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/cache"
	"github.com/SyntaxNyah/AsyncAO/internal/helper/value"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

// Debug panel (Extras > Debug): a sectioned, draggable floating diagnostics window. It reuses the existing readouts
// rather than cloning them; its fmt allocations are accepted because it draws only while open.

const (
	debugPanelW    = int32(580)
	debugPanelH    = int32(470)
	debugPanelMinW = int32(460)
	debugPanelMinH = int32(320)
	debugPanelIn   = int32(16)
	// debugRowH is one text row in the scrollable Packets / Log lists.
	debugRowH = int32(16)
	// debugLineH is one text row in the flowed sections (Session, Perf, Cache). Separate from debugRowH,
	// which is the packet list's hit-test and scroll pitch.
	debugLineH = int32(19)
	// debugSectionGap is the step after a section heading.
	debugSectionGap = int32(22)
)

// debugSections are the panel's tabs, indexed by a.debugSection.
var debugSections = []string{"Session", "Packets", "Perf", "Cache", "Log", "Fonts"}

// toggleDebugPanel opens / closes the Debug panel (Extras → Debug, the F8 key,
// or Settings → Power user → Diagnostics).
func (a *App) toggleDebugPanel() { a.showDebugPanel = !a.showDebugPanel }

// debugPanelRect is the Debug panel's floating-window rect (floatwin.go).
func (a *App) debugPanelRect(w, h int32) sdl.Rect {
	if r, ok := a.seedPanelFromSlot(&a.debugWin, slotPanelDebug, debugPanelW, debugPanelH, debugPanelMinW, debugPanelMinH, w, h); ok {
		return r
	}
	return a.debugWin.rect(debugPanelW, debugPanelH, debugPanelMinW, debugPanelMinH, w, h)
}

func (a *App) drawDebugPanel(w, h int32, pressed *bool) {
	c := a.ctx
	if c.escPressed {
		a.showDebugPanel = false
		return
	}
	wasActive := a.debugWin.dragging || a.debugWin.resizing // detect the drag/resize-end frame for slot persistence
	panel := a.debugPanelRect(w, h)
	pw, ph := panel.W, panel.H
	c.Fill(panel, ColPanel)
	c.Border(panel, ColAccent)
	c.Fill(sdl.Rect{X: panel.X, Y: panel.Y, W: pw, H: floatTitleH}, ColPanelHi) // title bar / drag handle
	a.floatWinDrag(&a.debugWin, sdl.Rect{X: panel.X, Y: panel.Y, W: pw - 84 - debugPanelIn, H: floatTitleH}, pressed)
	grip := sdl.Rect{X: panel.X + pw - floatGripSz, Y: panel.Y + ph - floatGripSz, W: floatGripSz, H: floatGripSz}
	a.floatWinResize(&a.debugWin, grip, panel, debugPanelMinW, debugPanelMinH, pressed)
	a.drawResizeGrip(grip)
	if wasActive && !a.debugWin.dragging && !a.debugWin.resizing { // drag/resize just ended → remember where
		a.persistPanelSlot(slotPanelDebug, panel, w, h)
	}
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
	case 5:
		a.drawDebugFonts(body)
	default:
		a.drawDebugSession(body)
	}
}

// drawDebugSession shows server software, health lines, ping, packet counters and chatbox geometry.
func (a *App) drawDebugSession(r sdl.Rect) {
	c := a.ctx
	y := r.Y
	line := func(s string, col sdl.Color) {
		c.LabelClipped(r.X, y, r.W, s, col)
		y += debugLineH
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
	line(a.debugHealthLine(), ColText) // phase · server · wire · last pkt · log fill
	line(a.debugDiagLine(), ColText)   // tabs · area · queue · ic · ooc · goroutines

	rtt := a.pingRTT.Load()
	switch {
	case rtt > 0:
		line(fmt.Sprintf("Ping (WS round-trip): %d ms", rtt/int64(time.Millisecond)), ColText)
	case a.d.Prefs.Load().PingChip:
		line("Ping: measuring…", ColTextDim)
	default:
		line("Ping: off (enable the connection chip in Settings to measure it)", ColTextDim)
	}
	if a.conn != nil {
		st := a.conn.Stats()
		line(fmt.Sprintf("Conn packets: %d sent · %d received", st.Sent, st.Received), ColTextDim)
	}
	// Chatbox geometry: "fits" compares the room under the showname strip with the wrapped text's height.
	// CUT means the box is too short; "fits" with text still missing means the fault is in drawing.
	d := &a.chatDiag
	need := d.lines * d.lineH
	room := d.boxY + d.boxH - d.textY
	grew := ""
	if d.grew {
		grew = " GREW"
	}
	line(fmt.Sprintf("Chatbox: vp.H=%d box=(y%d h%d)%s text.y=%d wrap=%d",
		d.vpH, d.boxY, d.boxH, grew, d.textY, d.wrapW), ColTextDim)
	verdict, col := "fits", ColTierGreen
	if need > room {
		verdict, col = fmt.Sprintf("CUT by %dpx", need-room), ColAccent
	}
	line(fmt.Sprintf("  text: %d lines x %dpx = %d need - %d room -> %s",
		d.lines, d.lineH, need, room, verdict), col)
	line(fmt.Sprintf("  scale: render=%d%% fontDev=%d%% - revealed %d/%d runes",
		d.renderPct, d.devScale, d.visible, d.total), ColTextDim)
}

// drawDebugPackets renders in/out totals and a scrollable newest-first list of recent packets.
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

// drawDebugPerf shows frame timing with a bar graph and the 1 Hz profiler's heap, GC and asset-cache numbers.
func (a *App) drawDebugPerf(r sdl.Rect) {
	c := a.ctx
	avg, worst, fps := a.frameStats()
	y := r.Y
	c.LabelClipped(r.X, y, r.W, fmt.Sprintf("Frame: %.2f ms avg · %.1f ms worst · %.0f fps", avg, worst, fps), ColText)
	y += debugSectionGap
	graph := sdl.Rect{X: r.X, Y: y, W: r.W, H: 46}
	c.Border(graph, ColPanelHi)
	a.drawFrameBars(graph)
	y += graph.H + 12

	if a.d.Profiler != nil {
		if s := a.d.Profiler.Latest(); s != nil {
			heapMiB := float64(s.HeapBytes) / (1 << 20)
			c.LabelClipped(r.X, y, r.W, fmt.Sprintf("Heap: %.1f MiB   ·   GC pause p99: %s", heapMiB, s.GCPauseP99), ColText)
			y += debugLineH
			c.LabelClipped(r.X, y, r.W, fmt.Sprintf("Assets — cache hit %.0f%%   ·   network probes %d   ·   cached 404s %d",
				s.CacheHitRate*100, s.Probes, s.Cached404s), ColText)
			y += debugLineH
		} else {
			c.LabelClipped(r.X, y, r.W, "Profiler warming up (first 1 Hz sample)…", ColTextDim)
			y += debugLineH
		}
	}
	c.LabelClipped(r.X, y, r.W, fmt.Sprintf("Goroutines: %d", runtime.NumGoroutine()), ColTextDim)
	y += debugLineH

	// Pacer: the last FramePace tier and why. focusSuffix reads focus now while the tier was stamped last frame,
	// so a focus flip can briefly show a mismatched pair.
	c.LabelClipped(r.X, y, r.W, "Pacer: "+pacerTierLabel(a.lastPacerTier)+focusSuffix(a.ctx.WindowFocused()), ColTextDim)
	y += debugLineH

	// Pump: GPU upload errors and transient network errors; rising uploads mean the texture budget is over-subscribed.
	if a.d.Pump != nil {
		ps := a.d.Pump.Stats()
		col := ColTextDim
		if ps.UploadErrs > 0 {
			col = ColTierYellow
		}
		c.LabelClipped(r.X, y, r.W, fmt.Sprintf("Pump: %d upload errors · %d transient (network) errors", ps.UploadErrs, ps.TransientErrs), col)
		y += debugLineH
	}

	// Held-frame bridge: steals, releases and stolen stage frames pinned now.
	// A Current stuck at the cap means the heal isn't landing the real pages.
	if a.d.Store != nil {
		hs := a.d.Store.HeldStats()
		c.LabelClipped(r.X, y, r.W, fmt.Sprintf("Held-frame bridge: %d steals · %d releases · %d held now", hs.Steals, hs.Releases, hs.Current), ColTextDim)
		y += debugLineH
		// Theme media: user-art bytes spent and remaining, and elements turned into placeholders. Pinned pages are
		// eviction-exempt, so an accounting bug would otherwise show only as a heavier client.
		col := ColTextDim
		if themeArtOverWarnPct(a.d.Store.ThemeMediaBytes(), render.ThemeMediaByteCap(a.d.Prefs.Load().TexBudgetMiB())) {
			col = ColTierYellow
		}
		c.LabelClipped(r.X, y, r.W, a.themeMediaDebugLine(), col)
		y += debugLineH
	}
	// Element clocks and effects: a theme with live elements holds the frame rate at the animation cadence on purpose,
	// and past themeFXCap a stateful effect freezes at its first frame.
	fxCol := ColTextDim
	if a.themeFXOver {
		fxCol = ColTierYellow
	}
	c.LabelClipped(r.X, y, r.W, a.themeFXDebugLine(), fxCol)
}

// themeFXDebugLine is the panel's `theme fx` row.
func (a *App) themeFXDebugLine() string {
	state := "live"
	if a.themeFXFrozen {
		state = "frozen (reduce motion)"
	}
	over := ""
	if a.themeFXOver {
		over = " · POOL FULL (further stateful effects freeze at frame 0)"
	}
	return fmt.Sprintf("theme fx %d / %d slots · %d elements · %s%s",
		a.themeFXInUse(), themeFXCap, a.themeLay.elN, state, over)
}

// focusSuffix annotates the pacer line with the window's focus state (the
// unfocused tier is only reachable when the window is tabbed away).
func focusSuffix(focused bool) string {
	if focused {
		return "   [focused]"
	}
	return "   [unfocused]"
}

// drawDebugCache renders where assets came from this session and each tier's fill, hit rate and counters.
// All numbers are atomic snapshots (DiskStats reads counters, not the disk); nil-guarded for test Apps.
func (a *App) drawDebugCache(r sdl.Rect) {
	c := a.ctx
	y := r.Y
	line := func(s string, col sdl.Color) {
		c.LabelClipped(r.X, y, r.W, s, col)
		y += debugLineH
	}

	// Source breakdown: which tier served each demand this session.
	if a.d.Manager != nil {
		ms := a.d.Manager.Stats()
		line("Asset sources this session:", ColAccent)
		// pack is its own column so a local-mount hit isn't mistaken for a network fetch.
		line(fmt.Sprintf("  T1 %d · T2 %d · disk %d · pack %d · network %d · missing %d",
			ms.T1Hits, ms.T2Hits, ms.DiskHits, ms.MountFetches, ms.NetFetches, ms.Missing), ColText)
		if ms.PackQuarantined > 0 {
			line(fmt.Sprintf("  pack files withdrawn after a decode failure: %d", ms.PackQuarantined), ColTierYellow)
		}
	}
	// T1 — decoded textures (render side).
	if a.d.Store != nil {
		line("T1 decoded textures — "+memTierLine(a.d.Store.Stats()), ColText)
		if ob := a.d.Store.OversizedBytes(); ob > 0 {
			line(fmt.Sprintf("  oversized animations — %.1f MiB (full, above the T1 budget)", float64(ob)/(1<<20)), ColTierYellow)
		}
	}
	// Speaker sprite animation: kept/source frame count and delay stats.
	if a.d.Viewport != nil {
		if si, ok := a.d.Viewport.SpeakerAnimInfo(); ok {
			line(fmt.Sprintf("Speaker anim — %s · %d/%d frames · %d/%d/%d ms (min/avg/max)",
				si.Base, si.Kept, si.Source,
				si.MinDelay/time.Millisecond, si.AvgDelay/time.Millisecond, si.MaxDelay/time.Millisecond), ColText)
		}
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

// drawFrameBars draws the frame-time bars oldest to newest, sharing the perf HUD's ring and thresholds.
func (a *App) drawFrameBars(r sdl.Rect) {
	c := a.ctx
	barW := r.W / int32(perfHUDFrames)
	if barW < 1 {
		barW = 1
	}
	for i := 0; i < perfHUDFrames; i++ {
		dt := a.frameDts[(a.frameDtIdx+i)%perfHUDFrames]
		hPx := int32(float32(r.H) * dt / perfHUDScaleMs)
		hPx = value.Clamp(hPx, 1, r.H)
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

// frameStats reduces the frame-time ring to avg, worst and fps.
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

// drawDebugLogSection renders the bounded failure-log ring, newest first.
func (a *App) drawDebugLogSection(r sdl.Rect) {
	c := a.ctx
	btnR := sdl.Rect{X: r.X + r.W - 100, Y: r.Y, W: 90, H: btnH}
	if c.Button(btnR, "Export Log") {
		a.exportDebugLog()
	}
	logR := sdl.Rect{X: r.X, Y: r.Y + btnH + 4, W: r.W, H: r.H - btnH - 4}
	c.Border(logR, ColPanelHi)
	if len(a.debugLog) == 0 {
		c.LabelClipped(logR.X+6, logR.Y+6, logR.W-12, "No failures logged this session.", ColTextDim)
		return
	}
	if !c.ctrlHeld {
		a.debugLogScroll -= c.WheelIn(logR) * scrollStepPx
	}
	contentH := int32(len(a.debugLog)) * debugRowH
	track := sdl.Rect{X: logR.X + logR.W - scrollBarW, Y: logR.Y, W: scrollBarW, H: logR.H}
	a.debugLogScroll = c.VScrollbar("debuglog", track, a.debugLogScroll, contentH, logR.H)
	clipPrev, clipHad := c.pushClip(logR)
	defer c.popClip(clipPrev, clipHad)
	rowW := logR.W - scrollBarW - 12
	rowY := logR.Y - a.debugLogScroll
	for i := len(a.debugLog) - 1; i >= 0; i-- { // newest first
		if rowY > logR.Y+logR.H {
			break
		}
		if rowY >= logR.Y-debugRowH {
			c.LabelClipped(logR.X+6, rowY+1, rowW, a.debugLog[i].text, ColTextDim)
		}
		rowY += debugRowH
	}
}

// exportDebugLog writes the debug log to a timestamped .txt file on the desktop.
func (a *App) exportDebugLog() {
	if len(a.debugLog) == 0 {
		return
	}

	now := time.Now()
	filename := fmt.Sprintf("asyncao-debug-%s.txt", now.Format("20060102-150405"))

	// Try Desktop first, fall back to current directory
	desktop := filepath.Join(os.Getenv("USERPROFILE"), "Desktop")
	path := filepath.Join(desktop, filename)
	if err := os.MkdirAll(desktop, 0755); err != nil {
		path = filename // fallback to current directory
	}

	var content string
	content += "AsyncAO Debug Log Export\n"
	content += fmt.Sprintf("Generated: %s\n", now.Format("2006-01-02 15:04:05"))
	content += fmt.Sprintf("Entries: %d\n", len(a.debugLog))
	content += fmt.Sprintf("\n%s\n\n", "============================================================")

	// Add all log entries (oldest first for readability)
	for i := 0; i < len(a.debugLog); i++ {
		e := a.debugLog[i]
		content += fmt.Sprintf("[%s] %s\n", e.at.Format("15:04:05"), e.text)
	}

	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		a.pushDebug("Failed to export debug log: " + err.Error())
	} else {
		a.pushDebug("Debug log exported to: " + path)
	}
}

// drawDebugFonts shows the font and colour the IC log resolved for its recent rows (see fontpickdebug.go).
func (a *App) drawDebugFonts(r sdl.Rect) {
	c := a.ctx
	y := r.Y
	line := func(s string, col sdl.Color) {
		c.LabelClipped(r.X, y, r.W, s, col)
		y += debugLineH
	}

	themeName := a.themeAppliedName
	if themeName == "" {
		themeName = "(none - default AO2 look)"
	}
	line("Theme: "+themeName, ColAccent)
	f := a.themeFonts.e[elemICChatlog]
	switch {
	case !f.dressed():
		line("ic_chatlog: undressed - draws in the client's own font chain", ColTextDim)
	case f.face == 0:
		line("ic_chatlog: theme sizes it but names no family - client's chain, theme's point size", ColTextDim)
	default:
		name := "(unresolved)"
		if idx := f.faceIdx(); idx >= 0 && idx < len(a.themeFaceNames) {
			name = a.themeFaceNames[idx]
		}
		line("ic_chatlog font file: "+name, ColText)
	}
	y += 4

	listR := sdl.Rect{X: r.X, Y: y, W: r.W, H: r.Y + r.H - y}
	c.Border(listR, ColPanelHi)

	a.debugFontBuf = a.icFontPickRows(a.debugFontBuf)
	if len(a.debugFontBuf) == 0 {
		c.LabelClipped(listR.X+6, listR.Y+6, listR.W-12, "No IC log rows yet.", ColTextDim)
		return
	}
	if !c.ctrlHeld {
		a.debugFontScroll -= c.WheelIn(listR) * scrollStepPx
	}
	contentH := int32(len(a.debugFontBuf)) * debugRowH
	track := sdl.Rect{X: listR.X + listR.W - scrollBarW, Y: listR.Y, W: scrollBarW, H: listR.H}
	a.debugFontScroll = c.VScrollbar("debugfonts", track, a.debugFontScroll, contentH, listR.H)
	clipPrev, clipHad := c.pushClip(listR)
	defer c.popClip(clipPrev, clipHad)
	rowW := listR.W - scrollBarW - 12
	rowY := listR.Y - a.debugFontScroll
	for i := range a.debugFontBuf {
		if rowY > listR.Y+listR.H {
			break
		}
		if rowY >= listR.Y-debugRowH {
			rec := a.debugFontBuf[i]
			col, flag := ColText, ""
			switch {
			case rec.shared && !rec.covered:
				col, flag = ColDanger, "  <- SHARED EMBEDDED FACE, uncovered"
			case !rec.covered:
				col = ColTierYellow
			}
			c.LabelClipped(listR.X+6, rowY+1, rowW,
				fmt.Sprintf("[%s %d/%d] cov=%-5v col=#%02X%02X%02X (%s) %q%s",
					rec.setName, rec.slot, rec.slotOf, rec.covered,
					rec.color.R, rec.color.G, rec.color.B, rec.colorSrc, rec.text, flag),
				col)
		}
		rowY += debugRowH
	}
}
