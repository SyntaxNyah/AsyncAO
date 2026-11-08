package courtroom

import (
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
)

func needFor(t *testing.T, needs []messageNeed, base string) messageNeed {
	t.Helper()
	for _, n := range needs {
		if n.base == base {
			return n
		}
	}
	t.Fatalf("no need for %q in %+v", base, needs)
	return messageNeed{}
}

func TestMessageNeedsPlainMessageIsOnlyTheSpeaker(t *testing.T) {
	const speakerSprites = 2
	room, _, _, _ := newCourtroomRig(t)
	room.SpriteWait = true
	needs := room.messageNeeds(waitMsg("Phoenix", "normal", "hi"))
	if len(needs) != speakerSprites {
		t.Fatalf("needs = %+v, want idle and talk", needs)
	}
	for _, n := range needs {
		if n.gate != gateSpeakerSprite || n.typ != assets.AssetTypeCharSprite || len(n.alts) == 0 {
			t.Errorf("need = %+v, want a speaker sprite with its alternate spellings", n)
		}
	}
}

func TestMessageNeedsPairIsWaitedOnOnlyWhenAskedFor(t *testing.T) {
	room, _, _, _ := newCourtroomRig(t)
	room.SpriteWait = true
	msg := waitMsg("Phoenix", "normal", "hi")
	msg.Pair = protocol.PairInfo{CharID: 1, Name: "Maya", Emote: "smile"}
	pairIdle := room.urls.Emote("Maya", "smile", EmoteIdle)

	for _, n := range room.messageNeeds(msg) {
		if n.base == pairIdle {
			t.Fatal("the pair partner is needed with SpriteWaitPair off")
		}
	}
	room.SpriteWaitPair = true
	if n := needFor(t, room.messageNeeds(msg), pairIdle); n.gate != gateSprite {
		t.Errorf("partner gate = %v, want the first frame only", n.gate)
	}
}

func TestMessageNeedsHiddenDeskIsRequestedButNotWaitedOn(t *testing.T) {
	room, sess, _, _ := newCourtroomRig(t)
	room.SequentialWait = true
	sess.Background = "court"
	msg := waitMsg("Phoenix", "normal", "hi")
	bg := room.urls.PositionScenery(sess.Background, msg.Side, nil).Background
	room.Assets = assetFake{files: map[string]string{bg: room.urls.Background(sess.Background, defaultPosBackground) + ".webp"}}
	desk := room.urls.Background(sess.Background, "stand")

	msg.DeskMod = protocol.DeskShow
	if n := needFor(t, room.messageNeeds(msg), desk); n.gate != gateDesk {
		t.Errorf("shown desk gate = %v, want gateDesk", n.gate)
	}
	msg.DeskMod = protocol.DeskHide
	if n := needFor(t, room.messageNeeds(msg), desk); n.gate != gateNone {
		t.Errorf("hidden desk gate = %v, want gateNone", n.gate)
	}
}

// TestWaitGateChecksEveryNeedBeforeReleasing: holding on one need must not let the message start while another is cold.
func TestWaitGateChecksEveryNeedBeforeReleasing(t *testing.T) {
	const tick = 16 * time.Millisecond
	room, _, _, _ := newCourtroomRig(t)
	ready := map[string]bool{}
	room.SpriteWait = true
	room.SpriteWaitTimeout = time.Minute
	room.Assets = assetFake{sprite: func(base string) bool { return ready[base] }}
	room.HandleEvent(Event{Kind: EventMessage, Message: waitMsg("Phoenix", "normal", "hi")})

	ready[room.urls.Emote("Phoenix", "normal", EmoteIdle)] = true
	room.Update(tick)
	if room.QueueLen() != 1 {
		t.Fatal("the message started with its talk sprite still cold")
	}
	ready[room.urls.Emote("Phoenix", "normal", EmoteTalk)] = true
	room.Update(tick)
	if room.QueueLen() != 0 {
		t.Fatal("the message stayed held with every need settled")
	}
}
