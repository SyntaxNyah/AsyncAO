package courtroom

// Tests for the settled last-message restore: Session.LastIC records the seed
// (persisted like Background/MusicTrack), and Courtroom.RestoreMessage
// re-stages it with the whole ceremony skipped — so a rebuilt room (tab
// reactivation, court re-entry, pinning a background tab) shows the stage a
// live watcher would have ended on instead of a blank viewport.

import (
	"strings"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
)

// TestSessionRecordsLastIC pins the seed itself: a well-formed MS lands in
// Session.LastIC (newest wins), a malformed one leaves it alone, and a fresh
// SI handshake clears it like MusicTrack (no stale stage across a rejoin).
func TestSessionRecordsLastIC(t *testing.T) {
	_, sess, _, _ := newCourtroomRig(t)
	setupReadySession(t, sess)
	if sess.LastIC != nil {
		t.Fatal("LastIC set before any MS")
	}
	feed(t, sess, "MS#1#-#Phoenix#normal#Hello!#def#0#0#0#0#0#0#0#0#0#%")
	if sess.LastIC == nil || sess.LastIC.Message != "Hello!" {
		t.Fatalf("LastIC after first MS = %+v", sess.LastIC)
	}
	feed(t, sess, "MS#1#-#Edgeworth#thinking#Noted.#pro#0#0#1#0#0#0#0#0#0#%")
	if sess.LastIC == nil || sess.LastIC.CharName != "Edgeworth" {
		t.Fatalf("LastIC not replaced by the newer MS: %+v", sess.LastIC)
	}
	// A malformed MS (too short) is dropped — the seed must survive it.
	feed(t, sess, "MS#1#-#Phoenix#%")
	if sess.LastIC == nil || sess.LastIC.CharName != "Edgeworth" {
		t.Error("a dropped malformed MS must not disturb LastIC")
	}
	feed(t, sess, "SI#2#0#1#%")
	if sess.LastIC != nil {
		t.Error("a fresh SI handshake must clear LastIC")
	}
}

// TestRestoreMessageSettled pins the restore's end state on a fresh room:
// phase idle, idle sprite active, full text revealed — with the ceremony's
// sounds and one-shot screen effects (field realization AND the inline \s
// code) all skipped, and the real audio sink back in place for the next live
// message.
func TestRestoreMessageSettled(t *testing.T) {
	room, sess, _, audio := newCourtroomRig(t)
	setupReadySession(t, sess)

	// A ceremony-heavy message: preanim + emote SFX + realization + inline \s.
	msg := &protocol.ChatMessage{
		CharName: "Phoenix", Emote: "normal", PreEmote: "pointing",
		EmoteMod: protocol.EmoteModPreanim, Message: `Take that!\sAnd this!`,
		Side: "def", SFXName: "whack", SFXDelay: 40, Realization: true,
		Showname: "Nick",
	}
	room.RestoreMessage(msg)

	if room.Phase() != PhaseIdle {
		t.Fatalf("phase = %v, want idle", room.Phase())
	}
	sc := &room.Scene
	if !sc.Speaker.Visible || sc.Speaker.Active != sc.Speaker.IdleBase || sc.Speaker.PlayOnce {
		t.Errorf("speaker not settled on the idle loop: %+v", sc.Speaker)
	}
	want := "Take that!And this!" // \s is an effect mark, not a glyph
	if sc.MessageText != want || sc.VisibleRunes != len([]rune(want)) {
		t.Errorf("text = %q visible %d, want fully revealed %q", sc.MessageText, sc.VisibleRunes, want)
	}
	if sc.ShownameText != "Nick" {
		t.Errorf("showname = %q", sc.ShownameText)
	}
	if sc.FlashLeft != 0 || sc.ShakeLeft != 0 {
		t.Error("restore must not re-fire the message's flash/shake")
	}
	if n := len(audio.shouts) + len(audio.sfx) + len(audio.blips) + len(audio.music); n != 0 {
		t.Errorf("restore played %d sounds: %+v", n, audio)
	}
	// The real sink is back: the next LIVE message blips during its reveal.
	room.HandleEvent(Event{Kind: EventMessage, Message: pairedMS(t, sess, "Hello!")})
	for i := 0; i < len([]rune("Hello!")); i++ {
		room.Update(DefaultCharInterval)
	}
	if len(audio.blips) == 0 {
		t.Error("audio sink not restored after RestoreMessage")
	}
}

// TestRestoreMessageShout pins a shout seed: settled means PAST the bubble —
// it is off stage and the cry stays silent.
func TestRestoreMessageShout(t *testing.T) {
	room, sess, _, audio := newCourtroomRig(t)
	setupReadySession(t, sess)
	room.RestoreMessage(&protocol.ChatMessage{
		CharName: "Phoenix", Emote: "normal", Message: "OBJECTION!", Side: "def",
		Objection: protocol.ShoutObjection,
	})
	if room.Phase() != PhaseIdle {
		t.Fatalf("phase = %v, want idle", room.Phase())
	}
	if room.Scene.ShoutBase != "" {
		t.Errorf("shout bubble still on stage: %q", room.Scene.ShoutBase)
	}
	if len(audio.shouts) != 0 {
		t.Error("restore must not replay the shout cry")
	}
}

// TestRestoreMessageBypassesCatchUp pins the begin-reroute guard: with
// catch-up on (the App default) a restore must still stage the SPEAKER —
// beginCaughtUp leaves the sprite as-is, which on a fresh room is no speaker
// at all. The pair partner rides the same full begin, and the caller's
// setting survives.
func TestRestoreMessageBypassesCatchUp(t *testing.T) {
	room, sess, _, _ := newCourtroomRig(t)
	setupReadySession(t, sess)
	room.CatchUp, room.CatchUpThreshold = true, 0 // harshest: everything fast-forwards
	room.RestoreMessage(pairedMS(t, sess, "Hello!"))
	sc := &room.Scene
	if !sc.Speaker.Visible || sc.Speaker.IdleBase == "" || sc.Speaker.Active != sc.Speaker.IdleBase {
		t.Fatalf("restore under catch-up staged no settled speaker: %+v", sc.Speaker)
	}
	if !sc.PairActive || !strings.HasSuffix(sc.Pair.IdleBase, "characters/edgeworth/(a)thinking") {
		t.Errorf("pair not restored: %+v", sc.Pair)
	}
	if !room.CatchUp {
		t.Error("caller's CatchUp setting must survive the restore")
	}
}

// TestRestoreMessageHonorsRoomPrefs pins that a restore bakes the ROOM's
// viewer knobs into the settled scene — buildRoom pushes prefs
// (applyTimingToRoom) BEFORE restoring for exactly this reason: begin()
// decides showname-vs-charname and the transmitted-style gate at stage time,
// and nothing recomputes the scene afterwards.
func TestRestoreMessageHonorsRoomPrefs(t *testing.T) {
	styled := SpriteStyle{Tint: true, R: 255, Wobble: true, HueCycle: true, Glitch: true}
	mkMsg := func() *protocol.ChatMessage {
		return &protocol.ChatMessage{
			CharName: "Phoenix", Emote: "normal", Side: "def", Showname: "Nick",
			Message: styled.EncodeChangeMarker(SpriteStyle{}) + "Take that!",
		}
	}

	room, _, _, _ := newCourtroomRig(t)
	room.ForceCharNames = true
	room.HideSpriteStyles = true
	room.RestoreMessage(mkMsg())
	if room.Scene.ShownameText != "Phoenix" {
		t.Errorf("ForceCharNames restore showed %q, want the char name", room.Scene.ShownameText)
	}
	if room.Scene.Speaker.Style.Active() {
		t.Errorf("HideSpriteStyles restore kept the transmitted style: %+v", room.Scene.Speaker.Style)
	}

	room2, _, _, _ := newCourtroomRig(t)
	room2.ReduceMotion = true
	room2.RestoreMessage(mkMsg())
	if st := room2.Scene.Speaker.Style; st.Wobble || st.Spin || st.Motion != 0 || st.HueCycle || st.Glitch {
		t.Errorf("ReduceMotion restore kept transmitted motion: %+v", st)
	} else if !st.Tint {
		t.Error("ReduceMotion must strip only motion — the recolour stays")
	}
}

// TestRestoreMessageBlankpost pins the edges: a nil seed is a no-op, and a
// blankpost seed restores sprite-only (IsBlankPost keeps the chatbox hidden).
func TestRestoreMessageBlankpost(t *testing.T) {
	room, sess, _, _ := newCourtroomRig(t)
	setupReadySession(t, sess)
	room.RestoreMessage(nil) // no seed: nothing staged, nothing panics
	if room.Scene.Speaker.Visible {
		t.Fatal("nil restore staged a speaker")
	}
	room.RestoreMessage(pairedMS(t, sess, " "))
	if room.Phase() != PhaseIdle || !room.Scene.IsBlankPost || !room.Scene.Speaker.Visible {
		t.Errorf("blankpost restore: phase=%v blank=%v speaker=%v",
			room.Phase(), room.Scene.IsBlankPost, room.Scene.Speaker.Visible)
	}
}
