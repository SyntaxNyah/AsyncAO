package courtroom

import (
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
)

// TestSceneAssetsDedup pins the de-duplication that keeps archives small: two
// identical lines enumerate one set of art, not two — and sprite refs carry the
// bare-spelling fallback, music is included once.
func TestSceneAssetsDedup(t *testing.T) {
	urls := NewURLBuilder("https://cdn/base/")
	events := []Event{
		{Kind: EventBackground, Text: "gs4"},
		{Kind: EventMessage, Message: &protocol.ChatMessage{CharName: "Phoenix", Emote: "normal", Side: "wit"}},
		{Kind: EventMessage, Message: &protocol.ChatMessage{CharName: "Phoenix", Emote: "normal", Side: "wit"}}, // identical → no new refs
		{Kind: EventMusic, Text: "trial.opus"},
	}
	counts := map[assets.AssetType]int{}
	var sprites []AssetRef
	for _, r := range SceneAssets(urls, "", events) {
		counts[r.Type]++
		if r.Type == assets.AssetTypeCharSprite {
			sprites = append(sprites, r)
		}
	}
	if counts[assets.AssetTypeCharSprite] != 2 { // idle + talk, deduped across the two identical lines
		t.Errorf("sprites=%d want 2 (idle+talk, deduped)", counts[assets.AssetTypeCharSprite])
	}
	if counts[assets.AssetTypeBackground] != 1 || counts[assets.AssetTypeDeskOverlay] != 1 {
		t.Errorf("bg=%d desk=%d want 1,1", counts[assets.AssetTypeBackground], counts[assets.AssetTypeDeskOverlay])
	}
	if counts[assets.AssetTypeMusic] != 1 {
		t.Errorf("music=%d want 1", counts[assets.AssetTypeMusic])
	}
	for _, r := range sprites {
		// AO2's full spelling chain rides along: "(a)/X" folder + bare X.
		if len(r.Alts) != 2 {
			t.Errorf("sprite ref should carry the folder + bare alt spellings: %+v", r)
		}
	}
}
