package ui

import (
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
)

// TestBuildReplayChapters pins the chapter derivation (#70): background changes,
// music changes, and shouted messages become jump entries; plain messages don't;
// the list is bounded by maxReplayChapters.
func TestBuildReplayChapters(t *testing.T) {
	events := []recEvent{
		{Kind: int(courtroom.EventBackground), Text: "courtroom"},
		{Kind: int(courtroom.EventMusic), Text: "trial2.opus"},
		{Kind: int(courtroom.EventMessage), Message: &protocol.ChatMessage{CharName: "Phoenix", Message: "hello"}},
		{Kind: int(courtroom.EventMessage), Message: &protocol.ChatMessage{CharName: "Edgeworth", Showname: "Miles", Objection: protocol.ShoutObjection, Message: "no"}},
		{Kind: int(courtroom.EventBackground), Text: "lobby"},
	}
	ch := buildReplayChapters(events)
	want := []replayChapter{
		{0, "Scene: courtroom"},
		{1, "♪ trial2.opus"},
		{3, "⚡ Miles: Objection!"},
		{4, "Scene: lobby"},
	}
	if len(ch) != len(want) {
		t.Fatalf("chapters = %d, want %d: %+v", len(ch), len(want), ch)
	}
	for i := range want {
		if ch[i] != want[i] {
			t.Errorf("chapter %d = %+v, want %+v", i, ch[i], want[i])
		}
	}

	// The cap holds: a flood of scene changes stops at maxReplayChapters.
	flood := make([]recEvent, maxReplayChapters+50)
	for i := range flood {
		flood[i] = recEvent{Kind: int(courtroom.EventBackground), Text: "bg"}
	}
	if got := len(buildReplayChapters(flood)); got != maxReplayChapters {
		t.Errorf("chapter cap = %d, want %d", got, maxReplayChapters)
	}
}

// TestReplayJumpContext pins the seek seeding: the most recent bg + music
// BEFORE the target and the last message index, so a jump lands with the right
// stage/track/speaker without feeding the in-between events.
func TestReplayJumpContext(t *testing.T) {
	events := []recEvent{
		{Kind: int(courtroom.EventBackground), Text: "courtroom"},                                            // 0
		{Kind: int(courtroom.EventMusic), Text: "trial.opus"},                                                // 1
		{Kind: int(courtroom.EventMessage), Message: &protocol.ChatMessage{CharName: "A", Message: "one"}},   // 2
		{Kind: int(courtroom.EventMusic), Text: "cross.opus"},                                                // 3
		{Kind: int(courtroom.EventMessage), Message: &protocol.ChatMessage{CharName: "B", Message: "two"}},   // 4
		{Kind: int(courtroom.EventBackground), Text: "lobby"},                                                // 5
		{Kind: int(courtroom.EventMessage), Message: &protocol.ChatMessage{CharName: "C", Message: "three"}}, // 6
	}
	bg, music, lastMsg := replayJumpContext(events, 6)
	if bg != "lobby" || music != "cross.opus" || lastMsg != 4 {
		t.Errorf("jump@6 context = (%q, %q, %d), want (lobby, cross.opus, 4)", bg, music, lastMsg)
	}
	bg, music, lastMsg = replayJumpContext(events, 0)
	if bg != "" || music != "" || lastMsg != -1 {
		t.Errorf("jump@0 must seed nothing, got (%q, %q, %d)", bg, music, lastMsg)
	}
	// An out-of-range idx clamps rather than panicking.
	if bg, _, _ = replayJumpContext(events, 99); bg != "lobby" {
		t.Errorf("clamped jump context bg = %q, want lobby", bg)
	}
}
