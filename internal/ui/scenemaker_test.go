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
	if e.Message.DeskMod != protocol.DeskShow {
		t.Errorf("new line must default to desk-shown (grounded framing), got deskmod %d", e.Message.DeskMod)
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

func TestBundledSceneRoundTrip(t *testing.T) {
	scene := &sceneRecording{
		Version: recordingVersion,
		Origin:  "https://cdn/base",
		Bundled: true,
		Formats: map[string]string{"CharSprite": ".webp", "Background": ".png"},
		Events:  []recEvent{newMessageEvent("A", "n", "hi")},
	}
	data, err := json.MarshalIndent(scene, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	var back sceneRecording
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if !back.Bundled || back.Formats["CharSprite"] != ".webp" || back.Formats["Background"] != ".png" {
		t.Errorf("bundled archive fields lost in round-trip: %+v", back)
	}
}

func TestMakerDuplicateSel(t *testing.T) {
	a := &App{}
	a.makerScene = &sceneRecording{Events: []recEvent{newMessageEvent("A", "n", "orig")}}
	a.makerSel = 0
	a.makerDuplicateSel()
	if len(a.makerScene.Events) != 2 || a.makerSel != 1 {
		t.Fatalf("dup: len=%d sel=%d", len(a.makerScene.Events), a.makerSel)
	}
	a.makerScene.Events[1].Message.Message = "changed" // editing the dup must not touch the original
	if a.makerScene.Events[0].Message.Message != "orig" {
		t.Error("duplicate shares the Message pointer with the original")
	}
}

// TestMakerCropRange pins the crop/trim logic: In/Out resolve to an in-bounds
// inclusive range, the trimmed scene carries the background in effect at the crop
// start (so a mid-scene crop isn't blank), the clone never mutates the edit
// buffer, and an inverted/out-of-range range degrades to the whole scene rather
// than producing an empty export.
func TestMakerCropRange(t *testing.T) {
	bg := func(name string) recEvent { return recEvent{Kind: int(courtroom.EventBackground), Text: name} }
	msg := func(text string) recEvent {
		return recEvent{Kind: int(courtroom.EventMessage), Message: &protocol.ChatMessage{Message: text}}
	}
	newApp := func() *App {
		return &App{
			makerScene:     &sceneRecording{StartBg: "lobby", Events: []recEvent{bg("court"), msg("a"), msg("b"), msg("c"), msg("d")}},
			makerTrimStart: -1,
			makerTrimEnd:   -1,
		}
	}

	// No crop: whole scene, a fresh clone.
	a := newApp()
	if a.trimActive() {
		t.Fatal("trimActive must be false with no In/Out set")
	}
	if sub := a.trimmedScene(); len(sub.Events) != 5 || sub.StartBg != "lobby" {
		t.Fatalf("no-crop scene = %d events bg=%q, want 5 / lobby", len(sub.Events), sub.StartBg)
	}

	// In at index 2 (msg "b"): range [2,4], StartBg carried from the bg before it.
	a = newApp()
	a.makerTrimStart = 2
	if s, e := a.trimRange(); s != 2 || e != 4 {
		t.Fatalf("In-only range = %d..%d, want 2..4", s, e)
	}
	sub := a.trimmedScene()
	if len(sub.Events) != 3 || sub.StartBg != "court" {
		t.Fatalf("In-only crop = %d events bg=%q, want 3 / court", len(sub.Events), sub.StartBg)
	}
	if sub.Events[0].Message == nil || sub.Events[0].Message.Message != "b" {
		t.Fatalf("crop should start at msg b, got %+v", sub.Events[0])
	}
	if len(a.makerScene.Events) != 5 || a.makerScene.StartBg != "lobby" {
		t.Error("trimmedScene mutated the edit buffer (must clone)")
	}

	// Out at index 2: range [0,2].
	a = newApp()
	a.makerTrimEnd = 2
	if s, e := a.trimRange(); s != 0 || e != 2 {
		t.Fatalf("Out-only range = %d..%d, want 0..2", s, e)
	}

	// Inverted (In after Out) degrades to the whole scene, never empty.
	a = newApp()
	a.makerTrimStart, a.makerTrimEnd = 4, 1
	if s, e := a.trimRange(); s != 0 || e != 4 {
		t.Fatalf("inverted range = %d..%d, want full 0..4", s, e)
	}
	if sub := a.trimmedScene(); len(sub.Events) != 5 {
		t.Fatalf("inverted crop = %d events, want full 5", len(sub.Events))
	}

	// Out-of-range Out (e.g. after deletes) clamps to the last event.
	a = newApp()
	a.makerTrimStart, a.makerTrimEnd = 2, 99
	if s, e := a.trimRange(); s != 2 || e != 4 {
		t.Fatalf("clamped range = %d..%d, want 2..4", s, e)
	}
}

// TestMakerPreviewKeyReflectsEffects ensures toggling a line's screenshake /
// realization / sprite-move changes its preview key, so the WYSIWYG pane rebuilds
// and replays the effect instead of showing a stale frame.
func TestMakerPreviewKeyReflectsEffects(t *testing.T) {
	base := &protocol.ChatMessage{CharName: "Phoenix", Emote: "normal", Side: "wit"}
	a := &App{makerScene: &sceneRecording{Events: []recEvent{{Kind: int(courtroom.EventMessage), Message: base}}}}
	k0 := a.makerPreviewKeyFor(0)

	for _, mut := range []func(m *protocol.ChatMessage){
		func(m *protocol.ChatMessage) { m.Screenshake = true },
		func(m *protocol.ChatMessage) { m.Realization = true },
		func(m *protocol.ChatMessage) { m.SelfOffsetX = 30 },
		func(m *protocol.ChatMessage) { m.SelfOffsetY = -20 },
	} {
		msg := *base
		mut(&msg)
		a.makerScene.Events[0].Message = &msg
		if a.makerPreviewKeyFor(0) == k0 {
			t.Errorf("preview key unchanged after mutating %+v — the pane wouldn't rebuild to show it", msg)
		}
		a.makerScene.Events[0].Message = base
	}
}

func TestMakerNewLineSeedInherits(t *testing.T) {
	a := &App{}
	a.makerScene = &sceneRecording{Events: []recEvent{{
		Kind:    int(courtroom.EventMessage),
		Message: &protocol.ChatMessage{CharName: "Edgeworth", Emote: "deskslam", Side: "pro", TextColor: 3, Flip: true, DeskMod: protocol.DeskShow},
	}}}
	a.makerSel = 0
	m := a.makerNewLineSeed().Message
	if m == nil {
		t.Fatal("nil seed message")
	}
	if m.CharName != "Edgeworth" || m.Emote != "deskslam" || m.Side != "pro" || m.TextColor != 3 || !m.Flip || m.DeskMod != protocol.DeskShow {
		t.Errorf("new line did not inherit the previous speaker: %+v", m)
	}
	if m.Message != "" {
		t.Errorf("inherited line should start with empty text, got %q", m.Message)
	}
	if m.EmoteMod != protocol.EmoteModIdle {
		t.Errorf("inherited line should be idle, got %d", m.EmoteMod)
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

func TestMakerPreviewKeyForBackgroundContext(t *testing.T) {
	a := &App{}
	a.makerScene = &sceneRecording{
		StartBg: "courtroom",
		Events: []recEvent{
			newMessageEvent("A", "n", "1"),     // 0: bg = StartBg (courtroom)
			newBackgroundEvent("gs4"),          // 1: bg = gs4 (itself)
			newMessageEvent("B", "point", "2"), // 2: bg = gs4 (most recent before it)
		},
	}
	if k := a.makerPreviewKeyFor(0); k.bg != "courtroom" {
		t.Errorf("line 0 bg = %q, want courtroom (StartBg)", k.bg)
	}
	if k := a.makerPreviewKeyFor(1); k.bg != "gs4" {
		t.Errorf("selected BG line bg = %q, want gs4 (itself)", k.bg)
	}
	k := a.makerPreviewKeyFor(2)
	if k.bg != "gs4" {
		t.Errorf("line 2 bg = %q, want gs4 (most recent before it)", k.bg)
	}
	if k.char != "B" || k.emote != "point" {
		t.Errorf("line 2 key missing visual fields: %+v", k)
	}
	// text is NOT part of the key (so typing doesn't rebuild the preview)
	a.makerScene.Events[2].Message.Message = "edited"
	if a.makerPreviewKeyFor(2) != k {
		t.Error("editing message text must not change the preview key")
	}
}

func TestContainsFold(t *testing.T) {
	yes := [][2]string{{"Phoenix", "pho"}, {"Phoenix", "NIX"}, {"Miles Edgeworth", "edge"}, {"abc", ""}, {"abc", "abc"}}
	for _, c := range yes {
		if !containsFold(c[0], c[1]) {
			t.Errorf("containsFold(%q,%q) = false, want true", c[0], c[1])
		}
	}
	no := [][2]string{{"Phoenix", "xyz"}, {"abc", "abcd"}, {"Phoenix", "phx"}}
	for _, c := range no {
		if containsFold(c[0], c[1]) {
			t.Errorf("containsFold(%q,%q) = true, want false", c[0], c[1])
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
