package ui

import (
	"path/filepath"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/cache"
	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/network"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
)

// newRoomForTest builds a real Courtroom over a local-mode Manager (mirrors the
// courtroom package's rig) so HandleEvent can populate the per-speaker style memory.
func newRoomForTest(t *testing.T) *courtroom.Courtroom {
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
	pool := network.NewPool(2)
	t.Cleanup(pool.Close)
	decoder := assets.NewDecoderPool(2)
	t.Cleanup(decoder.Close)
	local := assets.NewLocalFetcher([]string{t.TempDir()})
	mgr := assets.NewManager(assets.ManagerDeps{
		Resolver: assets.NewResolver(prefs), Prefs: prefs, T2: t2, Disk: disk,
		Source: local, LocalMode: true, Pool: pool, Decoder: decoder,
	})
	return courtroom.NewCourtroom(courtroom.NewURLBuilder(local.BaseURL()), mgr, nil, courtroom.NopAudio{})
}

func msgFor(charID int, name, text string) *protocol.ChatMessage {
	return &protocol.ChatMessage{
		CharID: charID, CharName: name, Emote: "normal",
		Side: "wit", Message: text, EmoteMod: protocol.EmoteModIdle,
	}
}

// TestRecEventSelfContainsStyle pins the advisor's load-bearing fix: send-on-change
// puts the style marker only on CHANGE messages, so a no-marker line inherits the
// speaker's last style — but a clip/export that starts mid-stream would lose it. The
// recorder therefore re-injects the speaker's remembered style into a COPY of a
// no-marker line (never the live msg), keeping recordings self-contained.
func TestRecEventSelfContainsStyle(t *testing.T) {
	a := &App{}
	a.room = newRoomForTest(t)
	style := courtroom.SpriteStyle{Tint: true, R: 200, G: 40, B: 40, Glow: true}

	// Feed a styled message so the room remembers char 5's style.
	a.room.HandleEvent(courtroom.Event{Kind: courtroom.EventMessage, Message: msgFor(5, "Phoenix", "hi"+style.EncodeMarker())})
	a.room.SkipToIdle()

	// A NO-marker line from char 5 is recorded WITH the style re-injected.
	bare := msgFor(5, "Phoenix", "no marker here")
	re := a.recEventFrom(courtroom.Event{Kind: courtroom.EventMessage, Message: bare})
	if !courtroom.HasSpriteMarker(re.Message.Message) {
		t.Fatal("a styled speaker's no-marker line was recorded WITHOUT its style — clips would lose it")
	}
	if got, _ := courtroom.DecodeSpriteStyle(re.Message.Message); got != style {
		t.Errorf("re-injected style = %+v, want %+v", got, style)
	}
	// The LIVE message must be untouched — the recording copies, never mutates it.
	if courtroom.HasSpriteMarker(bare.Message) {
		t.Error("recEventFrom mutated the live message instead of copying")
	}

	// An UNSTYLED speaker's line is left bare (no spurious marker).
	re2 := a.recEventFrom(courtroom.Event{Kind: courtroom.EventMessage, Message: msgFor(9, "Maya", "plain")})
	if courtroom.HasSpriteMarker(re2.Message.Message) {
		t.Error("an unstyled speaker's line was given a marker")
	}
}
