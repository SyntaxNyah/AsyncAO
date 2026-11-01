package courtroom

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
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
	_, deskPart := PositionScene(msg.Side)
	desk := room.urls.Background(sess.Background, deskPart)

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

// recordingSource holds every fetch until released, then answers 404, and remembers what it was asked for.
type recordingSource struct {
	release chan struct{}
	started atomic.Int64
	urls    sync.Map
}

func (s *recordingSource) Fetch(_ context.Context, url string) ([]byte, error) {
	s.started.Add(1)
	s.urls.Store(url, struct{}{})
	<-s.release
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

func stalledRoom(t *testing.T) (*Courtroom, *assets.Manager, *recordingSource) {
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
	pool := network.NewPool(1)
	t.Cleanup(pool.Close)
	decoder := assets.NewDecoderPool(1)
	t.Cleanup(decoder.Close)
	src := &recordingSource{release: make(chan struct{})}
	mgr := assets.NewManager(assets.ManagerDeps{
		Resolver: assets.NewResolver(prefs), Prefs: prefs, T2: t2, Disk: disk,
		Source: src, Pool: pool, Decoder: decoder,
	})
	sess := NewSession((&sentRecorder{}).send, "test-hdid")
	return NewCourtroom(NewURLBuilder("https://h.example/"), mgr, sess, &audioRecorder{}), mgr, src
}

func waitFor(t *testing.T, what string, done func() bool) {
	t.Helper()
	const limit = 10 * time.Second
	deadline := time.Now().Add(limit)
	for !done() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

// TestWaitGateAsksAgainAfterAFullLaneRefusedIt: nothing else re-requests a held message's assets, so a refusal would
// otherwise hold the message for the whole timeout with nothing loading.
func TestWaitGateAsksAgainAfterAFullLaneRefusedIt(t *testing.T) {
	const (
		holdLonger = time.Minute
		tick       = 16 * time.Millisecond
	)
	room, mgr, src := stalledRoom(t)
	var once sync.Once
	release := func() { once.Do(func() { close(src.release) }) }
	t.Cleanup(release)
	room.SpriteWait = true
	room.SpriteWaitTimeout = holdLonger
	room.Assets = assetFake{sprite: func(string) bool { return false }}

	mgr.Prefetch("https://h.example/characters/c/(a)busy", assets.AssetTypeCharSprite, network.PriorityHigh) // AssetType: CharSprite
	waitFor(t, "the only worker to stall", func() bool { return src.started.Load() > 0 })
	for i := range mgr.WorkStats().Pool.HighCap {
		mgr.Prefetch(fmt.Sprintf("https://h.example/characters/c/(a)fill%d", i), assets.AssetTypeCharSprite, network.PriorityHigh) // AssetType: CharSprite
	}

	room.HandleEvent(Event{Kind: EventMessage, Message: waitMsg("Phoenix", "normal", "hi")})
	if room.QueueLen() != 1 {
		t.Fatalf("queue = %d, want the message held", room.QueueLen())
	}
	if mgr.RefusedRequests() == 0 {
		t.Fatal("the full lane should have refused the gate's requests")
	}

	release()
	waitFor(t, "the lane to drain", func() bool {
		s := mgr.WorkStats().Pool
		return s.HighQueued == 0 && s.Busy == 0
	})
	idle := room.urls.Emote("Phoenix", "normal", EmoteIdle)
	if src.askedFor(idle) {
		t.Fatal("the refused sprite was fetched before the gate asked again")
	}
	room.Update(tick)
	waitFor(t, "the gate's second request to reach the source", func() bool { return src.askedFor(idle) })
}
