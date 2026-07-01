package courtroom

import (
	"reflect"
	"testing"
)

// TestParseEffectTags pins the markup parser: span positions over the cleaned text,
// unknown brackets left literal, and an unclosed tag running to the end.
func TestParseEffectTags(t *testing.T) {
	cases := []struct {
		raw       string
		wantClean string
		wantSpans []TextEffectSpan
	}{
		{"plain text", "plain text", nil},
		{"ab[shake]cd[/shake]ef", "abcdef", []TextEffectSpan{{2, 2, TextEffectShake}}},
		{"[rainbow]all of it[/rainbow]", "all of it", []TextEffectSpan{{0, 9, TextEffectRainbow}}},
		{"x[wave]yz", "xyz", []TextEffectSpan{{1, 2, TextEffectWave}}},                                        // unclosed → to end
		{"keep [OOC] literal", "keep [OOC] literal", nil},                                                     // unknown bracket
		{"a[shake]b[wave]c[/wave]", "abc", []TextEffectSpan{{1, 1, TextEffectShake}, {2, 1, TextEffectWave}}}, // switch
		{"[/shake]nope", "nope", nil},                                                                         // stray close → nothing
	}
	for _, tc := range cases {
		clean, spans := parseEffectTags(tc.raw)
		if clean != tc.wantClean {
			t.Errorf("parseEffectTags(%q) clean = %q, want %q", tc.raw, clean, tc.wantClean)
		}
		if !reflect.DeepEqual(spans, tc.wantSpans) {
			t.Errorf("parseEffectTags(%q) spans = %v, want %v", tc.raw, spans, tc.wantSpans)
		}
	}
}

// TestParseTextEffectsAlignment is the crux: when \cN colour markup interleaves with effect
// tags, the decoded spans must index the receiver's FINAL display text (MessageText =
// StripChatMarkup(wire)), proving the two strips commute.
func TestParseTextEffectsAlignment(t *testing.T) {
	raw := `ab\c2[shake]cd[/shake]\c0ef`
	wire, spans := ParseTextEffects(raw)
	// The receiver shows what the typewriter shows: StripChatMarkup of the wire text.
	display := StripChatMarkup(wire)
	if display != "abcdef" {
		t.Fatalf("display = %q, want abcdef", display)
	}
	if len(spans) != 1 {
		t.Fatalf("spans = %v, want one", spans)
	}
	sp := spans[0]
	got := string([]rune(display)[sp.Start : sp.Start+sp.Len])
	if got != "cd" || sp.Effect != TextEffectShake {
		t.Errorf("span covers %q (effect %d), want \"cd\" shake", got, sp.Effect)
	}
}

// TestEffectsMarkerRoundTrip pins encode→decode of the invisible frame.
func TestEffectsMarkerRoundTrip(t *testing.T) {
	spans := []TextEffectSpan{{0, 3, TextEffectShake}, {300, 5, TextEffectRainbow}}
	msg := "hello world" + EncodeEffectsMarker(spans)
	got, ok := DecodeEffectsMarker(msg)
	if !ok {
		t.Fatal("DecodeEffectsMarker found nothing")
	}
	if !reflect.DeepEqual(got, spans) {
		t.Errorf("round-trip = %v, want %v", got, spans)
	}
	// The frame is invisible: it carries no visible runes (DecodeSpriteStyle strips them).
	if _, clean := DecodeSpriteStyle(msg); clean != "hello world" {
		t.Errorf("clean = %q, want \"hello world\" (effects frame must be zero-width)", clean)
	}
	if s, _ := parseEffectTags(EncodeEffectsMarker(nil)); s != "" {
		t.Errorf("empty spans must encode to nothing, got %q", s)
	}
}

// TestNewEffectsParseAndDegrade pins the #M5+ additions: every new tag parses to its effect id,
// a new effect round-trips through the marker, and an effect id past TextEffectCount is dropped by
// the decoder — the graceful path an OLDER client takes for a NEWER client's effect.
func TestNewEffectsParseAndDegrade(t *testing.T) {
	for tag, want := range map[string]uint8{
		"bounce": TextEffectBounce, "sway": TextEffectSway, "shiver": TextEffectShiver,
		"wobble": TextEffectWobble, "tremble": TextEffectTremble, "float": TextEffectFloat,
		"pulse": TextEffectPulse, "gradient": TextEffectGradient, "blink": TextEffectBlink,
		"sparkle": TextEffectSparkle,
	} {
		if _, spans := ParseTextEffects("[" + tag + "]hi[/" + tag + "]"); len(spans) != 1 || spans[0].Effect != want {
			t.Errorf("[%s] parsed to %v, want one span with effect %d", tag, spans, want)
		}
	}

	spans := []TextEffectSpan{{0, 4, TextEffectSparkle}} // a new effect round-trips through the wire
	if got, ok := DecodeEffectsMarker("word" + EncodeEffectsMarker(spans)); !ok || !reflect.DeepEqual(got, spans) {
		t.Errorf("sparkle round-trip = %v ok=%v, want %v", got, ok, spans)
	}

	over := []TextEffectSpan{{0, 3, TextEffectCount + 5}} // an id past the cap (a newer client's effect)
	if _, ok := DecodeEffectsMarker("word" + EncodeEffectsMarker(over)); ok {
		t.Error("an effect id >= TextEffectCount must be dropped by the decoder (graceful old-client degradation)")
	}
}

// TestEffectsFourFrameCoexist proves the four zero-width frame types (style / profile /
// status / effects) ride one message together and each decodes independently — the channel
// is magic-byte discriminated.
func TestEffectsFourFrameCoexist(t *testing.T) {
	style := SpriteStyle{Tint: true, R: 200, G: 40, B: 40, Glow: true}
	prof := WireProfile{Pronouns: "he/him", Tag: "lawyer"}
	spans := []TextEffectSpan{{0, 2, TextEffectShake}, {3, 4, TextEffectRainbow}}
	msg := "hi there" +
		style.EncodeMarker() +
		prof.EncodeMarker() +
		EncodeStatusChangeMarker(StatusAFK, StatusNone) +
		EncodeEffectsMarker(spans)

	gotStyle, clean := DecodeSpriteStyle(msg)
	if clean != "hi there" {
		t.Errorf("clean = %q, want \"hi there\"", clean)
	}
	if gotStyle != style || !HasStyleMarker(msg) {
		t.Errorf("style frame lost: %+v", gotStyle)
	}
	if gotProf, ok := DecodeProfileMarker(msg); !ok || gotProf != prof {
		t.Errorf("profile frame lost: %+v ok=%v", gotProf, ok)
	}
	if st, ok := DecodeStatusMarker(msg); !ok || st != StatusAFK {
		t.Errorf("status frame lost: %v ok=%v", st, ok)
	}
	if gotSpans, ok := DecodeEffectsMarker(msg); !ok || !reflect.DeepEqual(gotSpans, spans) {
		t.Errorf("effects frame lost: %v ok=%v", gotSpans, ok)
	}
}

// TestEffectsMarkerBenign: a malformed / absent frame yields no spans and never panics, and
// a profile-only message is not misread as effects.
func TestEffectsMarkerBenign(t *testing.T) {
	if _, ok := DecodeEffectsMarker("no markers here"); ok {
		t.Error("plain text decoded effects")
	}
	prof := WireProfile{Pronouns: "they/them"}
	if _, ok := DecodeEffectsMarker("msg" + prof.EncodeMarker()); ok {
		t.Error("a profile frame was misread as effects")
	}
	// Truncated effects frame (claims 8 spans, carries none) → benign nil.
	short := packZeroWidth([]byte{effectsMarkerMagic, 8})
	if _, ok := DecodeEffectsMarker("x" + short); ok {
		t.Error("a truncated effects frame decoded")
	}
}

// TestParseTextEffectsNoAllocPlain: the overwhelming common case (a message with no effect
// markup) must not allocate spans — the send path runs this on every message.
func TestParseTextEffectsNoAllocPlain(t *testing.T) {
	if allocs := testing.AllocsPerRun(100, func() {
		_, spans := ParseTextEffects("just a normal message, no effects")
		if spans != nil {
			t.Fatal("plain message produced spans")
		}
	}); allocs != 0 {
		t.Errorf("ParseTextEffects(plain) allocated %.1f/op, want 0", allocs)
	}
}
