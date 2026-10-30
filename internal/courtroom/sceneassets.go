package courtroom

import "github.com/SyntaxNyah/AsyncAO/internal/assets"

// blankSpeakerSentinel is AO's "no character" placeholder.
const blankSpeakerSentinel = "-"

// IsBlankSpeaker reports an empty or "-" name, which owns no sprite, so enumeration must not invent refs.
func IsBlankSpeaker(charName string) bool {
	return charName == "" || charName == blankSpeakerSentinel
}

// AssetRef is one asset the exporter needs: an AO base, or a URL when Exact is set.
// A desk's nil alts must stay: a ladder would resurrect the previous room's desk.
type AssetRef struct {
	Base  string
	Alts  []string
	Type  assets.AssetType
	Exact bool
}

// SceneAssets lists the core assets an archive bundles; keep in sync with begin and setBackground.
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
			// The background carries the witness ladder, or an archive shows black where live showed the witness view.
			add(urls.Background(bg, bgPart), backgroundAltURLs(urls, bg, bgPart), assets.AssetTypeBackground, false)
			add(urls.Background(bg, deskPart), nil, assets.AssetTypeDeskOverlay, false)
			// Sprites carry the spelling chain "(a)X", X, "(a)/X"; a blank speaker or emote has no sprite to bundle.
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
