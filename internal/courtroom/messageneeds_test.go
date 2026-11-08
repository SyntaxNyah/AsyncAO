package courtroom

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/cache"
	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/network"
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

// recordingSource answers 404 at once and remembers what it was asked for.
type recordingSource struct{ urls sync.Map }

func (s *recordingSource) Fetch(_ context.Context, url string) ([]byte, error) {
	s.urls.Store(url, struct{}{})
	return nil, network.ErrAssetNotFound
}

func (s *recordingSource) askedFor(base string) bool {
	found := false
	s.urls.Range(func(k, _ any) bool {
		found = strings.HasPrefix(k.(string), base)
		return !found
	})
	return found
}

func recordingRoom(t *testing.T) (*Courtroom, *recordingSource) {
	t.Helper()
	prefs, err := config.New(filepath.Join(t.TempDir(), config.PrefsFileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = prefs.Close() })
	t2, err := cache.NewByteBudgetLRU[string, []byte](cache.DefaultMaxEntries, cache.DefaultT2BudgetBytes, nil)
	if err != nil {
		t.Fatal(err)
	}
	disk, err := cache.NewDiskCache(filepath.Join(t.TempDir(), "assets"), 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(disk.Close)
	decoder := assets.NewDecoderPool(1)
	t.Cleanup(decoder.Close)
	src := &recordingSource{}
	mgr := assets.NewManager(assets.ManagerDeps{
		Resolver: assets.NewResolver(prefs), Prefs: prefs, T2: t2, Disk: disk,
		Source: src, Decoder: decoder,
	})
	t.Cleanup(mgr.CloseMedia)
	sess := NewSession((&sentRecorder{}).send, "test-hdid")
	return NewCourtroom(NewURLBuilder("https://h.example/"), mgr, sess, &audioRecorder{}), src
}

// TestWaitGateRequestsANeedThatJoinsLater: the chat skin is known only once char.ini lands, often after the gate armed,
// and nothing else asks for it until the message plays, so the gate must ask or it holds for the whole limit.
func TestWaitGateRequestsANeedThatJoinsLater(t *testing.T) {
	const tick = 16 * time.Millisecond
	room, src := recordingRoom(t)
	room.SequentialWait = true
	ready := map[string]bool{}
	room.Assets = assetFake{sprite: func(base string) bool { return ready[base] }}
	skin := ""
	room.Chars = charFake{skin: func(string) string { return skin }}

	room.HandleEvent(Event{Kind: EventMessage, Message: waitMsg("Phoenix", "normal", "hi")})
	if room.QueueLen() != 1 {
		t.Fatal("cold sprites should hold the message")
	}
	skin = "p3" // char.ini landed while the sprites loaded
	readySpeaker(room, ready, "Phoenix", "normal")
	room.Update(tick)

	chat := room.urls.MiscChatboxCandidates(skin)[0]
	const limit = 5 * time.Second
	deadline := time.Now().Add(limit)
	for !src.askedFor(chat) {
		if time.Now().After(deadline) {
			t.Fatal("the gate never asked for the chat skin that joined its needs")
		}
		time.Sleep(time.Millisecond)
	}
}
