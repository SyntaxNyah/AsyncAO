package courtroom

import "testing"

// TestReactionMarkerRoundTrip pins encode→decode of the invisible reaction frame and that
// it carries no visible runes (the reactor's own message stays clean).
func TestReactionMarkerRoundTrip(t *testing.T) {
	ref := MakeReactionRef("Phoenix", "Objection!")
	r := WireReaction{Ref: ref, Index: 3}
	msg := "I agree" + r.EncodeMarker()

	got, ok := DecodeReactionMarker(msg)
	if !ok {
		t.Fatal("DecodeReactionMarker found nothing")
	}
	if got != r {
		t.Errorf("round-trip = %+v, want %+v", got, r)
	}
	// The frame is invisible: stripping the zero-width codec leaves only the visible text.
	if clean := StripSpriteStyle(msg); clean != "I agree" {
		t.Errorf("clean = %q, want \"I agree\" (reaction frame must be zero-width)", clean)
	}
}

// TestReactionRefContentStable is the crux: the ref is a pure function of (char name, clean
// text), so every client computes the same value for the same wire message — and it's
// sensitive to both fields (so distinct messages get distinct refs), with the 0x00 separator
// preventing a field-boundary collision.
func TestReactionRefContentStable(t *testing.T) {
	a := MakeReactionRef("Phoenix", "Take that!")
	if a != MakeReactionRef("Phoenix", "Take that!") {
		t.Error("ref is not stable for identical inputs")
	}
	if a == MakeReactionRef("Edgeworth", "Take that!") {
		t.Error("ref ignored the character name")
	}
	if a == MakeReactionRef("Phoenix", "Take this!") {
		t.Error("ref ignored the text")
	}
	// Separator: "ab"+"c" must not hash the same as "a"+"bc".
	if MakeReactionRef("ab", "c") == MakeReactionRef("a", "bc") {
		t.Error("missing field separator: a boundary shift collided")
	}
}

// TestReactionFiveFrameCoexist proves a reaction frame rides one message alongside the four
// existing zero-width frames (style / profile / status / effects) and each still decodes
// independently — the channel is magic-byte discriminated, and a reaction frame must not be
// misread as (nor clobber) any of them.
func TestReactionFiveFrameCoexist(t *testing.T) {
	style := SpriteStyle{Tint: true, R: 200, G: 40, B: 40, Glow: true}
	prof := WireProfile{Pronouns: "he/him", Tag: "lawyer"}
	spans := []TextEffectSpan{{0, 2, TextEffectShake}}
	react := WireReaction{Ref: MakeReactionRef("Maya", "Nice one"), Index: 7}
	msg := "hi there" +
		style.EncodeMarker() +
		prof.EncodeMarker() +
		EncodeStatusChangeMarker(StatusAFK, StatusNone) +
		EncodeEffectsMarker(spans) +
		react.EncodeMarker()

	gotStyle, clean := DecodeSpriteStyle(msg)
	if clean != "hi there" {
		t.Errorf("clean = %q, want \"hi there\"", clean)
	}
	if gotStyle != style || !HasStyleMarker(msg) {
		t.Errorf("style frame lost beside a reaction: %+v", gotStyle)
	}
	if gotProf, ok := DecodeProfileMarker(msg); !ok || gotProf != prof {
		t.Errorf("profile frame lost beside a reaction: %+v ok=%v", gotProf, ok)
	}
	if st, ok := DecodeStatusMarker(msg); !ok || st != StatusAFK {
		t.Errorf("status frame lost beside a reaction: %v ok=%v", st, ok)
	}
	if _, ok := DecodeEffectsMarker(msg); !ok {
		t.Error("effects frame lost beside a reaction")
	}
	if gotReact, ok := DecodeReactionMarker(msg); !ok || gotReact != react {
		t.Errorf("reaction frame lost: %+v ok=%v", gotReact, ok)
	}
}

// TestReactionMarkerBenign: an absent / corrupt / unknown-emoji frame yields no reaction and
// never panics, and another codec's frame is not misread as a reaction.
func TestReactionMarkerBenign(t *testing.T) {
	if _, ok := DecodeReactionMarker("no markers here"); ok {
		t.Error("plain text decoded a reaction")
	}
	prof := WireProfile{Pronouns: "they/them"}
	if _, ok := DecodeReactionMarker("msg" + prof.EncodeMarker()); ok {
		t.Error("a profile frame was misread as a reaction")
	}
	// A reaction frame with a palette index this build doesn't know (a newer peer) → benign.
	future := packZeroWidth([]byte{reactionFrameMagic, reactionWireVersion, 0, 0, 0, 0, 0xFE})
	if _, ok := DecodeReactionMarker("x" + future); ok {
		t.Error("an unknown-emoji reaction frame decoded")
	}
	// Truncated reaction frame (magic+version only) → benign.
	short := packZeroWidth([]byte{reactionFrameMagic, reactionWireVersion})
	if _, ok := DecodeReactionMarker("x" + short); ok {
		t.Error("a truncated reaction frame decoded")
	}
	// An out-of-range index never encodes (nothing valid to send).
	if (WireReaction{Index: uint8(ReactionCount())}).EncodeMarker() != "" {
		t.Error("an out-of-range palette index encoded a frame")
	}
}

// TestReactionPalette pins the fixed palette: the count, range checking, and that the one
// BMP entry (the heart) carries its variation selector — a mangled source file (e.g. a lost
// U+FE0F) would render it as a black glyph / tofu, so pin the exact runes.
func TestReactionPalette(t *testing.T) {
	if ReactionCount() == 0 {
		t.Fatal("empty reaction palette")
	}
	if _, ok := ReactionEmoji(uint8(ReactionCount())); ok {
		t.Error("out-of-range index returned an emoji")
	}
	for i := 0; i < ReactionCount(); i++ {
		if s, ok := ReactionEmoji(uint8(i)); !ok || s == "" {
			t.Errorf("palette[%d] missing", i)
		}
	}
	// Index 2 is the red heart: U+2764 U+FE0F. Pinning the runes catches a UTF-8 mangle.
	heart, _ := ReactionEmoji(2)
	if rs := []rune(heart); len(rs) != 2 || rs[0] != 0x2764 || rs[1] != 0xFE0F {
		t.Errorf("palette[2] = %q (%U), want U+2764 U+FE0F", heart, rs)
	}
}

// TestReactionNoAlloc: the hot paths are allocation-free — MakeReactionRef runs per logged
// message (receiver side) and DecodeReactionMarker's no-marker fast path runs per message.
func TestReactionNoAlloc(t *testing.T) {
	if allocs := testing.AllocsPerRun(200, func() {
		_ = MakeReactionRef("Phoenix Wright", "a fairly typical line of dialogue")
	}); allocs != 0 {
		t.Errorf("MakeReactionRef allocated %.1f/op, want 0", allocs)
	}
	if allocs := testing.AllocsPerRun(200, func() {
		if _, ok := DecodeReactionMarker("just a normal message with no markers"); ok {
			t.Fatal("plain text decoded a reaction")
		}
	}); allocs != 0 {
		t.Errorf("DecodeReactionMarker(plain) allocated %.1f/op, want 0", allocs)
	}
}
