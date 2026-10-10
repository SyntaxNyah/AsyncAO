package ui

// Log browser (ScreenLogs): browse and search the per-server transcripts that
// detailed logging writes under logs/<server>/<session>.log. A streaming client
// can't index a server's history, but YOUR OWN logs are right here on disk, so
// this is a "look through any log, any server, filter by text" view.
//
// Disk work is OFF the render thread (rule §2) and HARD-bounded (rule §4): a
// scope load reads at most maxLogScopeFiles files / maxLogScopeLines lines into
// memory once, and the live text filter then runs over that in-memory slice
// (memoized — one pass per query change, never per frame). Opening / switching
// scope kicks an off-thread load that lands on logBrowserRes (polled per frame).

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/logbrowser"
	"github.com/veandco/go-sdl2/sdl"
)

const (
	logBrowserRowH = int32(20)
	logBrowserColW = int32(240) // left column (servers + sessions)

	// Hard caps (rule §4: nothing unbounded). Generous — nobody logs to hundreds
	// of servers — but a heavy logs/ dir can never balloon memory or stall.
	maxLogServers    = 200      // distinct server folders listed
	maxLogSessions   = 500      // session files listed for one server
	maxLogScopeFiles = 400      // files read into one scope load
	maxLogScopeLines = 20000    // parsed lines held in memory for a scope
	maxLogFileBytes  = 16 << 20 // skip a single log bigger than this
	maxLogLineRunes  = 600      // truncate one very long line (display + match)

	// logFilterUnset is a sentinel filterKey that never equals a real query, so
	// new scope data always forces one refilter.
	logFilterUnset = "\x00unset"
)

// logBrowserState is all the browser's state (one field on App).
type logBrowserState struct {
	servers    []string // server folders under logs/
	selServer  int      // index into servers; -1 = all servers
	sessions   []logbrowser.Session
	selSession int // index into sessions; -1 = all sessions of the server

	query         string
	useRegex      bool              // treat the query as a regular expression
	charFilter    string            // restrict to one speaker ("" = all)
	regexErr      bool              // the current regex query didn't compile (matched as text)
	chars         []string          // distinct speakers in the loaded scope (the speaker-filter cycle)
	showStats     bool              // results pane shows per-speaker stats instead of lines
	stats         []logbrowser.Stat // per-speaker line/word counts, computed once on load
	statLines     int
	statWords     int
	statSessions  int
	scroll        int32 // results list
	serverScroll  int32
	sessionScroll int32

	lines     []logbrowser.Line // the loaded scope, in memory
	filtered  []int             // indices into lines matching the filter (memoized)
	filterKey string            // the filter (query + regex + speaker) filtered was built for

	loading bool
	gen     int // scope-load generation; a stale async result is dropped
}

// openLogBrowser resets the browser to "all servers" and kicks the first load.
func (a *App) openLogBrowser() {
	lb := &a.logBrowser
	lb.selServer, lb.selSession = -1, -1
	lb.query, lb.charFilter, lb.useRegex, lb.regexErr = "", "", false, false
	lb.showStats = false
	lb.scroll, lb.serverScroll, lb.sessionScroll = 0, 0, 0
	lb.lines, lb.filtered, lb.chars, lb.stats = nil, nil, nil, nil
	lb.filterKey = logFilterUnset
	lb.servers, lb.sessions = nil, nil
	a.kickLogScope()
}

// kickLogScope reads the current scope (selServer/selSession) off-thread.
func (a *App) kickLogScope() {
	lb := &a.logBrowser
	server, session := "", ""
	if lb.selServer >= 0 && lb.selServer < len(lb.servers) {
		server = lb.servers[lb.selServer]
		if lb.selSession >= 0 && lb.selSession < len(lb.sessions) {
			session = lb.sessions[lb.selSession].File
		}
	}
	lb.gen++
	lb.loading = true
	gen := lb.gen
	ch := a.logBrowserRes
	go func() {
		out := logbrowser.Load(logbrowser.RootDir(), server, session, gen)
		// Latest-wins: clear any stale pending result, then post ours.
		select {
		case <-ch:
		default:
		}
		select {
		case ch <- out:
		default:
		}
		PushWake() // wake the event-driven loop so Background drains this at idle=0
	}()
}

// pollLogBrowser lands an off-thread scope load on the render thread.
func (a *App) pollLogBrowser() {
	select {
	case out := <-a.logBrowserRes:
		lb := &a.logBrowser
		if out.Gen != lb.gen {
			return // a newer request superseded this one
		}
		lb.loading = false
		a.uiDirty = true // the log scope just loaded: force a redraw so the session list + log area appear at idle=0 (not just the chrome)
		lb.servers = out.Servers
		lb.sessions = out.Sessions
		lb.lines = out.Lines
		lb.chars = logbrowser.DistinctChars(out.Lines)
		lb.stats, lb.statLines, lb.statWords, lb.statSessions = logbrowser.ComputeStats(out.Lines)
		lb.charFilter = "" // a new scope resets the speaker filter
		lb.filtered = nil
		lb.filterKey = logFilterUnset // force one refilter against the new data
	default:
	}
}

// logFiltered returns the indices of lines matching the query, recomputed only
// when the query (or the scope data) changed — so typing never re-scans per frame.
func (a *App) logFiltered() []int {
	lb := &a.logBrowser
	key := fmt.Sprintf("%t\x00%s\x00%s", lb.useRegex, lb.charFilter, lb.query)
	if lb.filterKey != key {
		lb.regexErr = lb.useRegex && strings.TrimSpace(lb.query) != "" && !logbrowser.ValidRegex(lb.query)
		lb.filtered = logbrowser.FilterLines(lb.lines, lb.query, lb.useRegex, lb.charFilter)
		lb.filterKey = key
	}
	return lb.filtered
}

// exportLogView writes the current filtered result lines to a timestamped text file
// under logs/exports/, off the render thread — a "save this search" for sharing or
// archiving. Each line carries its server · session so a mixed-scope export stays
// attributable.
func (a *App) exportLogView(idx []int) {
	lb := &a.logBrowser
	if len(idx) == 0 {
		a.warnLine = "Log export: nothing to export."
		a.warnAt = a.now()
		return
	}
	lines := make([]string, 0, len(idx))
	for _, i := range idx {
		ln := lb.lines[i]
		lines = append(lines, "["+ln.Server+" · "+ln.Session+"] "+ln.Text)
	}
	stamp := time.Now().Format("2006-01-02_15-04-05")
	a.warnLine = clampLine("Exported " + strconv.Itoa(len(lines)) + " lines -> logs/exports/search-" + stamp + ".txt")
	a.warnAt = a.now()
	root := logbrowser.RootDir()
	go func() {
		if root == "" {
			return
		}
		dir := filepath.Join(root, "exports")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return
		}
		_ = os.WriteFile(filepath.Join(dir, "search-"+stamp+".txt"), []byte(strings.Join(lines, "\n")+"\n"), 0o644)
	}()
}

// modcallClipLines is how many recent IC lines a modcall clip captures — enough
// context to see what triggered the call without dumping the whole session.
const modcallClipLines = 60

// autoClipModcall snapshots the recent IC log when a modcall fires (sent or
// received) and writes it to logs/<server>/modcalls/<ts>-modcall.txt off the
// render thread, so mods/CMs always have a frozen record of the surrounding
// context. The tail is flattened to fresh strings on the (main) calling thread
// before the writer goroutine runs, so it never races the live icLog. Opt-out via
// the AutoClipModcall pref (default ON); a no-op (one pref read) when off.
func (a *App) autoClipModcall(server string, log []icEntry, notice string) {
	if !a.d.Prefs.AutoClipModcallOn() {
		return
	}
	root := logbrowser.RootDir()
	if root == "" {
		return
	}
	now := time.Now()
	lines := buildModcallClip(server, notice, log, modcallClipLines, now)
	folder := sanitizeLogFolder(server)
	stamp := now.Format("2006-01-02_15-04-05")
	a.warnLine = clampLine("Modcall clip saved -> logs/" + folder + "/modcalls/" + stamp + "-modcall.txt")
	a.warnAt = a.now()
	go func() {
		dir := filepath.Join(root, folder, "modcalls")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return
		}
		_ = os.WriteFile(filepath.Join(dir, stamp+"-modcall.txt"), []byte(strings.Join(lines, "\n")+"\n"), 0o644)
	}()
}

// buildModcallClip flattens the header + the last min(n, len(log)) IC lines into
// plain text. Pure (now injected) so the clip contents are unit-testable without
// touching disk; the returned strings are fresh, so the caller can hand them to a
// writer goroutine without racing the live icLog.
func buildModcallClip(server, notice string, log []icEntry, n int, now time.Time) []string {
	if n > len(log) {
		n = len(log)
	}
	lines := make([]string, 0, n+5)
	lines = append(lines, "AsyncAO modcall clip")
	lines = append(lines, "Server : "+server)
	lines = append(lines, "When   : "+now.Format("2006-01-02 15:04:05"))
	lines = append(lines, "Notice : "+notice)
	lines = append(lines, "--- last "+strconv.Itoa(n)+" IC lines ---")
	for _, e := range log[len(log)-n:] {
		if e.stamp != "" {
			lines = append(lines, "["+e.stamp+"] "+e.text)
		} else {
			lines = append(lines, e.text)
		}
	}
	return lines
}

// drawLogBrowser paints the full-window log browser screen: a left column to
// pick the server then the session, and a main pane with a live text filter over
// the matching lines (click a line to copy it). Back / Esc returns to prevScreen.
func (a *App) drawLogBrowser(w, h int32) {
	c := a.ctx
	lb := &a.logBrowser
	a.drawScreenBackdrop(w, h, "lobbybackground")
	c.Fill(sdl.Rect{X: 0, Y: 0, W: w, H: h}, sdl.Color{R: 0, G: 0, B: 0, A: 150}) // dim for readability

	// Content starts below the app-chrome band (#14, chrometop.go).
	hdrY := a.topChromeH() + pad
	c.Heading(pad, hdrY, "Logs — search your saved transcripts", ColText)
	if c.Button(sdl.Rect{X: w - 110 - pad, Y: hdrY, W: 110, H: btnH}, "Back") {
		a.screen = a.prevScreen
		return
	}

	idx := a.logFiltered()
	if c.Button(sdl.Rect{X: w - 230 - pad, Y: hdrY, W: 110, H: btnH}, "Export") {
		a.exportLogView(idx)
	}
	statsLabel := "Stats"
	if lb.showStats {
		statsLabel = "Hide stats"
	}
	if c.Button(sdl.Rect{X: w - 350 - pad, Y: hdrY, W: 110, H: btnH}, statsLabel) {
		lb.showStats = !lb.showStats
	}
	scope := "All servers"
	if lb.selServer >= 0 && lb.selServer < len(lb.servers) {
		scope = lb.servers[lb.selServer]
		if lb.selSession >= 0 && lb.selSession < len(lb.sessions) {
			scope += " · " + lb.sessions[lb.selSession].Label
		}
	}
	status := fmt.Sprintf("%s — %d / %d lines", scope, len(idx), len(lb.lines))
	switch {
	case lb.loading:
		status = "loading…"
	case len(lb.lines) >= logbrowser.MaxScopeLines:
		status += fmt.Sprintf(" (showing the first %d — narrow the scope or filter)", maxLogScopeLines)
	}
	c.Label(pad, hdrY+30, status, ColTextDim)

	top := hdrY + 58
	bottom := h - pad
	leftX := pad
	mainX := leftX + logBrowserColW + 16
	mainW := w - mainX - pad
	if mainW < 200 {
		mainW = 200
	}

	// LEFT COLUMN: servers (top) then sessions (bottom).
	colH := bottom - top
	c.Label(leftX, top, "Server", ColTextDim)
	srvR := sdl.Rect{X: leftX, Y: top + 20, W: logBrowserColW, H: colH/2 - 30}
	srvLabels := make([]string, 0, len(lb.servers)+1)
	srvLabels = append(srvLabels, "All servers")
	srvLabels = append(srvLabels, lb.servers...)
	if clicked := a.drawLogList("logsrv", srvR, srvLabels, lb.selServer+1, &lb.serverScroll); clicked >= 0 {
		if ns := clicked - 1; ns != lb.selServer {
			lb.selServer, lb.selSession = ns, -1
			lb.sessionScroll, lb.scroll = 0, 0
			a.kickLogScope()
		}
	}

	sesLabelY := srvR.Y + srvR.H + 12
	c.Label(leftX, sesLabelY, "Session", ColTextDim)
	sesR := sdl.Rect{X: leftX, Y: sesLabelY + 20, W: logBrowserColW, H: bottom - (sesLabelY + 20)}
	if lb.selServer < 0 {
		c.Border(sesR, ColPanelHi)
		c.LabelClipped(sesR.X+6, sesR.Y+6, sesR.W-12, "Pick a server to list its sessions.", ColTextDim)
	} else {
		sesLabels := make([]string, 0, len(lb.sessions)+1)
		sesLabels = append(sesLabels, "All sessions")
		for _, s := range lb.sessions {
			sesLabels = append(sesLabels, s.Label)
		}
		if clicked := a.drawLogList("logses", sesR, sesLabels, lb.selSession+1, &lb.sessionScroll); clicked >= 0 {
			if ns := clicked - 1; ns != lb.selSession {
				lb.selSession, lb.scroll = ns, 0
				a.kickLogScope()
			}
		}
	}

	// MAIN PANE: live filter (text / regex / speaker) + results.
	lb.query, _ = c.TextField("logquery", sdl.Rect{X: mainX, Y: top, W: mainW, H: fieldH}, lb.query, "filter — name, word, phrase, or a regex…")
	fy := top + fieldH + 6
	lb.useRegex = c.Checkbox(mainX, fy, "Regex", lb.useRegex)
	charLabel := "Speaker: All"
	if lb.charFilter != "" {
		charLabel = "Speaker: " + lb.charFilter
	}
	cbW := c.TextWidth(charLabel) + 24
	if cbW > 220 {
		cbW = 220
	}
	if c.Button(sdl.Rect{X: mainX + 90, Y: fy - 2, W: cbW, H: btnH}, charLabel) {
		lb.charFilter = logbrowser.CycleChar(lb.chars, lb.charFilter)
	}
	if lb.regexErr {
		c.LabelClipped(mainX+96+cbW, fy, mainW-96-cbW, "invalid regex — matching as text", ColDanger)
	}
	resTop := fy + btnH + 8
	resBox := sdl.Rect{X: mainX, Y: resTop, W: mainW, H: bottom - resTop}
	if lb.showStats {
		a.drawLogStats(resBox)
	} else {
		a.drawLogResults(resBox, idx)
	}
}

// drawLogStats renders the cached per-speaker tallies for the loaded scope: a totals
// line, then each speaker with a line-count bar and their line / word counts.
func (a *App) drawLogStats(box sdl.Rect) {
	c := a.ctx
	lb := &a.logBrowser
	c.Border(box, ColPanelHi)
	if lb.statLines == 0 {
		c.LabelClipped(box.X+6, box.Y+6, box.W-12, "No lines in this scope.", ColTextDim)
		return
	}
	y := box.Y + 6
	c.LabelClipped(box.X+8, y, box.W-16,
		fmt.Sprintf("%d lines · %d words · %d session(s)", lb.statLines, lb.statWords, lb.statSessions), ColAccent)
	y += 22
	maxLines := 1
	if len(lb.stats) > 0 && lb.stats[0].Lines > 0 {
		maxLines = lb.stats[0].Lines
	}
	const rowH = int32(18)
	clipPrev, clipHad := c.pushClip(box)
	defer c.popClip(clipPrev, clipHad)
	barX := box.X + 170
	barMaxW := box.W - 170 - 96
	for _, s := range lb.stats {
		if y > box.Y+box.H-rowH {
			break
		}
		c.LabelClipped(box.X+8, y, 158, s.Name, ColText)
		if barMaxW > 0 {
			if bw := int32(s.Lines) * barMaxW / int32(maxLines); bw > 0 {
				c.Fill(sdl.Rect{X: barX, Y: y + 2, W: bw, H: rowH - 6}, ColAccent)
			}
		}
		c.LabelClipped(box.X+box.W-90, y, 84, fmt.Sprintf("%dL  %dW", s.Lines, s.Words), ColTextDim)
		y += rowH
	}
}

// drawLogList draws a bordered, scrollable, single-select list of labels in r,
// highlighting index sel; returns the index clicked this frame, or -1. id
// namespaces the scrollbar; scroll is owned by the caller.
func (a *App) drawLogList(id string, r sdl.Rect, labels []string, sel int, scroll *int32) int {
	c := a.ctx
	c.Border(r, ColPanelHi)
	if !c.ctrlHeld {
		*scroll -= c.WheelIn(r) * scrollStepPx
	}
	rowH := logBrowserRowH
	track := sdl.Rect{X: r.X + r.W - scrollBarW, Y: r.Y, W: scrollBarW, H: r.H}
	*scroll = c.VScrollbar(id, track, *scroll, int32(len(labels))*rowH, r.H)
	clipPrev, clipHad := c.pushClip(r)
	defer c.popClip(clipPrev, clipHad)
	rowW := r.W - scrollBarW - 2
	rowY := r.Y - *scroll
	clicked := -1
	for i, lab := range labels {
		if rowY > r.Y+r.H {
			break
		}
		if rowY >= r.Y-rowH {
			rr := sdl.Rect{X: r.X, Y: rowY, W: rowW, H: rowH}
			col := ColText
			if i == sel {
				c.Fill(rr, ColPanelHi)
				col = ColAccent
			} else if c.hovering(rr) {
				c.Fill(rr, ColPanelHi)
			}
			a.labelName(rr.X+6, rr.Y+(rowH-14)/2, rowW-12, lab, col) // CJK-safe (chat-list peer/group names)
			if c.hovering(rr) && c.clicked {
				clicked = i
			}
		}
		rowY += rowH
	}
	return clicked
}

// drawLogResults paints the filtered transcript lines in r. Each line shows a
// dim scope prefix (server · session, or just the session, depending on scope)
// and the text; clicking a line copies it to the clipboard.
func (a *App) drawLogResults(r sdl.Rect, idx []int) {
	c := a.ctx
	lb := &a.logBrowser
	c.Border(r, ColPanelHi)
	if len(idx) == 0 {
		msg := "No lines yet — pick a server, or enable detailed logging (Settings → Audio & Chat)."
		if len(lb.lines) > 0 {
			msg = "No lines match your filter."
		}
		c.LabelClipped(r.X+6, r.Y+6, r.W-12, msg, ColTextDim)
		return
	}
	rowH := logBrowserRowH
	if !c.ctrlHeld {
		lb.scroll -= c.WheelIn(r) * scrollStepPx
	}
	track := sdl.Rect{X: r.X + r.W - scrollBarW, Y: r.Y, W: scrollBarW, H: r.H}
	lb.scroll = c.VScrollbar("logresults", track, lb.scroll, int32(len(idx))*rowH, r.H)
	clipPrev, clipHad := c.pushClip(r)
	defer c.popClip(clipPrev, clipHad)
	rowW := r.W - scrollBarW - 4

	var gutter int32 // dim scope prefix width (0 = single session, no prefix)
	switch {
	case lb.selServer < 0:
		gutter = 190 // all servers: "server · session"
	case lb.selSession < 0:
		gutter = 120 // one server: "session"
	}

	rowY := r.Y - lb.scroll
	for _, li := range idx {
		if rowY > r.Y+r.H {
			break
		}
		if rowY >= r.Y-rowH {
			ln := lb.lines[li]
			rr := sdl.Rect{X: r.X, Y: rowY, W: rowW, H: rowH}
			if c.hovering(rr) {
				c.Fill(rr, ColPanelHi)
				if c.clicked {
					if strings.TrimSpace(lb.query) != "" || lb.charFilter != "" {
						// Jump to context: clear the filter and scroll to this line.
						lb.query, lb.charFilter, lb.useRegex = "", "", false
						lb.filterKey = logFilterUnset
						lb.scroll = int32(li)*rowH - r.H/2 // VScrollbar clamps next frame
						a.warnLine = clampLine("Jumped to context")
						a.warnAt = a.now()
					} else {
						_ = sdl.SetClipboardText(ln.Text)
						a.warnLine = clampLine("Copied: " + ln.Text)
						a.warnAt = a.now()
					}
				}
			}
			tx := rr.X + 6
			if gutter > 0 {
				prefix := ln.Session
				if lb.selServer < 0 {
					prefix = ln.Server + " · " + ln.Session
				}
				c.LabelClipped(tx, rr.Y+(rowH-14)/2, gutter-10, prefix, ColTextDim)
				tx += gutter
			}
			a.labelName(tx, rr.Y+(rowH-14)/2, rr.X+rr.W-tx-4, ln.Text, ColText) // CJK-safe transcript (showname + message)
		}
		rowY += rowH
	}
}
