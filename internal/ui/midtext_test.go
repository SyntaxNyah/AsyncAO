package ui

import (
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
)

// TestMidEmoteResolver pins the send-side :stem: → MidEmote lookup: case-insensitive
// on Anim and Comment, carrying the emote's preanim and per-emote sound, with the
// AO silence sentinels ("", "0", "1") reading as "no sound".
func TestMidEmoteResolver(t *testing.T) {
	a := &App{}
	a.emotes = []courtroom.Emote{
		{Comment: "normal", Preanim: "-", Anim: "normal", SFXName: ""},
		{Comment: "Angry", Preanim: "flourish", Anim: "angry", SFXName: "whip"},
		{Comment: "quiet", Anim: "quiet", SFXName: "1"},
	}

	if e, ok := a.midEmoteResolver("angry"); !ok || e.Emote != "angry" || e.Pre != "flourish" || e.SFX != "whip" {
		t.Errorf("angry = %+v ok=%v", e, ok)
	}
	if e, ok := a.midEmoteResolver("ANGRY"); !ok || e.Emote != "angry" {
		t.Errorf("case-insensitive ANGRY = %+v ok=%v", e, ok)
	}
	if e, ok := a.midEmoteResolver("normal"); !ok || e.SFX != "" {
		t.Errorf("normal sfx = %q, want empty (silent)", e.SFX)
	}
	if e, ok := a.midEmoteResolver("quiet"); !ok || e.SFX != "" {
		t.Errorf("quiet sfx = %q, want empty (silent sentinel)", e.SFX)
	}
	if _, ok := a.midEmoteResolver("missing"); ok {
		t.Error("unknown emote resolved")
	}
}
