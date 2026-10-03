package courtroom

import (
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
)

// waitMsg builds a synthetic no-preanim message for the wait-gate tests.
func waitMsg(char, emote, text string) *protocol.ChatMessage {
	return &protocol.ChatMessage{
		CharName: char, Emote: emote, Message: text, Side: "wit",
		EmoteMod: protocol.EmoteModIdle,
	}
}

// readySpeaker marks a speaker's emote as landed the way the render store would:
// BOTH spellings, because a message draws its TALK sprite the instant it begins
// and its IDLE one when it settles. Fixtures that mean "this character's art has
// arrived" say so through this, so they pin the gate's subject rather than the
// exact base list it happens to consult.
func readySpeaker(room *Courtroom, ready map[string]bool, char, emote string) {
	ready[room.urls.Emote(char, emote, EmoteIdle)] = true
	ready[room.urls.Emote(char, emote, EmoteTalk)] = true
}

// TestSpriteWaitGate pins cold-load mode 3 ("wait"): with the gate on, a message
// whose speaker idle sprite hasn't decoded is HELD in the queue (nothing begins);
// it begins the moment the sprite lands; and the timeout releases it anyway so a
// 404/decode failure can only ever delay a message, never hang the room.
func TestSpriteWaitGate(t *testing.T) {
	newRig := func(t *testing.T) (*Courtroom, map[string]bool) {
		room, _, _, _ := newCourtroomRig(t)
		ready := map[string]bool{}
		room.SpriteWait = true
		room.SpriteWaitTimeout = 500 * time.Millisecond
		room.SpriteReady = func(base string) bool { return ready[base] }
		return room, ready
	}

	t.Run("holds until ready", func(t *testing.T) {
		room, ready := newRig(t)
		room.HandleEvent(Event{Kind: EventMessage, Message: waitMsg("Phoenix", "normal", "hi")})
		if room.Phase() != PhaseIdle || room.QueueLen() != 1 || room.Scene.Speaker.Visible {
			t.Fatalf("cold sprite must hold: phase=%v queue=%d visible=%v", room.Phase(), room.QueueLen(), room.Scene.Speaker.Visible)
		}
		room.Update(100 * time.Millisecond) // still cold, still held
		if room.Phase() != PhaseIdle || room.QueueLen() != 1 {
			t.Fatal("hold must persist while the sprite is cold and the timeout hasn't expired")
		}
		readySpeaker(room, ready, "Phoenix", "normal")
		room.Update(16 * time.Millisecond) // sprite landed → begins
		if room.QueueLen() != 0 || !room.Scene.Speaker.Visible || room.Scene.Speaker.Name != "Phoenix" {
			t.Fatalf("ready sprite must begin the held message: queue=%d visible=%v", room.QueueLen(), room.Scene.Speaker.Visible)
		}
	})

	t.Run("timeout releases", func(t *testing.T) {
		room, _ := newRig(t)
		room.HandleEvent(Event{Kind: EventMessage, Message: waitMsg("Phoenix", "normal", "hi")})
		if room.QueueLen() != 1 {
			t.Fatal("setup: message should be held")
		}
		room.Update(600 * time.Millisecond) // one tick past the 500 ms cap
		if room.QueueLen() != 0 || !room.Scene.Speaker.Visible {
			t.Fatal("an expired hold must play the message anyway (a 404 can only delay, never hang)")
		}
	})

	t.Run("shout bypasses", func(t *testing.T) {
		room, _ := newRig(t)
		m := waitMsg("Phoenix", "normal", "OBJECTION!")
		m.Objection = protocol.ShoutObjection
		room.HandleEvent(Event{Kind: EventMessage, Message: m})
		if room.Phase() != PhaseShout {
			t.Fatalf("a shout must play NOW (AO2 parity), got phase %v", room.Phase())
		}
	})

	t.Run("catch-up wins", func(t *testing.T) {
		room, ready := newRig(t)
		room.CatchUp, room.CatchUpThreshold = true, 1
		room.HandleEvent(Event{Kind: EventMessage, Message: waitMsg("Phoenix", "normal", "one")})
		room.HandleEvent(Event{Kind: EventMessage, Message: waitMsg("Edgeworth", "normal", "two")})
		if room.QueueLen() != 2 {
			t.Fatalf("setup: both messages should be queued (head held), got %d", room.QueueLen())
		}
		room.Update(16 * time.Millisecond) // backlog ≥ threshold → the head must NOT wait
		if room.QueueLen() != 1 {
			t.Fatalf("a backlog at the catch-up threshold must never wait, queue=%d", room.QueueLen())
		}
		_ = ready // never marked ready — catch-up alone must release the head
	})

	t.Run("pair strictness", func(t *testing.T) {
		room, ready := newRig(t)
		room.SpriteWaitPair = true
		m := waitMsg("Phoenix", "normal", "hi")
		m.Pair = protocol.PairInfo{CharID: 1, Name: "Edgeworth", Emote: "normal"} // Active(): valid id + folder
		readySpeaker(room, ready, "Phoenix", "normal")                            // speaker ready, pair cold
		room.HandleEvent(Event{Kind: EventMessage, Message: m})
		if room.QueueLen() != 1 {
			t.Fatal("pair strictness on: a cold pair sprite must hold the message")
		}
		ready[room.urls.Emote("Edgeworth", "normal", EmoteIdle)] = true
		room.Update(16 * time.Millisecond)
		if room.QueueLen() != 0 || !room.Scene.PairActive {
			t.Fatal("both sprites ready must begin the paired message")
		}
	})

	t.Run("preanim strictness", func(t *testing.T) {
		room, ready := newRig(t)
		room.SpriteWaitPreanim = true
		m := waitMsg("Phoenix", "normal", "hi")
		m.PreEmote, m.EmoteMod = "flourish", protocol.EmoteModPreanim
		readySpeaker(room, ready, "Phoenix", "normal") // idle ready, preanim cold
		room.HandleEvent(Event{Kind: EventMessage, Message: m})
		if room.QueueLen() != 1 {
			t.Fatal("preanim strictness on: a cold preanim must hold the message")
		}
		ready[room.urls.Emote("Phoenix", "flourish", EmotePreanim)] = true
		room.Update(16 * time.Millisecond)
		if room.QueueLen() != 0 {
			t.Fatal("a ready preanim must release the hold")
		}
	})

	t.Run("confirmed-missing preanim releases on the miss signal, not the timeout", func(t *testing.T) {
		room, ready := newRig(t)
		room.SpriteWaitPreanim = true
		m := waitMsg("Phoenix", "normal", "hi")
		// A dummy preanim name (live packs ship "-<n>" on every emote); idle emote-mod
		// upgrades to preanim on the wire when a preanim name is present, so the gate
		// arms on a preanim sprite that will never resolve.
		m.PreEmote, m.EmoteMod = "-1", protocol.EmoteModPreanim
		readySpeaker(room, ready, "Phoenix", "normal") // idle ready; the preanim never will be
		room.HandleEvent(Event{Kind: EventMessage, Message: m})
		if room.QueueLen() != 1 {
			t.Fatal("setup: a cold preanim must hold the message")
		}
		room.Update(50 * time.Millisecond) // far under the 500 ms cap, no miss learned yet → still held
		if room.QueueLen() != 1 {
			t.Fatal("without the miss signal the hold must persist (nothing has said the preanim is gone)")
		}
		// The manager conclusively 404s the dummy preanim — the App's warning relay
		// calls exactly this on the game thread.
		room.NotifyAssetMissing(room.urls.Emote("Phoenix", "-1", EmotePreanim))
		room.Update(16 * time.Millisecond)
		if room.QueueLen() != 0 || !room.Scene.Speaker.Visible {
			t.Fatal("a conclusively-missing preanim must release the hold on the miss signal, not burn the full timeout")
		}
	})

	t.Run("gate off is unchanged", func(t *testing.T) {
		room, _, _, _ := newCourtroomRig(t)
		room.HandleEvent(Event{Kind: EventMessage, Message: waitMsg("Phoenix", "normal", "hi")})
		if !room.Scene.Speaker.Visible || room.QueueLen() != 0 {
			t.Fatal("with the gate off a message must begin immediately (default behaviour pinned)")
		}
	})
}

// TestWaitGateCoversThePreanimItWillPlay is the pre-message-stall audit's
// finding: with the strictness knob OFF (the default), a message whose
// PREANIMATION is the first thing the stage shows used to be released the moment
// the IDLE sprite landed — and then enterAfterShout parked Active on a preanim
// that had not arrived. The gate now covers whatever the first frame actually
// uses, so the message waits for the preanim it is about to play.
//
// The predicate is preanimWillPlay (courtroom.go), the same one enterAfterShout
// uses, so the gate can never disagree with the play path about what will be
// shown.
func TestWaitGateCoversThePreanimItWillPlay(t *testing.T) {
	newRig := func(t *testing.T) (*Courtroom, map[string]bool) {
		room, _, _, _ := newCourtroomRig(t)
		ready := map[string]bool{}
		room.SpriteWait = true
		room.SpriteWaitTimeout = 500 * time.Millisecond
		room.SpriteReady = func(base string) bool { return ready[base] }
		return room, ready
	}

	t.Run("a preanim that WILL play holds the message with the knob off", func(t *testing.T) {
		room, ready := newRig(t)
		if room.SpriteWaitPreanim {
			t.Fatal("premise: the strictness knob must default off")
		}
		m := waitMsg("Phoenix", "normal", "hi")
		m.PreEmote, m.EmoteMod = "flourish", protocol.EmoteModPreanim
		readySpeaker(room, ready, "Phoenix", "normal") // idle ready, preanim cold
		room.HandleEvent(Event{Kind: EventMessage, Message: m})
		if room.QueueLen() != 1 {
			t.Fatal("the message must wait for the preanim it is about to show")
		}
		ready[room.urls.Emote("Phoenix", "flourish", EmotePreanim)] = true
		room.Update(16 * time.Millisecond)
		if room.QueueLen() != 0 || room.Scene.Speaker.Active != room.Scene.Speaker.PreanimBase {
			t.Fatalf("a ready preanim must begin the message ON the preanim: queue=%d active=%q", room.QueueLen(), room.Scene.Speaker.Active)
		}
	})

	t.Run("an IMMEDIATE preanim counts too", func(t *testing.T) {
		room, ready := newRig(t)
		m := waitMsg("Phoenix", "normal", "hi")
		m.PreEmote, m.Immediate = "flourish", true // EmoteMod stays IDLE; immediate plays it anyway
		readySpeaker(room, ready, "Phoenix", "normal")
		room.HandleEvent(Event{Kind: EventMessage, Message: m})
		if room.QueueLen() != 1 {
			t.Fatal("an immediate-mode preanim is drawn from frame 1 — it must be waited for")
		}
	})

	t.Run("a DECLARED but never-played preanim does not hold", func(t *testing.T) {
		room, ready := newRig(t)
		m := waitMsg("Phoenix", "normal", "hi") // EmoteMod IDLE, immediate off
		m.PreEmote = "-1"                       // the dummy live packs ship on every emote
		readySpeaker(room, ready, "Phoenix", "normal")
		room.HandleEvent(Event{Kind: EventMessage, Message: m})
		if room.QueueLen() != 0 || !room.Scene.Speaker.Visible {
			t.Fatal("a preanim the message will never play must not delay it (that is what the knob is for)")
		}
	})

	t.Run("a conclusively-missing IDLE releases on the miss signal", func(t *testing.T) {
		room, _ := newRig(t)
		room.HandleEvent(Event{Kind: EventMessage, Message: waitMsg("Phoenix", "normal", "hi")})
		if room.QueueLen() != 1 {
			t.Fatal("setup: a cold idle must hold")
		}
		room.Update(50 * time.Millisecond) // far under the 500 ms cap
		if room.QueueLen() != 1 {
			t.Fatal("without the miss signal the hold must persist")
		}
		// A character folder that 404s 404s both spellings, and the gate covers
		// both (the talk sprite is what a plain message draws first), so the relay
		// reports both — exactly as the App's warning drain would.
		room.NotifyAssetMissing(room.urls.Emote("Phoenix", "normal", EmoteIdle))
		room.NotifyAssetMissing(room.urls.Emote("Phoenix", "normal", EmoteTalk))
		room.Update(16 * time.Millisecond)
		if room.QueueLen() != 0 {
			t.Fatal("a sprite that can never arrive must release the hold at once, not burn the timeout")
		}
	})
}

// TestWaitGateCoversTheTalkSpriteItOpensOn is the other half of the
// pre-message-stall audit. For a message with NO preanim — the overwhelming
// majority — the first sprite on screen is the TALK one, not the idle one:
// enterAfterShout falls straight through to startTalking, which parks Active on
// TalkBase in the very tick begin() runs, and the idle base is only what the
// message settles onto a second later. The gate waited on idle alone, so on a
// pack whose (a)/(b) spellings resolve at different times it opened onto a cold
// talk sprite and flashed the placeholder — a message that "starts early".
//
// The warm was never the problem: the arm has always prefetched talk beside idle
// (see waitHolds). Only the WAIT was missing.
func TestWaitGateCoversTheTalkSpriteItOpensOn(t *testing.T) {
	room, _, _, _ := newCourtroomRig(t)
	ready := map[string]bool{}
	room.SpriteWait, room.SpriteWaitTimeout = true, 500*time.Millisecond
	room.SpriteReady = func(base string) bool { return ready[base] }

	ready[room.urls.Emote("Phoenix", "normal", EmoteIdle)] = true // idle in, talk still cold
	room.HandleEvent(Event{Kind: EventMessage, Message: waitMsg("Phoenix", "normal", "hi")})
	if room.QueueLen() != 1 {
		t.Fatal("a cold TALK sprite must hold: it is what the first frame of a plain message draws")
	}

	ready[room.urls.Emote("Phoenix", "normal", EmoteTalk)] = true
	room.Update(16 * time.Millisecond)
	if room.QueueLen() != 0 {
		t.Fatal("the talk sprite landing must release the hold")
	}
	if room.Scene.Speaker.Active != room.Scene.Speaker.TalkBase {
		t.Errorf("the message opened on %q, want the talk base %q — the gate's subject must be what is drawn",
			room.Scene.Speaker.Active, room.Scene.Speaker.TalkBase)
	}
}

// TestWaitGateReleasesOnAConclusivelyMissingTalkSprite keeps the new hold inside
// the bound the whole gate lives by: a sprite that can never arrive is settled,
// not something to wait for. A pack that ships only the bare spelling resolves
// idle and talk to the same file; a pack missing the talk sprite entirely must
// cost the message nothing beyond the miss signal.
func TestWaitGateReleasesOnAConclusivelyMissingTalkSprite(t *testing.T) {
	room, _, _, _ := newCourtroomRig(t)
	ready := map[string]bool{}
	room.SpriteWait, room.SpriteWaitTimeout = true, 10*time.Second // long enough that a burn is unmistakable
	room.SpriteReady = func(base string) bool { return ready[base] }

	ready[room.urls.Emote("Phoenix", "normal", EmoteIdle)] = true
	room.HandleEvent(Event{Kind: EventMessage, Message: waitMsg("Phoenix", "normal", "hi")})
	if room.QueueLen() != 1 {
		t.Fatal("setup: the cold talk sprite must hold")
	}
	room.NotifyAssetMissing(room.urls.Emote("Phoenix", "normal", EmoteTalk))
	room.Update(16 * time.Millisecond)
	if room.QueueLen() != 0 {
		t.Fatal("a conclusively-missing talk sprite must release the hold at once, not burn the timeout")
	}
}

// TestWaitGateAndPlayPathAgreeOnThePreanim is the encapsulation test for the
// shared predicate. The gate and enterAfterShout must answer the same question
// with the same code: if preanimWillPlay is ever forked into a second spelling,
// one of these rows starts disagreeing and the stall comes back silently.
func TestWaitGateAndPlayPathAgreeOnThePreanim(t *testing.T) {
	rows := []struct {
		name string
		mod  int
		imm  bool
		pre  string
	}{
		{"preanim mod", protocol.EmoteModPreanim, false, "flourish"},
		{"preanim zoom", protocol.EmoteModPreanimZoom, false, "flourish"},
		{"idle + immediate", protocol.EmoteModIdle, true, "flourish"},
		{"idle only", protocol.EmoteModIdle, false, "flourish"},
		{"zoom only", protocol.EmoteModZoom, false, "flourish"},
		{"no preanim at all", protocol.EmoteModPreanim, false, ""},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			m := waitMsg("Phoenix", "normal", "hi")
			m.EmoteMod, m.Immediate, m.PreEmote = row.mod, row.imm, row.pre
			want := preanimWillPlay(m)

			// The PLAY path's answer, observed through the stage it produces: with
			// every sprite resident, Active lands on the preanim iff it plays.
			room, _, _, _ := newCourtroomRig(t)
			room.SpriteReady = func(string) bool { return true }
			room.HandleEvent(Event{Kind: EventMessage, Message: m})
			playedIt := room.Scene.Speaker.PlayOnce && room.Scene.Speaker.Active == room.Scene.Speaker.PreanimBase
			if playedIt != want {
				t.Fatalf("the play path %v the preanim, preanimWillPlay says %v", playedIt, want)
			}

			// The GATE's answer, observed through the hold: with the idle resident and
			// the preanim cold, the message is held iff the preanim plays.
			gated, _, _, _ := newCourtroomRig(t)
			gated.SpriteWait, gated.SpriteWaitTimeout = true, 500*time.Millisecond
			// The speaker's own art has landed; only the PREANIM is cold, so the
			// hold that remains is about the preanim and nothing else.
			speaker := map[string]bool{
				gated.urls.Emote("Phoenix", "normal", EmoteIdle): true,
				gated.urls.Emote("Phoenix", "normal", EmoteTalk): true,
			}
			gated.SpriteReady = func(base string) bool { return speaker[base] }
			m2 := waitMsg("Phoenix", "normal", "hi")
			m2.EmoteMod, m2.Immediate, m2.PreEmote = row.mod, row.imm, row.pre
			gated.HandleEvent(Event{Kind: EventMessage, Message: m2})
			if held := gated.QueueLen() == 1; held != want {
				t.Fatalf("the gate held=%v, preanimWillPlay says %v", held, want)
			}
		})
	}
}

// TestSequentialWait pins the EXPERIMENTAL strict-sequencing override (#136): a
// 1:1 AO2-style queue that holds each message until EVERY on-screen sprite
// (speaker idle/talk, pair partner, group members) has settled, never
// fast-forwards a backlog, and never times out. It is an override of the normal
// sequencing, so it holds even when the cold-load "wait" mode (SpriteWait) is
// off.
func TestSequentialWait(t *testing.T) {
	newRig := func(t *testing.T) (*Courtroom, map[string]bool) {
		room, _, _, _ := newCourtroomRig(t)
		ready := map[string]bool{}
		room.SequentialWait = true
		// Far under any Update below, so a timeout burn would be unmistakable.
		room.SpriteWaitTimeout = 50 * time.Millisecond
		room.SpriteReady = func(base string) bool { return ready[base] }
		return room, ready
	}

	t.Run("forces the wait even with the cold-load wait mode off", func(t *testing.T) {
		room, ready := newRig(t)
		room.SpriteWait = false // the normal wait gate is OFF; strict sequencing overrides it
		room.HandleEvent(Event{Kind: EventMessage, Message: waitMsg("Phoenix", "normal", "hi")})
		if room.QueueLen() != 1 || room.Scene.Speaker.Visible {
			t.Fatalf("a cold sprite must hold under strict sequencing: queue=%d visible=%v", room.QueueLen(), room.Scene.Speaker.Visible)
		}
		readySpeaker(room, ready, "Phoenix", "normal")
		room.Update(16 * time.Millisecond)
		if room.QueueLen() != 0 || !room.Scene.Speaker.Visible {
			t.Fatal("the settled sprite must begin the held message")
		}
	})

	t.Run("waits for every on-screen sprite", func(t *testing.T) {
		room, ready := newRig(t)
		m := waitMsg("Phoenix", "normal", "hi")
		m.CharID = 3
		m.Pair = protocol.PairInfo{CharID: 1, Name: "Edgeworth", Emote: "normal"}
		m.Additional = []protocol.GroupPairMember{
			{CharID: 3, Name: "Phoenix", Emote: "normal"}, // the speaker — already on the Speaker layer, skipped
			{CharID: 5, Name: "Maya", Emote: "normal"},
		}
		readySpeaker(room, ready, "Phoenix", "normal") // speaker idle + talk ready
		room.HandleEvent(Event{Kind: EventMessage, Message: m})
		if room.QueueLen() != 1 {
			t.Fatal("setup: cold pair/group sprites must hold the message")
		}
		ready[room.urls.Emote("Edgeworth", "normal", EmoteIdle)] = true
		room.Update(16 * time.Millisecond)
		if room.QueueLen() != 1 {
			t.Fatal("the cold GROUP member must keep holding after the pair landed")
		}
		ready[room.urls.Emote("Maya", "normal", EmoteIdle)] = true
		room.Update(16 * time.Millisecond)
		if room.QueueLen() != 0 {
			t.Fatal("all on-screen sprites settled must release the message")
		}
	})

	t.Run("catch-up stays an independent AO2 knob", func(t *testing.T) {
		// Catch-up OFF: a backlog waits under strict sequencing (1:1 queue).
		room, _ := newRig(t)
		room.CatchUp, room.CatchUpThreshold = false, 1
		room.HandleEvent(Event{Kind: EventMessage, Message: waitMsg("Phoenix", "normal", "one")})
		room.HandleEvent(Event{Kind: EventMessage, Message: waitMsg("Edgeworth", "normal", "two")})
		if room.QueueLen() != 2 {
			t.Fatalf("setup: both messages should be queued (head held), got %d", room.QueueLen())
		}
		room.Update(16 * time.Millisecond)
		if room.QueueLen() != 2 {
			t.Fatal("with catch-up OFF a backlog must wait, not fast-forward")
		}

		// Catch-up ON (AO2 default): the backlog still fast-forwards, independent
		// of strict sequencing.
		room2, _ := newRig(t)
		room2.CatchUp, room2.CatchUpThreshold = true, 1
		room2.HandleEvent(Event{Kind: EventMessage, Message: waitMsg("Phoenix", "normal", "one")})
		room2.HandleEvent(Event{Kind: EventMessage, Message: waitMsg("Edgeworth", "normal", "two")})
		room2.Update(16 * time.Millisecond)
		if room2.QueueLen() != 1 {
			t.Fatal("with catch-up ON a backlog must fast-forward (AO2 parity)")
		}
	})

	t.Run("never times out", func(t *testing.T) {
		room, ready := newRig(t)
		room.HandleEvent(Event{Kind: EventMessage, Message: waitMsg("Phoenix", "normal", "hi")})
		for i := 0; i < 20; i++ {
			room.Update(16 * time.Millisecond) // 320 ms total — well past the 50 ms timeout
		}
		if room.QueueLen() != 1 {
			t.Fatal("strict sequencing must wait for settlement, never burn a timeout")
		}
		readySpeaker(room, ready, "Phoenix", "normal")
		room.Update(16 * time.Millisecond)
		if room.QueueLen() != 0 {
			t.Fatal("settlement must still release after the no-timeout hold")
		}
	})
}

// TestSequentialWaitSceneryAndAudio pins the strict-sequencing gate's scenery and
// audio arms: a message also holds until its position background, desk, custom
// chatbox and emote SFX settle (resident or conclusively 404'd) — and a 404'd
// base releases the no-timeout gate instead of hanging it.
func TestSequentialWaitSceneryAndAudio(t *testing.T) {
	newRig := func(t *testing.T) (*Courtroom, map[string]bool) {
		room, _, _, _ := newCourtroomRig(t)
		ready := map[string]bool{}
		room.SequentialWait = true
		room.SpriteWaitTimeout = 50 * time.Millisecond // far under every Update below
		room.SpriteReady = func(base string) bool { return ready[base] }
		return room, ready
	}

	t.Run("background holds until ready", func(t *testing.T) {
		room, sess, _, _ := newCourtroomRig(t)
		sess.Background = "court"
		ready := map[string]bool{}
		room.SequentialWait = true
		room.SpriteWaitTimeout = 50 * time.Millisecond
		room.SpriteReady = func(base string) bool { return ready[base] }
		readySpeaker(room, ready, "Phoenix", "normal")
		room.HandleEvent(Event{Kind: EventMessage, Message: waitMsg("Phoenix", "normal", "hi")})
		if room.QueueLen() != 1 {
			t.Fatal("a cold background must hold the message")
		}
		bgPart, _ := PositionScene("wit")
		ready[room.urls.Background("court", bgPart)] = true
		room.Update(16 * time.Millisecond)
		if room.QueueLen() != 0 {
			t.Fatal("the background landing must release the hold")
		}
	})

	t.Run("desk holds until ready", func(t *testing.T) {
		room, sess, _, _ := newCourtroomRig(t)
		sess.Background = "court"
		ready := map[string]bool{}
		room.SequentialWait = true
		room.SpriteWaitTimeout = 50 * time.Millisecond
		room.SpriteReady = func(base string) bool { return ready[base] }
		m := waitMsg("Phoenix", "normal", "hi")
		m.DeskMod = protocol.DeskShow // the desk is on screen, so it must be waited for
		readySpeaker(room, ready, "Phoenix", "normal")
		bgPart, deskPart := PositionScene("wit")
		ready[room.urls.Background("court", bgPart)] = true // background settled; only the desk is cold
		room.HandleEvent(Event{Kind: EventMessage, Message: m})
		if room.QueueLen() != 1 {
			t.Fatal("a cold desk must hold the message")
		}
		ready[room.urls.Background("court", deskPart)] = true
		room.Update(16 * time.Millisecond)
		if room.QueueLen() != 0 {
			t.Fatal("the desk landing must release the hold")
		}
	})

	t.Run("chatbox holds until ready", func(t *testing.T) {
		room, ready := newRig(t)
		room.ChatSkinFor = func(char string) string { return "YTTD" }
		readySpeaker(room, ready, "Phoenix", "normal")
		room.HandleEvent(Event{Kind: EventMessage, Message: waitMsg("Phoenix", "normal", "hi")})
		if room.QueueLen() != 1 {
			t.Fatal("a cold custom chatbox must hold the message")
		}
		cands := room.urls.MiscChatboxCandidates("YTTD")
		ready[cands[0]] = true
		room.Update(16 * time.Millisecond)
		if room.QueueLen() != 0 {
			t.Fatal("the chatbox landing must release the hold")
		}
	})

	t.Run("emote SFX holds until ready", func(t *testing.T) {
		room, ready := newRig(t)
		audio := map[string]bool{}
		room.AudioReady = func(base string) bool { return audio[base] }
		readySpeaker(room, ready, "Phoenix", "normal")
		audio[room.urls.BlipRef(defaultBlipSet).Base] = true // pre-settle the blip so only the SFX is cold
		m := waitMsg("Phoenix", "normal", "hi")
		m.SFXName, m.EmoteMod = "bang", protocol.EmoteModPreanim
		room.HandleEvent(Event{Kind: EventMessage, Message: m})
		if room.QueueLen() != 1 {
			t.Fatal("a cold emote SFX must hold the message")
		}
		audio[room.urls.SFX("bang")] = true
		room.Update(16 * time.Millisecond)
		if room.QueueLen() != 0 {
			t.Fatal("the SFX landing must release the hold")
		}
	})

	t.Run("a conclusively-404'd background releases, never hangs", func(t *testing.T) {
		room, sess, _, _ := newCourtroomRig(t)
		sess.Background = "court"
		ready := map[string]bool{}
		room.SequentialWait = true
		room.SpriteWaitTimeout = 50 * time.Millisecond
		room.SpriteReady = func(base string) bool { return ready[base] }
		readySpeaker(room, ready, "Phoenix", "normal")
		room.HandleEvent(Event{Kind: EventMessage, Message: waitMsg("Phoenix", "normal", "hi")})
		if room.QueueLen() != 1 {
			t.Fatal("setup: a cold background must hold")
		}
		for i := 0; i < 20; i++ {
			room.Update(16 * time.Millisecond) // 320 ms — far past any timeout; must never release
		}
		if room.QueueLen() != 1 {
			t.Fatal("a cold background must NOT release on any timeout under strict sequencing")
		}
		bgPart, _ := PositionScene("wit")
		room.NotifyAssetMissing(room.urls.Background("court", bgPart)) // the warning relay
		room.Update(16 * time.Millisecond)
		if room.QueueLen() != 0 {
			t.Fatal("a conclusively-404'd background must release the hold on the miss signal")
		}
	})
}
