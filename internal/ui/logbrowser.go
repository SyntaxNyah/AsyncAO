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
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

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

// logSession is one saved transcript file for a server.
type logSession struct {
	file  string // file name (with .log) — used to read it
	label string // readable label derived from the timestamp name
}

// logLine is one parsed transcript line held in memory for the open scope.
type logLine struct {
	server  string // server folder it came from
	session string // session label
	text    string // the line as written ("[ts] who: message"), truncated
	lower   string // text lowercased once, for the 0-alloc live filter
	who     string // speaker parsed from the line, for the per-character filter
}

// logBrowserState is all the browser's state (one field on App).
type logBrowserState struct {
	servers    []string // server folders under logs/
	selServer  int      // index into servers; -1 = all servers
	sessions   []logSession
	selSession int // index into sessions; -1 = all sessions of the server

	query         string
	useRegex      bool      // treat the query as a regular expression
	charFilter    string    // restrict to one speaker ("" = all)
	regexErr      bool      // the current regex query didn't compile (matched as text)
	chars         []string  // distinct speakers in the loaded scope (the speaker-filter cycle)
	showStats     bool      // results pane shows per-speaker stats instead of lines
	stats         []logStat // per-speaker line/word counts, computed once on load
	statLines     int
	statWords     int
	statSessions  int
	scroll        int32 // results list
	serverScroll  int32
	sessionScroll int32

	lines     []logLine // the loaded scope, in memory
	filtered  []int     // indices into lines matching the filter (memoized)
	filterKey string    // the filter (query + regex + speaker) filtered was built for

	loading bool
	gen     int // scope-load generation; a stale async result is dropped
}

// logReq is one off-thread scope-load request.
type logReq struct {
	gen     int
	server  string // "" = all servers
	session string // "" = all sessions of server
}

// logBrowserLoad is the off-thread loader's result, polled on the render thread.
type logBrowserLoad struct {
	gen      int
	servers  []string
	sessions []logSession
	lines    []logLine
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
			session = lb.sessions[lb.selSession].file
		}
	}
	lb.gen++
	lb.loading = true
	req := logReq{gen: lb.gen, server: server, session: session}
	go func() {
		out := loadLogData(req)
		// Latest-wins: clear any stale pending result, then post ours.
		select {
		case <-a.logBrowserRes:
		default:
		}
		select {
		case a.logBrowserRes <- out:
		default:
		}
	}()
}

// pollLogBrowser lands an off-thread scope load on the render thread.
func (a *App) pollLogBrowser() {
	select {
	case out := <-a.logBrowserRes:
		lb := &a.logBrowser
		if out.gen != lb.gen {
			return // a newer request superseded this one
		}
		lb.loading = false
		lb.servers = out.servers
		lb.sessions = out.sessions
		lb.lines = out.lines
		lb.chars = distinctChars(out.lines)
		lb.stats, lb.statLines, lb.statWords, lb.statSessions = computeLogStats(out.lines)
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
		lb.regexErr = lb.useRegex && strings.TrimSpace(lb.query) != "" && !validRegex(lb.query)
		lb.filtered = filterLogLines(lb.lines, lb.query, lb.useRegex, lb.charFilter)
		lb.filterKey = key
	}
	return lb.filtered
}

// filterLogLines returns the indices of lines whose text contains query
// (case-insensitive). An empty query matches everything. Pure — unit-tested.
func filterLogLines(lines []logLine, query string, useRegex bool, who string) []int {
	who = strings.ToLower(strings.TrimSpace(who))
	q := strings.TrimSpace(query)
	var re *regexp.Regexp
	if useRegex && q != "" {
		re, _ = regexp.Compile("(?i)" + q) // nil on a bad pattern → falls through to substring
	}
	ql := strings.ToLower(q)
	idx := make([]int, 0, 64)
	for i := range lines {
		if who != "" && strings.ToLower(lines[i].who) != who {
			continue
		}
		switch {
		case q == "":
		case re != nil:
			if !re.MatchString(lines[i].text) {
				continue
			}
		default:
			if !strings.Contains(lines[i].lower, ql) {
				continue
			}
		}
		idx = append(idx, i)
	}
	return idx
}

// validRegex reports whether q compiles as a case-insensitive regex.
func validRegex(q string) bool {
	_, err := regexp.Compile("(?i)" + strings.TrimSpace(q))
	return err == nil
}

// parseLogWho extracts the speaker from a transcript line "[ts] who: message".
func parseLogWho(line string) string {
	i := strings.Index(line, "] ")
	if i < 0 {
		return ""
	}
	rest := line[i+2:]
	j := strings.Index(rest, ": ")
	if j < 0 {
		return ""
	}
	return strings.TrimSpace(rest[:j])
}

// logStat is one speaker's tally in the loaded scope.
type logStat struct {
	name  string
	lines int
	words int
}

// computeLogStats tallies per-speaker line and word counts plus scope totals. Pure
// — unit-tested. Speakers sort by line count, descending.
func computeLogStats(lines []logLine) (stats []logStat, totalLines, totalWords, sessions int) {
	byName := map[string]*logStat{}
	sess := map[string]bool{}
	for i := range lines {
		ln := &lines[i]
		sess[ln.session] = true
		w := len(strings.Fields(ln.text))
		totalLines++
		totalWords += w
		who := ln.who
		if who == "" {
			who = "(server)"
		}
		s := byName[who]
		if s == nil {
			s = &logStat{name: who}
			byName[who] = s
		}
		s.lines++
		s.words += w
	}
	stats = make([]logStat, 0, len(byName))
	for _, s := range byName {
		stats = append(stats, *s)
	}
	sort.Slice(stats, func(i, j int) bool {
		if stats[i].lines != stats[j].lines {
			return stats[i].lines > stats[j].lines
		}
		return stats[i].name < stats[j].name
	})
	return stats, totalLines, totalWords, len(sess)
}

// distinctChars returns the distinct speakers across lines, sorted, bounded.
func distinctChars(lines []logLine) []string {
	seen := map[string]bool{}
	out := make([]string, 0, 32)
	for i := range lines {
		w := lines[i].who
		if w == "" || seen[strings.ToLower(w)] {
			continue
		}
		seen[strings.ToLower(w)] = true
		out = append(out, w)
		if len(out) >= 200 {
			break
		}
	}
	sort.Strings(out)
	return out
}

// cycleChar advances the speaker filter: "" (All) → chars[0] → … → "".
func cycleChar(chars []string, cur string) string {
	if cur == "" {
		if len(chars) > 0 {
			return chars[0]
		}
		return ""
	}
	for i, ch := range chars {
		if ch == cur {
			if i+1 < len(chars) {
				return chars[i+1]
			}
			return ""
		}
	}
	return ""
}

// sessionLabel turns a transcript file name (2006-01-02_15-04-05.log) into a
// readable "2006-01-02 15:04" label; an unrecognized name shows without .log.
// Pure — unit-tested.
func sessionLabel(file string) string {
	name := strings.TrimSuffix(file, ".log")
	if t, err := time.Parse("2006-01-02_15-04-05", name); err == nil {
		return t.Format("2006-01-02 15:04")
	}
	return name
}

// truncateRunes caps s at max runes, appending "…" when it cut. Pure.
func truncateRunes(s string, max int) string {
	if max <= 0 {
		return s
	}
	count := 0
	for i := range s {
		if count == max {
			return s[:i] + "…"
		}
		count++
	}
	return s
}

// --- disk (off-thread only) -------------------------------------------------

// logsRootDir is logs\ next to the exe (mirrors recordingsDir / the transcript
// writer's own path). Empty when the exe path can't be resolved.
func logsRootDir() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	return filepath.Join(filepath.Dir(exe), "logs")
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
		lines = append(lines, "["+ln.server+" · "+ln.session+"] "+ln.text)
	}
	stamp := time.Now().Format("2006-01-02_15-04-05")
	a.warnLine = clampLine("Exported " + strconv.Itoa(len(lines)) + " lines -> logs/exports/search-" + stamp + ".txt")
	a.warnAt = a.now()
	root := logsRootDir()
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
	root := logsRootDir()
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

// listLogServerDirs returns the server folder names under logs\, sorted, bounded.
func listLogServerDirs(root string) []string {
	if root == "" {
		return nil
	}
	ents, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(ents))
	for _, e := range ents {
		if e.IsDir() {
			out = append(out, e.Name())
			if len(out) >= maxLogServers {
				break
			}
		}
	}
	sort.Strings(out)
	return out
}

// listLogSessionFiles returns one server's .log files, newest first, bounded.
func listLogSessionFiles(root, server string) []logSession {
	if root == "" || server == "" {
		return nil
	}
	ents, err := os.ReadDir(filepath.Join(root, server))
	if err != nil {
		return nil
	}
	type fe struct {
		name string
		mod  time.Time
	}
	files := make([]fe, 0, len(ents))
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".log") {
			continue
		}
		mt := time.Time{}
		if info, err := e.Info(); err == nil {
			mt = info.ModTime()
		}
		files = append(files, fe{e.Name(), mt})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod.After(files[j].mod) })
	if len(files) > maxLogSessions {
		files = files[:maxLogSessions]
	}
	out := make([]logSession, len(files))
	for i, f := range files {
		out[i] = logSession{file: f.name, label: sessionLabel(f.name)}
	}
	return out
}

// loadLogData reads the servers list, the selected server's sessions, and the
// scope's lines — all off-thread, all bounded. Returns everything the browser
// needs for one scope.
func loadLogData(req logReq) logBrowserLoad {
	out := logBrowserLoad{gen: req.gen}
	root := logsRootDir()
	out.servers = listLogServerDirs(root)
	if req.server != "" {
		out.sessions = listLogSessionFiles(root, req.server)
	}
	out.lines = readLogScope(root, req.server, req.session)
	return out
}

// readLogScope reads the lines for a scope into memory, honoring the caps. With
// no server it spans every server (newest sessions first); with a server but no
// session it spans that server's sessions; with both it reads one file.
func readLogScope(root, server, session string) []logLine {
	if root == "" {
		return nil
	}
	lines := make([]logLine, 0, 256)
	files := 0
	// addFile appends one log's lines; returns false once a cap is hit (stop).
	addFile := func(srv, file string) bool {
		if files >= maxLogScopeFiles || len(lines) >= maxLogScopeLines {
			return false
		}
		files++
		path := filepath.Join(root, srv, file)
		info, err := os.Stat(path)
		if err != nil || info.Size() > maxLogFileBytes {
			return true // skip this one, keep going
		}
		f, err := os.Open(path)
		if err != nil {
			return true
		}
		defer f.Close()
		label := sessionLabel(file)
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 64*1024), 1<<20) // tolerate long lines
		for sc.Scan() {
			if len(lines) >= maxLogScopeLines {
				return false
			}
			t := strings.TrimRight(sc.Text(), "\r")
			if strings.TrimSpace(t) == "" {
				continue
			}
			t = truncateRunes(t, maxLogLineRunes)
			lines = append(lines, logLine{server: srv, session: label, text: t, lower: strings.ToLower(t), who: parseLogWho(t)})
		}
		return true
	}

	switch {
	case server != "" && session != "":
		addFile(server, session)
	case server != "":
		for _, s := range listLogSessionFiles(root, server) {
			if !addFile(server, s.file) {
				break
			}
		}
	default: // all servers
		for _, srv := range listLogServerDirs(root) {
			stop := false
			for _, s := range listLogSessionFiles(root, srv) {
				if !addFile(srv, s.file) {
					stop = true
					break
				}
			}
			if stop {
				break
			}
		}
	}
	return lines
}

// --- UI (render thread) -----------------------------------------------------

// drawLogBrowser paints the full-window log browser screen: a left column to
// pick the server then the session, and a main pane with a live text filter over
// the matching lines (click a line to copy it). Back / Esc returns to prevScreen.
func (a *App) drawLogBrowser(w, h int32) {
	c := a.ctx
	lb := &a.logBrowser
	a.drawScreenBackdrop(w, h, "lobbybackground")
	c.Fill(sdl.Rect{X: 0, Y: 0, W: w, H: h}, sdl.Color{R: 0, G: 0, B: 0, A: 150}) // dim for readability

	c.Heading(pad, pad, "Logs — search your saved transcripts", ColText)
	if c.Button(sdl.Rect{X: w - 110 - pad, Y: pad, W: 110, H: btnH}, "Back") {
		a.screen = a.prevScreen
		return
	}

	idx := a.logFiltered()
	if c.Button(sdl.Rect{X: w - 230 - pad, Y: pad, W: 110, H: btnH}, "Export") {
		a.exportLogView(idx)
	}
	statsLabel := "Stats"
	if lb.showStats {
		statsLabel = "Hide stats"
	}
	if c.Button(sdl.Rect{X: w - 350 - pad, Y: pad, W: 110, H: btnH}, statsLabel) {
		lb.showStats = !lb.showStats
	}
	scope := "All servers"
	if lb.selServer >= 0 && lb.selServer < len(lb.servers) {
		scope = lb.servers[lb.selServer]
		if lb.selSession >= 0 && lb.selSession < len(lb.sessions) {
			scope += " · " + lb.sessions[lb.selSession].label
		}
	}
	status := fmt.Sprintf("%s — %d / %d lines", scope, len(idx), len(lb.lines))
	switch {
	case lb.loading:
		status = "loading…"
	case len(lb.lines) >= maxLogScopeLines:
		status += fmt.Sprintf(" (showing the first %d — narrow the scope or filter)", maxLogScopeLines)
	}
	c.Label(pad, pad+30, status, ColTextDim)

	top := pad + 58
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
			sesLabels = append(sesLabels, s.label)
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
		lb.charFilter = cycleChar(lb.chars, lb.charFilter)
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
	if len(lb.stats) > 0 && lb.stats[0].lines > 0 {
		maxLines = lb.stats[0].lines
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
		c.LabelClipped(box.X+8, y, 158, s.name, ColText)
		if barMaxW > 0 {
			if bw := int32(s.lines) * barMaxW / int32(maxLines); bw > 0 {
				c.Fill(sdl.Rect{X: barX, Y: y + 2, W: bw, H: rowH - 6}, ColAccent)
			}
		}
		c.LabelClipped(box.X+box.W-90, y, 84, fmt.Sprintf("%dL  %dW", s.lines, s.words), ColTextDim)
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
			c.LabelClipped(rr.X+6, rr.Y+(rowH-14)/2, rowW-12, lab, col)
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
						_ = sdl.SetClipboardText(ln.text)
						a.warnLine = clampLine("Copied: " + ln.text)
						a.warnAt = a.now()
					}
				}
			}
			tx := rr.X + 6
			if gutter > 0 {
				prefix := ln.session
				if lb.selServer < 0 {
					prefix = ln.server + " · " + ln.session
				}
				c.LabelClipped(tx, rr.Y+(rowH-14)/2, gutter-10, prefix, ColTextDim)
				tx += gutter
			}
			c.LabelClipped(tx, rr.Y+(rowH-14)/2, rr.X+rr.W-tx-4, ln.text, ColText)
		}
		rowY += rowH
	}
}
