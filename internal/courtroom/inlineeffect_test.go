package courtroom

// Bare inline screen effects: a message whose ENTIRE text is `\f` / `\s` (no
// glyphs at all) must still fire its effect. It didn't, because such a message
// strips to zero runes, so Typewriter.Done() was true from the first instant and
// startTalking jumped straight to linger — skipping PhaseTalking, which is the
// only place Update drains NextEffect. AO2 has no such hole: its emptiness test
// reads the RAW wire text (start_chat_ticking, ../AO2-Client/src/courtroom.cpp
// :4183), and a raw "\f" is not empty, so chat_tick walks the escape and calls
// do_flash (:4442-4448).

import (
	"testing"
	"time"
)

// effectTick is one lifecycle step for these tests: long enough that a message
// WITH text would reveal a rune, short enough that a fired 250/350 ms effect
// countdown is still visibly non-zero on the next assertion.
const effectTick = 20 * time.Millisecond

// stageIC feeds one IC message with the given text into a fresh rig with the
// screen-effect gate OPEN, and returns the room.
func stageIC(t *testing.T, text string) *Courtroom {
	t.Helper()
	room, sess, _, _ := newCourtroomRig(t)
	setupReadySession(t, sess)
	room.ScreenEffects, room.ReduceMotion = true, false
	room.HandleEvent(Event{Kind: EventMessage, Message: pairedMS(t, sess, text)})
	return room
}

// TestBareInlineEffectFiresWithoutText is the regression pin for the reported
// symptom: \f and \s worked when the message also carried text and did nothing
// at all on their own. Each bare code must fire its own effect EXACTLY once and
// must not fire the other one.
func TestBareInlineEffectFiresWithoutText(t *testing.T) {
	cases := []struct {
		name      string
		text      string
		wantFlash time.Duration
		wantShake time.Duration
	}{
		{name: `bare \f`, text: `\f`, wantFlash: RealizationFlashDuration, wantShake: 0},
		{name: `bare \s`, text: `\s`, wantShake: ScreenshakeDuration, wantFlash: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			room := stageIC(t, tc.text)
			// The message must reach the talking phase at all — that is the phase
			// whose Update drains the effect marks. Landing in PhaseLinger here is
			// exactly the bug.
			if got := room.Phase(); got != PhaseTalking {
				t.Fatalf("phase after a bare effect = %v, want PhaseTalking (the only phase that drains effect marks)", got)
			}
			// The marks sit at reveal position 0, so the first tick fires them.
			room.Update(effectTick)
			if room.Scene.FlashLeft != tc.wantFlash {
				t.Errorf("FlashLeft = %v, want %v", room.Scene.FlashLeft, tc.wantFlash)
			}
			if room.Scene.ShakeLeft != tc.wantShake {
				t.Errorf("ShakeLeft = %v, want %v", room.Scene.ShakeLeft, tc.wantShake)
			}
			// Exactly once: clear the countdowns and keep ticking. A re-arm here
			// would mean the mark fired again (a repeat-shake spam vector).
			room.Scene.FlashLeft, room.Scene.ShakeLeft = 0, 0
			for i := 0; i < 100; i++ {
				room.Update(effectTick)
			}
			if room.Scene.FlashLeft != 0 || room.Scene.ShakeLeft != 0 {
				t.Errorf("effect re-fired: flash=%v shake=%v, want a single fire",
					room.Scene.FlashLeft, room.Scene.ShakeLeft)
			}
			// And the lifecycle still settles — a bare effect must not park the
			// room in the talking phase forever.
			if got := room.Phase(); got != PhaseIdle {
				t.Errorf("phase after settling = %v, want PhaseIdle", got)
			}
		})
	}
}

// TestBareInlineEffectsBothFireInOrder pins two codes in ONE bare message: both
// reach the Scene, and the typewriter hands them out in the order they were
// written (the Scene countdowns can't show order — both marks sit at position 0
// and fire on the same tick — so the ordering contract is pinned where it lives).
func TestBareInlineEffectsBothFireInOrder(t *testing.T) {
	room := stageIC(t, `\s\f`)
	room.Update(effectTick)
	if room.Scene.ShakeLeft != ScreenshakeDuration || room.Scene.FlashLeft != RealizationFlashDuration {
		t.Errorf(`bare "\s\f": shake=%v flash=%v, want both armed`, room.Scene.ShakeLeft, room.Scene.FlashLeft)
	}

	for _, tc := range []struct {
		text string
		want []EffectKind
	}{
		{`\s\f`, []EffectKind{EffectShake, EffectFlash}},
		{`\f\s`, []EffectKind{EffectFlash, EffectShake}},
	} {
		tw := NewTypewriter()
		tw.Start(tc.text)
		if got := tw.Text(); got != "" {
			t.Errorf("%q must strip to no glyphs, got %q", tc.text, got)
		}
		var got []EffectKind
		for {
			m, ok := tw.NextEffect()
			if !ok {
				break
			}
			got = append(got, m.Kind)
		}
		if len(got) != len(tc.want) {
			t.Fatalf("%q drained %d marks, want %d", tc.text, len(got), len(tc.want))
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("%q mark %d = %v, want %v", tc.text, i, got[i], tc.want[i])
			}
		}
	}
}

// TestRealBlankPostStaysBlankAndSilent pins that the fix did not disturb the
// deliberate blankpost: the AO single-space message still hides the chatbox,
// still reveals no text, and still fires nothing.
func TestRealBlankPostStaysBlankAndSilent(t *testing.T) {
	room := stageIC(t, " ")
	if !room.Scene.IsBlankPost {
		t.Error("the AO single-space blankpost must still be a blankpost")
	}
	for i := 0; i < 100; i++ {
		room.Update(effectTick)
		if room.Scene.FlashLeft != 0 || room.Scene.ShakeLeft != 0 {
			t.Fatalf("a blankpost must fire no effect, got flash=%v shake=%v",
				room.Scene.FlashLeft, room.Scene.ShakeLeft)
		}
	}
	if got := room.Phase(); got != PhaseIdle {
		t.Errorf("blankpost phase after settling = %v, want PhaseIdle", got)
	}

	// A bare-effect message carries no glyphs either, so it stays a blankpost for
	// DISPLAY purposes exactly like the already-pinned markup-only `\c1` case
	// (TestBlankPostDetection) — the chatbox hides and only the sprite shows. The
	// fix separates that display decision from the effect drain; it does not
	// change it. (AO2 instead leaves an empty chatbox frame on screen, which is
	// the pre-existing AsyncAO deviation, not something this fix introduces.)
	if fx := stageIC(t, `\f`); !fx.Scene.IsBlankPost {
		t.Error(`a glyph-less "\f" message must still hide the chatbox like any markup-only message`)
	}
}

// TestBareInlineEffectRespectsEffectsGate pins constraint: the ScreenEffects
// toggle and accessibility reduce-motion still suppress a bare effect, and the
// suppressed message still settles instead of hanging in the talking phase.
func TestBareInlineEffectRespectsEffectsGate(t *testing.T) {
	for _, tc := range []struct {
		name                      string
		screenEffects, reduceMotn bool
	}{
		{"ScreenEffects off", false, false},
		{"ReduceMotion on", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			room, sess, _, _ := newCourtroomRig(t)
			setupReadySession(t, sess)
			room.ScreenEffects, room.ReduceMotion = tc.screenEffects, tc.reduceMotn
			room.HandleEvent(Event{Kind: EventMessage, Message: pairedMS(t, sess, `\s\f`)})
			for i := 0; i < 100; i++ {
				room.Update(effectTick)
				if room.Scene.FlashLeft != 0 || room.Scene.ShakeLeft != 0 {
					t.Fatalf("gated off, but flash=%v shake=%v", room.Scene.FlashLeft, room.Scene.ShakeLeft)
				}
			}
			if got := room.Phase(); got != PhaseIdle {
				t.Errorf("phase after settling = %v, want PhaseIdle", got)
			}
		})
	}
}

// TestTypewriterEffectMarksCapped pins the named bound on per-message effect
// marks (hard rule §17.4): a wall of \s from a hostile peer records at most
// effectMarksMax of them, so it can neither grow an unbounded slice nor be used
// as a screenshake-spam channel.
func TestTypewriterEffectMarksCapped(t *testing.T) {
	tw := NewTypewriter()
	over := effectMarksMax + 20
	msg := ""
	for i := 0; i < over; i++ {
		msg += `\s`
	}
	tw.Start(msg)
	n := 0
	for {
		if _, ok := tw.NextEffect(); !ok {
			break
		}
		n++
	}
	if n != effectMarksMax {
		t.Errorf("%d \\s codes recorded %d marks, want the cap %d", over, n, effectMarksMax)
	}
}

// TestAppendTailKeepsItsOwnEffectBudget pins the cap's PHASE rule on the 2.8 additive
// path. StartAppend re-parses prefix+message in one pass, and the prefix is every
// ADDITIVE line accumulated so far — so a budget spent over the whole parse would let a
// long chain refuse every mark in the newest continuation, including the mark AT the
// join that the skip loop deliberately preserves and that a tail of pure markup exists
// to fire. The prefix here already carries a FULL effectMarksMax worth of codes.
func TestAppendTailKeepsItsOwnEffectBudget(t *testing.T) {
	// Each code is followed by a glyph so every prefix mark lands STRICTLY inside the
	// prefix (At < pre) and is therefore skipped as already-fired — a prefix of bare
	// codes would strip to zero runes, putting all of its marks at the join instead and
	// testing nothing.
	prefix := ""
	for i := 0; i < effectMarksMax; i++ {
		prefix += `\sx`
	}

	// The tail is nothing but the effect code, so its mark sits exactly AT the join —
	// the case the bare-effect path exists for, and the first one a whole-parse budget
	// silently drops.
	tw := NewTypewriter()
	tw.StartAppend(prefix, `\f`)
	m, ok := tw.NextEffect()
	if !ok {
		t.Fatal("an additive continuation that is only an effect code fired nothing after a saturated prefix")
	}
	if m.Kind != EffectFlash {
		t.Errorf("tail mark = %v, want the flash the tail asked for (a prefix mark leaked through)", m.Kind)
	}
	if _, more := tw.NextEffect(); more {
		t.Error("a prefix mark survived: marks strictly inside the prefix already fired on the original line")
	}

	// An ordinary trailing effect after real appended text is the same requirement.
	tw.StartAppend(prefix, `and then\s`)
	tw.SkipToEnd() // reveal the tail so the trailing mark is reached, not dropped as unreached
	if tw.EffectsPending() {
		t.Error("SkipToEnd drops unreached marks; nothing should remain pending")
	}
	tw.StartAppend(prefix, `and then\s`)
	if !tw.EffectsPending() {
		t.Error("a long additive chain must still record an ordinary trailing shake in its tail")
	}

	// The bound is still NAMED and still hard (rule 4): the tail's own allowance is
	// exactly effectMarksMax, so the whole parse can never exceed two phases' worth.
	over := ""
	for i := 0; i < effectMarksMax+20; i++ {
		over += `\s`
	}
	tw.StartAppend(prefix, over)
	n := 0
	for {
		if _, ok := tw.NextEffect(); !ok {
			break
		}
		n++
	}
	if n != effectMarksMax {
		t.Errorf("a saturated tail drained %d marks, want the per-phase cap %d", n, effectMarksMax)
	}
}

// TestTypewriterEffectsPending pins the accessor the lifecycle branches on:
// pending while a mark is unreleased, clear once every mark has been handed out
// (or dropped by a skip), and always clear for a message with no codes.
func TestTypewriterEffectsPending(t *testing.T) {
	tw := NewTypewriter()

	tw.Start("plain text, no codes")
	if tw.EffectsPending() {
		t.Error("a message with no codes must never report pending effects")
	}

	tw.Start(`\f`) // zero glyphs: Done() is true immediately, but the mark is live
	if !tw.Done() {
		t.Fatal(`a glyph-less "\f" must report Done`)
	}
	if !tw.EffectsPending() {
		t.Fatal(`a glyph-less "\f" must still report a pending effect`)
	}
	if _, ok := tw.NextEffect(); !ok {
		t.Fatal("the pending mark must be drainable")
	}
	if tw.EffectsPending() {
		t.Error("no mark should remain after the drain")
	}

	tw.Start(`ab\sc`)
	if !tw.EffectsPending() {
		t.Fatal("an unreached mark counts as pending")
	}
	tw.SkipToEnd() // a skip DROPS unreached marks — nothing left to fire
	if tw.EffectsPending() {
		t.Error("SkipToEnd must leave no pending effects")
	}

	// The 2.8 additive case: a continuation whose whole tail is an effect code.
	// The prefix is pre-revealed so Done() is true immediately, and the mark sits
	// exactly AT the join — StartAppend only drops marks strictly INSIDE the
	// prefix (those already fired on the original line), so the tail's effect
	// must survive and still be reported pending.
	tw.StartAppend("prior line", `\f`)
	if !tw.Done() {
		t.Fatal("an additive tail of pure markup reveals nothing new")
	}
	if !tw.EffectsPending() {
		t.Error("an additive continuation that is only an effect code must still fire it")
	}
	if m, ok := tw.NextEffect(); !ok || m.Kind != EffectFlash {
		t.Errorf("additive tail mark = %+v ok=%v, want a flash", m, ok)
	}
}
