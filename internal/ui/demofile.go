package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
)

// AO2 .demo backwards compatibility ("it's backwards COMPATIBILITY baby"):
// read the demo files AO2's built-in recorder writes — play them in the replay
// player, edit them in the Scene Maker, export them to GIF/WebP/Video/Comic —
// and write our recordings BACK out as .demo so AO2 users can watch them.
//
// The format (canonical: ../AO2-Client/src/demoserver.cpp): a text file of raw
// SERVER→client packets, one per line (a packet may span lines — the loader
// joins until the line ends with '%'), with "wait#<ms>#%" packets carrying the
// timing and usually an "SC#…#%" char list first. Pre-2.9.1 files have the
// wait-desync bug (waits recorded one slot late — AO2 PR #496); AO2 detects
// those by "starts with SC, ENDS with wait" and shifts every wait one slot
// earlier, and we mirror that exactly (in memory only — the file is untouched).
const (
	demoExt = ".demo"
	// demoMaxWaitMs caps one imported gap, in the spirit of the demo server's
	// /max_wait: an hour of AFK between two lines shouldn't become an hour of
	// timeline. Only OffsetMs metadata (timeline/exports) — replay itself is
	// feed-on-idle and never sleeps on these.
	demoMaxWaitMs = 3000
)

// parseDemoRecords splits a .demo into packet records, joining continuation
// lines until a record ends with '%' (multi-line message text) — the exact
// loop demoserver.cpp::load_demo runs. CRLF is tolerated; blank tails drop.
func parseDemoRecords(data []byte) []string {
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	var out []string
	cur := ""
	for _, ln := range lines {
		if cur == "" {
			cur = ln
		} else {
			cur += "\n" + ln // a packet spanning lines keeps its literal newline
		}
		if strings.HasSuffix(cur, "%") {
			out = append(out, cur)
			cur = ""
		}
	}
	if strings.TrimSpace(cur) != "" { // unterminated tail: keep it, ParsePacket will reject
		out = append(out, cur)
	}
	return out
}

// fixDemoWaitDesync applies AO2's pre-2.9.1 repair when the file shows the
// desync signature (SC first AND wait last): every wait packet moves one slot
// earlier (insert at max(1, len-1) — the same queue walk demoserver.cpp runs).
func fixDemoWaitDesync(records []string) []string {
	if len(records) < 2 || !strings.HasPrefix(records[0], "SC#") || !strings.HasPrefix(records[len(records)-1], "wait#") {
		return records
	}
	out := make([]string, 0, len(records))
	for _, r := range records {
		if !strings.HasPrefix(r, "SC#") && strings.HasPrefix(r, "wait#") {
			at := len(out) - 1
			if at < 1 {
				at = 1
			}
			if at > len(out) {
				at = len(out)
			}
			out = append(out, "")
			copy(out[at+1:], out[at:])
			out[at] = r
			continue
		}
		out = append(out, r)
	}
	return out
}

// demoToRecording converts a .demo into our replay model: MS → message events,
// BN → background, MC → music, waits → cumulative OffsetMs (each gap capped at
// demoMaxWaitMs). Every other packet (SC/CT/HP/TI/LE/…) is counted and skipped
// — the scene model deliberately covers what the stage shows. origin is the
// asset host to stream from (demos don't store one; AO2 plays them against a
// local base folder).
//
// Two independent counters come back: skipped (packets we recognize but don't
// model — SC/CT/HP/…, byte-identical to before) and truncated (valid scene
// packets dropped because the scene already holds maxRecordedEvents). They are
// reported separately so the user can tell "this demo has non-scene chatter" from
// "this demo is longer than the editor's scene cap."
func demoToRecording(data []byte, origin string) (rec *sceneRecording, skipped, truncated int, err error) {
	records := fixDemoWaitDesync(parseDemoRecords(data))
	if len(records) == 0 {
		return nil, 0, 0, fmt.Errorf("empty demo file")
	}
	// Demos are recorded by 2.8+ clients from servers with the full feature set
	// (the demo server itself advertises everything), so extended fields parse.
	features := protocol.ParseFeatures([]string{protocol.FeatureCCCCIC})
	rec = &sceneRecording{Version: recordingVersion, Origin: origin}
	cum := 0
	// capReached: the scene already holds maxRecordedEvents. We keep a coherent
	// PREFIX (stop-at-cap, exactly recordEvent's replay.go:155 / makerInsert's
	// scenemaker.go:436 semantics — NOT the instant-replay ring's oldest-eviction:
	// OffsetMs is cumulative, so only a leading run stays timeline-consistent).
	// maxRecordedEvents is deliberately the SAME ceiling every ingestion path uses
	// (the scene maker's insert guard, live recording, replay, cloneScene), and it is
	// now sized (50000) to admit a whole real session — the largest real fixture is
	// 8943 scene events — so a full demo imports intact. Video export length is bounded
	// separately by maxVideoHours (video streams to ffmpeg, holding nothing), so a big
	// import stays fully exportable.
	capReached := func() bool { return len(rec.Events) >= maxRecordedEvents }
	for _, raw := range records {
		pkt, err := protocol.ParsePacket(strings.TrimSuffix(raw, "\n"))
		if err != nil {
			skipped++
			continue
		}
		switch pkt.Header {
		case "wait":
			d := atoiClamped(pkt.Field(0), 0, demoMaxWaitMs)
			cum += d
		case "MS":
			msg, err := protocol.ParseMS(pkt.Fields, features, 0)
			if err != nil {
				skipped++
				continue
			}
			if capReached() {
				truncated++
				continue
			}
			rec.Events = append(rec.Events, recEvent{OffsetMs: cum, Kind: int(courtroom.EventMessage), Message: msg})
		case "BN":
			bg := pkt.Field(0)
			if bg == "" {
				skipped++
				continue
			}
			if capReached() {
				truncated++
				continue
			}
			if rec.StartBg == "" && len(rec.Events) == 0 {
				rec.StartBg = bg // opening state: seed the stage like our recorder does
			}
			rec.Events = append(rec.Events, recEvent{OffsetMs: cum, Kind: int(courtroom.EventBackground), Text: bg})
		case "MC":
			if song := pkt.Field(0); song != "" {
				if capReached() {
					truncated++
					continue
				}
				rec.Events = append(rec.Events, recEvent{OffsetMs: cum, Kind: int(courtroom.EventMusic), Text: song})
			} else {
				skipped++
			}
		default:
			skipped++
		}
	}
	// Zero-events error rides the post-loop reality: truncation only fires once
	// the scene already holds maxRecordedEvents (>0), so it can never make this
	// spuriously trip — it still means "nothing playable in the whole file."
	if len(rec.Events) == 0 {
		return nil, skipped, truncated, fmt.Errorf("no playable events (MS/BN/MC) in the demo")
	}
	rec.RecordedAt = time.Now().Format(time.RFC3339)
	return rec, skipped, truncated, nil
}

// atoiClamped parses n with AO tolerance (garbage = lo) and clamps to [lo, hi].
func atoiClamped(s string, lo, hi int) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n < lo {
		return lo
	}
	if n > hi {
		return hi
	}
	return n
}

// recordingToDemo serializes a scene back into the AO2 .demo shape: a synthetic
// SC# from every character folder the scene uses (message speakers + pair
// partners, in appearance order), wait#<delta># between events from OffsetMs,
// and full server-shape MS lines (protocol.BuildServerMS) with char ids
// REMAPPED onto the synthetic SC — a demo is self-consistent, so AO2's RC
// handshake serves the right list.
func recordingToDemo(rec *sceneRecording) ([]byte, error) {
	if rec == nil || len(rec.Events) == 0 {
		return nil, fmt.Errorf("nothing to export")
	}
	scIdx := map[string]int{}
	var scList []string
	adopt := func(folder string) int {
		if folder == "" {
			return 0
		}
		if i, ok := scIdx[folder]; ok {
			return i
		}
		scIdx[folder] = len(scList)
		scList = append(scList, folder)
		return scIdx[folder]
	}
	for _, e := range rec.Events {
		if courtroom.EventKind(e.Kind) == courtroom.EventMessage && e.Message != nil {
			adopt(e.Message.CharName)
			if e.Message.Pair.Name != "" {
				adopt(e.Message.Pair.Name)
			}
		}
	}
	if len(scList) == 0 {
		scList = []string{"Narrator"} // a bg/music-only scene still needs a non-empty SC
	}

	var b strings.Builder
	b.WriteString(protocol.NewPacket("SC", scList...).String())
	if rec.StartBg != "" {
		b.WriteString("\n")
		b.WriteString(protocol.NewPacket("BN", rec.StartBg).String())
	}
	prev := 0
	for _, e := range rec.Events {
		if d := e.OffsetMs - prev; d > 0 {
			b.WriteString("\n")
			b.WriteString(protocol.NewPacket("wait", strconv.Itoa(d)).String())
		}
		if e.OffsetMs > prev {
			prev = e.OffsetMs
		}
		switch courtroom.EventKind(e.Kind) {
		case courtroom.EventMessage:
			if e.Message == nil {
				continue
			}
			m := *e.Message // remap ids on a copy; the scene stays untouched
			m.CharID = adopt(m.CharName)
			if m.Pair.Name != "" {
				m.Pair.CharID = adopt(m.Pair.Name)
			}
			b.WriteString("\n")
			b.WriteString(protocol.BuildServerMS(&m).String())
		case courtroom.EventBackground:
			b.WriteString("\n")
			b.WriteString(protocol.NewPacket("BN", e.Text).String())
		case courtroom.EventMusic:
			b.WriteString("\n")
			b.WriteString(protocol.NewPacket("MC", e.Text, "0").String())
		}
	}
	b.WriteString("\n")
	return []byte(b.String()), nil
}

// demoDefaultOrigin picks the asset host an imported demo streams from: the
// current URL builder's origin (the live session's when connected; "" offline
// is fine — set one in the Scene Maker afterwards, like a new scene).
func (a *App) demoDefaultOrigin() string {
	return a.urls.Origin()
}

// loadRecordingAny opens a recording by extension: our .aorec JSON, or an AO2
// .demo (converted on the fly — same model, so Play/Edit/Export all work).
func (a *App) loadRecordingAny(path string) (*sceneRecording, error) {
	if !strings.EqualFold(filepath.Ext(path), demoExt) {
		return loadRecording(path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	rec, skipped, truncated, err := demoToRecording(data, a.demoDefaultOrigin())
	if err != nil {
		return nil, err
	}
	// Two independent clauses (a demo can trip either, both, or neither): the
	// skipped wording stays byte-identical to before, and truncation is its own
	// distinct note. Kept short/factual, no exclamation — the settings-text style.
	base := filepath.Base(path)
	switch {
	case skipped > 0 && truncated > 0:
		a.pushDebug(fmt.Sprintf("demo import: %s — %d non-scene packets skipped (SC/CT/HP/…); stopped at the %d-event scene cap (%d later events not imported)", base, skipped, maxRecordedEvents, truncated))
	case skipped > 0:
		a.pushDebug(fmt.Sprintf("demo import: %s — %d non-scene packets skipped (SC/CT/HP/…)", base, skipped))
	case truncated > 0:
		a.pushDebug(fmt.Sprintf("demo import: %s — stopped at the %d-event scene cap (%d later events not imported)", base, maxRecordedEvents, truncated))
	}
	// A .demo records only bare asset names — never WHERE the server's assets live.
	// rec.Origin was just filled from the current session (demoDefaultOrigin), so an
	// import with no session gets "" and every music/SFX/sprite URL is unfetchable:
	// the demo plays SILENT with a blank stage and nothing says why (live report,
	// v1.72.0). Warn honestly, with the two real remedies. warnLine is visible on
	// Settings since the picker-hotfix banner fix.
	if strings.TrimSpace(rec.Origin) == "" {
		a.warnLine = "Imported " + base + " without a server — music and sprites won't stream. Connect to the demo's server first, or set Origin/CDN in the Scene Maker."
		a.warnAt = time.Now()
	}
	return rec, nil
}

// nextRecordingDest returns a non-colliding destination path under dir for a
// file named base+ext: base+ext if free, else base-2+ext, base-3+ext, … (the
// exact "-2"/"-3" collision walk HandleFileDrop has always used). Pure over the
// filesystem's current state so it unit-tests directly.
func nextRecordingDest(dir, base, ext string) string {
	cand := filepath.Join(dir, base+ext)
	for n := 2; ; n++ {
		if _, err := os.Stat(cand); os.IsNotExist(err) {
			return cand
		}
		cand = filepath.Join(dir, fmt.Sprintf("%s-%d%s", base, n, ext))
	}
}

// importDroppedRecording copies a dropped .aorec/.demo into recordings\ (keeping
// the name; "-2", "-3", … on collision — nextRecordingDest) so it joins the
// Studio list, and returns the path to feed downstream (Play or Export). A file
// already inside recordings\ is used in place. One-off user action on the event
// loop (like the Studio picker's reads), not a render-path I/O.
func importDroppedRecording(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	dir := recordingsDir()
	if dir == "" || filepath.Dir(path) == dir {
		return path // no recordings\ dir, or it's already there — use in place
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return path
	}
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	cand := nextRecordingDest(dir, base, ext)
	if data, err := os.ReadFile(path); err == nil && os.WriteFile(cand, data, 0o644) == nil {
		return cand // imported: it now lives in the Studio list too
	}
	return path
}

// HandleFileDrop imports a file dropped onto the window (#73): a .aorec or an
// AO2 .demo is copied into recordings\ (importDroppedRecording) and then, by
// default, starts playing immediately — share a recording by literally dragging
// the file onto someone's AsyncAO. Anything else is politely ignored.
//
// Screen-aware routing: when the Settings screen's Studio tab is showing, the
// drop is treated as a ".demo → video" request (the dedicated call-out lives
// there) and routes to the video-export flow instead of playback — so a user who
// opened Studio to make a video gets one from a drop, with a confirming warnLine.
// Everywhere else the behavior is unchanged (import + play). This is the SINGLE
// owner of dropped recordings: the Settings-screen c.dropped consumer skips them.
func (a *App) HandleFileDrop(path string) {
	ext := strings.ToLower(filepath.Ext(path))
	if ext != recordingExt && ext != demoExt {
		a.warnLine = "Dropped file isn't a recording (.aorec) or an AO2 demo (.demo) — ignored."
		a.warnAt = time.Now()
		return
	}
	dest := importDroppedRecording(path)
	if a.Screen() == ScreenSettings && settings.tab == tabStudio {
		a.importRecordingToVideo(dest) // Studio's dedicated .demo → video entry point
		return
	}
	a.replayFromPath(dest)
}

// importRecordingToVideo is the shared tail of both Studio entry points (the
// native picker and a drop onto the Studio tab): confirm the import, then start
// the video export for it. The warnLine is set BEFORE sceneExportFromPath so
// that path's own ffmpeg-refusal message (gifexport.go) wins on a box without
// ffmpeg — the file still imported into the Recordings list either way.
// "Imported into recordings\" is only claimed when the file really landed
// there — importDroppedRecording falls back to the original path when the
// copy fails (or the file already lives there), and the note must not lie.
func (a *App) importRecordingToVideo(dest string) {
	if filepath.Dir(dest) == recordingsDir() {
		a.warnLine = "Imported " + filepath.Base(dest) + " into recordings\\ — exporting video…"
	} else {
		a.warnLine = "Exporting video from " + filepath.Base(dest) + "…"
	}
	a.warnAt = time.Now()
	a.sceneExportFromPath(dest, exportVideo)
}

// makerExportDemo writes the maker's scene as recordings\<stem>.demo — the AO2
// interchange shape (makerSave's .demo sibling; same never-overwrite policy,
// same off-thread write).
func (a *App) makerExportDemo() {
	if a.makerScene == nil || len(a.makerScene.Events) == 0 {
		a.warnLine = "Nothing to export yet — add a line first."
		a.warnAt = time.Now()
		return
	}
	data, err := recordingToDemo(a.makerScene)
	if err != nil {
		a.warnLine = "Demo export failed: " + err.Error()
		a.warnAt = time.Now()
		return
	}
	name := sanitizeStem(a.makerName) + "-" + time.Now().Format("20060102-150405") + demoExt
	go func() {
		dir := recordingsDir()
		if dir == "" {
			return
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return
		}
		_ = os.WriteFile(filepath.Join(dir, name), data, 0o644)
	}()
	a.warnLine = "AO2 demo saved: recordings\\" + name + " — plays in AO2's demo player too."
	a.warnAt = time.Now()
}
