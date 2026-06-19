package ui

import (
	"encoding/json"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
)

// TestSceneRecordingRoundTrip pins the .aorec format: the header (origin,
// starting background) and every scene event — message, background, music —
// survive a JSON save→load so a replay can reconstruct the scene exactly.
func TestSceneRecordingRoundTrip(t *testing.T) {
	rec := &sceneRecording{
		Version: recordingVersion,
		Origin:  "https://example.com/base/",
		StartBg: "courtroom",
		Events: []recEvent{
			{OffsetMs: 0, Kind: int(courtroom.EventBackground), Text: "gallery"},
			{OffsetMs: 500, Kind: int(courtroom.EventMusic), Text: "Song.opus", Name: "Phoenix", Int: 3},
			{OffsetMs: 1200, Kind: int(courtroom.EventMessage), Message: &protocol.ChatMessage{
				CharName: "Phoenix", Emote: "pointing", Message: "Objection!",
			}},
		},
	}
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got sceneRecording
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Version != recordingVersion || got.Origin != rec.Origin || got.StartBg != rec.StartBg {
		t.Fatalf("header lost: %+v", got)
	}
	if len(got.Events) != 3 {
		t.Fatalf("event count = %d, want 3", len(got.Events))
	}
	if bg := got.Events[0]; bg.Kind != int(courtroom.EventBackground) || bg.Text != "gallery" {
		t.Errorf("background event lost: %+v", bg)
	}
	if mu := got.Events[1]; mu.Text != "Song.opus" || mu.Name != "Phoenix" || mu.Int != 3 {
		t.Errorf("music event lost: %+v", mu)
	}
	m := got.Events[2].Message
	if m == nil || m.CharName != "Phoenix" || m.Emote != "pointing" || m.Message != "Objection!" {
		t.Errorf("message event lost: %+v", got.Events[2])
	}
}
