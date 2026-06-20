package ui

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
)

// TestRebuildBgListOffline pins the edit-maker-offline crash: opening the Scene
// Maker while disconnected (a.sess == nil) ran rebuildBgList, which dereferenced
// a.sess.Background. The bg picker is courtroom-only so it never hit this; the
// maker opens from Settings. Must not panic, and still build a list (server +
// favorites) when offline.
func TestRebuildBgListOffline(t *testing.T) {
	prefs, err := config.New(filepath.Join(t.TempDir(), config.PrefsFileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = prefs.Close() })
	a := &App{d: Deps{Prefs: prefs}} // a.sess == nil (offline)
	a.bgPick.server = []string{"court", "gs4", "lobby"}
	a.rebuildBgList() // would nil-deref a.sess.Background before the guard
	if len(a.bgPick.list) == 0 {
		t.Error("offline bg list dropped the server list + favorites")
	}
}

// TestHideSpriteSuppression pins the Missingno render hook: a sprite in the
// session hidden-set is dropped (Visible=false) by applySpriteOverrides, a
// non-hidden one is untouched, the empty-set case is a no-op, and reshow un-hides.
func TestHideSpriteSuppression(t *testing.T) {
	prefs, err := config.New(filepath.Join(t.TempDir(), config.PrefsFileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = prefs.Close() })
	a := &App{} // field assignment, not a keyed literal (known phantom "unknown field" glitch)
	a.d.Prefs = prefs
	a.room = &courtroom.Courtroom{}
	a.room.Scene.Speaker = courtroom.SpriteLayer{Name: "Booba", Visible: true}
	a.room.Scene.Pair = courtroom.SpriteLayer{Name: "Phoenix", Visible: true}
	a.room.Scene.ShowDesk = true

	a.applySpriteOverrides() // empty hidden-set + no overrides + desk shown: a no-op
	if !a.room.Scene.Speaker.Visible || !a.room.Scene.Pair.Visible || !a.room.Scene.ShowDesk {
		t.Fatal("empty hidden-set / hide-desk-off must not change anything")
	}

	// Hide-desk option suppresses ShowDesk; turning it off restores it.
	prefs.SetHideDesk(true)
	a.applySpriteOverrides()
	if a.room.Scene.ShowDesk {
		t.Error("hide-desk did not suppress ShowDesk")
	}
	prefs.SetHideDesk(false)
	a.room.Scene.ShowDesk = true // the courtroom re-sets it each frame
	a.applySpriteOverrides()
	if !a.room.Scene.ShowDesk {
		t.Error("desk should render again when hide-desk is off")
	}

	a.hiddenSprites = map[string]struct{}{"booba": {}}
	a.applySpriteOverrides()
	if a.room.Scene.Speaker.Visible {
		t.Error("hidden sprite still visible")
	}
	if !a.room.Scene.Pair.Visible {
		t.Error("non-hidden pair wrongly hidden")
	}

	a.reshowSprites()
	if a.hiddenSprites != nil {
		t.Error("reshow must clear the hidden-set")
	}
	a.room.Scene.Speaker.Visible = true // the courtroom re-sets Visible each frame
	a.applySpriteOverrides()
	if !a.room.Scene.Speaker.Visible {
		t.Error("after reshow the sprite must render again")
	}
}

// TestRequestDisconnectConfirms pins the safety gate: with instant-disconnect OFF
// (the default), the Disconnect button opens the confirm modal instead of acting.
func TestRequestDisconnectConfirms(t *testing.T) {
	prefs, err := config.New(filepath.Join(t.TempDir(), config.PrefsFileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = prefs.Close() })
	a := &App{d: Deps{Prefs: prefs}}
	a.requestDisconnect()
	if !a.confirmDisconnect {
		t.Error("with instant-disconnect off, requestDisconnect must open the confirm modal, not disconnect")
	}
}

// TestMakerDrawNoPanic drives the maker's list + per-event editor over a loaded
// recording (message / background / music events, an empty-charname line, effects
// + crop set) the way "✎ Edit" does — directly, NOT through drawSceneMaker, so a
// panic propagates and names the edit crash instead of being swallowed by
// recoverMaker. The preview pane needs a Viewport and is nil-guarded, so it's
// excluded here; this targets the un-recovered draw (list + editor).
func TestMakerDrawNoPanic(t *testing.T) {
	ren, cleanup := newCaptureHarness(t)
	defer cleanup()
	ctx, err := NewCtx(ren)
	if err != nil {
		t.Skipf("kit unavailable: %v", err)
	}
	prefs, err := config.New(filepath.Join(t.TempDir(), config.PrefsFileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = prefs.Close() })

	a := &App{ctx: ctx, d: Deps{Prefs: prefs}, makerName: "test", makerTrimStart: -1, makerTrimEnd: -1}
	// A real session + bg list enables the char/background autocompletes — the
	// draw paths a nil-session test would skip.
	a.sess = &courtroom.Session{Chars: []courtroom.CharacterSlot{{Name: "Sekai"}, {Name: "Phoenix"}, {Name: "Häschen"}}}
	a.bgPick.server = []string{"court", "gs4", "lobby"}
	a.makerScene = &sceneRecording{
		Version: recordingVersion,
		Origin:  "https://example.test/base/",
		StartBg: "court",
		Events: []recEvent{
			{Kind: int(courtroom.EventMessage), Message: &protocol.ChatMessage{
				CharName: "Sekai", Emote: "normal", Side: "wit",
				Message: "this is a fairly long line of dialogue that wraps", Screenshake: true, SelfOffsetX: 30, SelfOffsetY: -10,
			}},
			{Kind: int(courtroom.EventBackground), Text: "gs4"},
			{Kind: int(courtroom.EventMusic), Text: "trial.opus"},
			{Kind: int(courtroom.EventMessage), Message: &protocol.ChatMessage{Side: "def", Realization: true}}, // empty CharName
			{Kind: int(courtroom.EventMessage), Message: nil},                                                   // defensive: nil message
			{Kind: int(courtroom.EventMessage), Message: &protocol.ChatMessage{ // a full 256-char IC line
				CharName: "Sekai", Emote: "normal", Side: "wit",
				Message: strings.Repeat("A very long sentence. ", 16)[:256],
			}},
			{Kind: int(courtroom.EventMessage), Message: &protocol.ChatMessage{ // unicode / emoji + long
				CharName: "Häschen", Emote: "normal", Side: "wit", Showname: "🍅 fünfzehn",
				Message: strings.Repeat("🍅 Häschen fünfzehn — ", 14),
			}},
		},
	}

	// No crop, then a crop range — both exercise the list visuals + toolbar.
	for _, crop := range []bool{false, true} {
		if crop {
			a.makerTrimStart, a.makerTrimEnd = 1, 3
		}
		for sel := range a.makerScene.Events {
			a.makerSel = sel
			a.drawMakerList(8, 40, 360, 600)
			a.drawMakerEditor(380, 40, 600)
		}
	}

	// Full draw (actions row + body dispatch), the way "✎ Edit" hits it. The
	// preview pane needs a Viewport and nil-guards out here. drawSceneMaker's
	// recoverMaker closes the maker on a panic, so makerOpen staying true is the
	// assertion that the whole draw — including the parts not covered above —
	// survived (this is what would catch the reported edit crash).
	for _, exp := range []bool{false, true} {
		a.makerOpen = true
		a.makerExportOpen = exp
		a.makerSel = 0
		a.drawSceneMaker(1280, 720)
		if !a.makerOpen {
			t.Fatalf("drawSceneMaker (exportPanel=%v) panicked — recoverMaker closed the maker; edit crash reproduced", exp)
		}
	}
}
