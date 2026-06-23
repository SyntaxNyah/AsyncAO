package courtroom

import "testing"

// TestSpriteStyleOutlineWire pins #8's backward-compatible payload extension: outline +
// drop-shadow round-trip (a v2 frame), a plain style still emits the original 9-byte frame,
// and a hand-built v1 (9-byte) vs v2 (10-byte) payload both decode correctly — so a future
// edit can't silently break mixed-build compatibility.
func TestSpriteStyleOutlineWire(t *testing.T) {
	// v2 round-trip: the new flags survive alongside a base field.
	s := SpriteStyle{Tint: true, R: 10, G: 20, B: 30, Outline: true, DropShadow: true, Glitch: true}
	got, clean := DecodeSpriteStyle("hi" + s.EncodeMarker())
	if clean != "hi" {
		t.Fatalf("clean = %q, want hi", clean)
	}
	if got != s {
		t.Fatalf("v2 round-trip: got %+v, want %+v", got, s)
	}

	// A style with NO outline/shadow must still encode the original 9-byte frame (so older
	// clients + existing behaviour are untouched).
	plain := SpriteStyle{Glow: true}
	if len(plain.payloadBytes()) != spriteStyleBytes {
		t.Errorf("plain payload = %d bytes, want %d (no flags2 appended)", len(plain.payloadBytes()), spriteStyleBytes)
	}
	if g, _ := DecodeSpriteStyle("x" + plain.EncodeMarker()); g != plain {
		t.Errorf("plain round-trip changed: %+v", g)
	}
	if len(s.payloadBytes()) != spriteStyleBytesV2 {
		t.Errorf("outline payload = %d bytes, want %d", len(s.payloadBytes()), spriteStyleBytesV2)
	}

	// Hand-built v1 (9 bytes) decodes WITHOUT outline/shadow; v1+flags2 (10 bytes) decodes
	// WITH them; and an old decoder seeing only the first 9 bytes of a v2 frame still gets
	// the base style and never leaks an outline.
	v1 := []byte{spriteStyleVersion, styleFlagTint, 9, 9, 9, 0, 0, 0, 0}
	if d := styleFromBytes(v1); d.Outline || d.DropShadow || !d.Tint {
		t.Errorf("v1 decode wrong: %+v", d)
	}
	v2 := append(append([]byte(nil), v1...), styleFlag2Outline|styleFlag2DropShadow)
	if d := styleFromBytes(v2); !d.Outline || !d.DropShadow || !d.Tint {
		t.Errorf("v2 decode wrong: %+v", d)
	}
	if d := styleFromBytes(v2[:spriteStyleBytes]); !d.Tint || d.Outline || d.DropShadow {
		t.Errorf("v2-as-v1 truncation lost the base style or leaked outline: %+v", d)
	}
}
