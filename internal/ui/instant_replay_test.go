package ui

import (
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
)

// TestBuildClip pins the instant-replay clip assembly: only events inside the
// window are kept, each OffsetMs is rebased to the first kept event, the
// background AND music active BEFORE the window are carried (so a mid-conversation
// clip isn't blank or silent), and an empty window yields nil.
func TestBuildClip(t *testing.T) {
	base := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	at := func(sec int) time.Time { return base.Add(time.Duration(sec) * time.Second) }

	// A scene: background + music set well before the window, then three messages.
	// The window is the last 20s of "now" (t=120) → only the t=105 and t=112
	// messages are in it; the t=90 one is too old.
	entries := []replayBufEntry{
		{at: at(0), ev: recEvent{Kind: int(courtroom.EventBackground), Text: "gs4"}},
		{at: at(1), ev: recEvent{Kind: int(courtroom.EventMusic), Text: "trial.opus"}},
		{at: at(90), ev: recEvent{Kind: int(courtroom.EventMessage), Message: &protocol.ChatMessage{Message: "old"}}},
		{at: at(105), ev: recEvent{Kind: int(courtroom.EventMessage), Message: &protocol.ChatMessage{Message: "mid"}}},
		{at: at(112), ev: recEvent{Kind: int(courtroom.EventMessage), Message: &protocol.ChatMessage{Message: "new"}}},
	}
	now := at(120)
	clip := buildClip(entries, now.Add(-20*time.Second), "https://cdn.test/base", "fallbackbg")
	if clip == nil {
		t.Fatal("buildClip returned nil for a non-empty window")
	}
	if clip.Origin != "https://cdn.test/base" {
		t.Errorf("origin = %q", clip.Origin)
	}
	if clip.StartBg != "gs4" { // carried from the BG before the window, not the session fallback
		t.Errorf("StartBg = %q, want gs4 (carried from before the window)", clip.StartBg)
	}
	// A prepended music seed (the track playing when the clip opens) + the two
	// in-window messages.
	if len(clip.Events) != 3 {
		t.Fatalf("len(events) = %d, want 3 (music seed + 2 msgs): %+v", len(clip.Events), clip.Events)
	}
	if courtroom.EventKind(clip.Events[0].Kind) != courtroom.EventMusic || clip.Events[0].Text != "trial.opus" {
		t.Errorf("event 0 = %+v, want a trial.opus music seed", clip.Events[0])
	}
	if clip.Events[1].OffsetMs != 0 { // rebased to the first kept message (t=105 → 0)
		t.Errorf("first kept msg OffsetMs = %d, want 0", clip.Events[1].OffsetMs)
	}
	if clip.Events[2].OffsetMs != 7000 { // t=112 is 7s after t=105
		t.Errorf("second kept msg OffsetMs = %d, want 7000", clip.Events[2].OffsetMs)
	}
	if clip.Events[1].Message == nil || clip.Events[1].Message.Message != "mid" {
		t.Errorf("first kept msg = %+v, want \"mid\"", clip.Events[1])
	}

	// A window with nothing in it → nil.
	if got := buildClip(entries, at(200), "o", "bg"); got != nil {
		t.Errorf("empty window must give nil, got %+v", got)
	}

	// No background/music before the window → StartBg falls back to the session bg.
	noCtx := entries[2:] // just the three messages
	clip2 := buildClip(noCtx, at(0), "o", "fallbackbg")
	if clip2 == nil || clip2.StartBg != "fallbackbg" {
		t.Errorf("StartBg should fall back to the session bg when none precedes the window, got %+v", clip2)
	}
	for i, e := range clip2.Events { // no music seed prepended here
		if courtroom.EventKind(e.Kind) == courtroom.EventMusic {
			t.Errorf("event %d is an unexpected music seed: %+v", i, e)
		}
	}
}
