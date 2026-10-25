package theme

// THE WIRE NAMES, PUBLISHED (v1.90.0 W7a).
//
// One concern: hand the editor's enum dropdowns the format's OWN name tables.
//
// WHY THIS EXISTS RATHER THAN A LIST IN internal/ui. The enum tables above are the
// format — "WIRE NAMES ARE THE FORMAT: append-only, at the END, never renamed and
// never repurposed" (sidecar.go). An editor dropdown offering a hand-copied list is
// a SECOND copy of the format that no gate compares against the first, so a value
// appended here would ship an editor that cannot select it, and a value spelled
// differently there would write a theme this package refuses to read. It is the
// same argument the caps block makes for const aliases: two copies of a name are
// two names.
//
// EVERY ACCESSOR RETURNS A COPY. The tables are package state read on the parse
// path; handing out the backing array would let a caller rename a wire value at
// runtime, which is the one thing the append-only rule cannot survive. Cold path
// only — the editor builds its table once at package init.

// ElementKindNames / ElementBandNames / SpaceNames / FitNames / EffectNames /
// AlignNames / ConditionAxisNames publish one enum's wire names, indexed by the
// enum's own value. Pinned against the enums themselves by
// TestPublishedEnumNamesMatchTheTables.
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
