package courtroom

// blipFake is a test double for BlipResolver with independently settable lookups,
// so a test can set only the lookup it exercises and leave the rest at their AO
// defaults.
type blipFake struct {
	muted func(name string) bool
	vol   func(char string) int
	name  func(char string) (string, bool)
}

func (f blipFake) SFXMuted(name string) bool {
	if f.muted != nil {
		return f.muted(name)
	}
	return false
}

func (f blipFake) BlipVolumeFor(char string) int {
	if f.vol != nil {
		return f.vol(char)
	}
	return blipVolumeFull
}

func (f blipFake) BlipNameFor(char string) (string, bool) {
	if f.name != nil {
		return f.name(char)
	}
	return "", true
}
