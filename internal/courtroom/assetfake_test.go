package courtroom

import "github.com/SyntaxNyah/AsyncAO/internal/theme"

// assetFake is a test double for AssetResolver with independently settable lookups,
// so a test can set only the lookup it exercises and leave the rest at their AO
// defaults.
type assetFake struct {
	effects func(char string) string
	overlay func(name, folder string) (OverlayResolved, bool)
	sprite  func(base string) bool
	// playable defaults to sprite, as most tests don't separate resident from fully decoded.
	playable func(base string) bool
	audio    func(base string) bool
	scaling  func(charName string) ScalingMode
	// files maps a resident base to the file it was decoded from; nil means none.
	files map[string]string
}

func (f assetFake) SceneryFile(base string) string { return f.files[base] }

// BackgroundDesign answers that no background has a design.ini.
func (f assetFake) BackgroundDesign(string) (*theme.INI, bool) { return nil, true }

func (f assetFake) SpritePlayable(base string) bool {
	if f.playable != nil {
		return f.playable(base)
	}
	return f.SpriteReady(base)
}

func (f assetFake) EffectsFolderFor(char string) string {
	if f.effects != nil {
		return f.effects(char)
	}
	return ""
}

func (f assetFake) OverlayFor(name, folder string) (OverlayResolved, bool) {
	if f.overlay != nil {
		return f.overlay(name, folder)
	}
	return OverlayResolved{}, false
}

func (f assetFake) SpriteReady(base string) bool {
	if f.sprite != nil {
		return f.sprite(base)
	}
	return true // not wired = not gated = "ready"
}

func (f assetFake) AudioReady(base string) bool {
	if f.audio != nil {
		return f.audio(base)
	}
	return true // not wired = not gated = "ready"
}

func (f assetFake) SpriteScaling(charName string) ScalingMode {
	if f.scaling != nil {
		return f.scaling(charName)
	}
	return ScalingAuto
}
