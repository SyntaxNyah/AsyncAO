package courtroom

import (
	"strings"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
)

// TestSpriteStyleRoundTrip pins encode→decode: a styled message carries its style
// invisibly in the text, and decoding recovers the exact style plus the original
// visible text.
func TestSpriteStyleRoundTrip(t *testing.T) {
	cases := []SpriteStyle{
		{Tint: true, R: 255, G: 60, B: 60},              // red Phoenix
		{Tint: true, R: 80, G: 255, B: 120, Glow: true}, // glowing green
		{Opacity: 40}, // ghostly
		{Tint: true, R: 10, G: 20, B: 30, Opacity: 70, Glow: true, Wobble: true, Spin: true},
		{Wobble: true},
		{Spin: true},
		{Scale: 130},                      // bigger sprite
		{Rotation: 64},                    // tilt
		{FlipH: true},                     // mirror
		{Brightness: 160, HueCycle: true}, // brighter + rainbow
		{Tint: true, R: 1, G: 2, B: 3, Opacity: 50, Glow: true, Wobble: true, Spin: true,
			HueCycle: true, FlipH: true, Brightness: 80, Scale: 120, Rotation: 200}, // the works
	}
	for _, want := range cases {
		marker := want.EncodeMarker()
		if marker == "" {
			t.Fatalf("active style %+v encoded to empty marker", want)
		}
		msg := "Objection!" + marker
		got, clean := DecodeSpriteStyle(msg)
		if got != want {
			t.Errorf("round-trip: got %+v, want %+v", got, want)
		}
		if clean != "Objection!" {
			t.Errorf("clean text = %q, want %q", clean, "Objection!")
		}
	}
}

// TestInactiveStyleEncodesEmpty: a do-nothing style adds nothing to the message
// (so unstyled messages stay byte-identical, and the fast no-marker path holds).
func TestInactiveStyleEncodesEmpty(t *testing.T) {
	for _, s := range []SpriteStyle{{}, {Opacity: 100}} {
		if m := s.EncodeMarker(); m != "" {
			t.Errorf("inactive style %+v encoded to %q, want empty", s, m)
		}
	}
}

// TestMarkerIsInvisible is the cross-client guarantee: every rune the encoder
// emits is zero-width, so AO2/webAO render NOTHING — the message text is
// unaffected, not just degraded like the \cN colour markup.
func TestMarkerIsInvisible(t *testing.T) {
	marker := SpriteStyle{Tint: true, R: 1, G: 2, B: 3, Opacity: 50, Glow: true, Wobble: true, Spin: true}.EncodeMarker()
	if marker == "" {
		t.Fatal("expected a non-empty marker")
	}
	for _, r := range marker {
		if r != zwStart && octalIndex(r) < 0 {
			t.Errorf("marker contains a non-codec rune U+%04X — standard clients might show it", r)
		}
	}
	// Density guard: the 9-byte payload packs 3 bits/rune → 24 data runes + the
	// sentinel = 25. (A regression to 1 bit/rune is the ~73-rune blip-spam tail that
	// webAO listeners complained about.)
	if n := len([]rune(marker)); n != 25 {
		t.Errorf("marker is %d runes, want 25 (sentinel + 24 at 3 bits/rune)", n)
	}
}

// TestStyleSurvivesWireEscaping is the AsyncAO-side proof: the marker rides
// through the protocol field escape/unescape round-trip (the same path a real MS
// message takes) intact. (Server-side survival is confirmed in live playtest —
// the failure mode is benign: a mangled marker just yields no style.)
func TestStyleSurvivesWireEscaping(t *testing.T) {
	want := SpriteStyle{Tint: true, R: 200, G: 30, B: 240, Opacity: 60, Glow: true}
	// A message that ALSO contains wire-special characters, to be thorough.
	msg := "Take that! 100% #real$ & true" + want.EncodeMarker()
	wire := protocol.EncodeField(msg)
	back := protocol.DecodeField(wire)
	got, clean := DecodeSpriteStyle(back)
	if got != want {
		t.Errorf("after wire round-trip: style = %+v, want %+v", got, want)
	}
	if clean != "Take that! 100% #real$ & true" {
		t.Errorf("after wire round-trip: clean = %q", clean)
	}
}

// TestNoMarkerUntouched: a plain message decodes to the zero style and the
// original text, unchanged.
func TestNoMarkerUntouched(t *testing.T) {
	const plain = "just a normal message, nothing hidden"
	got, clean := DecodeSpriteStyle(plain)
	if got.Active() {
		t.Errorf("plain message produced an active style %+v", got)
	}
	if clean != plain {
		t.Errorf("plain message text changed: %q", clean)
	}
	if StripSpriteStyle(plain) != plain {
		t.Errorf("StripSpriteStyle altered a plain message")
	}
}

// TestStripRemovesAllZeroWidth: the cleaned text has none of the codec runes left
// (so the typewriter, clipboard copy, and callword matcher never see them).
func TestStripRemovesAllZeroWidth(t *testing.T) {
	msg := "a" + SpriteStyle{Glow: true}.EncodeMarker() + "b"
	clean := StripSpriteStyle(msg)
	for _, r := range clean {
		if r == zwStart || octalIndex(r) >= 0 {
			t.Fatalf("clean text still has a codec rune U+%04X", r)
		}
	}
	if clean != "ab" {
		t.Errorf("clean = %q, want %q", clean, "ab")
	}
}

// TestStripsOlderClientMarker pins the mixed-build playtest case: a NEW client must
// fully clean an OLDER client's 1-bit marker (U+2060 framing a run of U+200B/U+200C)
// so no invisible runes leak into the log — 200B/200C are in the new symbol set, so
// strip drops them. (Decode yields no style: the old run mis-parses under the 3-bit
// reader, the intended benign degrade until everyone updates to render styles again.)
func TestStripsOlderClientMarker(t *testing.T) {
	var b strings.Builder
	b.WriteString("hi")
	b.WriteRune(0x2060)       // old sentinel (same rune as the new zwStart)
	for i := 0; i < 72; i++ { // 72 one-bit runes = the old 9-byte encoding
		if i%2 == 0 {
			b.WriteRune(0x200B)
		} else {
			b.WriteRune(0x200C)
		}
	}
	if clean := StripSpriteStyle(b.String()); clean != "hi" {
		t.Errorf("old 1-bit marker not fully stripped: %q (%d runes left)", clean, len([]rune(clean))-2)
	}
}

// TestAlphaModFloorAndDefault pins the opacity clamp: 0/100 are opaque, and an
// explicit low value can't drop below the visible floor (no invisible sprites).
func TestAlphaModFloorAndDefault(t *testing.T) {
	if a := (SpriteStyle{}).AlphaMod(); a != 255 {
		t.Errorf("default opacity AlphaMod = %d, want 255", a)
	}
	if a := (SpriteStyle{Opacity: 100}).AlphaMod(); a != 255 {
		t.Errorf("100%% opacity AlphaMod = %d, want 255", a)
	}
	// 5% is below the floor; it clamps up to minVisibleOpacity, never to ~0.
	floorMod := uint8(minVisibleOpacity * 255 / 100)
	if a := (SpriteStyle{Opacity: 5}).AlphaMod(); a != floorMod {
		t.Errorf("5%% opacity AlphaMod = %d, want floored %d", a, floorMod)
	}
}

// TestCorruptMarkerIsBenign: a truncated zero-width run yields no style (and the
// text strips clean) rather than a garbage style — a length-limiting server can
// never turn into a corrupted sprite.
func TestCorruptMarkerIsBenign(t *testing.T) {
	full := SpriteStyle{Tint: true, R: 9, G: 9, B: 9}.EncodeMarker()
	// Keep the sentinel + only a few bits (simulate truncation).
	truncated := "hi" + string([]rune(full)[:5])
	got, clean := DecodeSpriteStyle(truncated)
	if got.Active() {
		t.Errorf("truncated marker produced an active style %+v", got)
	}
	if !strings.HasPrefix(clean, "hi") {
		t.Errorf("clean text = %q, want it to keep the visible prefix", clean)
	}
}
