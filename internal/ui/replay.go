package ui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/archive"
	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

// Scene recording: record the courtroom event stream, not pixels, to a small .aorec file to reopen and replay natively.
// The asset origin is stored so a replay fetches sprites and backgrounds over HTTP, independent of the game WebSocket.
// Editing manipulates this event list; only the opt-in export touches pixels.
const (
	recordingVersion = 1
	recordingExt     = ".aorec"
	// maxRecordedEvents bounds one recording (hard rule 4): recording and import stop accepting events past the cap,
	// keeping a coherent leading prefix. Sized to import a whole real session (the largest real fixture is under 9000
	// scene events). Video export streams to ffmpeg under its own maxVideoHours limit, so a big import is still fully
	// exportable.
	maxRecordedEvents = 50000

	// instantReplayMaxEvents bounds the rolling clip buffer (hard rule 4): past the cap the oldest events fall off the
	// ring.
	instantReplayMaxEvents = 5000

	// Replay pacing base at 100% playback speed. A replay is meant to be watched, so it is slower than live chat: a
	// relaxed crawl, then a linger to read the finished line. The ReplaySpeed pref scales both (lower percent =
	// slower).
	replayBaseCrawlMs = 55   // ms per character typed at 100%
	replayBaseStayMs  = 3200 // ms the finished line lingers at 100%
)

// recEvent is one recorded scene-mutating event: only the events Courtroom.HandleEvent acts on (message, background,
// music); the fields mirror courtroom.Event.
type recEvent struct {
	OffsetMs int                   `json:"offsetMs"` // wall-clock ms from record start
	Kind     int                   `json:"kind"`     // courtroom.EventKind
	Message  *protocol.ChatMessage `json:"message,omitempty"`
	Name     string                `json:"name,omitempty"`
	Text     string                `json:"text,omitempty"`
	Int      int                   `json:"int,omitempty"`
}

// sceneRecording is the serialized .aorec: the asset origin, the background at record start (so the first frames aren't
// blank) and the timed event stream.
type sceneRecording struct {
	Version    int        `json:"version"`
	Origin     string     `json:"origin"`
	StartBg    string     `json:"startBackground"`
	RecordedAt string     `json:"recordedAt"`
	Events     []recEvent `json:"events"`
	// Bundled marks a self-contained archive whose assets live in this file's folder (webAO layout); Formats maps
	// AssetType.Name() to ext to seed the resolver. Set only by the archive exporter; replay then plays from the folder
	// instead of a possibly dead Origin CDN.
	Bundled bool              `json:"bundled,omitempty"`
	Formats map[string]string `json:"formats,omitempty"`
}

// recordable reports whether an event mutates the scene: exactly the set Courtroom.HandleEvent acts on.
func recordable(k courtroom.EventKind) bool {
	return k == courtroom.EventMessage || k == courtroom.EventBackground || k == courtroom.EventMusic
}

// toggleRecording starts or stops scene recording (the Ctrl+W hotkey).
func (a *App) toggleRecording() {
	if a.recActive {
		a.stopRecording()
	} else {
		a.startRecording()
	}
}

// startRecording begins capturing the event stream, snapshotting the asset origin and background so a replay starts on
// the right scene.
func (a *App) startRecording() {
	if a.recActive {
		return
	}
	bg := ""
	if a.sess != nil {
		bg = a.sess.Background
	}
	a.rec = &sceneRecording{
		Version:    recordingVersion,
		Origin:     a.urls.Origin(),
		StartBg:    bg,
		RecordedAt: time.Now().Format(time.RFC3339),
	}
	a.recStart = time.Now()
	a.recActive = true
	a.warnLine = "● Recording scene — press the Record key again to stop & save"
	a.warnAt = time.Now()
}

// recEventFrom builds a recEvent from a live event, keeping the recording self-contained for sprite styles. The style
// marker is sent only on a change, so a clip starting mid-stream would lose the speaker's inherited style. When the
// message omits the marker but the speaker has a remembered style, record a copy with it re-injected; the live message
// is never mutated. Called before room.HandleEvent so RecalledStyle still holds the speaker's last style. The caller
// fills OffsetMs. Shared by the recorder and the instant-replay buffer.
func (a *App) recEventFrom(ev courtroom.Event) recEvent {
	// Gate on HasStyleMarker, not any zero-width marker: a line may carry a profile marker but no style marker and
	// still needs the style re-injected. DecodeSpriteStyle scans all frames, so appending after a profile marker
	// decodes fine.
	if ev.Kind == courtroom.EventMessage && ev.Message != nil && a.room != nil &&
		!courtroom.HasStyleMarker(ev.Message.Message) {
		if style := a.room.RecalledStyle(ev.Message.CharID); style.Active() {
			msgCopy := *ev.Message
			msgCopy.Message = ev.Message.Message + style.EncodeMarker()
			ev.Message = &msgCopy // record the self-contained copy, not the bare line
		}
	}
	return recEvent{
		Kind:    int(ev.Kind),
		Message: ev.Message,
		Name:    ev.Name,
		Text:    ev.Text,
		Int:     ev.Int,
	}
}

// recordEvent appends a scene-mutating event to the active recording (bounded).
func (a *App) recordEvent(ev courtroom.Event) {
	if a.rec == nil || !recordable(ev.Kind) || len(a.rec.Events) >= maxRecordedEvents {
		return
	}
	re := a.recEventFrom(ev)
	re.OffsetMs = int(time.Since(a.recStart).Milliseconds())
	a.rec.Events = append(a.rec.Events, re)
}

// replayBufEntry is one ring entry: capture time plus the event, so a clip can window by real elapsed time.
type replayBufEntry struct {
	at time.Time
	ev recEvent
}

// bufferReplayEvent stamps a scene-mutating event into the rolling clip buffer when the opt-in pref is on. It runs per
// incoming message, O(1) and allocation-free after the one-time ring allocation. The ring is released when the feature
// is off and reset when the asset origin changes, so a clip never mixes two servers' assets.
func (a *App) bufferReplayEvent(ev courtroom.Event) {
	if !a.d.Prefs.Load().InstantReplay {
		if a.replayBuf != nil { // turned off at runtime → free the ring
			a.replayBuf = nil
			a.replayBufN, a.replayBufW, a.replayBufOrigin = 0, 0, ""
		}
		return
	}
	if !recordable(ev.Kind) {
		return
	}
	origin := a.urls.Origin()
	if a.replayBuf == nil || a.replayBufOrigin != origin {
		a.replayBuf = make([]replayBufEntry, instantReplayMaxEvents)
		a.replayBufW, a.replayBufN, a.replayBufOrigin = 0, 0, origin
	}
	a.replayBuf[a.replayBufW] = replayBufEntry{at: time.Now(), ev: a.recEventFrom(ev)}
	a.replayBufW++
	if a.replayBufW == len(a.replayBuf) {
		a.replayBufW = 0
	}
	if a.replayBufN < len(a.replayBuf) {
		a.replayBufN++
	}
}

// linearizeReplayBuf copies the ring into an oldest-to-newest slice; only the clip key calls it.
func (a *App) linearizeReplayBuf() []replayBufEntry {
	n := a.replayBufN
	if n == 0 || len(a.replayBuf) == 0 {
		return nil
	}
	out := make([]replayBufEntry, 0, n)
	start := a.replayBufW - n
	if start < 0 {
		start += len(a.replayBuf)
	}
	for i := 0; i < n; i++ {
		out = append(out, a.replayBuf[(start+i)%len(a.replayBuf)])
	}
	return out
}

// buildClip assembles a sceneRecording from buffered entries captured within [cutoff, now], recomputing each OffsetMs
// from the first kept entry and carrying the last background and music change before the window so the clip isn't blank
// or silent. Pure (no ring, clock or SDL). entries must be oldest-first; returns nil when nothing falls in the window.
func buildClip(entries []replayBufEntry, cutoff time.Time, origin, sessBg string) *sceneRecording {
	start := -1
	for i := range entries {
		if !entries[i].at.Before(cutoff) {
			start = i
			break
		}
	}
	if start < 0 {
		return nil
	}
	// Scene context at the window start: the most recent background and music change before it.
	startBg, startMusic := sessBg, ""
	foundBg, foundMus := false, false
	for i := start - 1; i >= 0 && (!foundBg || !foundMus); i-- {
		switch courtroom.EventKind(entries[i].ev.Kind) {
		case courtroom.EventBackground:
			if !foundBg {
				startBg, foundBg = entries[i].ev.Text, true
			}
		case courtroom.EventMusic:
			if !foundMus {
				startMusic, foundMus = entries[i].ev.Text, true
			}
		}
	}
	base := entries[start].at
	evs := make([]recEvent, 0, len(entries)-start+1)
	if startMusic != "" { // no StartMusic field on .aorec — seed the playing track as the first event
		evs = append(evs, recEvent{Kind: int(courtroom.EventMusic), Text: startMusic})
	}
	for i := start; i < len(entries); i++ {
		e := cloneEvent(entries[i].ev) // own the message pointer; the clip outlives the ring slot
		e.OffsetMs = int(entries[i].at.Sub(base).Milliseconds())
		evs = append(evs, e)
	}
	return &sceneRecording{
		Version:    recordingVersion,
		Origin:     origin,
		StartBg:    startBg,
		RecordedAt: base.Format(time.RFC3339),
		Events:     evs,
	}
}

// clipInstantReplay saves the last capture window as a .aorec with no recording started in advance; saveSceneRecording
// defers the write off the render thread.
func (a *App) clipInstantReplay() {
	if !a.d.Prefs.Load().InstantReplay {
		a.warnLine = "Instant Replay is off — enable “Pre-record recent conversation” in Settings → Studio first."
		a.warnAt = time.Now()
		return
	}
	if a.replayBufN == 0 {
		a.warnLine = "Nothing captured yet — Instant Replay needs some conversation to clip."
		a.warnAt = time.Now()
		return
	}
	window := time.Duration(a.d.Prefs.Load().InstantReplaySecondsValue()) * time.Second
	bg := ""
	if a.sess != nil {
		bg = a.sess.Background
	}
	now := time.Now()
	clip := buildClip(a.linearizeReplayBuf(), now.Add(-window), a.replayBufOrigin, bg)
	if clip == nil || len(clip.Events) == 0 {
		a.warnLine = "Nothing in the last " + formatReplayWindow(window) + " to clip."
		a.warnAt = time.Now()
		return
	}
	stem := "asyncao-clip-" + now.Format("20060102-150405")
	name, _, err := saveSceneRecording(clip, stem)
	if err != nil {
		a.pushDebug("instant replay: " + err.Error())
		a.warnLine = "Clip failed: " + err.Error()
		a.warnAt = time.Now()
		return
	}
	a.warnLine = fmt.Sprintf("📎 Clipped the last %s (%d events): recordings\\%s — open it in the Scene Maker to trim/export.",
		formatReplayWindow(window), len(clip.Events), name)
	a.warnAt = time.Now()
}

// formatReplayWindow renders a capture window for a toast/readout: 45s, 1m, 1m30s.
func formatReplayWindow(d time.Duration) string {
	s := int(d.Seconds())
	switch {
	case s < 60:
		return fmt.Sprintf("%ds", s)
	case s%60 == 0:
		return fmt.Sprintf("%dm", s/60)
	default:
		return fmt.Sprintf("%dm%02ds", s/60, s%60)
	}
}

// stopRecording ends capture and writes the .aorec under recordings\ off the render thread (hard rule 2). It returns a
// channel closed when the write finishes or gives up, or nil if nothing was written.
func (a *App) stopRecording() <-chan struct{} {
	if !a.recActive {
		return nil
	}
	a.recActive = false
	rec := a.rec
	a.rec = nil
	if rec == nil || len(rec.Events) == 0 {
		a.warnLine = "Recording discarded — no scene happened while recording."
		a.warnAt = time.Now()
		return nil
	}
	stamp := time.Now().Format("20060102-150405")
	stem := "asyncao-" + stamp
	name, done, err := saveSceneRecording(rec, stem)
	if err != nil {
		a.pushDebug("recording: " + err.Error())
		return nil
	}
	a.warnLine = "Recording saved (" + strconv.Itoa(len(rec.Events)) + " events): recordings\\" + name
	a.warnAt = time.Now()

	// "Recordings keep their assets" (default on): the scene was rendered live so its assets are warm in the cache;
	// package them into a bundle folder beside the .aorec so the recording survives its CDN going dark. This hooks
	// after the async write and only starts the single-flight content job the Studio buttons run; the work happens
	// off-thread. It skips an offline recording (no origin) or a running content job. The "Recording packaged" toast
	// lands when the build finishes (tickContentJob).
	if a.d.Prefs.Load().RecordingsKeepAssets {
		a.startAutoBundle(rec, stem)
	}
	return done
}

// saveSceneRecording marshals a scene to hand-editable JSON and writes recordings\<stem>.aorec off the render thread
// (hard rule 2); only the WriteFile is deferred. It returns the filename and a channel closed when the write finishes
// or gives up. Shared by stopRecording and the scene maker's Save.
func saveSceneRecording(rec *sceneRecording, stem string) (string, <-chan struct{}, error) {
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return "", nil, err
	}
	name := stem + recordingExt
	done := make(chan struct{})
	go func() {
		defer close(done)
		dir := recordingsDir()
		if dir == "" {
			return
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return
		}
		_ = os.WriteFile(filepath.Join(dir, name), data, 0o644)
	}()
	return name, done, nil
}

// eventFromRec reconstructs the courtroom event a recorded entry stands for.
func eventFromRec(e recEvent) courtroom.Event {
	ev := courtroom.Event{
		Kind:    courtroom.EventKind(e.Kind),
		Message: e.Message,
		Name:    e.Name,
		Text:    e.Text,
		Int:     e.Int,
	}
	// .aorec doesn't record the 2.9 MC loop/effect flags, so a replayed music event would play once. Replay has always
	// looped its soundtrack: default recorded music to loop-forever (no format change).
	if ev.Kind == courtroom.EventMusic {
		ev.Loop = true
	}
	return ev
}

// loadRecording reads and parses a .aorec file.
func loadRecording(path string) (*sceneRecording, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var rec sceneRecording
	if err := json.Unmarshal(data, &rec); err != nil {
		return nil, err
	}
	// A hand-edited .aorec must not smuggle an over-cap scene past the guards (hard rule 4): enforce the same
	// maxRecordedEvents ceiling as the recorder and demo import, keeping the leading prefix (OffsetMs is cumulative).
	if len(rec.Events) > maxRecordedEvents {
		rec.Events = rec.Events[:maxRecordedEvents]
	}
	return &rec, nil
}

// recordingsDir is recordings\ in the exe data dir (created on demand).
func recordingsDir() string {
	root, err := exeDataDir()
	if err != nil {
		return ""
	}
	return filepath.Join(root, "recordings")
}

// recordingFile is one saved replay in the picker.
type recordingFile struct {
	name string
	path string
}

// listRecordings returns the .aorec files under recordings\, newest first.
func listRecordings() []recordingFile {
	dir := recordingsDir()
	if dir == "" {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	type withMod struct {
		recordingFile
		mod time.Time
	}
	var all []withMod
	for _, en := range entries {
		// .aorec is ours; .demo is AO2's recorder format, converted on the fly (demofile.go).
		if en.IsDir() || !(strings.HasSuffix(en.Name(), recordingExt) || strings.HasSuffix(strings.ToLower(en.Name()), demoExt)) {
			continue
		}
		info, err := en.Info()
		if err != nil {
			continue
		}
		all = append(all, withMod{recordingFile{en.Name(), filepath.Join(dir, en.Name())}, info.ModTime()})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].mod.After(all[j].mod) })
	out := make([]recordingFile, len(all))
	for i, w := range all {
		out[i] = w.recordingFile
	}
	return out
}

// latestRecordingPath returns the newest .aorec under recordings\ ("" = none).
func latestRecordingPath() string {
	if rs := listRecordings(); len(rs) > 0 {
		return rs[0].path
	}
	return ""
}

// openRecordingsFolder ensures recordings\ exists and opens it in the OS file manager.
func (a *App) openRecordingsFolder() {
	dir := recordingsDir()
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		a.pushDebug("recordings folder: " + err.Error())
		return
	}
	a.openInFileManager(dir)
	a.warnLine = "Opened recordings folder: " + dir
	a.warnAt = time.Now()
}

// replayFromPath loads a specific .aorec and starts replaying it. The replay plays in an overlay over whatever screen
// you're on (drawReplayOverlay).
func (a *App) replayFromPath(path string) {
	if a.recActive {
		a.warnLine = "Stop recording first, then replay."
		a.warnAt = time.Now()
		return
	}
	rec, err := a.loadRecordingAny(path) // .aorec, or an AO2 .demo converted on the fly
	if err != nil {
		a.warnLine = "Couldn't load recording: " + err.Error()
		a.warnAt = time.Now()
		return
	}
	a.playRecording(rec, path)
}

// playRecording starts replaying a loaded recording. A bundled archive plays its assets from the .aorec's folder,
// CDN-free; a normal one streams from its Origin.
func (a *App) playRecording(rec *sceneRecording, path string) {
	if rec.Bundled {
		a.beginBundledReplay(rec, filepath.Dir(path))
	}
	a.startReplay(rec, filepath.Base(path))
}

// beginBundledReplay points the shared Manager at the archive folder (local:// asset URLs resolve from disk) and seeds
// the resolver with the archive's formats. rec.Origin is rewritten to the archive's local base so the courtroom builds
// matching URLs. It must be the shared Manager: its Decoded channel is the one the render Pump drains, so textures
// upload. Undone by endBundledReplay.
func (a *App) beginBundledReplay(rec *sceneRecording, dir string) {
	local := assets.NewLocalFetcher([]string{dir})
	rec.Origin = local.BaseURL()
	a.d.Manager.SetArchiveSource(local)
	archive.SeedFormats(a.d.Resolver, rec.Origin, rec.Formats)
	a.replayBundled = true
}

// endBundledReplay removes the archive source override (replay teardown).
func (a *App) endBundledReplay() {
	if a.replayBundled {
		a.d.Manager.ClearArchiveSource()
		a.replayBundled = false
	}
}

// toggleReplay starts replaying the most recent recording, or stops the current
// replay (the Ctrl+I hotkey). Recording and replay are mutually exclusive.
func (a *App) toggleReplay() {
	if a.replaying {
		a.stopReplay()
		return
	}
	if a.recActive {
		a.warnLine = "Stop recording first, then replay."
		a.warnAt = time.Now()
		return
	}
	path := latestRecordingPath()
	if path == "" {
		a.warnLine = "No recordings yet — press the Record key to capture a scene first."
		a.warnAt = time.Now()
		return
	}
	rec, err := a.loadRecordingAny(path) // .aorec, or an AO2 .demo converted on the fly
	if err != nil {
		a.warnLine = "Couldn't load recording: " + err.Error()
		a.warnAt = time.Now()
		return
	}
	a.playRecording(rec, path)
}

// replayTiming maps the ReplaySpeed pref (percent, 100 = base) to the typewriter crawl and linger; lower percent slows
// both. Re-applied every frame in driveReplay so the slider is live: the new crawl takes the next typed message and the
// new linger the next settle (Typewriter precomputes per-rune delays at Start).
func (a *App) replayTiming() (crawl, stay time.Duration) {
	spd := a.d.Prefs.Load().ReplayPlaybackSpeed
	if spd < 1 {
		spd = 1 // defensive: the pref is clamped to ≥25, but never divide by zero
	}
	crawl = time.Duration(replayBaseCrawlMs*100/spd) * time.Millisecond
	stay = time.Duration(replayBaseStayMs*100/spd) * time.Millisecond
	return crawl, stay
}

// startReplay builds a throwaway courtroom on the recorded asset origin, seeds the starting background so the first
// frames aren't blank, and feeds events, paced by replayTiming.
func (a *App) startReplay(rec *sceneRecording, name string) {
	if rec == nil || len(rec.Events) == 0 {
		return
	}
	defer a.recoverReplay("start") // building the room / seeding must never crash the app
	// Constructed and wired through newRoom (newroom.go), so speakers blip, skin and flash as they do live, under the
	// viewer's own visual prefs.
	a.replayRoom = a.newRoom(courtroom.NewURLBuilder(rec.Origin), nil, a.d.Audio)
	a.replayRoom.Typewriter.Interval, a.replayRoom.TextStay = a.replayTiming() // slower than live + slider-driven
	a.replayRoom.CatchUp = false                                               // play every recorded line in full; the driver feeds one at a time
	// Post-construction override: a Preview launched from the maker is authoring and shows screenshake, flash and
	// overlays, while a normal replay honours the viewer's reduce-motion, screen-effects and sprite-style choices. The
	// master off-switch follows the same makerOpen exemption, or the maker would preview a lie. The gates come from
	// newroom.go so this mode answers like the export it previews.
	if a.makerOpen {
		a.applyAuthoringVisualsToRoom(a.replayRoom)
	}

	if a.d.Viewport != nil { // one-shot preanim completion must notify the REPLAY room now
		a.d.Viewport.OnPreanimDone = a.replayRoom.NotifyPreanimDone
		a.d.Viewport.OnFrameShown = a.replayRoom.NotifyFrameShown // #17: frame effects follow the room that owns the on-screen sprite
	}
	if rec.StartBg != "" {
		a.replayRoom.HandleEvent(courtroom.Event{Kind: courtroom.EventBackground, Text: rec.StartBg})
	}
	a.replayEvents = rec.Events
	a.replayIdx = 0
	a.replayName = name
	a.replayRec = rec                                  // kept so ⏮ Restart can rebuild from the top
	a.replayChapters = buildReplayChapters(rec.Events) // #70 the jump list (idempotent across restarts/jumps)
	a.replayPaused = false                             // a fresh replay starts playing
	a.replaying = true
	// A flat (streamed) recording pre-warms its sprites and music before the first event so nothing pops in or plays
	// silent. A bundled replay reads local files (beginBundledReplay already set replayBundled), so it skips the warm;
	// an empty or non-http origin skips too (startReplayWarm no-ops).
	a.endReplayWarm() // clear any prior phase (⏮ Restart re-enters here)
	if !a.replayBundled {
		a.startReplayWarm(rec)
	}
	a.warnLine = "▶ Replaying " + name + " — press the Replay key to stop"
	a.warnAt = time.Now()
}

// replayNext skips the current message to idle and feeds the next event; works while paused. During the pre-warm it
// means "start already": end the warm and let the first event feed next frame.
func (a *App) replayNext() {
	if a.replayRoom == nil {
		return
	}
	if a.replayWarming() {
		a.endReplayWarm()
		return
	}
	a.replayRoom.SkipToIdle()
	a.advanceReplay(0) // now idle → feed the next event immediately (or end if exhausted)
}

// replayTogglePause freezes or resumes playback (Next and Restart still work while paused).
func (a *App) replayTogglePause() { a.replayPaused = !a.replayPaused }

// replayRestart rebuilds the replay from the first event.
func (a *App) replayRestart() {
	if a.replayRec != nil {
		a.startReplay(a.replayRec, a.replayName)
	}
}

// replayMessagePos reports the current and total message count (ignoring bg/music events).
func (a *App) replayMessagePos() (cur, total int) {
	for i, e := range a.replayEvents {
		if courtroom.EventKind(e.Kind) != courtroom.EventMessage {
			continue
		}
		total++
		if i < a.replayIdx {
			cur++
		}
	}
	return cur, total
}

// advanceReplay feeds the next recorded event whenever the replay room returns to idle, so the courtroom's own pacing
// times playback, not the recorded wall-clock deltas, which would double-drag. When the stream is exhausted and the
// room settles, the replay ends.
func (a *App) advanceReplay(dt time.Duration) {
	if !a.replaying || a.replayRoom == nil {
		return
	}
	if a.replayWarming() {
		return // H5: hold every event off-stage until the pre-warm finishes / is skipped
	}
	if a.replayRoom.Phase() != courtroom.PhaseIdle || a.replayRoom.QueueLen() != 0 {
		return // a message is still on stage — let it finish
	}
	if a.replayIdx >= len(a.replayEvents) {
		a.stopReplay() // exhausted and idle: done
		return
	}
	a.replayRoom.HandleEvent(eventFromRec(a.replayEvents[a.replayIdx]))
	a.replayIdx++
}

// stopReplay tears down the replay and returns the stage to the live room.
func (a *App) stopReplay() {
	if !a.replaying {
		return
	}
	a.endBundledReplay() // drop the archive source override if this was a bundled replay
	a.endReplayWarm()    // H5: drop any in-flight pre-warm phase
	a.replaying = false
	a.replayRoom = nil
	a.replayEvents = nil
	a.replayIdx = 0
	a.replayName = ""
	a.replayPaused = false
	a.replayRec = nil
	a.replayChapters = nil
	a.replayChaptersOpen = false
	if a.d.Viewport != nil { // rebind preanim completion to the live room (or clear it)
		if a.room != nil {
			a.d.Viewport.OnPreanimDone = a.room.NotifyPreanimDone
			a.d.Viewport.OnFrameShown = a.room.NotifyFrameShown // #17: frame effects back to the live room
		} else {
			a.d.Viewport.OnPreanimDone = nil
			a.d.Viewport.OnFrameShown = nil
		}
	}
	a.warnLine = "Replay ended."
	a.warnAt = time.Now()
}

// recoverReplay turns a panic in the replay drive/render into a clean stop and a debug line: a replay is optional and a
// bad recording must not take the client down. The recovered value goes to the debug overlay.
func (a *App) recoverReplay(where string) {
	r := recover()
	if r == nil {
		return
	}
	a.pushDebug("replay " + where + " panic: " + fmt.Sprint(r))
	a.endBundledReplay()
	a.endReplayWarm() // H5: drop any in-flight pre-warm phase
	a.replaying = false
	a.replayRoom = nil
	a.replayEvents = nil
	a.replayIdx = 0
	a.replayName = ""
	a.replayPaused = false
	a.replayRec = nil
	a.replayChapters = nil
	a.replayChaptersOpen = false
	if a.d.Viewport != nil {
		if a.room != nil {
			a.d.Viewport.OnPreanimDone = a.room.NotifyPreanimDone
			a.d.Viewport.OnFrameShown = a.room.NotifyFrameShown // #17: frame effects back to the live room
		} else {
			a.d.Viewport.OnPreanimDone = nil
			a.d.Viewport.OnFrameShown = nil
		}
	}
	a.warnLine = "Replay stopped (error) — open the debug overlay for details."
	a.warnAt = time.Now()
}

// driveReplay advances the replay one frame (feed-on-idle, courtroom Update,
// viewport sync), wrapped so a panic stops the replay instead of crashing.
func (a *App) driveReplay(dt time.Duration) {
	defer a.recoverReplay("update")
	a.replayRoom.Typewriter.Interval, a.replayRoom.TextStay = a.replayTiming() // live Playback-speed slider
	// Pre-warm: while a flat recording's sprites and music load, hold the first event off-stage (advanceReplay skipped)
	// but still Update the room and viewport so the stage draws under the loading overlay. The phase self-ends or Skip
	// clears it; the next frame feeds the first event.
	if a.replayWarming() {
		a.tickReplayWarm()
		a.replayRoom.Update(dt)
		a.applySpriteOverridesTo(a.replayRoom) // the warm-up stage draws under the overlay — apply here too
		a.d.Viewport.SetSpriteFX(a.spriteFX())
		a.d.Viewport.SetPostFX(a.postFX())
		a.d.Viewport.Update(&a.replayRoom.Scene, dt)
		return
	}
	if a.replayPaused {
		return // ⏸ frozen — the overlay keeps drawing the last frame; Next/Restart still act
	}
	a.advanceReplay(dt)
	a.replayRoom.Update(dt)
	a.applySpriteOverridesTo(a.replayRoom) // a hidden sprite / hidden desk stays hidden in playback too
	a.d.Viewport.SetSpriteFX(a.spriteFX())
	a.d.Viewport.SetPostFX(a.postFX()) // #10 retro overlays in replay viewing
	a.d.Viewport.Update(&a.replayRoom.Scene, dt)
}

// drawStageRecordButton draws the optional on-stage control at the viewport's top-left: Stop replay while replaying,
// else the opt-in Record toggle (setting "Show a Record button").
func (a *App) drawStageRecordButton(vp sdl.Rect) {
	c := a.ctx
	if a.replaying {
		if c.Button(sdl.Rect{X: vp.X + 6, Y: vp.Y + 6, W: 104, H: 22}, "■ Stop replay") {
			a.stopReplay()
		}
		return
	}
	a.drawInstantReplayDot(vp) // armed-buffer indicator (independent of the Record button)
	if !a.d.Prefs.Load().ShowRecordButton {
		return
	}
	label := "● Record"
	if a.recActive {
		label = "■ Stop rec"
	}
	if c.Button(sdl.Rect{X: vp.X + 6, Y: vp.Y + 6, W: 92, H: 22}, label) {
		a.toggleRecording()
	}
}

// drawInstantReplayDot marks that the instant-replay buffer is armed: an accent dot at the stage's top-right, dim until
// hovered. Click it (or the clip key) to save the last window. Hidden during an explicit recording and unless the
// opt-in pref is on.
func (a *App) drawInstantReplayDot(vp sdl.Rect) {
	if a.recActive || !a.d.Prefs.Load().InstantReplay {
		return
	}
	c := a.ctx
	const sz = 18
	r := sdl.Rect{X: vp.X + vp.W - 6 - sz, Y: vp.Y + 6, W: sz, H: sz}
	hot := c.hovering(r)
	backA := uint8(70)
	if hot {
		backA = 150
	}
	c.Fill(r, sdl.Color{R: 0, G: 0, B: 0, A: backA}) // subtle backing so the dot reads on any background
	c.Border(r, ColPanelHi)
	col := ColAccent
	if !hot {
		col.A = 160 // dim until hovered — present but unobtrusive
	}
	c.Fill(sdl.Rect{X: r.X + sz/2 - 3, Y: r.Y + sz/2 - 3, W: 6, H: 6}, col)
	if hot {
		win := formatReplayWindow(time.Duration(a.d.Prefs.Load().InstantReplaySecondsValue()) * time.Second)
		c.Tooltip(r, "Instant Replay armed — click or press Ctrl+"+strings.ToUpper(a.hotkeyFor(hotkeyClipReplay))+" to clip the last "+win)
		if c.clicked {
			a.clipInstantReplay()
			c.clicked = false // consumed: don't let the viewport treat it as a sprite click
		}
	}
}

// drawReplayOverlay renders an in-progress replay as a full-window viewer, used when launched off the courtroom screen.
// The caller draws it instead of the underlying screen, so the Stop button owns the input; the courtroom screen renders
// the replay in its own stage.
func (a *App) drawReplayOverlay(w, h int32) {
	if a.replayRoom == nil {
		return
	}
	defer a.recoverReplay("render")
	c := a.ctx
	c.Fill(sdl.Rect{X: 0, Y: 0, W: w, H: h}, sdl.Color{R: 10, G: 10, B: 14, A: 255})
	// A 4:3 stage, centred, with a top strip for title + Stop and a bottom strip for the transport controls.
	stageH := h - 120
	stageW := stageH * 4 / 3
	if stageW > w-40 {
		stageW = w - 40
		stageH = stageW * 3 / 4
	}
	stage := sdl.Rect{X: (w - stageW) / 2, Y: 46, W: stageW, H: stageH}
	c.Fill(stage, sdl.Color{R: 0, G: 0, B: 0, A: 255})
	a.d.Viewport.Render(c.Ren, &a.replayRoom.Scene, stage)
	a.drawStageFrame(stage)               // #56: the viewer stage wears the same frame as live
	a.drawChatOverlay(stage, false, 0, 0) // M16: the spoken text (reads the replay scene via renderScene)
	a.drawReplayWarmBanner(stage)         // H5: "Loading scene…" + Skip while the FLAT-replay pre-warm runs
	c.Label(20, 16, "▶ Replaying — "+a.replayName, ColText)
	if c.Button(sdl.Rect{X: w - 136, Y: 12, W: 120, H: 26}, "■ Stop replay") {
		a.stopReplay()
	}
	a.drawReplayControls(stage, w)
	a.drawReplayChapters(stage, w) // #70: the jump list, right of the transport strip
}

// drawReplayWarmBanner draws the pre-warm progress line and a Skip button, low on the stage, while a flat recording's
// sprites and music load. A no-op once the phase ends. Skip plays with whatever has loaded; the rest streams on demand.
func (a *App) drawReplayWarmBanner(stage sdl.Rect) {
	w := a.replayWarm
	if w == nil {
		return
	}
	c := a.ctx
	total := len(w.warmRefs)
	strip := sdl.Rect{X: stage.X, Y: stage.Y + stage.H - 64, W: stage.W, H: 64}
	c.Fill(strip, sdl.Color{R: 10, G: 10, B: 16, A: 200})
	line := "Loading scene…"
	if w.seeding {
		line = "Checking server formats…"
	} else if total > 0 {
		line = fmt.Sprintf("Loading scene… %d / %d ready", w.resident, total)
	}
	c.Label(strip.X+12, strip.Y+10, line, ColText)
	bar := sdl.Rect{X: strip.X + 12, Y: strip.Y + 34, W: strip.W - 120, H: 10}
	c.Fill(bar, ColPanel)
	if total > 0 {
		fillW := int32(w.resident) * bar.W / int32(total)
		c.Fill(sdl.Rect{X: bar.X, Y: bar.Y, W: fillW, H: bar.H}, ColAccent)
	}
	if c.Button(sdl.Rect{X: strip.X + strip.W - 96, Y: strip.Y + 30, W: 84, H: 24}, "▶ Skip") {
		a.endReplayWarm() // play now with whatever has loaded; the rest streams on demand
	}
}

// drawReplayControls draws the transport strip below the stage: Restart, Pause/Play, Next message and a position
// readout.
func (a *App) drawReplayControls(stage sdl.Rect, w int32) {
	c := a.ctx
	y := stage.Y + stage.H + 14
	bx := stage.X
	if c.Button(sdl.Rect{X: bx, Y: y, W: 92, H: 28}, "⏮ Restart") {
		a.replayRestart()
		return // the room was rebuilt; don't touch it again this frame
	}
	bx += 100
	pp := "⏸ Pause"
	if a.replayPaused {
		pp = "▶ Play"
	}
	if c.Button(sdl.Rect{X: bx, Y: y, W: 86, H: 28}, pp) {
		a.replayTogglePause()
	}
	bx += 94
	if c.Button(sdl.Rect{X: bx, Y: y, W: 150, H: 28}, "⏭ Next message") {
		a.replayNext()
	}
	bx += 160
	cur, total := a.replayMessagePos()
	c.Label(bx, y+6, fmt.Sprintf("message %d / %d", cur, total), ColTextDim)
}
