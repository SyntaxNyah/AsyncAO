package assets

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
)

// extensions.json at the asset origin (webAO fetchLists.ts fetchExtensions) declares extension order per class;
// seeding the learned table from it gives one fetch per asset.

const (
	ManifestFileName = "extensions.json"
	// manifestExtCap bounds each parsed list (hard rule 4).
	manifestExtCap = 8
)

// Manifest mirrors webAO's extensions.json shape.
type Manifest struct {
	CharIcon   []string `json:"charicon_extensions"`
	Emote      []string `json:"emote_extensions"`
	Emotions   []string `json:"emotions_extensions"`
	Background []string `json:"background_extensions"`
	// Optional: webAO's file declares only image classes; absent, the default order and learning apply.
	SFX   []string `json:"sfx_extensions"`
	Blips []string `json:"blips_extensions"`
}

// manifestKnownExts is what a manifest may declare; a wrong-kind extension on a class self-corrects on probe.
var manifestKnownExts = map[string]bool{
	config.ExtWebP: true,
	config.ExtAVIF: true,
	config.ExtAPNG: true,
	config.ExtGIF:  true,
	config.ExtPNG:  true,
	config.ExtJPG:  true,
	config.ExtOpus: true,
	config.ExtOgg:  true,
	config.ExtMP3:  true,
	config.ExtWAV:  true,
}

// bundledVanillaManifestJSON is the vanilla AO extensions.json, the default for servers without one, as vanilla is PNG.
const bundledVanillaManifestJSON = `{
	"charicon_extensions": [".png"],
	"emote_extensions": [".apng", ".webp", ".png"],
	"emotions_extensions": [".png"],
	"background_extensions": [".png"]
}`

// BundledVanillaManifestJSON is that manifest as text, offered as the example for per-server format profiles.
const BundledVanillaManifestJSON = bundledVanillaManifestJSON

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
	m.SFX = sanitizeManifestExts(m.SFX)
	m.Blips = sanitizeManifestExts(m.Blips)
	return &m, nil
}

// sanitizeManifestExts lowercases, maps .webp.static/.webp.animated to .webp, drops unknowns, dedupes and caps.
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

// manifestSeedTargets maps classes to types; misc is not seeded from emotes, as mirrors such as miku.pizza differ.
func (m *Manifest) manifestSeedTargets() []struct {
	exts  []string
	types []AssetType
} {
	return []struct {
		exts  []string
		types []AssetType
	}{
		{m.CharIcon, []AssetType{AssetTypeCharIcon}},
		{m.Emote, []AssetType{AssetTypeCharSprite, AssetTypeShoutBubble}},
		{m.Emotions, []AssetType{AssetTypeEmoteButton}},
		{m.Background, []AssetType{AssetTypeBackground, AssetTypeDeskOverlay}},
		{m.SFX, []AssetType{AssetTypeSFX}},
		{m.Blips, []AssetType{AssetTypeBlip}},
	}
}

// SeedLearned writes every declared extension in order, as servers such as umineko.online genuinely mix formats.
// Call Resolver.WarmFromPrefs afterwards.
func (m *Manifest) SeedLearned(prefs *config.AssetPreferences, host string) int {
	if prefs == nil || host == "" {
		return 0
	}
	// Desks follow the manifest unless the user opts out for servers with wrong manifests.
	deskFollows := prefs.Load().DeskFollowManifest
	seeded := 0
	for _, target := range m.manifestSeedTargets() {
		if len(target.exts) == 0 {
			continue
		}
		for _, t := range target.types {
			if t == AssetTypeDeskOverlay && !deskFollows {
				continue
			}
			prefs.RecordLearnedList(host, t.Name(), target.exts)
			seeded++
		}
	}
	return seeded
}
