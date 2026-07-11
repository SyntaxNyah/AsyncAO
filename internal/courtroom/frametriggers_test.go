package courtroom

import (
	"strings"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
)

// countTriggers sums a table's triggers across groups (mirrors the .total field
// the parser maintains) for assertions.
func countTriggers(t frameTriggerTable) int {
	n := 0
	for _, g := range t.groups {
		n += len(g)
	}
	return n
}

// TestParseFrameFieldGroupsByPosition pins the position→group binding: section 0
// is the preanim group, 1 the talk group, 2 the idle group, and the leading emote
// name of each section is dropped (only the "<frame>=<value>" pairs become
// triggers). Matches AO2-Client setFrameEffects (animationlayer.cpp:472-505).
func TestParseFrameFieldGroupsByPosition(t *testing.T) {
	room, _, _, _ := newCourtroomRig(t)
	msg := &protocol.ChatMessage{
		// pre section: frame 2 shakes; talk: frame 4; idle: frame 1.
		FrameShake: "leap|2=1^(b)normal|4=1^(a)normal|1=1",
	}
	tbl := room.buildFrameTriggers(msg)

	if got := tbl.groups[frameGroupPreanim]; len(got) != 1 || got[0].frame != 2 {
		t.Errorf("preanim group = %+v, want one trigger at frame 2", got)
	}
	if got := tbl.groups[frameGroupTalk]; len(got) != 1 || got[0].frame != 4 {
		t.Errorf("talk group = %+v, want one trigger at frame 4", got)
	}
	if got := tbl.groups[frameGroupIdle]; len(got) != 1 || got[0].frame != 1 {
		t.Errorf("idle group = %+v, want one trigger at frame 1", got)
	}
	for g := range tbl.groups {
		for _, tr := range tbl.groups[g] {
			if tr.kind != frameEffectShake {
				t.Errorf("group %d trigger kind = %d, want shake", g, tr.kind)
			}
		}
	}
}

// TestParseFrameFieldKinds pins each wire field mapping to its effect kind and the
// SFX value being resolved to a URL (shake/realize keep no url). Also checks the
// per-group sort (a section with out-of-order frames comes back ascending).
func TestParseFrameFieldKinds(t *testing.T) {
	room, _, _, _ := newCourtroomRig(t)
	msg := &protocol.ChatMessage{
		FrameShake:   "leap|3=1^(b)normal^(a)normal",
		FrameRealize: "leap|1=1^(b)normal^(a)normal",
		// two SFX triggers on the preanim, out of frame order → must sort.
		FrameSFX: "leap|5=snap|2=slap^(b)normal^(a)normal",
	}
	tbl := room.buildFrameTriggers(msg)
	pre := tbl.groups[frameGroupPreanim]
	if len(pre) != 4 {
		t.Fatalf("preanim group = %+v, want 4 triggers (1 shake, 1 realize, 2 sfx)", pre)
	}
	// Sorted ascending by frame: 1(realize), 2(sfx slap), 3(shake), 5(sfx snap).
	wantFrames := []int{1, 2, 3, 5}
	for i, f := range wantFrames {
		if pre[i].frame != f {
			t.Errorf("trigger %d frame = %d, want %d (sorted)", i, pre[i].frame, f)
		}
	}
	// SFX triggers carry a resolved, non-empty URL naming the sound; shake/realize
	// carry none.
	for _, tr := range pre {
		if tr.kind == frameEffectSFX {
			low := strings.ToLower(tr.sfxURL)
			if tr.sfxURL == "" || (!strings.Contains(low, "slap") && !strings.Contains(low, "snap")) {
				t.Errorf("sfx trigger frame %d url = %q, want a resolved sfx url", tr.frame, tr.sfxURL)
			}
		} else if tr.sfxURL != "" {
			t.Errorf("non-sfx trigger frame %d carries a url %q", tr.frame, tr.sfxURL)
		}
	}
}

// TestParseFrameFieldSilentSFX pins the AO silent values ("", "0", "1") resolving
// to no URL: the trigger still exists (so it advances the cursor) but plays
// nothing — same rule as armSFXDelay.
func TestParseFrameFieldSilentSFX(t *testing.T) {
	room, _, _, _ := newCourtroomRig(t)
	msg := &protocol.ChatMessage{FrameSFX: "leap|3=1|4=0|5=^(b)normal^(a)normal"}
	tbl := room.buildFrameTriggers(msg)
	pre := tbl.groups[frameGroupPreanim]
	if len(pre) != 3 {
		t.Fatalf("want 3 sfx triggers (all silent but present), got %+v", pre)
	}
	for _, tr := range pre {
		if tr.sfxURL != "" {
			t.Errorf("silent sfx frame %d resolved to url %q, want empty", tr.frame, tr.sfxURL)
		}
	}
}

// TestParseFrameFieldHostileClamp pins the §17.4 bound: a malicious server packing
// far more "<frame>=<value>" pairs than maxFrameTriggers is clamped, never
// panicking or building an unbounded table.
func TestParseFrameFieldHostileClamp(t *testing.T) {
	room, _, _, _ := newCourtroomRig(t)
	var b strings.Builder
	b.WriteString("leap")
	for i := 0; i < maxFrameTriggers*4; i++ { // 4× the cap
		b.WriteString("|")
		b.WriteString(itoa(i))
		b.WriteString("=1")
	}
	b.WriteString("^(b)normal^(a)normal")
	msg := &protocol.ChatMessage{FrameShake: b.String()}
	tbl := room.buildFrameTriggers(msg)
	if got := countTriggers(tbl); got > maxFrameTriggers {
		t.Fatalf("hostile input built %d triggers, exceeds cap %d", got, maxFrameTriggers)
	}
	if tbl.total > maxFrameTriggers {
		t.Fatalf("table.total = %d exceeds cap %d", tbl.total, maxFrameTriggers)
	}
}

// TestParseFrameFieldMalformed pins tolerance: a part without "=", a non-numeric
// frame, a negative frame, and an empty field are all skipped without panic.
func TestParseFrameFieldMalformed(t *testing.T) {
	room, _, _, _ := newCourtroomRig(t)
	msg := &protocol.ChatMessage{
		FrameShake:   "", // empty: no triggers
		FrameRealize: "leap|notanumber=1|=1|-3=1|nokv^(b)normal^(a)normal",
	}
	tbl := room.buildFrameTriggers(msg)
	if got := countTriggers(tbl); got != 0 {
		t.Fatalf("malformed input produced %d triggers, want 0: %+v", got, tbl.groups)
	}
}

// TestNotifyFrameShownFires drives the fire-decision directly: with the speaker on
// the preanim base, crossing the trigger frame fires shake+flash+sfx exactly once;
// re-reporting the same/earlier frame does NOT re-fire (forward-only cursor).
func TestNotifyFrameShownFires(t *testing.T) {
	room, _, _, audio := newCourtroomRig(t)
	// Stage a message with a preanim so PreanimBase is non-empty and Active.
	room.Scene.Speaker = SpriteLayer{
		PreanimBase: "leap-base",
		TalkBase:    "talk-base",
		IdleBase:    "idle-base",
		Active:      "leap-base",
		Visible:     true,
	}
	room.frameTriggers = room.buildFrameTriggers(&protocol.ChatMessage{
		FrameShake:   "leap|3=1^(b)normal^(a)normal",
		FrameRealize: "leap|3=1^(b)normal^(a)normal",
		FrameSFX:     "leap|3=whip^(b)normal^(a)normal",
	})

	room.NotifyFrameShown(0) // before the trigger — nothing fires
	room.NotifyFrameShown(1)
	if room.Scene.ShakeLeft != 0 || room.Scene.FlashLeft != 0 || len(audio.sfx) != 0 {
		t.Fatalf("effects fired before the trigger frame (shake=%v flash=%v sfx=%v)", room.Scene.ShakeLeft, room.Scene.FlashLeft, audio.sfx)
	}
	room.NotifyFrameShown(3) // crosses the trigger
	if room.Scene.ShakeLeft != ScreenshakeDuration {
		t.Errorf("shake not fired at frame 3 (ShakeLeft=%v)", room.Scene.ShakeLeft)
	}
	if room.Scene.FlashLeft != RealizationFlashDuration {
		t.Errorf("flash not fired at frame 3 (FlashLeft=%v)", room.Scene.FlashLeft)
	}
	if len(audio.sfx) != 1 {
		t.Fatalf("want exactly 1 frame sfx at frame 3, got %v", audio.sfx)
	}
	// Reset the visual latches; re-reporting must NOT re-fire (once per playback).
	room.Scene.ShakeLeft, room.Scene.FlashLeft = 0, 0
	room.NotifyFrameShown(3)
	room.NotifyFrameShown(5)
	if room.Scene.ShakeLeft != 0 || room.Scene.FlashLeft != 0 {
		t.Errorf("trigger re-fired on a repeat report (shake=%v flash=%v)", room.Scene.ShakeLeft, room.Scene.FlashLeft)
	}
	if len(audio.sfx) != 1 {
		t.Errorf("sfx re-fired on a repeat report, sfx=%v", audio.sfx)
	}
}

// TestNotifyFrameShownDecimationSweep pins the range sweep: a decimated jump can
// report a source frame well past several triggers in one step, and every skipped
// in-range trigger must still fire (the cursor sweeps [cursor, src]).
func TestNotifyFrameShownDecimationSweep(t *testing.T) {
	room, _, _, audio := newCourtroomRig(t)
	room.Scene.Speaker = SpriteLayer{PreanimBase: "leap-base", Active: "leap-base", Visible: true}
	room.frameTriggers = room.buildFrameTriggers(&protocol.ChatMessage{
		FrameSFX: "leap|2=a|5=b|8=c^(b)normal^(a)normal",
	})
	// One report jumps from before all triggers to frame 9 (a coarse decimation):
	// all three sfx must fire.
	room.NotifyFrameShown(9)
	if len(audio.sfx) != 3 {
		t.Fatalf("decimation sweep fired %d sfx, want all 3: %v", len(audio.sfx), audio.sfx)
	}
}

// TestNotifyFrameShownBindsActiveLayer pins that triggers bind to whichever base
// is Active: a talk-group trigger does NOT fire while the preanim base is on
// screen, and DOES once the layer swaps to the talk base.
func TestNotifyFrameShownBindsActiveLayer(t *testing.T) {
	room, _, _, audio := newCourtroomRig(t)
	room.Scene.Speaker = SpriteLayer{
		PreanimBase: "leap-base",
		TalkBase:    "talk-base",
		IdleBase:    "idle-base",
		Active:      "leap-base", // preanim playing
		Visible:     true,
	}
	room.frameTriggers = room.buildFrameTriggers(&protocol.ChatMessage{
		FrameSFX: "leap^(b)normal|2=talksound^(a)normal",
	})
	room.NotifyFrameShown(4) // preanim frame 4 — no PREANIM triggers exist, talk one must not fire
	if len(audio.sfx) != 0 {
		t.Fatalf("talk-group trigger fired while preanim active: %v", audio.sfx)
	}
	room.Scene.Speaker.Active = "talk-base" // swap to talk loop
	room.NotifyFrameShown(3)                // crosses the talk trigger at frame 2
	if len(audio.sfx) != 1 {
		t.Fatalf("talk trigger did not fire after the layer swap: %v", audio.sfx)
	}
}

// TestReduceMotionGatesFrameEffects pins that reduce-motion / effects-off kills the
// VISUAL shake+flash but keeps the SFX (accessibility parity with fireMessageEffects).
func TestReduceMotionGatesFrameEffects(t *testing.T) {
	room, _, _, audio := newCourtroomRig(t)
	room.ReduceMotion = true // effectsVisible() → false
	room.Scene.Speaker = SpriteLayer{PreanimBase: "leap-base", Active: "leap-base", Visible: true}
	room.frameTriggers = room.buildFrameTriggers(&protocol.ChatMessage{
		FrameShake:   "leap|1=1^(b)normal^(a)normal",
		FrameRealize: "leap|1=1^(b)normal^(a)normal",
		FrameSFX:     "leap|1=whip^(b)normal^(a)normal",
	})
	room.NotifyFrameShown(1)
	if room.Scene.ShakeLeft != 0 || room.Scene.FlashLeft != 0 {
		t.Errorf("reduce-motion must suppress shake/flash (shake=%v flash=%v)", room.Scene.ShakeLeft, room.Scene.FlashLeft)
	}
	if len(audio.sfx) != 1 {
		t.Errorf("reduce-motion must keep the frame sfx, got %v", audio.sfx)
	}
}

// TestBeginBuildsFrameTable pins the table being built ONCE at message-begin from
// the wire fields (not per frame), and cleared for a catch-up flash.
func TestBeginBuildsFrameTable(t *testing.T) {
	room, _, _, _ := newCourtroomRig(t)
	room.HandleEvent(Event{Kind: EventMessage, Message: &protocol.ChatMessage{
		CharName: "Phoenix", Emote: "normal", Side: "wit", Message: "hi",
		EmoteMod:   protocol.EmoteModIdle,
		FrameShake: "leap^(b)normal|4=1^(a)normal|1=1",
	}})
	if room.frameTriggers.total == 0 {
		t.Fatal("begin() did not build the frame trigger table from the wire fields")
	}
}

// TestRestoreMessageClearsFrameTriggers pins the no-replay restore invariant for
// #17: a settled restore (tab-back / court re-entry / pin) of a message whose
// char.ini authored IDLE-phase FRAME_* data must NOT re-fire it. RestoreMessage
// calls begin() (which rebuilds the table with cursors at 0) then restores the
// real audio sink; without the clear, the looping idle sprite would fire idle
// triggers on the real sink. After restore the table must be inert.
func TestRestoreMessageClearsFrameTriggers(t *testing.T) {
	room, _, _, audio := newCourtroomRig(t)
	msg := &protocol.ChatMessage{
		CharName: "Phoenix", Emote: "normal", Side: "wit", Message: "hi",
		EmoteMod: protocol.EmoteModIdle,
		// idle-phase (section 2) shake + sfx: these would replay on the settled loop.
		FrameShake: "leap^(b)normal^(a)normal|1=1",
		FrameSFX:   "leap^(b)normal^(a)normal|1=whip",
	}
	room.RestoreMessage(msg)

	if room.frameTriggers.total != 0 {
		t.Fatalf("RestoreMessage left %d frame triggers armed; a settled restore must clear the table", room.frameTriggers.total)
	}
	// Drive a fresh idle-loop frame report on the real (restored) sink: nothing may fire.
	room.NotifyFrameShown(1)
	room.NotifyFrameShown(2)
	if room.Scene.ShakeLeft != 0 {
		t.Errorf("idle-phase shake replayed after restore (ShakeLeft=%v)", room.Scene.ShakeLeft)
	}
	if len(audio.sfx) != 0 {
		t.Errorf("idle-phase sfx replayed on the real sink after restore: %v", audio.sfx)
	}
}

// TestActiveFrameGroupSharedTalkIdleBase pins the TalkBase==IdleBase tie-break: a
// char whose (b)talk and (a)idle emotes resolve to the same file must fire the
// TALK group while talking and the IDLE group when settled — the phase decides,
// not the switch's case order (which would always pick talk).
func TestActiveFrameGroupSharedTalkIdleBase(t *testing.T) {
	room, _, _, _ := newCourtroomRig(t)
	room.Scene.Speaker = SpriteLayer{
		PreanimBase: "leap-base",
		TalkBase:    "shared-base", // (b) and (a) resolve to the same sprite
		IdleBase:    "shared-base",
		Active:      "shared-base",
		Visible:     true,
	}
	// Talking phase → talk group.
	room.phase = PhaseTalking
	if g := room.activeFrameGroup(); g != frameGroupTalk {
		t.Errorf("shared base + PhaseTalking = group %d, want talk group %d", g, frameGroupTalk)
	}
	// Settled (idle) → idle group even though the base string equals TalkBase.
	room.phase = PhaseIdle
	if g := room.activeFrameGroup(); g != frameGroupIdle {
		t.Errorf("shared base + PhaseIdle = group %d, want idle group %d", g, frameGroupIdle)
	}
}

// itoa is a tiny local int→string for the hostile-input builder (avoids importing
// strconv just for the test).
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
