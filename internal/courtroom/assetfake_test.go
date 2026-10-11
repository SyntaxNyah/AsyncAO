package courtroom

// assetFake is a test double for AssetResolver with independently settable lookups,
// so a test can set only the lookup it exercises and leave the rest at their AO
// defaults.
type assetFake struct {
	effects func(char string) string
	overlay func(name, folder string) (OverlayResolved, bool)
	sprite  func(base string) bool
	audio   func(base string) bool
	scaling func(charName string) ScalingMode
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
