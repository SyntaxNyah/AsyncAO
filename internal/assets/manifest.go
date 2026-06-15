package assets

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
)

// Server asset manifest, webAO-compatible: every server ships its own
// "extensions.json" at the asset origin (e.g. <origin>/extensions.json)
// declaring the extension order it serves per asset class. Reference:
// webAO src/client/fetchLists.ts fetchExtensions.
//
// Seeding the per-host learned table from it turns the cold-load probe
// storm into exactly one fetch per asset: the learned slot answers first,
// and the user's configured per-type order still covers servers without a
// manifest (plus any file that deviates from its server's declared format).

const (
	// ManifestFileName is the well-known manifest path under the origin.
	ManifestFileName = "extensions.json"
	// manifestExtCap bounds each parsed list (rule §17.4).
	manifestExtCap = 8
)

// Manifest mirrors webAO's extensions.json shape.
type Manifest struct {
	CharIcon   []string `json:"charicon_extensions"`
	Emote      []string `json:"emote_extensions"`
	Emotions   []string `json:"emotions_extensions"`
	Background []string `json:"background_extensions"`
}

// manifestKnownExts is the decoder-supported image set a manifest may
// declare; anything else (audio, typos, future formats) is dropped.
var manifestKnownExts = map[string]bool{
	config.ExtWebP: true,
	config.ExtAVIF: true,
	config.ExtAPNG: true,
	config.ExtGIF:  true,
	config.ExtPNG:  true,
	config.ExtJPG:  true,
}

// ParseManifest decodes and sanitizes extensions.json content.
func ParseManifest(data []byte) (*Manifest, error) {
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("assets: parsing %s: %w", ManifestFileName, err)
	}
	m.CharIcon = sanitizeManifestExts(m.CharIcon)
	m.Emote = sanitizeManifestExts(m.Emote)
	m.Emotions = sanitizeManifestExts(m.Emotions)
	m.Background = sanitizeManifestExts(m.Background)
	return &m, nil
}

// sanitizeManifestExts lowercases, maps webAO's pseudo-suffixes
// (".webp.static"/".webp.animated" — animation is a payload property
// here, never a separate probe) onto plain .webp, drops unknown entries,
// dedupes, and caps the list.
func sanitizeManifestExts(in []string) []string {
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, e := range in {
		e = strings.ToLower(strings.TrimSpace(e))
		if e == ".webp.static" || e == ".webp.animated" {
			e = config.ExtWebP
		}
		if !manifestKnownExts[e] || seen[e] {
			continue
		}
		seen[e] = true
		out = append(out, e)
		if len(out) >= manifestExtCap {
			break
		}
	}
	return out
}

// manifestSeedTargets maps each manifest class onto the asset types it
// governs (emote art covers sprites, shout bubbles, and misc art — the
// same files family on AO servers; backgrounds cover desk overlays).
func (m *Manifest) manifestSeedTargets() []struct {
	exts  []string
	types []AssetType
} {
	return []struct {
		exts  []string
		types []AssetType
	}{
		{m.CharIcon, []AssetType{AssetTypeCharIcon}},
		{m.Emote, []AssetType{AssetTypeCharSprite, AssetTypeShoutBubble, AssetTypeMisc}},
		{m.Emotions, []AssetType{AssetTypeEmoteButton}},
		{m.Background, []AssetType{AssetTypeBackground, AssetTypeDeskOverlay}},
	}
}

// SeedLearned writes each class's primary extension into the learned table
// for host: the learned slot holds exactly one ext per (host, type), and
// the manifest's first valid entry IS that ext. Returns how many types
// were seeded. Call Resolver.WarmFromPrefs afterwards to publish.
func (m *Manifest) SeedLearned(prefs *config.AssetPreferences, host string) int {
	if prefs == nil || host == "" {
		return 0
	}
	// Desks default to WebP and ignore the manifest unless the player opts in:
	// a server declaring e.g. PNG backgrounds (which desks share a class with)
	// shouldn't silently drag desks off WebP. See defaultDeskFollowManifest /
	// Settings → Assets → "Always use WebP for desks".
	deskFollows := prefs.DeskFollowsManifest()
	seeded := 0
	for _, target := range m.manifestSeedTargets() {
		if len(target.exts) == 0 {
			continue
		}
		for _, t := range target.types {
			if t == AssetTypeDeskOverlay && !deskFollows {
				continue
			}
			prefs.RecordLearned(host, t.Name(), target.exts[0])
			seeded++
		}
	}
	return seeded
}
