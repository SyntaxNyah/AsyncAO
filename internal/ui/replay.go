package ui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"time"

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
