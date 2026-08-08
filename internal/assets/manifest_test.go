package assets

import (
	"path/filepath"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
)

// TestManifestParseSanitizes pins the extensions.json hygiene: webAO's
// ".webp.static" pseudo-suffix maps to .webp, casing normalizes, unknown
// extensions drop, duplicates collapse.
func TestManifestParseSanitizes(t *testing.T) {
	data := []byte(`{
		"charicon_extensions": [".PNG", ".bogus"],
		"emote_extensions": [".webp.static", ".gif", ".webp"],
		"emotions_extensions": [],
		"background_extensions": [".png", ".apng"]
	}`)
	m, err := ParseManifest(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.CharIcon) != 1 || m.CharIcon[0] != config.ExtPNG {
		t.Errorf("charicon = %v", m.CharIcon)
	}
	// .webp.static → .webp (kept first), .gif kept, trailing .webp dedupes.
	if len(m.Emote) != 2 || m.Emote[0] != config.ExtWebP || m.Emote[1] != config.ExtGIF {
		t.Errorf("emote = %v", m.Emote)
	}
	if len(m.Background) != 2 {
		t.Errorf("background = %v", m.Background)
	}
	if _, err := ParseManifest([]byte("not json")); err == nil {
		t.Error("malformed manifest parsed")
	}
}

// TestBundledVanillaManifest pins the shipped official-vanilla example: it parses
// and carries the AO defaults — PNG for char icons / emote buttons / backgrounds,
// .apng-first for emote sprites — and seeds without error.
func TestBundledVanillaManifest(t *testing.T) {
	m := BundledVanillaManifest()
	if len(m.Background) != 1 || m.Background[0] != config.ExtPNG {
		t.Errorf("vanilla background = %v, want [.png]", m.Background)
	}
	if len(m.CharIcon) != 1 || m.CharIcon[0] != config.ExtPNG {
		t.Errorf("vanilla charicon = %v, want [.png]", m.CharIcon)
	}
	if len(m.Emotions) != 1 || m.Emotions[0] != config.ExtPNG {
		t.Errorf("vanilla emotions = %v, want [.png]", m.Emotions)
	}
	if len(m.Emote) == 0 || m.Emote[0] != config.ExtAPNG {
		t.Errorf("vanilla emote = %v, want .apng first", m.Emote)
	}
	prefs, err := config.New(filepath.Join(t.TempDir(), config.PrefsFileName))
	if err != nil {
		t.Fatal(err)
	}
	defer prefs.Close()
	if n := m.SeedLearned(prefs, "vanilla.example"); n == 0 {
		t.Error("bundled vanilla manifest seeded nothing")
	}
}

// TestManifestSeedLearned pins the seeding fan-out: emote art covers
// sprites + shout bubbles (NOT misc — extensions.json has no misc key, and
// live mirrors ship png misc art beside webp emotes), backgrounds cover desk
// overlays, empty classes seed nothing, and the learned entry keeps EVERY
// declared extension in the manifest's own order.
//
// The full list, not just the first: a server declaring two formats for a class
// serves both, and truncating to exts[0] left the other half of its fleet behind
// a single candidate with no way to fall back (see learnedTable / promoteExt).
func TestManifestSeedLearned(t *testing.T) {
	prefs, err := config.New(filepath.Join(t.TempDir(), config.PrefsFileName))
	if err != nil {
		t.Fatal(err)
	}
	defer prefs.Close()

	m := &Manifest{
		CharIcon:   []string{config.ExtPNG},
		Emote:      []string{config.ExtWebP, config.ExtGIF},
		Background: []string{config.ExtPNG},
	}
	const host = "miku.pizza"
	// Desks follow the manifest by default (deliberate default flip, user order
	// 2026-08-08 — desks auto-detect like every other class), so the background
	// class seeds background AND desk: charicon(1) + emote(sprite, bubble = 2)
	// + background(bg, desk = 2) = 5; Misc unseeded by design.
	if n := m.SeedLearned(prefs, host); n != 5 {
		t.Fatalf("seeded %d, want 5 (desks follow by default, misc never)", n)
	}
	snap := prefs.LearnedSnapshot()
	// The emote class declares TWO formats, so sprites and shout bubbles must
	// carry both — .webp first (probed), .gif behind it (the recovery ladder).
	checks := map[string][]string{
		config.LearnedKey(host, config.TypeCharIcon):    {config.ExtPNG},
		config.LearnedKey(host, config.TypeCharSprite):  {config.ExtWebP, config.ExtGIF},
		config.LearnedKey(host, config.TypeShoutBubble): {config.ExtWebP, config.ExtGIF},
		config.LearnedKey(host, config.TypeBackground):  {config.ExtPNG},
		config.LearnedKey(host, config.TypeDeskOverlay): {config.ExtPNG},
	}
	for key, want := range checks {
		got := snap[key]
		if len(got) != len(want) {
			t.Errorf("learned[%s] = %v, want %v", key, got, want)
			continue
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("learned[%s] = %v, want %v (order matters: front is probed first)", key, got, want)
				break
			}
		}
	}
	if _, ok := snap[config.LearnedKey(host, config.TypeMisc)]; ok {
		t.Error("Misc seeded from the emote class (png misc art beside webp emotes is real — it must learn per host)")
	}
	if _, ok := snap[config.LearnedKey(host, config.TypeEmoteButton)]; ok {
		t.Error("empty emotions class seeded EmoteButton")
	}

	// Opt out: pin desks to WebP; the manifest may no longer touch DeskOverlay.
	// The clear mirrors what the Settings toggle does, so the pin is observable
	// as an ABSENT entry rather than a stale seeded one.
	prefs.SetDeskFollowManifest(false)
	prefs.ClearLearnedType(config.TypeDeskOverlay)
	if n := m.SeedLearned(prefs, host); n != 4 {
		t.Fatalf("seeded %d with the WebP pin on, want 4 (desk exempt)", n)
	}
	if got, ok := prefs.LearnedSnapshot()[config.LearnedKey(host, config.TypeDeskOverlay)]; ok {
		t.Errorf("DeskOverlay seeded from the manifest despite the WebP pin: %v", got)
	}
}
