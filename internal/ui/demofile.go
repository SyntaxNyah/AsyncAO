package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/helper/parse"
	"github.com/SyntaxNyah/AsyncAO/internal/helper/value"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
)

// AO2 .demo compatibility: read the demo files AO2's recorder writes (replay, Scene Maker, GIF/WebP/Video/Comic
// export) and write our recordings back out as .demo.
// The format (../AO2-Client/src/demoserver.cpp) is a text file of raw server-to-client packets, one per line (a
// packet may span lines; the loader joins until the line ends with '%'), with "wait#<ms>#%" packets for timing and
// usually an "SC#…#%" char list first. Pre-2.9.1 files have the wait-desync bug (AO2 PR #496): AO2 detects
// "starts with SC, ends with wait" and shifts every wait one position earlier, and we mirror that in memory only.
const (
	demoExt = ".demo"
	// demoMaxWaitMs caps one imported gap, like the demo server's /max_wait. It only affects OffsetMs metadata
	// (timeline/exports); replay is feed-on-idle and never sleeps on it.
	demoMaxWaitMs = 3000
)

// parseDemoRecords splits a .demo into packet records, joining continuation lines until a record ends with '%'
// (demoserver.cpp::load_demo). CRLF is tolerated; blank tails drop.
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

// fixDemoWaitDesync applies AO2's pre-2.9.1 repair when the file shows the desync signature (SC first and wait
// last): every wait packet moves one position earlier, as the queue walk in demoserver.cpp does.
func fixDemoWaitDesync(records []string) []string {
	if len(records) < 2 || !strings.HasPrefix(records[0], "SC#") || !strings.HasPrefix(records[len(records)-1], "wait#") {
		return records
	}
	out := make([]string, 0, len(records))
	for _, r := range records {
		if !strings.HasPrefix(r, "SC#") && strings.HasPrefix(r, "wait#") {
			at := len(out) - 1
			at = value.Clamp(at, 1, len(out))
			out = append(out, "")
			copy(out[at+1:], out[at:])
			out[at] = r
			continue
		}
		out = append(out, r)
	}
	return out
}

// demoToRecording converts a .demo into our replay model: MS to message events, BN to background, MC to music,
// waits to cumulative OffsetMs (each gap capped at demoMaxWaitMs). Other packets (SC/CT/HP/TI/LE/…) are counted and
// skipped. origin is the asset host to stream from (demos store none).
// It returns two counters: skipped (recognised packets we don't model) and truncated (scene packets dropped
// because the scene already holds maxRecordedEvents), so the user can tell chatter from an over-long demo.
func demoToRecording(data []byte, origin string) (rec *sceneRecording, skipped, truncated int, err error) {
	records := fixDemoWaitDesync(parseDemoRecords(data))
	if len(records) == 0 {
		return nil, 0, 0, fmt.Errorf("empty demo file")
	}
	// Demos come from 2.8+ clients on servers with the full feature set, so extended fields parse. custom_blips is
	// named explicitly because indices 30/31 are the blipname/slide pair only under that feature (ParseMS's getBlips,
	// courtroom.cpp:4251), and protocol.BuildServerMS, which defines this format, always lays them out that way.
	// A demo from a KFO-family server puts a "triplex" third_charid ("-1") at index 30; courtroom.validBlipName
	// guards that.
	features := protocol.ParseFeatures([]string{protocol.FeatureCCCCIC, protocol.FeatureCustomBlips})
	rec = &sceneRecording{Version: recordingVersion, Origin: origin}
	cum := 0
	// capReached: the scene already holds maxRecordedEvents. We keep a coherent prefix and stop at the cap (as
	// recordEvent, replay.go:155, and makerInsert, scenemaker.go:436), not the instant-replay ring's oldest eviction:
	// OffsetMs is cumulative, so only a leading run stays timeline-consistent. maxRecordedEvents is the same ceiling
	// every ingestion path uses and admits a whole real session. Video export length is bounded separately by
	// maxVideoHours.
	capReached := func() bool { return len(rec.Events) >= maxRecordedEvents }
	for _, raw := range records {
		pkt, err := protocol.ParsePacket(strings.TrimSuffix(raw, "\n"))
		if err != nil {
			skipped++
			continue
		}
		switch pkt.Header {
		case "wait":
			d := value.Clamp(parse.AtoiOr(pkt.Field(0), 0), 0, demoMaxWaitMs)
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
	// Truncation only fires once the scene already holds maxRecordedEvents, so it can't trip this error: it means
	// nothing playable in the whole file.
	if len(rec.Events) == 0 {
		return nil, skipped, truncated, fmt.Errorf("no playable events (MS/BN/MC) in the demo")
	}
	rec.RecordedAt = time.Now().Format(time.RFC3339)
	return rec, skipped, truncated, nil
}

// recordingToDemo serializes a scene back into the .demo shape: a synthetic SC# from every character folder the
// scene uses (speakers and pair partners, in appearance order), wait#<delta># between events, and server-shape MS
// lines (protocol.BuildServerMS) with char ids remapped onto that SC so AO2's RC handshake serves the right list.
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

// mountOrigin returns the local:// origin the configured local-asset mounts resolve against, or "" when no mounts
// are set. It keys off mounts being configured, not the legacy "read from local folders" flag: the streaming
// Manager consults a local:// overlay built from the same mounts. The origin string comes from the canonical
// local-origin helper (a hash of the ordered mount set), so it equals the origin the production overlay and a
// local-mode source serve. It asks for the string rather than building a fetcher, which carries the rule-6 memo.
func (a *App) mountOrigin() string {
	if _, mounts := a.localAssets(); len(mounts) > 0 {
		return assets.LocalOriginFor(mounts)
	}
	return ""
}

// demoDefaultOrigin picks the asset host an imported .demo streams from. With local-asset mounts configured it is
// the mounts' local:// origin, so the demo resolves against the local base folder (AO2 parity) even in a
// streaming session, and sidesteps a dead recorded server (a .demo stores none). With no mounts it is the current
// URL builder's origin; "" offline is fine, the empty-origin warning fires and the user can set one in the Scene
// Maker. Only .demo import is steered here: loadRecordingForExport ignores this for a .aorec, which carries its
// own Origin.
func (a *App) demoDefaultOrigin() string {
	if m := a.mountOrigin(); m != "" {
		return m // mounts configured: resolve the demo against the local base (AO2 parity)
	}
	return a.urls.Origin()
}

// loadRecordingAny opens a recording by extension: our .aorec JSON, or an AO2 .demo converted on the fly. It
// emits the import notes as side effects, so it must run on the render thread. The async export path
// (loadRecordingForExport) uses the pure core and replays the notes when the result lands.
func (a *App) loadRecordingAny(path string) (*sceneRecording, error) {
	res := loadRecordingForExport(path, a.demoDefaultOrigin())
	if res.err != nil {
		return nil, res.err
	}
	a.emitLoadNotes(res)
	return res.rec, nil
}

// loadRecordingForExport is the pure disk-load and parse for a recording path (.aorec JSON, or .demo converted on
// the fly). It does no SDL or App mutation, so it is safe off the render thread. origin is resolved on the render
// thread and passed in. The render thread replays res.debug / res.warnMsg via emitLoadNotes.
func loadRecordingForExport(path, origin string) gifLoadResult {
	if !strings.EqualFold(filepath.Ext(path), demoExt) {
		rec, err := loadRecording(path)
		return gifLoadResult{rec: rec, err: err}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return gifLoadResult{err: err}
	}
	rec, skipped, truncated, err := demoToRecording(data, origin)
	if err != nil {
		return gifLoadResult{err: err}
	}
	res := gifLoadResult{rec: rec}
	// A demo can trip skipped, truncated, both or neither; each gets its own short factual note.
	base := filepath.Base(path)
	switch {
	case skipped > 0 && truncated > 0:
		res.debug = append(res.debug, fmt.Sprintf("demo import: %s — %d non-scene packets skipped (SC/CT/HP/…); stopped at the %d-event scene cap (%d later events not imported)", base, skipped, maxRecordedEvents, truncated))
	case skipped > 0:
		res.debug = append(res.debug, fmt.Sprintf("demo import: %s — %d non-scene packets skipped (SC/CT/HP/…)", base, skipped))
	case truncated > 0:
		res.debug = append(res.debug, fmt.Sprintf("demo import: %s — stopped at the %d-event scene cap (%d later events not imported)", base, maxRecordedEvents, truncated))
	}
	// This branch is .demo-only. A .demo stores bare asset names, never where the assets live. rec.Origin was just
	// filled by demoDefaultOrigin, so the note reflects the source it will stream from, keyed on the origin's scheme.
	// A local:// origin means the configured mounts (AO2 parity): a positive note, no warning. An empty origin (no
	// mounts, no session) means every URL is unfetchable and the demo would play silent with a blank stage, so warn
	// with the two real remedies. Mounts suppress that warning because a local:// origin is non-empty.
	switch {
	case strings.HasPrefix(rec.Origin, assets.LocalScheme):
		res.warnMsg = "Imported " + base + " — resolving from your local assets. Anything not in your mounts shows in the content report."
	case strings.TrimSpace(rec.Origin) == "":
		res.warnMsg = "Imported " + base + " without a server — music and sprites won't stream. Connect to the demo's server first, or set Origin/CDN in the Scene Maker."
	}
	return res
}

// emitLoadNotes replays a loader's notes on the render thread: pushDebug for the skipped/truncated lines, warnLine
// for the empty-origin warning.
func (a *App) emitLoadNotes(res gifLoadResult) {
	for _, line := range res.debug {
		a.pushDebug(line)
	}
	if res.warnMsg != "" {
		a.warnLine = res.warnMsg
		a.warnAt = time.Now()
	}
}

// nextRecordingDest returns a non-colliding destination under dir for base+ext: base+ext if free, else
// base-2+ext, base-3+ext, and so on. Pure over the filesystem's current state.
func nextRecordingDest(dir, base, ext string) string {
	cand := filepath.Join(dir, base+ext)
	for n := 2; ; n++ {
		if _, err := os.Stat(cand); os.IsNotExist(err) {
			return cand
		}
		cand = filepath.Join(dir, fmt.Sprintf("%s-%d%s", base, n, ext))
	}
}

// importDroppedRecording copies a dropped .aorec/.demo into recordings\ (renaming on collision) so it joins the
// Studio list, and returns the path to feed downstream. A file already in recordings\ is used in place. A one-off
// user action, not render-path I/O.
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

// HandleFileDrop imports a file dropped onto the window: a .aorec or AO2 .demo is copied into recordings\ and then
// starts playing. Anything else is ignored. When the Settings Studio tab is showing, the drop is a ".demo to video"
// request and routes to importRecordingToVideo instead of playback. This is the single owner of dropped
// recordings (the Settings c.dropped consumer skips them) and of dropped theme bundles via claimDroppedFile; see
// dropclaim.go.
func (a *App) HandleFileDrop(path string) {
	switch claimDroppedFile(path, settings.importArmed) {
	case dropClaimRecording:
		dest := importDroppedRecording(path)
		if a.Screen() == ScreenSettings && settings.tab == tabStudio {
			a.importRecordingToVideo(dest) // Studio's dedicated .demo → video entry point
			return
		}
		a.replayFromPath(dest)
	case dropClaimThemeBundle:
		a.handleThemeBundleDrop(path)
	case dropClaimThemeFont:
		a.handleThemeFontDrop(path)
	case dropClaimThemeImage:
		a.handleThemeImageDrop(path)
	case dropClaimSettingsImport:
		// The Settings screen armed this and consumes it in the same frame (importSettingsAsync); silence here is
		// right.
	default:
		a.handleUnclaimedDrop(path)
	}
}

// handleThemeFontDrop handles a dropped .ttf / .otf. The claim stops the file repointing the theme root at its
// folder. Installing the face means writing two keys into the user's asyncao_theme.ini, and the sidecar writer is
// tied to the editor's document and undo stack. So with the editor shut it names the folder and both keys, like
// handleThemeBundleDrop; a silent drop would be worse.
func (a *App) handleThemeFontDrop(path string) {
	// With the editor open the face installs: copied into the theme's fonts\ folder off-thread, then added to [fonts]
	// as one undoable op (editorTakeFontDrop). With the editor shut the message below tells the user what to do by
	// hand.
	if a.editorTakeFontDrop(path) {
		return
	}
	name := filepath.Base(path)
	line := "Font " + name + " — drop it in your theme's fonts\\ folder, then add it to [fonts] and point a " +
		"[fontbind] row at it (Settings → Theme → Open folder)."
	if dir := config.UserThemesDir(); dir != "" {
		line = "Font " + name + " — put it in <your theme>\\fonts\\ under " + dir +
			", then add it to [fonts] and point a [fontbind] row at it."
	}
	a.warnLine = line
	a.warnAt = time.Now()
}

// handleThemeImageDrop routes a dropped picture, like handleThemeFontDrop. With the editor open the picture is
// copied into the theme and declared as one undoable act (editorTakeImage, which owns the caps and refusals).
// With the editor shut there is no document or undo stack, so the drop says where the feature is. The claim also
// stops the file reaching the Settings theme-folder arm, which would repoint the theme root at its folder.
func (a *App) handleThemeImageDrop(path string) {
	if a.editorTakeImage(path) {
		return
	}
	a.warnLine = "Image " + filepath.Base(path) + " — open Settings ▸ Theme editor ▸ Open editor, " +
		"then drop it again (or use + Image on the element list) to add it to your theme."
	a.warnAt = time.Now()
}

// handleUnclaimedDrop is the global fallback. A dropped folder is the theme import the Settings screen owns (its
// c.dropped arm resolves it through normalizeThemeRoot), so this points the user at that screen instead of
// warning. One os.Stat on the event loop, never a render path (hard rule 2).
func (a *App) handleUnclaimedDrop(path string) {
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		if a.Screen() == ScreenSettings {
			return // the Settings screen consumes this drop itself, this frame
		}
		a.warnLine = "Dropped a folder — open Settings → Theme and drop it there to use it as a theme."
		a.warnAt = time.Now()
		return
	}
	a.warnLine = "Dropped file isn't a recording (.aorec), an AO2 demo (.demo) or a theme bundle (" + themePackExt + ") — ignored."
	a.warnAt = time.Now()
}

// importRecordingToVideo is the shared tail of both Studio entry points (the native picker and a drop onto the
// Studio tab): start the video export. sceneExportFromPath owns the on-screen banner from here: it posts
// "Reading …" synchronously, and startSceneExport's ffmpeg-refusal message still wins. No warnLine is set here
// because the export path overwrites it before any frame draws. The import happened upstream
// (importDroppedRecording), which falls back to the original path when the copy fails.
func (a *App) importRecordingToVideo(dest string) {
	a.sceneExportFromPath(dest, exportVideo)
}

// makerExportDemo writes the maker's scene as recordings\<stem>.demo (makerSave's .demo sibling; never overwrites,
// writes off-thread).
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
