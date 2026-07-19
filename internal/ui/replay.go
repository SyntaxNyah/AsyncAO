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

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/archive"
	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
)

// Scene recording (M16): record the courtroom EVENT stream — not pixels — to a
// tiny .aorec file you can reopen in AsyncAO and replay natively at perfect
// quality, with near-zero recording cost. The asset origin is stored so a
// replay fetches the same sprites/backgrounds over HTTP (independent of the
// game WebSocket), and it's the foundation the later phases (a replay player,
// an event-timeline editor, GIF/video export) build on: editing manipulates
// this lightweight event list, and only the opt-in export ever touches pixels.
const (
	recordingVersion = 1
	recordingExt     = ".aorec"
	// maxRecordedEvents bounds one recording (hard rule §17.4 — no unbounded
	// buffers): a runaway session can't balloon memory. Recording/import stops
	// accepting events past the cap (a coherent leading prefix is kept). 50000 is
	// sized to import a WHOLE real session: the largest real fixture is 8943 scene
	// events, so 50000 is ~5.6× that — full archives fit with room to spare. Memory
	// math: an MS event costs ~464 B of struct (the recEvent slot + its heap
	// *ChatMessage) + ~250 B of string backing ≈ 700 B avg (≈1 KB worst), so one
	// resident scene at 50000 events is ~35 MB, and the transient export/preview
	// second copy adds only ~23 MB (string bytes are shared across clones, only the
	// structs multiply) — well inside the 256 MiB budget. Video export length is NO
	// LONGER coupled to this: video streams to ffmpeg under its own maxVideoHours
	// wedge-brake, so a big import is fully exportable.
	maxRecordedEvents = 50000

	// instantReplayMaxEvents bounds the always-on rolling clip buffer (hard rule
	// §17.4): even a 1-hour window in a frantic room can't balloon memory — past
	// the cap the oldest captured events fall off the ring. Each entry is a
	// timestamp + a recEvent (which only holds a pointer to the already-parsed
	// message), so the whole ring is well under a megabyte.
	instantReplayMaxEvents = 5000

	// Replay pacing base (at 100% playback speed). A replay is meant to be
	// WATCHED, not skimmed, so it's deliberately slower than live chat: the
	// crawl is a relaxed reading cadence and the linger gives time to read the
	// whole finished line before the next event is fed. The ReplaySpeed pref
	// scales both — lower percent = slower (longer crawl + linger), higher =
	// faster — so the user can dial it from a slow cinematic to a quick recap.
	replayBaseCrawlMs = 55   // ms per character typed at 100%
	replayBaseStayMs  = 3200 // ms the finished line lingers at 100%
)

// recEvent is one recorded scene-mutating event. Only the three events that
// Courtroom.HandleEvent acts on are captured (message / background / music);
// the fields mirror courtroom.Event so a replay can reconstruct it exactly.
type recEvent struct {
	OffsetMs int                   `json:"offsetMs"` // wall-clock ms from record start
	Kind     int                   `json:"kind"`     // courtroom.EventKind
	Message  *protocol.ChatMessage `json:"message,omitempty"`
	Name     string                `json:"name,omitempty"`
	Text     string                `json:"text,omitempty"`
	Int      int                   `json:"int,omitempty"`
}

// sceneRecording is the serialized .aorec: the asset origin + the background at
// record-start (so the first frames render against the right scene, not a blank
// one) + the timed event stream.
type sceneRecording struct {
	Version    int        `json:"version"`
	Origin     string     `json:"origin"`
	StartBg    string     `json:"startBackground"`
	RecordedAt string     `json:"recordedAt"`
	Events     []recEvent `json:"events"`
	// Bundled marks a self-contained archive: the assets live in THIS file's
	// folder (webAO layout) and Formats maps AssetType.Name()→ext so replay can
	// seed the resolver. Set only by the archive exporter; replay reads them to
	// play from the folder instead of the (possibly dead) Origin CDN.
	Bundled bool              `json:"bundled,omitempty"`
	Formats map[string]string `json:"formats,omitempty"`
}

// recordable reports whether an event mutates the scene (so it belongs in a
// recording) — exactly the set Courtroom.HandleEvent acts on.
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

// startRecording begins capturing the scene event stream. Snapshots the asset
// origin + current background so a replay starts against the right scene.
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

// recEventFrom builds a recEvent from a live courtroom.Event, keeping the recording
// SELF-CONTAINED for sprite styles. Send-on-change transmits the style marker only on
// a CHANGE, so a no-marker message inherits the speaker's last style — which a clip
// that starts mid-stream (instant-replay, a cropped export) would otherwise lose. When
// the live message omits the marker but the speaker HAS a remembered style, record a
// COPY of the message with the marker re-injected; the live msg is never mutated (the
// room processes the original, and the recording shares that pointer for unstyled
// lines). Called BEFORE room.HandleEvent, so RecalledStyle still reflects this
// speaker's last style (a no-marker line doesn't change it). The caller fills OffsetMs
// (it owns the time base). Shared by the recorder and the instant-replay buffer.
func (a *App) recEventFrom(ev courtroom.Event) recEvent {
	// Gate on HasStyleMarker, not "any zero-width marker": a line may carry a #101
	// PROFILE marker but no style marker, and such a line from a styled speaker still
	// needs the style re-injected for a self-contained clip. DecodeSpriteStyle scans all
	// frames, so appending the style after an existing profile marker decodes fine.
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
// Called from the event loop for every event while recActive.
func (a *App) recordEvent(ev courtroom.Event) {
	if a.rec == nil || !recordable(ev.Kind) || len(a.rec.Events) >= maxRecordedEvents {
		return
	}
	re := a.recEventFrom(ev)
	re.OffsetMs = int(time.Since(a.recStart).Milliseconds())
	a.rec.Events = append(a.rec.Events, re)
}

// replayBufEntry is one ring slot for the instant-replay buffer: a wall-clock
// capture time plus the scene event, so a clip can window by real elapsed time.
type replayBufEntry struct {
	at time.Time
	ev recEvent
}

// bufferReplayEvent stamps a scene-mutating event into the rolling clip buffer
// when the opt-in pref is on. Called for EVERY game event from the event loop, so
// it stays O(1) and allocation-free after the one-time ring allocation (it runs
// per incoming message, never per render frame). The ring is released when the
// feature is off and reset when the asset origin changes — a clip must never mix
// two servers' assets (a long window can outlive a server switch).
func (a *App) bufferReplayEvent(ev courtroom.Event) {
	if !a.d.Prefs.InstantReplayOn() {
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

// linearizeReplayBuf copies the ring into a fresh oldest→newest slice. Only the
// clip key calls it (a rare user action), so the allocation is fine here.
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

// buildClip assembles a sceneRecording from the buffered entries whose capture
// time is within [cutoff, now], recomputing each OffsetMs from the first kept
// entry and carrying the scene context (the last background — and music — change
// BEFORE the window) so a mid-conversation clip isn't blank or silent. Pure: no
// ring, clock, or SDL, so it's tested directly. entries must be oldest-first.
// Returns nil when nothing falls in the window.
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
	// Scene context active at the window start: the most recent background and
	// music change BEFORE it, so the clip opens on the right stage and track.
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

// clipInstantReplay saves the last capture-window of conversation as a .aorec —
// the "save what just happened" key, with no recording started in advance. The
// write is deferred off the render thread by saveSceneRecording.
func (a *App) clipInstantReplay() {
	if !a.d.Prefs.InstantReplayOn() {
		a.warnLine = "Instant Replay is off — enable “Pre-record recent conversation” in Settings → Studio first."
		a.warnAt = time.Now()
		return
	}
	if a.replayBufN == 0 {
		a.warnLine = "Nothing captured yet — Instant Replay needs some conversation to clip."
		a.warnAt = time.Now()
		return
	}
	window := time.Duration(a.d.Prefs.InstantReplaySecondsValue()) * time.Second
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
	name, err := saveSceneRecording(clip, stem)
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

// stopRecording ends capture and writes the .aorec next to the exe under
// recordings\ off the render thread (§17.2: file I/O never blocks rendering).
func (a *App) stopRecording() {
	if !a.recActive {
		return
	}
	a.recActive = false
	rec := a.rec
	a.rec = nil
	if rec == nil || len(rec.Events) == 0 {
		a.warnLine = "Recording discarded — no scene happened while recording."
		a.warnAt = time.Now()
		return
	}
	stamp := time.Now().Format("20060102-150405")
	stem := "asyncao-" + stamp
	name, err := saveSceneRecording(rec, stem)
	if err != nil {
		a.pushDebug("recording: " + err.Error())
		return
	}
	a.warnLine = "Recording saved (" + strconv.Itoa(len(rec.Events)) + " events): recordings\\" + name
	a.warnAt = time.Now()

	// "Recordings keep their assets" (default ON): the scene we just recorded was
	// rendered live, so all its assets are already warm in the cache — package them
	// into a self-contained bundle folder beside the flat .aorec so the recording
	// survives its CDN going dark, at zero user effort. This hooks AFTER the async
	// .aorec write and is render-thread-cheap (it only STARTS the same single-flight
	// content job the Studio buttons run; the fetch/probe/write all happen off-thread
	// and never block or slow the stop). Skips silently on an offline recording (no
	// origin → nothing to package) or when a content job is already running. The
	// single "Recording packaged…" toast lands when the build finishes (tickContentJob).
	if a.d.Prefs.RecordingsKeepAssetsOn() {
		a.startAutoBundle(rec, stem)
	}
}

// saveSceneRecording marshals a scene to pretty-printed (hand-editable) JSON and
// writes it under recordings\ as <stem>.aorec OFF the render thread (§17.2: no
// synchronous disk I/O on the render path). The marshal is cheap and done on the
// caller; only the WriteFile is deferred. Returns the filename it will land as.
// Shared by recording (stopRecording) and the scene maker's Save so both produce
// identical, reopenable .aorec files.
func saveSceneRecording(rec *sceneRecording, stem string) (string, error) {
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return "", err
	}
	name := stem + recordingExt
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
	return name, nil
}

// --- M16 [2/2]: replay player ---

// eventFromRec reconstructs the courtroom event a recorded entry stands for.
func eventFromRec(e recEvent) courtroom.Event {
	ev := courtroom.Event{
		Kind:    courtroom.EventKind(e.Kind),
		Message: e.Message,
		Name:    e.Name,
		Text:    e.Text,
		Int:     e.Int,
	}
	// .aorec doesn't record the 2.9 MC loop/effect flags (#15), so a replayed
	// music event would carry the zero-value Loop=false and now PLAY ONCE. Replay
	// has always looped its soundtrack, so default recorded music to loop-forever
	// (no format change, no migration for existing recordings).
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
	// A hand-edited .aorec must not smuggle an over-cap scene past every guard
	// (hard rule §17.4): the file is the fifth ingestion path, so enforce the
	// same maxRecordedEvents ceiling the recorder and demo import hold — keep the
	// coherent leading prefix (OffsetMs is cumulative).
	if len(rec.Events) > maxRecordedEvents {
		rec.Events = rec.Events[:maxRecordedEvents]
	}
	return &rec, nil
}

// recordingsDir is recordings\ next to the exe (created on demand).
func recordingsDir() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	return filepath.Join(filepath.Dir(exe), "recordings")
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
		// .aorec is ours; .demo is AO2's recorder format (played/edited via the
		// on-the-fly converter in demofile.go — backwards compatibility).
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

// openRecordingsFolder makes sure the recordings\ folder exists (the default
// place .aorec files live) and opens it in the OS file manager, so saved
// recordings are easy to find and share.
func (a *App) openRecordingsFolder() {
	dir := recordingsDir()
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		a.pushDebug("recordings folder: " + err.Error())
		return
	}
	openInFileManager(dir) // windows→explorer / darwin→open / else→xdg-open (openpath.go)
	a.warnLine = "Opened recordings folder: " + dir
	a.warnAt = time.Now()
}

// replayFromPath loads a specific .aorec and starts replaying it (the picker
// entry point). The replay plays in an overlay over whatever screen you're on
// (drawReplayOverlay), so it works connected or from the lobby.
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

// playRecording starts replaying a loaded recording. A self-contained archive
// (Bundled) plays its assets from the .aorec's own folder, CDN-free; a normal
// recording streams from its Origin.
func (a *App) playRecording(rec *sceneRecording, path string) {
	if rec.Bundled {
		a.beginBundledReplay(rec, filepath.Dir(path))
	}
	a.startReplay(rec, filepath.Base(path))
}

// beginBundledReplay points the shared Manager at the archive folder (so the
// archive's local:// asset URLs resolve from disk, with the CDN gone) and seeds
// the resolver with the archive's bundled formats. rec.Origin is rewritten to
// the archive's local base so the courtroom builds matching URLs. The shared
// Manager is used deliberately — its Decoded channel is the one the render Pump
// drains, so textures actually upload. Undone by endBundledReplay.
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

// replayTiming maps the ReplaySpeed pref (percent, 100 = base) to the replay
// typewriter crawl + linger. 100% is the readable base; lower percent slows
// both (so the whole message plays out and can be read), higher speeds it up.
// Re-applied every frame in driveReplay so the Studio "Playback speed" slider
// is live — the new crawl takes the NEXT typed message, the new linger the next
// settle (Typewriter precomputes per-rune delays at Start; TextStay is read at
// linger), which is exactly when a mid-replay speed change should bite.
func (a *App) replayTiming() (crawl, stay time.Duration) {
	spd := a.d.Prefs.ReplaySpeed()
	if spd < 1 {
		spd = 1 // defensive: the pref is clamped to ≥25, but never divide by zero
	}
	crawl = time.Duration(replayBaseCrawlMs*100/spd) * time.Millisecond
	stay = time.Duration(replayBaseStayMs*100/spd) * time.Millisecond
	return crawl, stay
}

// startReplay spins up a throwaway courtroom pointed at the recorded asset
// origin (asset HTTP fetch is independent of the game WS), seeds the starting
// background so the first frames aren't blank, and begins feeding events. Paced
// by replayTiming (slower than live, scaled by the Playback-speed slider).
func (a *App) startReplay(rec *sceneRecording, name string) {
	if rec == nil || len(rec.Events) == 0 {
		return
	}
	defer a.recoverReplay("start") // building the room / seeding must never crash the app
	a.replayRoom = courtroom.NewCourtroom(courtroom.NewURLBuilder(rec.Origin), a.d.Manager, nil, a.d.Audio)
	a.wireRoomCharMeta(a.replayRoom)                                           // speakers blip/skin the same in replays
	a.replayRoom.Typewriter.Interval, a.replayRoom.TextStay = a.replayTiming() // slower than live + slider-driven
	a.replayRoom.CatchUp = false                                               // play every recorded line in full; the driver feeds one at a time
	// A maker Preview is authoring (show the scene's screenshake/flash); a normal
	// replay honours the viewer's reduce-motion accessibility pref.
	a.replayRoom.ReduceMotion = a.d.Prefs.ReduceMotion() && !a.makerOpen
	a.replayRoom.ForceCharNames = a.d.Prefs.ForceCharNamesOn()
	a.replayRoom.HideSpriteStyles = a.d.Prefs.HideSpriteStylesOn() && !a.makerOpen // maker preview shows styles; replay honours the viewer

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
	// H5: a FLAT (streamed) recording pre-warms its sprites + music before the first
	// event so nothing pops in or plays silent. A bundled replay reads local files
	// (already gap-free — beginBundledReplay ran first and set replayBundled), so it
	// skips the warm. An empty / non-http origin also skips (startReplayWarm no-ops).
	a.endReplayWarm() // clear any prior phase (⏮ Restart re-enters here)
	if !a.replayBundled {
		a.startReplayWarm(rec)
	}
	a.warnLine = "▶ Replaying " + name + " — press the Replay key to stop"
	a.warnAt = time.Now()
}

// --- replay player controls (drawReplayOverlay) ---

// replayNext skips the current message straight to idle and feeds the following
// event — the "fast-forward to the next line" button. Works while paused. While the
// H5 pre-warm is running, ⏭ Next means "start already": end the warm and let the
// first event feed next frame, rather than fast-forwarding a scene that hasn't begun.
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

// replayTogglePause freezes / resumes playback (Next + Restart still work while
// paused).
func (a *App) replayTogglePause() { a.replayPaused = !a.replayPaused }

// replayRestart rebuilds the replay from the first event.
func (a *App) replayRestart() {
	if a.replayRec != nil {
		a.startReplay(a.replayRec, a.replayName)
	}
}

// replayMessagePos reports the current / total MESSAGE count (ignoring bg/music
// events) for the player's position readout.
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

// advanceReplay feeds the next recorded event whenever the replay room returns
// to idle, so the courtroom's own pacing (typewriter / preanim / linger) times
// the playback — NOT the recorded wall-clock deltas, which would double-drag.
// When the stream is exhausted and the room settles, the replay ends.
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

// recoverReplay turns a panic anywhere in the replay drive/render into a clean
// stop + a debug line, instead of crashing the app — a replay is optional and a
// bad/edge recording must never take the client down. The recovered value names
// the cause (visible in the debug overlay) so the root bug can be pinned.
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
	// H5 pre-warm: while the FLAT recording's sprites + music load, hold the first
	// event off-stage (advanceReplay is skipped) so playback starts warm, not popping.
	// The room + viewport still Update so the stage (StartBg, once resident) draws
	// under the loading overlay. The phase self-ends (all-ready / quiesce / hard cap)
	// or the Skip button clears it; either way the next frame feeds the first event.
	if a.replayWarming() {
		a.tickReplayWarm()
		a.replayRoom.Update(dt)
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
	a.d.Viewport.SetSpriteFX(a.spriteFX())
	a.d.Viewport.SetPostFX(a.postFX()) // #10 retro overlays in replay viewing
	a.d.Viewport.Update(&a.replayRoom.Scene, dt)
}

// drawStageRecordButton draws the optional on-stage control at the top-left of
// the viewport. While replaying it's a Stop-replay button (so a replay is always
// easy to end on screen); otherwise it's the opt-in ● Record toggle, shown only
// when the "Show a Record button" setting is on.
func (a *App) drawStageRecordButton(vp sdl.Rect) {
	c := a.ctx
	if a.replaying {
		if c.Button(sdl.Rect{X: vp.X + 6, Y: vp.Y + 6, W: 104, H: 22}, "■ Stop replay") {
			a.stopReplay()
		}
		return
	}
	a.drawInstantReplayDot(vp) // armed-buffer indicator (independent of the Record button)
	if !a.d.Prefs.ShowRecordButtonOn() {
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

// drawInstantReplayDot marks that the instant-replay rolling buffer is ARMED — a
// small accent dot in the stage's top-right corner, dim until hovered, so the
// otherwise-invisible buffer is discoverable. Click it (or press the clip key) to
// save the last window. Hidden while an explicit recording is running (the Record
// button/toast already says so) and off entirely unless the opt-in pref is on, so
// the default client draws nothing here.
func (a *App) drawInstantReplayDot(vp sdl.Rect) {
	if a.recActive || !a.d.Prefs.InstantReplayOn() {
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
		win := formatReplayWindow(time.Duration(a.d.Prefs.InstantReplaySecondsValue()) * time.Second)
		c.Tooltip(r, "Instant Replay armed — click or press Ctrl+"+strings.ToUpper(a.hotkeyFor(hotkeyClipReplay))+" to clip the last "+win)
		if c.clicked {
			a.clipInstantReplay()
			c.clicked = false // consumed: don't let the viewport treat it as a sprite click
		}
	}
}

// drawReplayOverlay renders an in-progress replay as a full-window viewer — used
// when it was launched off the courtroom screen (lobby / Settings), so it can be
// watched in place. The caller draws it INSTEAD of the underlying screen, so the
// Stop button owns the input. The courtroom screen renders the replay in its own
// stage instead, so this isn't used there.
func (a *App) drawReplayOverlay(w, h int32) {
	if a.replayRoom == nil {
		return
	}
	defer a.recoverReplay("render")
	c := a.ctx
	c.Fill(sdl.Rect{X: 0, Y: 0, W: w, H: h}, sdl.Color{R: 10, G: 10, B: 14, A: 255})
	// A 4:3 stage, centred, leaving a top strip for the title + Stop and a bottom
	// strip for the player transport controls.
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

// drawReplayWarmBanner draws the H5 pre-warm progress line + a Skip button, centred
// low on the stage, while a FLAT recording's sprites/music load before the first
// event. A no-op once the phase ends (all-ready / quiesce / hard cap), so it simply
// vanishes when playback begins. Skip ends the warm immediately and plays with
// whatever has loaded (the rest streams in on demand, as before this phase existed).
func (a *App) drawReplayWarmBanner(stage sdl.Rect) {
	w := a.replayWarm
	if w == nil {
		return
	}
	c := a.ctx
	total := len(w.warmRefs)
	// A dim strip near the bottom of the stage so it doesn't cover the (loading) scene.
	strip := sdl.Rect{X: stage.X, Y: stage.Y + stage.H - 64, W: stage.W, H: 64}
	c.Fill(strip, sdl.Color{R: 10, G: 10, B: 16, A: 200})
	line := "Loading scene…"
	if w.seeding {
		line = "Checking server formats…"
	} else if total > 0 {
		line = fmt.Sprintf("Loading scene… %d / %d ready", w.resident, total)
	}
	c.Label(strip.X+12, strip.Y+10, line, ColText)
	// A slim progress bar under the line (settle fraction), mirroring the export overlay.
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

// drawReplayControls draws the player transport strip below the stage: Restart,
// Pause/Play, Next message (fast-forward), and a message position readout.
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
