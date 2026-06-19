package ui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/veandco/go-sdl2/sdl"

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
	// buffers): a runaway session can't balloon memory. ~5000 IC messages is
	// hours of play; recording stops accepting events past the cap.
	maxRecordedEvents = 5000
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

// recordEvent appends a scene-mutating event to the active recording (bounded).
// Called from the event loop for every event while recActive.
func (a *App) recordEvent(ev courtroom.Event) {
	if a.rec == nil || !recordable(ev.Kind) || len(a.rec.Events) >= maxRecordedEvents {
		return
	}
	a.rec.Events = append(a.rec.Events, recEvent{
		OffsetMs: int(time.Since(a.recStart).Milliseconds()),
		Kind:     int(ev.Kind),
		Message:  ev.Message,
		Name:     ev.Name,
		Text:     ev.Text,
		Int:      ev.Int,
	})
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
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		a.pushDebug("recording: " + err.Error())
		return
	}
	stamp := time.Now().Format("20060102-150405")
	name := "asyncao-" + stamp + recordingExt
	go func() {
		exe, err := os.Executable()
		if err != nil {
			return
		}
		dir := filepath.Join(filepath.Dir(exe), "recordings")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return
		}
		_ = os.WriteFile(filepath.Join(dir, name), data, 0o644)
	}()
	a.warnLine = "Recording saved (" + strconv.Itoa(len(rec.Events)) + " events): recordings\\" + name
	a.warnAt = time.Now()
}

// --- M16 [2/2]: replay player ---

// eventFromRec reconstructs the courtroom event a recorded entry stands for.
func eventFromRec(e recEvent) courtroom.Event {
	return courtroom.Event{
		Kind:    courtroom.EventKind(e.Kind),
		Message: e.Message,
		Name:    e.Name,
		Text:    e.Text,
		Int:     e.Int,
	}
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
		if en.IsDir() || !strings.HasSuffix(en.Name(), recordingExt) {
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

// replayFromPath loads a specific .aorec and starts replaying it (the picker
// entry point). v1 renders the replay on the courtroom stage, so it asks the
// user to connect first if they're in the lobby.
func (a *App) replayFromPath(path string) {
	if a.recActive {
		a.warnLine = "Stop recording first, then replay."
		a.warnAt = time.Now()
		return
	}
	rec, err := loadRecording(path)
	if err != nil {
		a.warnLine = "Couldn't load recording: " + err.Error()
		a.warnAt = time.Now()
		return
	}
	if a.room == nil {
		a.warnLine = "Connect to a server to watch a replay — it plays on the courtroom stage."
		a.warnAt = time.Now()
		return
	}
	a.screen = ScreenCourtroom
	a.startReplay(rec, filepath.Base(path))
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
	rec, err := loadRecording(path)
	if err != nil {
		a.warnLine = "Couldn't load recording: " + err.Error()
		a.warnAt = time.Now()
		return
	}
	a.startReplay(rec, filepath.Base(path))
}

// startReplay spins up a throwaway courtroom pointed at the recorded asset
// origin (asset HTTP fetch is independent of the game WS), seeds the starting
// background so the first frames aren't blank, and begins feeding events. Paced
// to the user's live timing settings.
func (a *App) startReplay(rec *sceneRecording, name string) {
	if rec == nil || len(rec.Events) == 0 {
		return
	}
	a.replayRoom = courtroom.NewCourtroom(courtroom.NewURLBuilder(rec.Origin), a.d.Manager, nil, a.d.Audio)
	crawlMs, stayMs, _ := a.d.Prefs.Timing()
	a.replayRoom.Typewriter.Interval = time.Duration(crawlMs) * time.Millisecond
	a.replayRoom.TextStay = time.Duration(stayMs) * time.Millisecond
	a.replayRoom.CatchUp = false // play every recorded line in full; the driver feeds one at a time
	a.replayRoom.ReduceMotion = a.d.Prefs.ReduceMotion()
	a.replayRoom.ForceCharNames = a.d.Prefs.ForceCharNamesOn()
	if a.d.Viewport != nil { // one-shot preanim completion must notify the REPLAY room now
		a.d.Viewport.OnPreanimDone = a.replayRoom.NotifyPreanimDone
	}
	if rec.StartBg != "" {
		a.replayRoom.HandleEvent(courtroom.Event{Kind: courtroom.EventBackground, Text: rec.StartBg})
	}
	a.replayEvents = rec.Events
	a.replayIdx = 0
	a.replaying = true
	a.warnLine = "▶ Replaying " + name + " — press the Replay key to stop"
	a.warnAt = time.Now()
}

// advanceReplay feeds the next recorded event whenever the replay room returns
// to idle, so the courtroom's own pacing (typewriter / preanim / linger) times
// the playback — NOT the recorded wall-clock deltas, which would double-drag.
// When the stream is exhausted and the room settles, the replay ends.
func (a *App) advanceReplay(dt time.Duration) {
	if !a.replaying || a.replayRoom == nil {
		return
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
	a.replaying = false
	a.replayRoom = nil
	a.replayEvents = nil
	a.replayIdx = 0
	if a.d.Viewport != nil { // rebind preanim completion to the live room (or clear it)
		if a.room != nil {
			a.d.Viewport.OnPreanimDone = a.room.NotifyPreanimDone
		} else {
			a.d.Viewport.OnPreanimDone = nil
		}
	}
	a.warnLine = "Replay ended."
	a.warnAt = time.Now()
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
