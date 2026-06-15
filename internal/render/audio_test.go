package render

import "testing"

// TestBuiltinAlertWAV pins that the synthesized callword/friend fallback ping
// is a structurally valid, non-empty 16-bit mono PCM WAV. It's the guaranteed
// default alert sound, so a malformed header — which SDL_mixer would reject,
// leaving callword/friend pings silent in the field — must fail here instead.
func TestBuiltinAlertWAV(t *testing.T) {
	const headerSize = 44
	w := builtinAlertWAV()
	if len(w) <= headerSize {
		t.Fatalf("alert WAV too small: %d bytes (no PCM samples)", len(w))
	}
	tag := func(off int) string { return string(w[off : off+4]) }
	if tag(0) != "RIFF" || tag(8) != "WAVE" || tag(12) != "fmt " || tag(36) != "data" {
		t.Fatalf("bad WAV header tags: %q %q %q %q", tag(0), tag(8), tag(12), tag(36))
	}
	le32 := func(off int) int {
		return int(w[off]) | int(w[off+1])<<8 | int(w[off+2])<<16 | int(w[off+3])<<24
	}
	le16 := func(off int) int { return int(w[off]) | int(w[off+1])<<8 }
	if riff := le32(4); riff != len(w)-8 {
		t.Errorf("RIFF chunk size = %d, want %d", riff, len(w)-8)
	}
	if data := le32(40); data != len(w)-headerSize {
		t.Errorf("data chunk size = %d, want %d", data, len(w)-headerSize)
	}
	if rate := le32(24); rate != 44100 {
		t.Errorf("sample rate = %d, want 44100", rate)
	}
	if ch := le16(22); ch != 1 {
		t.Errorf("channels = %d, want 1 (mono)", ch)
	}
	if bits := le16(34); bits != 16 {
		t.Errorf("bits/sample = %d, want 16", bits)
	}
}
