package ui

import (
	"strings"
	"testing"

	"github.com/veandco/go-sdl2/sdl"

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
		courtroom.TextEffectRainbow != render.EffectRainbow ||
		courtroom.TextEffectBounce != render.EffectBounce ||
		courtroom.TextEffectSway != render.EffectSway ||
		courtroom.TextEffectShiver != render.EffectShiver ||
		courtroom.TextEffectWobble != render.EffectWobble ||
		courtroom.TextEffectTremble != render.EffectTremble ||
		courtroom.TextEffectFloat != render.EffectFloat ||
		courtroom.TextEffectPulse != render.EffectPulse ||
		courtroom.TextEffectGradient != render.EffectGradient ||
		courtroom.TextEffectBlink != render.EffectBlink ||
		courtroom.TextEffectSparkle != render.EffectSparkle {
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

// TestAnimColors pins the chatbox-side colour flattening for #M5 colour+animation: a plain
// message yields a single base colour (uniform), a \cN-styled message yields per-rune
// colours so the effect can compose with the colour.
func TestAnimColors(t *testing.T) {
	base := sdl.Color{R: 10, G: 20, B: 30, A: 255}
	plain := &courtroom.Scene{MessageText: "hello", MessageStyles: []courtroom.StyleRun{{Len: 5, Color: courtroom.ColorDefault}}}
	if got := animColors(plain, base); len(got) != 1 || got[0] != base {
		t.Fatalf("plain animColors = %v, want one base entry", got)
	}
	styled := &courtroom.Scene{MessageText: "abcd", MessageStyles: []courtroom.StyleRun{{Len: 2, Color: courtroom.ColorDefault}, {Len: 2, Color: 2}}}
	got := animColors(styled, base)
	if len(got) != 4 {
		t.Fatalf("styled animColors len = %d, want 4 (one per rune)", len(got))
	}
	if got[0] != base || got[1] != base {
		t.Errorf("runes 0-1 = %v/%v, want base on both", got[0], got[1])
	}
	if got[2] != render.TextColor(2) || got[3] != render.TextColor(2) {
		t.Errorf("runes 2-3 = %v/%v, want palette colour 2", got[2], got[3])
	}
}

// TestICEmojiSetAllRouteToEmojiFont guards the picker against tofu: every entry must be
// detected as needing the colour-emoji font. A BMP symbol (✨ ⭐ ❓ …) only qualifies with a
// trailing U+FE0F selector — without it it's a text code point and would render as a box.
func TestICEmojiSetAllRouteToEmojiFont(t *testing.T) {
	for _, e := range icEmojiSet {
		if !render.NeedsEmojiFallback(e) {
			t.Errorf("picker emoji %q (% x) won't reach the colour-emoji font — a BMP symbol needs a U+FE0F variation selector", e, []byte(e))
		}
	}
}

// TestIsASCII pins the cheap gate that keeps plain fields on the single-font fast path.
func TestIsASCII(t *testing.T) {
	for _, s := range []string{"", "hello", "a/b [shake] 123! ~done"} {
		if !isASCII(s) {
			t.Errorf("isASCII(%q) = false, want true", s)
		}
	}
	for _, s := range []string{"héllo", "emoji 😀", "Привет", "日本語"} {
		if isASCII(s) {
			t.Errorf("isASCII(%q) = true, want false", s)
		}
	}
}

// TestICFieldFontsASCIIFastPath pins that a plain message asks for NO fallback fonts, so the
// IC/OOC input boxes keep the exact single-font path (no per-frame font work) — the
// performance guarantee for the common case.
func TestICFieldFontsASCIIFastPath(t *testing.T) {
	a := &App{ctx: &Ctx{}}
	if p, e := a.icFieldFonts("plain ascii message"); p != nil || e != nil {
		t.Errorf("ASCII field got fallback fonts, want nil,nil (fast path)")
	}
	if p, e := a.icFieldFonts(""); p != nil || e != nil {
		t.Errorf("empty field got fallback fonts, want nil,nil")
	}
}

// TestFxEffectCoverage pins that the FX picker lists Off + EVERY effect exactly once, and each
// real effect has a distinct label and a markup tag — so no effect is unreachable from the UI.
func TestFxEffectCoverage(t *testing.T) {
	seen := map[uint8]bool{}
	for _, e := range fxEffectOrder {
		if seen[e] {
			t.Fatalf("effect %d is listed twice in the picker", e)
		}
		seen[e] = true
		if e != courtroom.TextEffectNone {
			if icEffectLabel(e) == "FX" {
				t.Errorf("effect %d falls back to the generic FX label (missing a name)", e)
			}
			if effectTagName(e) == "" {
				t.Errorf("effect %d has no markup tag", e)
			}
		}
	}
	if len(fxEffectOrder) != int(courtroom.TextEffectCount) {
		t.Fatalf("picker lists %d entries, want %d (Off + every effect)", len(fxEffectOrder), courtroom.TextEffectCount)
	}
}

// TestApplyStickyEffect pins the dedicated FX button's send-time wrap: it wraps a normal
// message, but is a no-op when off, on a blankpost, or when the user already typed inline
// markup (inline wins). The wrap round-trips through ParseTextEffects to a whole-message span.
func TestApplyStickyEffect(t *testing.T) {
	a := &App{}
	a.icEffect = courtroom.TextEffectRainbow
	if got := a.applyStickyEffect("hello world"); got != "[rainbow]hello world[/rainbow]" {
		t.Fatalf("wrap = %q", got)
	}
	wire, spans := courtroom.ParseTextEffects(a.applyStickyEffect("hello world"))
	if wire != "hello world" || len(spans) != 1 || spans[0].Effect != courtroom.TextEffectRainbow {
		t.Fatalf("sticky wrap doesn't parse back: wire=%q spans=%v", wire, spans)
	}
	if got := a.applyStickyEffect(" "); got != " " { // blankpost untouched
		t.Errorf("blankpost wrap = %q, want \" \"", got)
	}
	if got := a.applyStickyEffect("type [shake]hi[/shake]"); strings.HasPrefix(got, "[rainbow]") {
		t.Errorf("sticky wrapped a message that already has inline markup: %q", got)
	}
	a.icEffect = courtroom.TextEffectNone
	if got := a.applyStickyEffect("plain"); got != "plain" { // off → untouched
		t.Errorf("off wrap = %q, want \"plain\"", got)
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
