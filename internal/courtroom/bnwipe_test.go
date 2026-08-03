package courtroom

import (
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
)

// Issue #23: swapping areas left the previous area's sprite and chatbox frozen on
// the stage. AO2-Client keys the viewport teardown on the BN packet's ARITY —
// `set_background(content.at(0), content.size() >= 2)`
// (../AO2-Client/src/packet_distribution.cpp) — and AsyncAO's reducer read only
// field 0, so the flag never arrived and no teardown existed to run.
//
// These tests pin BOTH halves: the reducer's arity rule, and the stage teardown
// itself. The most load-bearing negative is TestPlainBackgroundChangeNeverWipes:
// fourteen call sites build an EventBackground with no server BN behind them, and
// one of them is buildRoom's re-seed, which runs on every tab switch IMMEDIATELY
// BEFORE the message restore it would otherwise erase.

// TestBNWipeIsKeyedOnArityNotPosValue pins the exact upstream rule. The tempting
// simplification — wipe only when the pos is non-empty — is wrong: akashi's area
// side is empty until someone runs /side, and KFO's remote-listen form sends an
// explicitly empty pos, and AO2 wipes on both. Softening this would silently turn
// the fix off for the ordinary area jump on the two families that support it.
func TestBNWipeIsKeyedOnArityNotPosValue(t *testing.T) {
	for _, tc := range []struct {
		name     string
		packet   string
		wantWipe bool
		wantPos  string
	}{
		{"one field: a plain /bg never wipes", "BN#gs4#%", false, ""},
		{"two fields, empty pos: akashi's default area jump still wipes", "BN#gs4##%", true, ""},
		{"two fields with a pos: wipes and carries the side", "BN#gs4#def#%", true, "def"},
		{"four fields (KFO): still wipes", "BN#gs4#pro#overlay#1#%", true, "pro"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := NewSession(func(protocol.Packet) error { return nil }, "h")
			evs := feed(t, s, tc.packet)
			bg := lastBackgroundEvent(t, evs)
			if bg.Wipe != tc.wantWipe {
				t.Errorf("Wipe = %v, want %v for %q", bg.Wipe, tc.wantWipe, tc.packet)
			}
			if bg.Name != tc.wantPos {
				t.Errorf("carried pos = %q, want %q", bg.Name, tc.wantPos)
			}
			if s.Background != "gs4" {
				t.Errorf("Background = %q, want gs4", s.Background)
			}
		})
	}
}

// TestBNPosEmitsSetPosBeforeBackground pins the ordering: AO2 calls set_side
// BEFORE set_background, so the re-stage at the end of the teardown already sees
// the new side. A non-empty pos must therefore produce EventSetPos first.
func TestBNPosEmitsSetPosBeforeBackground(t *testing.T) {
	s := NewSession(func(protocol.Packet) error { return nil }, "h")
	evs := feed(t, s, "BN#gs4#def#%")
	if len(evs) != 2 {
		t.Fatalf("got %d events, want 2 (SetPos then Background): %+v", len(evs), evs)
	}
	if evs[0].Kind != EventSetPos || evs[0].Text != "def" {
		t.Errorf("first event = %+v, want EventSetPos(def)", evs[0])
	}
	if evs[1].Kind != EventBackground {
		t.Errorf("second event = %+v, want EventBackground", evs[1])
	}
	// An EMPTY pos must NOT emit a spurious SetPos that would blank our own side.
	if evs := feed(t, NewSession(func(protocol.Packet) error { return nil }, "h"), "BN#gs4##%"); len(evs) != 1 || evs[0].Kind != EventBackground {
		t.Errorf("empty pos produced %+v, want a lone EventBackground", evs)
	}
}

// TestBNEmptyPacketIsIgnored pins AO2's content.isEmpty() guard. Without it a
// bare "BN#%" sets the background to "" and burns an asset probe on
// "background//witnessempty".
func TestBNEmptyPacketIsIgnored(t *testing.T) {
	s := NewSession(func(protocol.Packet) error { return nil }, "h")
	s.Background = "keepme"
	if evs := feed(t, s, "BN#%"); len(evs) != 0 {
		t.Errorf("an empty BN produced %+v, want no events", evs)
	}
	if s.Background != "keepme" {
		t.Errorf("an empty BN overwrote the background with %q", s.Background)
	}
}

// TestWipingBNClearsLastICButPlainBNKeepsIt pins the half of the fix that lives
// outside the room. A parked tab has NO Courtroom, so the reducer is the only
// place that can stop buildRoom's RestoreMessage from re-staging the PREVIOUS
// area's message when the user tabs back — which would visibly undo the wipe.
func TestWipingBNClearsLastICButPlainBNKeepsIt(t *testing.T) {
	msg := &protocol.ChatMessage{CharName: "Witch", Message: "before the jump"}

	s := NewSession(func(protocol.Packet) error { return nil }, "h")
	s.LastIC = msg
	feed(t, s, "BN#gs4#def#%")
	if s.LastIC != nil {
		t.Error("a wiping BN left LastIC set — tabbing back would re-stage the area we just left")
	}

	// A plain /bg is not an area change and must NOT drop the remembered message.
	s2 := NewSession(func(protocol.Packet) error { return nil }, "h")
	s2.LastIC = msg
	feed(t, s2, "BN#gs4#%")
	if s2.LastIC != msg {
		t.Error("a plain one-field BN cleared LastIC — tab restore would lose the current message")
	}
}

// TestBackgroundWipeClearsTheStage walks the AO2 teardown field by field. Each
// assertion maps to a hide/stopPlayback in courtroom.cpp's `if (display)` block.
func TestBackgroundWipeClearsTheStage(t *testing.T) {
	room, _, _, _ := newCourtroomRig(t)
	stageBusyRoom(room)

	room.setBackground("newarea", "wit", true)

	if room.Scene.Speaker.Active != "" || room.Scene.Pair.Active != "" {
		t.Errorf("sprites survived the wipe: speaker=%q pair=%q", room.Scene.Speaker.Active, room.Scene.Pair.Active)
	}
	if room.Scene.PairActive {
		t.Error("PairActive survived the wipe")
	}
	if !room.Scene.SpeakerInFront {
		t.Error("SpeakerInFront did not return to the default — the departed pair's order stuck")
	}
	if room.Scene.MessageText != "" || room.Scene.ShownameText != "" {
		t.Errorf("chatbox text survived: showname=%q message=%q", room.Scene.ShownameText, room.Scene.MessageText)
	}
	if room.Scene.MessageRaw != "" || len(room.Scene.MessageStyles) != 0 || len(room.Scene.MessageEffects) != 0 {
		t.Error("message markup survived the wipe")
	}
	if room.Scene.VisibleRunes != 0 || room.Scene.TextColor != 0 || room.Scene.ChatSkinBase != "" {
		t.Error("message reveal/colour/skin state survived the wipe")
	}
	if room.Scene.Centered || room.Scene.IsBlankPost {
		t.Error("message layout flags survived the wipe")
	}
	if room.Scene.ShoutBase != "" || room.Scene.ShoutFallbackBase != "" || room.Scene.ShoutCustom {
		t.Error("the shout bubble survived the wipe")
	}
	if room.Scene.FlashLeft != 0 || room.Scene.ShakeLeft != 0 {
		t.Error("screen effects survived the wipe")
	}
	if room.phase != PhaseIdle || room.current != nil || room.currentText != "" {
		t.Errorf("the message state machine did not reset: phase=%v current=%v", room.phase, room.current)
	}
	if room.waitFor != nil || room.waitLeft != 0 {
		t.Error("a sprite-wait hold survived the wipe — the next area's first line would be stranded")
	}
	if room.sfxArmed || room.sfxBase != "" || room.sfxShake {
		t.Error("a delayed SFX survived the wipe and would fire in the new area")
	}
	if room.additivePrevText != "" || room.additivePrefix != "" {
		t.Error("the 2.8 additive chain survived the wipe")
	}
	if len(room.pendingEffects) != 0 {
		t.Error("animated-text spans survived the wipe")
	}
}

// TestBackgroundWipeDropsTheBacklog pins skip_chatmessage_queue(). This loses no
// IC LOG line — the UI appends at ingest, before the room ever sees a message —
// but the queued messages themselves belong to the area we left.
func TestBackgroundWipeDropsTheBacklog(t *testing.T) {
	room, _, _, _ := newCourtroomRig(t)
	stageBusyRoom(room)
	for i := 0; i < 3; i++ {
		room.enqueue(&protocol.ChatMessage{CharName: "Witch", Message: "backlog"})
	}
	if room.QueueLen() == 0 {
		t.Fatal("fixture: expected a non-empty queue before the wipe")
	}
	room.setBackground("newarea", "wit", true)
	if n := room.QueueLen(); n != 0 {
		t.Errorf("queue kept %d message(s) from the old area", n)
	}
}

// TestWipedPairDoesNotResurrectOnDeskMiss pins the subtle one. applyDeskMods
// recomputes PairActive and the speaker offsets from c.current, and
// NotifyDeskMissing calls it ASYNCHRONOUSLY from the warning drain — so a desk
// 404 arriving after the wipe (the common case on a desk-less background) would
// rebuild the pair sprite we just cleared. Nilling c.current is what prevents it.
func TestWipedPairDoesNotResurrectOnDeskMiss(t *testing.T) {
	room, _, _, _ := newCourtroomRig(t)
	stageBusyRoom(room)
	room.setBackground("newarea", "wit", true)

	room.NotifyDeskMissing(room.Scene.DeskBase)

	if room.Scene.PairActive || room.Scene.Pair.Active != "" {
		t.Error("a late desk-404 resurrected the wiped pair sprite")
	}
	if room.Scene.MessageText != "" {
		t.Error("a late desk-404 resurrected the wiped message")
	}
}

// TestBackgroundWipeReScenesAtOurOwnSideWhenBNCarriesNone is the regression guard
// for the defect a naive fix would have shipped. AO2 ends its teardown with
// set_scene(true, current_or_default_side()) — the POS DROPDOWN, i.e. OUR side.
// Scene.Position instead tracks the last SPEAKER's side, so on akashi and on
// KFO's remote-listen form (both of which send an EMPTY pos) using it would wipe
// the stage and immediately re-stage it at whoever talked last in the room we
// just left. That would be a NEW visible bug in exactly the scenario #23 is about.
func TestBackgroundWipeReScenesAtOurOwnSideWhenBNCarriesNone(t *testing.T) {
	room, _, _, _ := newCourtroomRig(t)
	room.LocalSide = func() string { return "def" }
	room.Scene.Position = "wit" // the previous area's last speaker

	room.setBackground("newarea", "", true) // akashi's default: wipe, no pos

	if room.Scene.Position != "def" {
		t.Fatalf("re-scened at %q, want our own side def", room.Scene.Position)
	}
	wantBG, wantDesk := PositionScene("def")
	if room.Scene.BackgroundBase != room.urls.Background("newarea", wantBG) {
		t.Errorf("background base = %q, want the def cut", room.Scene.BackgroundBase)
	}
	if room.Scene.DeskBase != room.urls.Background("newarea", wantDesk) {
		t.Errorf("desk base = %q, want the def cut", room.Scene.DeskBase)
	}
}

// TestBackgroundWipeReScenesAtTheNewPos pins the non-empty half: a BN that DOES
// carry a pos moves us there, and it beats the LocalSide fallback.
func TestBackgroundWipeReScenesAtTheNewPos(t *testing.T) {
	room, _, _, _ := newCourtroomRig(t)
	room.LocalSide = func() string { return "def" }
	room.Scene.Position = "wit"

	room.setBackground("newarea", "pro", true)

	if room.Scene.Position != "pro" {
		t.Errorf("Position = %q, want the pos the server sent (pro)", room.Scene.Position)
	}
}

// TestBackgroundWipeWithNoLocalSideHookIsSafe pins the nil-hook degradation: the
// split pane and every test rig must keep working, falling back to today's
// behaviour rather than panicking or blanking the position.
func TestBackgroundWipeWithNoLocalSideHookIsSafe(t *testing.T) {
	room, _, _, _ := newCourtroomRig(t)
	room.LocalSide = nil
	room.Scene.Position = "wit"
	room.setBackground("newarea", "", true) // must not panic
	if room.Scene.Position != "wit" {
		t.Errorf("Position = %q, want the untouched wit with no hook installed", room.Scene.Position)
	}
}

// TestPlainBackgroundChangeNeverWipes is the most important negative in this
// file. Fourteen in-tree call sites construct an EventBackground with no server
// BN behind them — the bg picker, replay, the scene maker, and critically
// buildRoom's re-seed, which runs on EVERY tab switch immediately before
// RestoreMessage. If the wipe were a property of setBackground rather than an
// opt-in flag on the event, tab-restore would blank the stage it just restored.
func TestPlainBackgroundChangeNeverWipes(t *testing.T) {
	room, _, _, _ := newCourtroomRig(t)
	stageBusyRoom(room)
	before := room.Scene.MessageText

	room.HandleEvent(Event{Kind: EventBackground, Text: "reseed"}) // no Wipe: the client-side form

	if room.Scene.MessageText != before {
		t.Errorf("a client-side background change wiped the message (%q → %q) — tab restore is broken",
			before, room.Scene.MessageText)
	}
	if room.current == nil {
		t.Error("a client-side background change tore down the message state machine")
	}
	if room.Scene.Speaker.Active == "" {
		t.Error("a client-side background change cleared the speaker sprite")
	}
}

// TestHandleEventCarriesWipeAndPos pins the wiring from the reducer's event
// through HandleEvent into the teardown, so the two halves cannot drift apart.
func TestHandleEventCarriesWipeAndPos(t *testing.T) {
	room, _, _, _ := newCourtroomRig(t)
	stageBusyRoom(room)
	room.Scene.Position = "wit"

	room.HandleEvent(Event{Kind: EventBackground, Text: "newarea", Name: "jud", Wipe: true})

	if room.Scene.MessageText != "" {
		t.Error("Wipe did not reach the teardown through HandleEvent")
	}
	if room.Scene.Position != "jud" {
		t.Errorf("Position = %q, want jud — Name did not carry the pos through HandleEvent", room.Scene.Position)
	}
}

// --- helpers -----------------------------------------------------------------

// stageBusyRoom puts the room in the state an area jump would interrupt: a
// settled message with a pair, a shout, screen effects and armed latches. Every
// field here is one the wipe must clear, so a future field added to the teardown
// without a fixture update shows up as a passing-but-vacuous test rather than a
// silent hole.
func stageBusyRoom(room *Courtroom) {
	room.current = &protocol.ChatMessage{
		CharName: "Witch", Message: "mid-scene",
		DeskMod: protocol.DeskShow,
		Pair:    protocol.PairInfo{CharID: 4, Name: "partner"},
	}
	room.currentText = "mid-scene"
	room.phase = PhaseTalking
	room.Scene.Speaker = SpriteLayer{Active: "http://example.invalid/characters/witch/(a)normal", Visible: true}
	room.Scene.Pair = SpriteLayer{Active: "http://example.invalid/characters/partner/(a)normal", Visible: true}
	room.Scene.PairActive = true
	room.Scene.SpeakerInFront = false
	room.Scene.ShownameText = "Witch"
	room.Scene.MessageText = "mid-scene"
	room.Scene.MessageRaw = "mid-scene"
	room.Scene.MessageStyles = append(room.Scene.MessageStyles[:0], StyleRun{})
	room.Scene.MessageEffects = append(room.Scene.MessageEffects[:0], TextEffectSpan{})
	room.Scene.VisibleRunes = 5
	room.Scene.TextColor = 3
	room.Scene.ChatSkinBase = "http://example.invalid/misc/witch/chat"
	room.Scene.Centered = true
	room.Scene.IsBlankPost = true
	room.Scene.ShoutBase = "http://example.invalid/characters/witch/objection"
	room.Scene.ShoutFallbackBase = "http://example.invalid/objection"
	room.Scene.ShoutCustom = true
	room.Scene.FlashLeft = 500 * time.Millisecond
	room.Scene.ShakeLeft = 500 * time.Millisecond
	room.waitFor = room.current
	room.waitLeft = time.Second
	room.sfxArmed, room.sfxLeft, room.sfxBase, room.sfxShake = true, time.Second, "http://example.invalid/sounds/general/sfx", true
	room.pendingEffects = append(room.pendingEffects[:0], TextEffectSpan{})
	room.additivePrevText, room.additivePrefix = "prev", "prefix"
	room.preanimDone = true
}

// lastBackgroundEvent picks the EventBackground out of a reducer result (a BN
// carrying a pos emits EventSetPos ahead of it).
func lastBackgroundEvent(t *testing.T, evs []Event) Event {
	t.Helper()
	for _, ev := range evs {
		if ev.Kind == EventBackground {
			return ev
		}
	}
	t.Fatalf("no EventBackground in %+v", evs)
	return Event{}
}
