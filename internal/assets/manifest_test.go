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

// TestManifestSeedLearned pins the seeding fan-out: emote art covers
// sprites + shout bubbles + misc, backgrounds cover desk overlays, empty
// classes seed nothing, and the learned slot gets the FIRST extension.
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
	// charicon(1) + emote(sprite, bubble, misc = 3) + background(bg, desk = 2)
	if n := m.SeedLearned(prefs, host); n != 6 {
		t.Fatalf("seeded %d, want 6", n)
	}
	snap := prefs.LearnedSnapshot()
	checks := map[string]string{
		config.LearnedKey(host, config.TypeCharIcon):    config.ExtPNG,
		config.LearnedKey(host, config.TypeCharSprite):  config.ExtWebP,
		config.LearnedKey(host, config.TypeShoutBubble): config.ExtWebP,
		config.LearnedKey(host, config.TypeMisc):        config.ExtWebP,
		config.LearnedKey(host, config.TypeBackground):  config.ExtPNG,
		config.LearnedKey(host, config.TypeDeskOverlay): config.ExtPNG,
	}
	for key, want := range checks {
		got := snap[key]
		if len(got) != 1 || got[0] != want {
			t.Errorf("learned[%s] = %v, want [%s]", key, got, want)
		}
	}
	if _, ok := snap[config.LearnedKey(host, config.TypeEmoteButton)]; ok {
		t.Error("empty emotions class seeded EmoteButton")
	}
}
