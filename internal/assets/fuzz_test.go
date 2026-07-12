package assets

import "testing"

// FuzzParseManifest drives the extensions.json parser (manifest.go): every
// server ships its own manifest at the asset origin (webAO convention), so the
// bytes are attacker-controlled. Contract is robustness — no panic, no hang, and
// every sanitized list stays bounded (rules #4, #7). encoding/json already
// rejects malformed JSON with an error, so the interesting surface is the
// post-decode sanitize (dedupe/cap/pseudo-suffix normalization).
func FuzzParseManifest(f *testing.F) {
	// Real shapes, pseudo-suffix normalization, over-cap dedupe, and degenerate
	// JSON (wrong shape / null / truncated) — the last three exercise the decode
	// error path, the rest the post-decode sanitize.
	seeds := []string{
		bundledVanillaManifestJSON,
		`{"charicon_extensions":[".png"],"emote_extensions":[".webp.static",".webp.animated",".webp"]}`,
		`{"background_extensions":[".gif",".GIF",".gif",".png",".jpg",".avif",".apng",".png",".bmp"]}`,
		`{"emotions_extensions":["  .PNG  ",".exe",".webp.static"]}`,
		`{}`,
		`[]`,
		`null`,
		`{"emote_extensions":null}`,
		``,
		`{`,
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		m, err := ParseManifest(data)
		if err != nil {
			return // malformed JSON is rejected, not a crash
		}
		// Every sanitized list must stay within the named cap (§17.4).
		for _, list := range [][]string{m.CharIcon, m.Emote, m.Emotions, m.Background} {
			if len(list) > manifestExtCap {
				t.Fatalf("sanitized manifest list of %d exceeds cap %d: %v", len(list), manifestExtCap, list)
			}
		}
		_ = m.manifestSeedTargets()
	})
}
