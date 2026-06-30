package courtroom

import "testing"

// TestTypingMarkerRoundTrip pins the typing wire (#3): a pulse round-trips, and plain
// text or a DIFFERENT zero-width frame is never mistaken for one.
func TestTypingMarkerRoundTrip(t *testing.T) {
	m := EncodeTypingMarker()
	if m == "" {
		t.Fatal("EncodeTypingMarker returned empty")
	}
	if !IsTypingMarker(m) {
		t.Error("IsTypingMarker should detect its own marker")
	}
	if IsTypingMarker("") || IsTypingMarker("hello there") {
		t.Error("plain text wrongly detected as a typing marker")
	}
	// A status frame (magic 0x71) must not read as a typing frame (magic 0x75).
	if IsTypingMarker(EncodeStatusChangeMarker(StatusAFK, StatusNone)) {
		t.Error("a status frame wrongly detected as typing")
	}
}
