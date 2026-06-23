package ui

import (
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
)

// TestEffectIDsMatchRender pins the cross-package contract: courtroom (SDL-free) and render
// duplicate the effect ids, and toRenderEffectSpans casts between them as a straight copy.
// If these drift, an animated message would render the wrong effect.
func TestEffectIDsMatchRender(t *testing.T) {
	if courtroom.TextEffectNone != render.EffectNone ||
		courtroom.TextEffectShake != render.EffectShake ||
		courtroom.TextEffectWave != render.EffectWave ||
		courtroom.TextEffectRainbow != render.EffectRainbow {
		t.Fatal("courtroom.TextEffect* must equal render.Effect* — toRenderEffectSpans relies on it")
	}
}

// TestRemoteEffectsThroughRoom pins #M5 end-to-end on the receive side: an effects-marked IC
// message lands its spans on the Scene (indexed into the clean text), while a plain message
// leaves the Scene effect-free so it keeps the untouched MessageRaster fast path.
func TestRemoteEffectsThroughRoom(t *testing.T) {
	a := &App{}
	a.room = newRoomForTest(t)
	spans := []courtroom.TextEffectSpan{{Start: 0, Len: 2, Effect: courtroom.TextEffectShake}, {Start: 3, Len: 5, Effect: courtroom.TextEffectRainbow}}
	a.room.HandleEvent(courtroom.Event{Kind: courtroom.EventMessage, Message: msgFor(3, "Phoenix", "hi there"+courtroom.EncodeEffectsMarker(spans))})
	a.room.SkipToIdle()

	if a.room.Scene.MessageText != "hi there" {
		t.Fatalf("MessageText = %q, want \"hi there\" (effects frame must be stripped)", a.room.Scene.MessageText)
	}
	got := a.room.Scene.MessageEffects
	if len(got) != 2 || got[0] != spans[0] || got[1] != spans[1] {
		t.Fatalf("Scene.MessageEffects = %v, want %v", got, spans)
	}

	// A plain follow-up clears the effects → back on the fast path.
	a.room.HandleEvent(courtroom.Event{Kind: courtroom.EventMessage, Message: msgFor(3, "Phoenix", "just talking")})
	a.room.SkipToIdle()
	if len(a.room.Scene.MessageEffects) != 0 {
		t.Errorf("a plain message left %d effect spans on the Scene, want 0", len(a.room.Scene.MessageEffects))
	}
}

// TestSendParsesEffectMarkup pins the send side: [shake]/[rainbow] markup becomes a wire
// message whose VISIBLE text is plain (AO2/webAO safe) but which carries an effects frame
// that decodes back to the tagged spans — and a round-trip through the room re-derives them.
func TestSendParsesEffectMarkup(t *testing.T) {
	wire, spans := courtroom.ParseTextEffects("ab[shake]cd[/shake] [rainbow]ef[/rainbow]")
	msg := wire + courtroom.EncodeEffectsMarker(spans)

	a := &App{}
	a.room = newRoomForTest(t)
	a.room.HandleEvent(courtroom.Event{Kind: courtroom.EventMessage, Message: msgFor(7, "Apollo", msg)})
	a.room.SkipToIdle()

	if a.room.Scene.MessageText != "abcd ef" {
		t.Fatalf("MessageText = %q, want \"abcd ef\"", a.room.Scene.MessageText)
	}
	got := a.room.Scene.MessageEffects
	if len(got) != 2 {
		t.Fatalf("spans = %v, want 2", got)
	}
	// Verify each span covers the intended substring of the displayed text.
	disp := []rune(a.room.Scene.MessageText)
	if s := string(disp[got[0].Start : got[0].Start+got[0].Len]); s != "cd" || got[0].Effect != courtroom.TextEffectShake {
		t.Errorf("span0 = %q effect %d, want \"cd\" shake", s, got[0].Effect)
	}
	if s := string(disp[got[1].Start : got[1].Start+got[1].Len]); s != "ef" || got[1].Effect != courtroom.TextEffectRainbow {
		t.Errorf("span1 = %q effect %d, want \"ef\" rainbow", s, got[1].Effect)
	}
}
