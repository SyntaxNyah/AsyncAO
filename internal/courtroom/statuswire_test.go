package courtroom

import "testing"

// TestStatusWireRoundTrip pins the #M1 codec: a status encodes to an invisible run,
// decodes back, and leaves the visible text clean.
func TestStatusWireRoundTrip(t *testing.T) {
	marker := EncodeStatusChangeMarker(StatusBusy, StatusNone)
	if marker == "" {
		t.Fatal("a status change encoded to nothing")
	}
	for _, r := range marker {
		if r != zwStart && octalIndex(r) < 0 {
			t.Fatalf("marker carries a visible rune U+%04X", r)
		}
	}
	got, ok := DecodeStatusMarker("hi" + marker)
	if !ok || got != StatusBusy {
		t.Fatalf("round trip = (%v,%v), want (Busy,true)", got, ok)
	}
	if clean := stripZeroWidth("hi" + marker); clean != "hi" {
		t.Errorf("stripped text = %q, want %q", clean, "hi")
	}
}

// TestStatusChangeAndClear pins send-on-change + clear: unchanged sends nothing, and
// returning to None is a clear (ok=true + None) so the receiver drops the badge.
func TestStatusChangeAndClear(t *testing.T) {
	if EncodeStatusChangeMarker(StatusAFK, StatusAFK) != "" {
		t.Error("an unchanged status should send no marker")
	}
	clear := EncodeStatusChangeMarker(StatusNone, StatusAFK)
	if clear == "" {
		t.Fatal("clearing a status should transmit a marker")
	}
	if got, ok := DecodeStatusMarker("x" + clear); !ok || got != StatusNone {
		t.Errorf("clear decoded to (%v,%v), want (None,true)", got, ok)
	}
}

// TestStatusCoexist pins that status, profile, and style frames share one message and
// each decoder reads only its own — and a status-only message isn't read as a style.
func TestStatusCoexist(t *testing.T) {
	style := SpriteStyle{Tint: true, R: 5, G: 6, B: 7}
	prof := WireProfile{Pronouns: "they/them"}
	msg := "objection" + style.EncodeMarker() + prof.EncodeMarker() + EncodeStatusChangeMarker(StatusWriting, StatusNone)

	if s, _ := DecodeSpriteStyle(msg); s != style {
		t.Errorf("style lost in a 3-frame message: %+v", s)
	}
	if p, ok := DecodeProfileMarker(msg); !ok || p != prof {
		t.Errorf("profile lost in a 3-frame message: (%+v,%v)", p, ok)
	}
	if st, ok := DecodeStatusMarker(msg); !ok || st != StatusWriting {
		t.Errorf("status lost in a 3-frame message: (%v,%v)", st, ok)
	}

	// A status-only message must NOT register as a style (a style clear would wipe it).
	sOnly := "afk" + EncodeStatusChangeMarker(StatusAFK, StatusNone)
	if HasStyleMarker(sOnly) {
		t.Error("a status-only message falsely reported a style marker")
	}
	if HasProfileMarker(sOnly) {
		t.Error("a status-only message falsely reported a profile marker")
	}
}

// TestRememberStatus pins the per-character store: remember, recall, a clear frees it,
// and a blank name is ignored.
func TestRememberStatus(t *testing.T) {
	c := &Courtroom{}
	c.rememberStatus("Phoenix", StatusBusy)
	if s, ok := c.RemoteStatus("Phoenix"); !ok || s != StatusBusy {
		t.Errorf("RemoteStatus = (%v,%v)", s, ok)
	}
	c.rememberStatus("Phoenix", StatusNone) // clear frees it
	if _, ok := c.RemoteStatus("Phoenix"); ok {
		t.Error("a cleared status should be gone")
	}
	c.rememberStatus("", StatusAFK)
	if _, ok := c.RemoteStatus(""); ok {
		t.Error("a blank character name should not be stored")
	}
}
