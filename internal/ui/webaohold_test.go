package ui

import (
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
)

// TestWebAOModeBriefHold: webAO mode gives a sprite that is only decoding a moment to land, then plays regardless.
func TestWebAOModeBriefHold(t *testing.T) {
	a := testTabApp(t)
	dir := t.TempDir()
	local, cleanup := wireLocalManager(t, a, dir)
	defer cleanup()
	sess := courtroom.NewSession(func(protocol.Packet) error { return nil }, "test-hdid")
	a.room = courtroom.NewCourtroom(courtroom.NewURLBuilder(local.BaseURL()), a.d.Manager, sess, nil)
	a.room.Assets = spriteProbe(func(string) bool { return false })
	a.d.Prefs.SetSpriteLoadMode(config.SpriteLoadHoldPrev)
	a.applyTimingToRoom()

	a.room.HandleEvent(courtroom.Event{Kind: courtroom.EventMessage, Message: &protocol.ChatMessage{
		CharName: "Phoenix", Emote: "normal", Side: "def", EmoteMod: protocol.EmoteModIdle,
	}})
	if a.room.QueueLen() != 1 {
		t.Fatal("a sprite still decoding must get a moment to land")
	}
	a.room.Update(webAOSpriteHold)
	if a.room.QueueLen() != 0 {
		t.Fatal("webAO mode must not wait longer than its brief hold")
	}
}
