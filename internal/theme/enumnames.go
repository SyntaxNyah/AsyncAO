package theme

// The format's wire-name tables for the editor's dropdowns, so a hand-copied list cannot disagree.
// Each accessor returns a copy, as a caller must not rename a value at runtime.

// ElementKindNames and its siblings publish one enum's wire names, indexed by value.
func ElementKindNames() []string   { return copyNames(elementKindNames[:]) }
func ElementBandNames() []string   { return copyNames(elementBandNames[:]) }
func SpaceNames() []string         { return copyNames(spaceNames[:]) }
func FitNames() []string           { return copyNames(fitNames[:]) }
func EffectNames() []string        { return copyNames(effectNames[:]) }
func AlignNames() []string         { return copyNames(alignNames[:]) }
func ConditionAxisNames() []string { return copyNames(conditionAxisNames[:]) }

func copyNames(src []string) []string {
	out := make([]string, len(src))
	copy(out, src)
	return out
}
