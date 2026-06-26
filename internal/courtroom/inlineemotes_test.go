package courtroom

import (
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
)

// stubEmotes is a tiny resolver for the tests — ASCII replacements so the assertions don't
// depend on emoji bytes, and a plain switch so it allocates nothing (the 0-alloc gate).
func stubEmotes(stem string) (string, bool) {
	switch stem {
	case "joy":
		return "JOY", true
	case "fire":
		return "FIRE", true
	}
	return "", false
}

// TestExpandInlineEmotesParser pins the parser: resolved stems are replaced, unknown tokens
// and stray colons (URLs, times) are left literal, a nil resolver / no-colon string is
// returned untouched, and the non-substituting cases allocate nothing.
func TestExpandInlineEmotesParser(t *testing.T) {
	cases := []struct{ in, want string }{
		{"plain, no colons", "plain, no colons"},
		{"hi :joy: there", "hi JOY there"},
		{":fire::joy:", "FIREJOY"},                                   // adjacent
		{"nope :unknown: nope", "nope :unknown: nope"},               // unknown stem
		{"see http://example.com now", "see http://example.com now"}, // URL colon
		{"meet at 12:30 sharp", "meet at 12:30 sharp"},               // time colon
		{":joy:", "JOY"}, // whole string
		{"a:b", "a:b"},   // no closing colon
	}
	for _, tc := range cases {
		if got := ExpandInlineEmotes(tc.in, stubEmotes); got != tc.want {
			t.Errorf("ExpandInlineEmotes(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	if got := ExpandInlineEmotes(":joy:", nil); got != ":joy:" {
		t.Errorf("nil resolver must pass through: got %q", got)
	}
	for _, s := range []string{"a normal message", "ratio 16:9 at http://h:8080/x"} {
		if allocs := testing.AllocsPerRun(100, func() {
			if ExpandInlineEmotes(s, stubEmotes) != s {
				t.Fatalf("non-substituting input changed: %q", s)
			}
		}); allocs != 0 {
			t.Errorf("ExpandInlineEmotes(%q) allocated %.1f/op, want 0", s, allocs)
		}
	}
}

// TestChatboxExpandsInlineEmotes pins #18 slice 2 end to end through begin(): with a resolver
// set, a known :shortcode: in the chatbox text is expanded (so the reveal + raster see the
// emoji); with NO resolver it's left literal; and when the message carries effect spans the
// expansion is GATED OFF (the wire span indices were measured over the literal text, so
// expanding would misalign them).
func TestChatboxExpandsInlineEmotes(t *testing.T) {
	icMsg := func(text string) *protocol.ChatMessage {
		return &protocol.ChatMessage{
			CharName: "Phoenix", Emote: "normal", Message: text, Side: "wit",
			EmoteMod: protocol.EmoteModIdle, // no preanim stall — begin → typewriter directly
		}
	}

	// Resolver set → expanded.
	room, _, _, _ := newCourtroomRig(t)
	room.InlineEmote = stubEmotes
	room.HandleEvent(Event{Kind: EventMessage, Message: icMsg(":joy: hi")})
	room.SkipToIdle()
	if room.Scene.MessageText != "JOY hi" {
		t.Errorf("chatbox didn't expand the shortcode: MessageText = %q, want %q", room.Scene.MessageText, "JOY hi")
	}

	// No resolver (default) → literal.
	plain, _, _, _ := newCourtroomRig(t)
	plain.HandleEvent(Event{Kind: EventMessage, Message: icMsg(":joy: hi")})
	plain.SkipToIdle()
	if plain.Scene.MessageText != ":joy: hi" {
		t.Errorf("nil resolver expanded the shortcode: MessageText = %q, want literal", plain.Scene.MessageText)
	}

	// Effect spans present → gated off (left literal), so the spans stay aligned.
	gated, _, _, _ := newCourtroomRig(t)
	gated.InlineEmote = stubEmotes
	withFX := ":joy: hi" + EncodeEffectsMarker([]TextEffectSpan{{Start: 0, Len: 2, Effect: TextEffectShake}})
	gated.HandleEvent(Event{Kind: EventMessage, Message: icMsg(withFX)})
	gated.SkipToIdle()
	if gated.Scene.MessageText != ":joy: hi" {
		t.Errorf("a message with effect spans expanded the shortcode (span misalignment risk): MessageText = %q", gated.Scene.MessageText)
	}
	if len(gated.Scene.MessageEffects) == 0 {
		t.Error("precondition failed: the effects frame didn't decode, so the gate wasn't exercised")
	}
}

// TestCenterPrefix pins the webAO "~~" convention through begin(): a message whose
// visible text starts with ~~ centres the chatbox (Scene.Centered) and the marker is
// stripped from the display text; a plain message does neither; and a ~~ message
// carrying transmitted effect spans keeps them aligned (shifted left by the 2 stripped
// runes).
func TestCenterPrefix(t *testing.T) {
	icMsg := func(text string) *protocol.ChatMessage {
		return &protocol.ChatMessage{
			CharName: "Phoenix", Emote: "normal", Message: text, Side: "wit",
			EmoteMod: protocol.EmoteModIdle,
		}
	}

	room, _, _, _ := newCourtroomRig(t)
	room.HandleEvent(Event{Kind: EventMessage, Message: icMsg("~~hello")})
	room.SkipToIdle()
	if !room.Scene.Centered || room.Scene.MessageText != "hello" {
		t.Errorf("~~hello: Centered=%v MessageText=%q, want true/\"hello\"", room.Scene.Centered, room.Scene.MessageText)
	}

	plain, _, _, _ := newCourtroomRig(t)
	plain.HandleEvent(Event{Kind: EventMessage, Message: icMsg("hello")})
	plain.SkipToIdle()
	if plain.Scene.Centered || plain.Scene.MessageText != "hello" {
		t.Errorf("hello: Centered=%v MessageText=%q, want false/\"hello\"", plain.Scene.Centered, plain.Scene.MessageText)
	}

	// ~~ + an effect span over "shake" (index 2..6 in "~~shake me") → stripped, centred,
	// and the span shifts to 0..4 so it still covers "shake" in the displayed "shake me".
	fx, _, _, _ := newCourtroomRig(t)
	withFX := "~~shake me" + EncodeEffectsMarker([]TextEffectSpan{{Start: 2, Len: 5, Effect: TextEffectShake}})
	fx.HandleEvent(Event{Kind: EventMessage, Message: icMsg(withFX)})
	fx.SkipToIdle()
	if !fx.Scene.Centered || fx.Scene.MessageText != "shake me" {
		t.Errorf("~~ + fx: Centered=%v MessageText=%q, want true/\"shake me\"", fx.Scene.Centered, fx.Scene.MessageText)
	}
	if len(fx.Scene.MessageEffects) != 1 || fx.Scene.MessageEffects[0].Start != 0 || fx.Scene.MessageEffects[0].Len != 5 {
		t.Errorf("effect span must shift left by the stripped ~~ (Start 0, Len 5): got %+v", fx.Scene.MessageEffects)
	}
}
