package ui

import (
	"encoding/json"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
)

// TestNewMessageEventDefaults pins the from-scratch line defaults: a real emote
// stem and — critically — idle emote-mod, so a synthetic line never stalls the
// preview waiting on a preanim asset that may not exist at the origin.
func TestNewMessageEventDefaults(t *testing.T) {
	e := newMessageEvent("Phoenix", "", "Objection!")
	if courtroom.EventKind(e.Kind) != courtroom.EventMessage {
		t.Fatalf("kind = %d, want message", e.Kind)
	}
	if e.Message == nil {
		t.Fatal("nil message")
	}
	if e.Message.Emote != defaultMakerEmote {
		t.Errorf("empty emote should default to %q, got %q", defaultMakerEmote, e.Message.Emote)
	}
	if e.Message.EmoteMod != protocol.EmoteModIdle {
		t.Errorf("new line must be idle (no preanim stall), got emotemod %d", e.Message.EmoteMod)
	}
	if e.Message.CharName != "Phoenix" || e.Message.Message != "Objection!" {
		t.Errorf("fields not set: %+v", e.Message)
	}
}

// TestCloneSceneIsDeep guards the edit buffer: editing the maker's copy must not
// reach back into a loaded file / live recording (or into a running Preview).
func TestCloneSceneIsDeep(t *testing.T) {
	orig := &sceneRecording{
		Origin: "https://x/base",
		Events: []recEvent{newMessageEvent("A", "normal", "hi")},
	}
	clone := cloneScene(orig)
	clone.Events[0].Message.Message = "changed"
	clone.Origin = "https://y"
	if orig.Events[0].Message.Message != "hi" {
		t.Error("clone shares the Message pointer with the original")
	}
	if orig.Origin != "https://x/base" {
		t.Error("clone shares Origin with the original")
	}
}

// TestSceneRoundTrip proves a from-scratch scene serializes to .aorec JSON and
// reloads identically — the foundation of Save + hand-editing in a text editor.
func TestSceneRoundTrip(t *testing.T) {
	scene := &sceneRecording{
		Version: recordingVersion,
		Origin:  "https://cdn/base",
		StartBg: "courtroom",
		Events: []recEvent{
			newBackgroundEvent("gs4"),
			newMessageEvent("Phoenix", "(a)normal", "Hold it!"),
			newMusicEvent("trial.opus"),
		},
	}
	data, err := json.MarshalIndent(scene, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	var back sceneRecording
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("reload failed (a hand-edited scene must reload): %v", err)
	}
	if len(back.Events) != 3 || back.Origin != scene.Origin || back.StartBg != scene.StartBg {
		t.Fatalf("round-trip mismatch: %+v", back)
	}
	if back.Events[1].Message == nil || back.Events[1].Message.Message != "Hold it!" {
		t.Errorf("message line lost in round-trip: %+v", back.Events[1])
	}
	if back.Events[0].Text != "gs4" || back.Events[2].Text != "trial.opus" {
		t.Errorf("bg/music lost in round-trip: %+v", back.Events)
	}
}

// TestMakerInsertDeleteMove exercises the event-list CRUD on a bare App (no SDL).
func TestMakerInsertDeleteMove(t *testing.T) {
	a := &App{}
	a.makerScene = &sceneRecording{Events: []recEvent{newMessageEvent("A", "n", "1")}}
	a.makerSel = 0

	a.makerInsert(newMessageEvent("B", "n", "2")) // inserts AFTER selection, selects it
	if len(a.makerScene.Events) != 2 || a.makerSel != 1 {
		t.Fatalf("insert: len=%d sel=%d", len(a.makerScene.Events), a.makerSel)
	}
	if a.makerScene.Events[1].Message.Message != "2" {
		t.Errorf("inserted at wrong spot: %+v", a.makerScene.Events)
	}

	a.makerMoveSel(-1) // move "2" up to index 0
	if a.makerSel != 0 || a.makerScene.Events[0].Message.Message != "2" {
		t.Errorf("move up failed: sel=%d ev=%+v", a.makerSel, a.makerScene.Events)
	}

	a.makerDeleteSel() // delete "2", leaving "1"
	if len(a.makerScene.Events) != 1 || a.makerScene.Events[0].Message.Message != "1" {
		t.Errorf("delete failed: %+v", a.makerScene.Events)
	}
}

func TestSanitizeStem(t *testing.T) {
	cases := map[string]string{
		"  my scene  ":     "my scene",
		"a/b\\c":           "a-b-c",
		"trial.aorec":      "trial",
		"":                 "scene",
		"../../etc/passwd": "------etc-passwd",
	}
	for in, want := range cases {
		if got := sanitizeStem(in); got != want {
			t.Errorf("sanitizeStem(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMakerSideIndexRoundTrip(t *testing.T) {
	for i, code := range makerSideValues {
		if got := makerSideIndex(code); got != i {
			t.Errorf("makerSideIndex(%q) = %d, want %d", code, got, i)
		}
	}
	if makerSideIndex("bogus") != 2 { // unknown → witness
		t.Error("unknown side should default to witness (index 2)")
	}
}
