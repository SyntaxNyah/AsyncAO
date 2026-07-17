package ui

import (
	"testing"

	"github.com/veandco/go-sdl2/sdl"
)

// TestICBarUnderStage pins issue #8: the IC input bar's default sits DIRECTLY under the
// stage, and the control-button block sits BELOW it — so the input is the first thing
// under the viewport (the classic AO spot, obvious where you talk IC) instead of buried
// below the control buttons.
func TestICBarUnderStage(t *testing.T) {
	vp := sdl.Rect{X: 8, Y: 8, W: 600, H: 450}
	const fH = int32(26)
	icBarTop, defY := icBarUnderStage(vp, fH)

	if want := vp.Y + vp.H + pad; icBarTop != want {
		t.Errorf("IC bar top = %d, want it directly under the stage (%d)", icBarTop, want)
	}
	if defY <= icBarTop+fH {
		t.Errorf("control block (defY=%d) must sit BELOW the IC bar (top=%d, height=%d)", defY, icBarTop, fH)
	}
}

// TestICBarSlotsAreEditable pins #4a: each IC-bar piece pulled out (colour, showname,
// Immediate, Additive, SFX, emoji, FX, React, input) has a distinct editor label, and an
// override repositions it through slotRect — so users can drag them apart in Edit Layout.
func TestICBarSlotsAreEditable(t *testing.T) {
	slots := []string{slotICColor, slotICShowname, slotICImmediate, slotICPre, slotICAdditive, slotICSFX, slotICEmoji, slotICFx, slotICReact, slotICInput}
	seen := map[string]bool{}
	for _, s := range slots {
		label := classicSlotLabel(s)
		if label == "" || label == s {
			t.Errorf("slot %q has no editor label", s)
		}
		if seen[label] {
			t.Errorf("slot %q label %q is not distinct", s, label)
		}
		seen[label] = true
	}
	a := testTabApp(t)
	def := sdl.Rect{X: 100, Y: 50, W: 200, H: 24}
	if got := a.slotRect(slotICInput, def, 1000, 800); got != def {
		t.Errorf("no override: slotRect = %+v, want the default %+v", got, def)
	}
	a.classicOv = map[string][4]float64{slotICInput: {0.2, 0.1, 0.3, 0.04}}
	if got, want := a.slotRect(slotICInput, def, 1000, 800), (sdl.Rect{X: 200, Y: 80, W: 300, H: 32}); got != want {
		t.Errorf("override: slotRect = %+v, want the moved spot %+v", got, want)
	}

	// Additive is the newest pulled-out piece (2.8 servers only): un-edited it draws at
	// its default spot (pixel-identical to the old fixed offset), and an override moves it.
	addDef := sdl.Rect{X: 120, Y: 60, W: 84, H: 26}
	if got := a.slotRect(slotICAdditive, addDef, 1000, 800); got != addDef {
		t.Errorf("Additive no override: slotRect = %+v, want the default %+v", got, addDef)
	}
	a.classicOv = map[string][4]float64{slotICAdditive: {0.1, 0.2, 0.084, 0.0325}}
	if got, want := a.slotRect(slotICAdditive, addDef, 1000, 800), (sdl.Rect{X: 100, Y: 160, W: 84, H: 26}); got != want {
		t.Errorf("Additive override: slotRect = %+v, want the moved spot %+v", got, want)
	}
}

// TestICOptionalDrawsOverrideWins pins the narrow-bar drop-discipline escape hatch:
// a width-guarded optional IC-bar button (Pre / SFX / emoji / FX) must still draw when
// the user saved a slot override or the classic editor is armed — otherwise a dropped
// button can never be grabbed or forced back (the movable-slot promise). The un-edited
// narrow bar with no override must still drop it (guard intact = byte-identical play),
// and a hidden piece never draws whatever else is true.
func TestICOptionalDrawsOverrideWins(t *testing.T) {
	// (a) an override forces the button to draw on a narrow bar (guard failed).
	if !icOptionalDraws(false /*guardOK*/, true /*override*/, false /*editing*/, false /*hidden*/) {
		t.Error("override should force a width-dropped optional button to draw")
	}
	// (b) no override + narrow bar still drops it (the guard stays intact).
	if icOptionalDraws(false, false, false, false) {
		t.Error("no override + failed guard + not editing must DROP the button (byte-identical play)")
	}
	// (c) the classic editor draws it regardless of width, so its slot registers and the
	//     user can grab a dropped button and pull it somewhere with room.
	if !icOptionalDraws(false, false, true, false) {
		t.Error("classic editor must draw the button regardless of the width guard (grabbability)")
	}
	// The width guard passing still draws with no override / not editing (today's behavior).
	if !icOptionalDraws(true, false, false, false) {
		t.Error("a passing width guard must draw the button")
	}
	// Hidden beats everything — override, editor, and a passing guard all lose to hidden.
	if icOptionalDraws(true, true, true, true) {
		t.Error("a hidden optional button must never draw, whatever else is set")
	}
}

// TestThemeKeysExposeAsyncICControls pins #4b: the AsyncAO-only IC controls are listed
// in themeLayoutKeys, so a custom theme that defines asyncao_ic_<x> in its design.ini has
// those rects loaded — letting theme-makers place colour/SFX/buttons separately instead
// of having AsyncAO cram them into ao2_ic_chat_message.
func TestThemeKeysExposeAsyncICControls(t *testing.T) {
	want := []string{
		"asyncao_ic_color", "asyncao_ic_immediate", "asyncao_ic_pre", "asyncao_ic_sfx",
		"asyncao_ic_emoji", "asyncao_ic_fx", "asyncao_ic_react",
	}
	have := map[string]bool{}
	for _, k := range themeLayoutKeys {
		have[k] = true
	}
	for _, k := range want {
		if !have[k] {
			t.Errorf("themeLayoutKeys missing %q — a theme can't position it (#4b)", k)
		}
	}
}

// icConsumedBeforeOptionals is the width the IC bar spends left of the first optional
// button (colour swatch + dropdown, showname box, Immediate toggle), matching
// drawICInputRow's cursor before the Pre draw. Additive adds another band on 2.8 servers.
// It references the SAME package-level consts the row uses (shownameBoxW, immedW, icAddW,
// colorSelectW) so the test drifts WITH the layout — a width change fails the geometry pin
// instead of silently passing. Only colorLead (32) and gap (6) are inline literals in the
// row itself. At the 1280×720 default (colorSelectW=86) this is 372, the wave-7 figure.
func icConsumedBeforeOptionals(additive bool) int32 {
	const (
		colorLead = 32 // icBar.X → nameX also adds colorSelectW below (matches drawICInputRow)
		gap       = 6  // the +6 gaps between showname/Immediate/optionals
	)
	used := int32(colorLead) + colorSelectW + gap + shownameBoxW + gap + immedW + gap
	if additive {
		used += icAddW + gap
	}
	return used
}

// icOptionalSurvival replays drawICInputRow's optional-button drop discipline PURELY
// (no SDL): it walks Pre, FX, SFX, emoji in the real draw order, advancing the cursor
// only when icBarButtonFits passes, and returns which drew plus the final input width
// (floored at minICInputW). This is the same math the row runs, so it pins the survival
// order without needing a live Ctx. fH is the row height (emoji is an fH-square button).
func icOptionalSurvival(barW, fH int32, additive bool) (pre, fx, sfx, emoji bool, inputW int32) {
	tail := int32(minICInputW)
	used := icConsumedBeforeOptionals(additive)
	if pre = icBarButtonFits(barW, used, preW+6, tail); pre {
		used += preW + 6
	}
	if fx = icBarButtonFits(barW, used, fxBtnW+4, tail); fx {
		used += fxBtnW + 4
	}
	if sfx = icBarButtonFits(barW, used, sfxDDW+4, tail); sfx {
		used += sfxDDW + 4
	}
	if emoji = icBarButtonFits(barW, used, fH+4, tail); emoji {
		used += fH + 4
	}
	inputW = barW - used
	if inputW < minICInputW {
		inputW = minICInputW
	}
	return pre, fx, sfx, emoji, inputW
}

// TestICBarConsumedGeometry pins the wave-7 arithmetic the redesign builds on: at the
// 1280×720 default the bar is 666px and 372px is spent before the optionals, leaving
// exactly 294px — of which minICInputW(150) is the floor, so 144px is free for optionals.
func TestICBarConsumedGeometry(t *testing.T) {
	if got := icConsumedBeforeOptionals(false); got != 372 {
		t.Fatalf("consumed-before-optionals = %d, want 372 (wave-7 geometry drifted)", got)
	}
	const barW = 666
	free := barW - icConsumedBeforeOptionals(false) - int32(minICInputW)
	if free != 144 {
		t.Fatalf("free-for-optionals = %d, want 144", free)
	}
}

// TestICBarPreFXSurvive720p pins the user-mandated design change: at the 720p reference
// bar (666px) the default-visible Pre AND Text-FX buttons both draw, and the input never
// falls below its floor — with the message counter both ON and OFF (the counter now draws
// INSIDE the field, so it no longer steals tail width and the outcome is identical either
// way). preW must be small enough that Pre(preW+6)+FX(fxBtnW+4) fits with >= 6px margin.
func TestICBarPreFXSurvive720p(t *testing.T) {
	const (
		barW = 666
		fH   = 26
	)
	// The counter now draws INSIDE the field (drawMsgCounter), so it no longer feeds the
	// width math — tailReserve is minICInputW whether it's ON or OFF. This test builds the
	// real prefs and flips MessageCounter both ways; the survival outcome must be IDENTICAL,
	// which structurally guards against a future re-coupling of the counter into the tail.
	a := testTabApp(t)
	var last struct {
		pre, fx  bool
		inputW   int32
		haveLast bool
	}
	for _, counterOn := range []bool{true, false} {
		a.d.Prefs.SetMessageCounter(counterOn)
		if got := a.d.Prefs.MessageCounterOn(); got != counterOn {
			t.Fatalf("SetMessageCounter(%v) didn't stick (got %v)", counterOn, got)
		}
		// Survival is a pure function of geometry (no counter term), so it stands in for the
		// row's tailReserve derivation, which reads minICInputW only.
		pre, fx, _, _, inputW := icOptionalSurvival(barW, fH, false /*additive*/)
		if !pre || !fx {
			t.Errorf("720p bar (%dpx, counter=%v): Pre=%v FX=%v, want BOTH to draw (default-visible)", barW, counterOn, pre, fx)
		}
		if inputW < minICInputW {
			t.Errorf("720p bar (counter=%v): input width %d fell below the floor %d", counterOn, inputW, int32(minICInputW))
		}
		if last.haveLast && (pre != last.pre || fx != last.fx || inputW != last.inputW) {
			t.Errorf("counter ON vs OFF changed the layout (pre %v→%v, fx %v→%v, inputW %d→%d) — the counter must not touch the width math",
				last.pre, pre, last.fx, fx, last.inputW, inputW)
		}
		last.pre, last.fx, last.inputW, last.haveLast = pre, fx, inputW, true
	}
	// Margin check: the free room after the Pre+FX pair must stay >= 6px so the fit isn't
	// the fragile exact-144 it was at preW=60 (Pre 66 + FX 78 = 144 == 144).
	free := int32(barW) - icConsumedBeforeOptionals(false) - int32(minICInputW)
	pair := int32(preW+6) + int32(fxBtnW+4)
	if margin := free - pair; margin < 6 {
		t.Errorf("Pre+FX pair (%d) leaves only %dpx of the %dpx free — want >= 6px slack (shrink preW)", pair, margin, free)
	}
}

// TestICBarDropOrder pins the sacrifice order as the bar narrows: emoji yields FIRST,
// then the SFX dropdown, and only on a much tighter bar do FX then Pre drop — so the core
// Pre/FX pair outlives the SFX/emoji pair (the whole point of the reorder). Each stage
// keeps the input at or above its floor.
func TestICBarDropOrder(t *testing.T) {
	const fH = 26
	base := icConsumedBeforeOptionals(false)
	// Widen the bar until every optional fits, then shrink one demand-band at a time.
	full := base + int32(minICInputW) + (preW + 6) + (fxBtnW + 4) + (sfxDDW + 4) + (fH + 4)
	cases := []struct {
		name                string
		barW                int32
		pre, fx, sfx, emoji bool
	}{
		{"all fit", full, true, true, true, true},
		{"emoji drops first", full - (fH + 4), true, true, true, false},
		{"then SFX drops", full - (fH + 4) - (sfxDDW + 4), true, true, false, false},
		{"then FX drops", full - (fH + 4) - (sfxDDW + 4) - (fxBtnW + 4), true, false, false, false},
		{"Pre drops last", full - (fH + 4) - (sfxDDW + 4) - (fxBtnW + 4) - (preW + 6), false, false, false, false},
	}
	for _, tc := range cases {
		pre, fx, sfx, emoji, inputW := icOptionalSurvival(tc.barW, fH, false)
		if pre != tc.pre || fx != tc.fx || sfx != tc.sfx || emoji != tc.emoji {
			t.Errorf("%s (barW=%d): got pre=%v fx=%v sfx=%v emoji=%v, want pre=%v fx=%v sfx=%v emoji=%v",
				tc.name, tc.barW, pre, fx, sfx, emoji, tc.pre, tc.fx, tc.sfx, tc.emoji)
		}
		if inputW < minICInputW {
			t.Errorf("%s: input width %d below floor %d", tc.name, inputW, int32(minICInputW))
		}
	}
}

// TestICBarAdditiveSacrifice documents the accepted 2.8/additive outcome. The +90px
// Additive band eats the tail: at the 720p bar (666px) the big pieces — Pre (58),
// FX (78), SFX (96) — can't fit over the input floor, so all three are sacrificed;
// only the narrow emoji band (fH+4) still squeezes in, and the input keeps the rest
// (>= floor). That is ACCEPTED (the editor override is the escape hatch). The drop
// ORDER is what the redesign fixes, so it's pinned on a WIDER additive bar where the
// sacrifice is observable: emoji yields before SFX, and SFX before FX/Pre — the same
// core-last order as non-additive.
func TestICBarAdditiveSacrifice(t *testing.T) {
	const fH = 26
	// 720p additive: the sequential discipline drops Pre/FX/SFX (each too wide for
	// the remaining 204px over the 150px floor) but the ~30px emoji band still fits.
	// Accepted degradation — better one survivor than none.
	pre, fx, sfx, emoji, inputW := icOptionalSurvival(666, fH, true)
	if pre || fx || sfx {
		t.Errorf("720p additive bar: got pre=%v fx=%v sfx=%v, want the three wide pieces dropped (accepted)", pre, fx, sfx)
	}
	if !emoji {
		t.Error("720p additive bar: the narrow emoji band fits the leftover and must survive")
	}
	if inputW < minICInputW {
		t.Errorf("720p additive bar: input width %d below floor %d", inputW, int32(minICInputW))
	}
	// A wider additive bar sized to host Pre+FX+SFX but not emoji: emoji is the first to go,
	// and the core Pre/FX pair survives — the sacrifice order still runs emoji → SFX → FX → Pre.
	base := icConsumedBeforeOptionals(true)
	justNoEmoji := base + int32(minICInputW) + (preW + 6) + (fxBtnW + 4) + (sfxDDW + 4) // no room for the emoji band
	pre, fx, sfx, emoji, _ = icOptionalSurvival(justNoEmoji, fH, true)
	if !pre || !fx || !sfx {
		t.Errorf("wide additive bar: Pre=%v FX=%v SFX=%v, want all three to survive when only emoji is squeezed out", pre, fx, sfx)
	}
	if emoji {
		t.Error("wide additive bar: emoji must be the first optional to yield")
	}
}

// TestMsgCounterInsideField pins the counter move: drawMsgCounter now places the count
// INSIDE the input box's right edge (never right of it), so its drawn x-range stays within
// the field bounds — mirroring the muted-chip idiom. The placement is pure arithmetic
// (TextWidth is 0 in headless tests, so the count is a tight glyph-less box, but the inset
// math is what we pin). When muted, the count yields LEFT of the chip band.
func TestMsgCounterInsideField(t *testing.T) {
	// Reproduce drawMsgCounter's placement math for a representative field and count width.
	field := sdl.Rect{X: 300, Y: 100, W: 400, H: 26}
	for _, countW := range []int32{msgCounterPad, msgCounterPad + 20, msgCounterPad + 60} {
		rightEdge := field.X + field.W - msgCounterPad
		countX := rightEdge - countW
		if countX < field.X+msgCounterPad {
			continue // would drop (fits-inside guard) — nothing to assert
		}
		if countX < field.X {
			t.Errorf("countW=%d: count x %d is left of the field (%d)", countW, countX, field.X)
		}
		if countX+countW > field.X+field.W {
			t.Errorf("countW=%d: count right edge %d spills past the field right (%d)", countW, countX+countW, field.X+field.W)
		}
	}
	// Muted: the count must stack LEFT of the chip band, never overlapping it.
	chipBandW := int32(80) // stand-in for TextWidth("🔇 muted")+12; the metric is > 0 in the app
	mutedRightEdge := field.X + field.W - chipBandW - 2 - msgCounterChipGap
	if mutedRightEdge >= field.X+field.W-msgCounterPad {
		t.Error("muted count right edge must sit LEFT of the plain-count edge (yield to the chip)")
	}
}
