package courtroom

import "github.com/SyntaxNyah/AsyncAO/internal/assets"

// blankSpeakerSentinel is AO's "no character" placeholder — a narrator/spectator
// line with no sprite (alongside the empty name).
const blankSpeakerSentinel = "-"

// IsBlankSpeaker reports whether a character name is the AO "no character"
// sentinel: empty, or the "-" placeholder. Such a speaker owns no sprite, so
// asset enumeration skips it instead of inventing always-missing "characters//…"
// refs.
func IsBlankSpeaker(charName string) bool {
	return charName == "" || charName == blankSpeakerSentinel
}

// AssetRef is one asset a scene needs, for the self-contained archive exporter.
// The DESK's nil is deliberate and must stay — set_scene's desk arm has no
// fallback (BackgroundFallbacks' documented asymmetry, pinned by
// TestTheDeskKeepsItsNoFallbackContract); "symmetrising" it with the
// background's ladder would resurrect the previous room's desk under a custom
// position. Add a ladder here only when the live prefetch site grows one.
type AssetRef struct {
	Base  string
	Alts  []string
	Type  assets.AssetType
	Exact bool
}

// SceneAssets enumerates the DISTINCT assets a scene needs to render — the set
// the self-contained archive bundles so a `.aorec` keeps its visuals when the
// origin CDN dies. It mirrors what Courtroom.begin / setBackground prefetch for
// the CORE: the position background + desk per (background, side), character
// idle/talk sprites (+ the bare-spelling fallback), pre-animation sprites, and
// playable music. De-duplicated by (type, base), so a 100-line scene with three
// characters bundles three characters' art, not a hundred copies — that keeps
// the archive as small as possible.
func SceneAssets(urls URLBuilder, startBg string, events []Event) []AssetRef {
	bg := startBg
	var out []AssetRef
	seen := make(map[string]struct{})
	add := func(base string, alts []string, t assets.AssetType, exact bool) {
		if base == "" {
			return
		}
		key := t.Name() + "\x00" + base
		if _, dup := seen[key]; dup {
			return
		}
		seen[key] = struct{}{}
		out = append(out, AssetRef{Base: base, Alts: alts, Type: t, Exact: exact})
	}
	for _, ev := range events {
		switch ev.Kind {
		case EventBackground:
			if ev.Text != "" {
				bg = ev.Text
			}
		case EventMusic:
			if ev.Text != "" && !isAreaTransfer(ev.Text) && !isMusicStop(ev.Text) {
				add(urls.MusicURL(ev.Text), nil, assets.AssetTypeMusic, true)
			}
		case EventMessage:
			m := ev.Message
			if m == nil {
				continue
			}
			bgPart, deskPart := PositionScene(m.Side) // Scene.Position == msg.Side
			// The background carries the witness ladder (prefetchScenery's chain):
			add(urls.Background(bg, bgPart), backgroundAltURLs(urls, bg, bgPart), assets.AssetTypeBackground, false)
			add(urls.Background(bg, deskPart), nil, assets.AssetTypeDeskOverlay, false)
			// Sprites carry the full spelling chain: "(a)X" → bare X → "(a)/X".
			// A blank speaker ("" or the "-" no-character sentinel) or a blank emote
			// has no sprite to bundle — enumerating it only invents "characters//…"
			// / "characters/-/…" refs that always miss, inflating the report.
			if !IsBlankSpeaker(m.CharName) && m.Emote != "" {
				add(urls.Emote(m.CharName, m.Emote, EmoteIdle), urls.EmoteAlts(m.CharName, m.Emote, EmoteIdle), assets.AssetTypeCharSprite, false)
				add(urls.Emote(m.CharName, m.Emote, EmoteTalk), urls.EmoteAlts(m.CharName, m.Emote, EmoteTalk), assets.AssetTypeCharSprite, false)
				if hasPreanim(m) {
					add(urls.Emote(m.CharName, m.PreEmote, EmotePreanim), nil, assets.AssetTypeCharSprite, false)
				}
			}
		}
	}
	return out
}
