package courtroom

import (
	"strings"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
)

// TestCharINIChatKey pins the [Options] chat= parse — the per-character
// chatbox-skin misc folder (AO2-Client get_chat).
func TestCharINIChatKey(t *testing.T) {
	ini, err := ParseCharINI([]byte("[Options]\nname = Dorothy\nblips = female\nchat = dorothybox\n[Emotions]\nnumber = 0\n"))
	if err != nil {
		t.Fatal(err)
	}
	if ini.Chat != "dorothybox" {
		t.Errorf("Chat = %q, want dorothybox", ini.Chat)
	}
	if ini.Blips != "female" {
		t.Errorf("Blips = %q, want female", ini.Blips)
	}
	plain, err := ParseCharINI([]byte("[Options]\nname = X\n[Emotions]\nnumber = 0\n"))
	if err != nil {
		t.Fatal(err)
	}
	if plain.Chat != "" {
		t.Errorf("absent chat key must parse empty, got %q", plain.Chat)
	}
}

// TestBlipAndSkinFallbacks pins the char.ini-driven message resolution: a
// message WITHOUT a wire blip asks BlipNameFor (webAO parity) and falls back
// to the AO default when that's empty too; a wire blip always wins. The
// speaker's chat skin lands on the scene as a misc/<x>/chatbox base and
// clears for skinless speakers.
func TestBlipAndSkinFallbacks(t *testing.T) {
	icMsg := func(text string) *protocol.ChatMessage {
		return &protocol.ChatMessage{
			CharName: "Phoenix", Emote: "normal", Message: text, Side: "wit",
			EmoteMod: protocol.EmoteModIdle, // no preanim stall — begin → typewriter directly
		}
	}
	room, _, _, _ := newCourtroomRig(t)
	room.BlipNameFor = func(char string) string {
		if char == "dorothy" {
			return "deep"
		}
		return ""
	}
	room.ChatSkinFor = func(char string) string {
		if char == "dorothy" {
			return "dorothybox"
		}
		return ""
	}

	msg := icMsg("hello")
	msg.CharName = "dorothy"
	msg.Blipname = ""
	room.HandleEvent(Event{Kind: EventMessage, Message: msg})
	room.SkipToIdle()
	if !strings.HasSuffix(room.blipBase, "sounds/blips/deep") {
		t.Errorf("empty wire blip must resolve via BlipNameFor: blipBase = %q", room.blipBase)
	}
	if !strings.Contains(room.Scene.ChatSkinBase, "misc/dorothybox/chatbox") {
		t.Errorf("chat skin base = %q, want misc/dorothybox/chatbox", room.Scene.ChatSkinBase)
	}

	// A wire blip wins over the char.ini fallback.
	msg2 := icMsg("hi")
	msg2.CharName = "dorothy"
	msg2.Blipname = "typewriter"
	room.HandleEvent(Event{Kind: EventMessage, Message: msg2})
	room.SkipToIdle()
	if !strings.HasSuffix(room.blipBase, "sounds/blips/typewriter") {
		t.Errorf("wire blip must win: blipBase = %q", room.blipBase)
	}

	// A skinless speaker clears the scene's skin (no stale carry-over).
	msg3 := icMsg("yo")
	msg3.CharName = "phoenix"
	room.HandleEvent(Event{Kind: EventMessage, Message: msg3})
	room.SkipToIdle()
	if room.Scene.ChatSkinBase != "" {
		t.Errorf("skinless speaker must clear ChatSkinBase, got %q", room.Scene.ChatSkinBase)
	}
	if !strings.HasSuffix(room.blipBase, "sounds/blips/male") {
		t.Errorf("unknown speaker with no wire blip must use the AO default: %q", room.blipBase)
	}
}
