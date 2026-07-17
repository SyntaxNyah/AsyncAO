package render

import (
	"testing"
	"unsafe"

	"github.com/veandco/go-sdl2/mix"
)

// TestPlayMusicAtIdempotent pins the cross-tab resume guard: PlayMusicAt for the
// EXACT URL already playing is a no-op — it neither re-fetches nor re-seeks a live
// stream, so a switch back to a tab whose track was kept rolling (ducked) preserves
// its true position untouched. A disabled device is also a no-op. Both paths return
// BEFORE touching the (nil-in-test) Manager, so this runs headlessly like
// TestPurgePendingMusic. CurrentMusicURL / MusicPlaying accessors are pinned too.
func TestPlayMusicAtIdempotent(t *testing.T) {
	// Disabled device: no-op, no Manager access, no pending entry.
	off := &Audio{pending: map[string]pendingPlay{}}
	off.PlayMusicAt("http://cdn/song.opus", true, 0, 12)
	if len(off.pending) != 0 {
		t.Error("a disabled device must not queue a pending music fetch")
	}
	if off.MusicPlaying() {
		t.Error("a disabled device must report no music playing")
	}

	// Enabled + this exact URL already the live stream: PlayMusicAt is a no-op.
	// A non-nil sentinel *mix.Music makes MusicPlaying() true without decoding; the
	// guard only compares != nil and never dereferences it, so pointing it at a real
	// dummy byte is safe (mix.Music is an opaque C type — can't be allocated with new).
	var dummy byte
	live := &Audio{
		enabled:  true,
		pending:  map[string]pendingPlay{},
		musicURL: "http://cdn/song.opus",
		music:    (*mix.Music)(unsafe.Pointer(&dummy)), // non-nil sentinel; never dereferenced
	}
	if live.CurrentMusicURL() != "http://cdn/song.opus" {
		t.Fatalf("CurrentMusicURL = %q, want the live URL", live.CurrentMusicURL())
	}
	if !live.MusicPlaying() {
		t.Fatal("MusicPlaying must be true with an enabled device and a loaded stream")
	}
	live.PlayMusicAt("http://cdn/song.opus", true, 0, 30) // same URL → idempotent
	if len(live.pending) != 0 {
		t.Error("PlayMusicAt for the already-playing URL must NOT queue a fetch (idempotent, position preserved)")
	}
}

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

// TestPurgePendingMusic pins the stale-track-race fix (§1.2): switching to a
// new music URL must leave at most one pendingMusic entry so a late-arriving
// delivery for a superseded track can't revert playback. Non-music pending
// entries (SFX/blip/etc.) are never touched. No SDL/mixer or Manager is needed
// — the purge only mutates the local a.pending map — so this test runs (not
// skips) headlessly.
func TestPurgePendingMusic(t *testing.T) {
	newAudio := func() *Audio {
		return &Audio{pending: map[string]pendingPlay{
			"http://cdn/song.opus":    {kind: pendingMusic},
			"http://cdn/song2.opus":   {kind: pendingMusic},
			"sounds/general/sfx.opus": {kind: pendingSFX},
			"sounds/blips/blip.opus":  {kind: pendingBlip},
		}}
	}

	// Switching tracks: keep only the newly requested music URL; every other
	// pendingMusic entry is dropped, non-music entries survive.
	a := newAudio()
	a.purgePendingMusic("http://cdn/song2.opus")
	if _, ok := a.pending["http://cdn/song2.opus"]; !ok {
		t.Errorf("kept music URL was purged")
	}
	if _, ok := a.pending["http://cdn/song.opus"]; ok {
		t.Errorf("superseded music URL survived the purge — stale-track race not closed")
	}
	if _, ok := a.pending["sounds/general/sfx.opus"]; !ok {
		t.Errorf("pendingSFX entry was wrongly purged")
	}
	if _, ok := a.pending["sounds/blips/blip.opus"]; !ok {
		t.Errorf("pendingBlip entry was wrongly purged")
	}
	musicKeys := 0
	for _, p := range a.pending {
		if p.kind == pendingMusic {
			musicKeys++
		}
	}
	if musicKeys != 1 {
		t.Errorf("pendingMusic keys after switch = %d, want exactly 1", musicKeys)
	}

	// Stop (keep=="" ): every pendingMusic entry is dropped, non-music survive.
	a = newAudio()
	a.purgePendingMusic("")
	for u, p := range a.pending {
		if p.kind == pendingMusic {
			t.Errorf("pendingMusic entry %q survived purgePendingMusic(\"\")", u)
		}
	}
	if _, ok := a.pending["sounds/general/sfx.opus"]; !ok {
		t.Errorf("pendingSFX entry was wrongly purged by purgePendingMusic(\"\")")
	}
	if _, ok := a.pending["sounds/blips/blip.opus"]; !ok {
		t.Errorf("pendingBlip entry was wrongly purged by purgePendingMusic(\"\")")
	}
}
