package courtroom

import "github.com/SyntaxNyah/AsyncAO/internal/assets"

// AssetRef is one asset a scene needs, for the self-contained archive exporter.
// Base is an extensionless AO base (sprite / background / desk) the exporter
// resolves via Manager.ResolveRaw, OR a complete URL when Exact is set (music
// carries its own extension). Alt is the bare-spelling sprite fallback that AO
// packs use instead of the "(a)"/"(b)" prefix ("" for non-sprites).
type AssetRef struct {
	Base  string
	Alt   string
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
//
// Exotica (shout bubbles, blip sounds, pair-partner edge art) is intentionally
// omitted: the zero-fallback renderer degrades gracefully on a missing asset, so
// v1 bundles the spine and those simply 404 on replay rather than bloating every
// archive. Keep this in sync with begin / setBackground (the prefetch sites).
func SceneAssets(urls URLBuilder, startBg string, events []Event) []AssetRef {
	bg := startBg
	var out []AssetRef
	seen := make(map[string]struct{})
	add := func(base, alt string, t assets.AssetType, exact bool) {
		if base == "" {
			return
		}
		key := t.Name() + "\x00" + base
		if _, dup := seen[key]; dup {
			return
		}
		seen[key] = struct{}{}
		out = append(out, AssetRef{Base: base, Alt: alt, Type: t, Exact: exact})
	}
	for _, ev := range events {
		switch ev.Kind {
		case EventBackground:
			if ev.Text != "" {
				bg = ev.Text
			}
		case EventMusic:
			if ev.Text != "" && !isAreaTransfer(ev.Text) && !isMusicStop(ev.Text) {
				add(urls.MusicURL(ev.Text), "", assets.AssetTypeMusic, true)
			}
		case EventMessage:
			m := ev.Message
			if m == nil {
				continue
			}
			bgPart, deskPart := PositionScene(m.Side) // Scene.Position == msg.Side
			add(urls.Background(bg, bgPart), "", assets.AssetTypeBackground, false)
			add(urls.Background(bg, deskPart), "", assets.AssetTypeDeskOverlay, false)
			bare := urls.EmoteBare(m.CharName, m.Emote) // packs ship "(a)X" OR bare "X"
			add(urls.Emote(m.CharName, m.Emote, EmoteIdle), bare, assets.AssetTypeCharSprite, false)
			add(urls.Emote(m.CharName, m.Emote, EmoteTalk), bare, assets.AssetTypeCharSprite, false)
			if hasPreanim(m) {
				add(urls.Emote(m.CharName, m.PreEmote, EmotePreanim), "", assets.AssetTypeCharSprite, false)
			}
		}
	}
	return out
}
