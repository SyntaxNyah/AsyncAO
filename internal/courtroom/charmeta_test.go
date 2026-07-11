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
	if !strings.Contains(room.Scene.ChatSkinBase, "misc/dorothybox/chat") {
		t.Errorf("chat skin base = %q, want misc/dorothybox/chat", room.Scene.ChatSkinBase)
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

	// The identity spelling is LOWERCASE with slashes kept as path
	// separators (chat=VA-11/Jill nests — PathEscape's %2F was a dead URL);
	// spaces still escape per segment. The authored casing rides the chain
	// as an alt (MiscChatboxCandidates), never the scene key.
	msgCase := icMsg("case test")
	msgCase.CharName = "dorothy"
	room.ChatSkinFor = func(string) string { return "VA-11 HallA/Jill Stingray" }
	room.HandleEvent(Event{Kind: EventMessage, Message: msgCase})
	room.SkipToIdle()
	if !strings.Contains(room.Scene.ChatSkinBase, "misc/va-11%20halla/jill%20stingray/chat") {
		t.Errorf("nested chat skin base = %q, want misc/va-11%%20halla/jill%%20stingray/chat", room.Scene.ChatSkinBase)
	}

	// A skinless speaker clears the scene's skin (no stale carry-over).
	room.ChatSkinFor = func(char string) string {
		if char == "dorothy" {
			return "dorothybox"
		}
		return ""
	}
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

// TestCharINIFrameEffects pins the outgoing FRAME_* assembly (#17): an emote with
// [<emote>_Frame*] sections builds AO2's exact "<pre>[|k=v]^(b)<anim>[|k=v]^(a)<anim>[|k=v]^"
// wire form (frame keys sorted); an emote with none stays "" so the wire is
// unchanged for the (vast majority of) char.inis without frame data.
func TestCharINIFrameEffects(t *testing.T) {
	ini, err := ParseCharINI([]byte(`
[Emotions]
number = 2
1 = normal#leap#normal#1
2 = plain#-#plain#0

[leap_FrameScreenshake]
3 = 1

[(b)normal_FrameSFX]
5 = whip
2 = slap

[(a)normal_FrameRealization]
1 = 1
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(ini.Emotes) != 2 {
		t.Fatalf("want 2 emotes, got %d", len(ini.Emotes))
	}
	e := ini.Emotes[0]
	// Screenshake: only the preanim (leap) section has a tag; talk/idle are bare.
	if e.FrameShake != "leap|3=1^(b)normal^(a)normal^" {
		t.Errorf("FrameShake = %q, want the leap preanim tag with bare talk/idle", e.FrameShake)
	}
	// SFX: only the talk "(b)normal" section, frame-sorted (2 before 5).
	if e.FrameSFX != "leap^(b)normal|2=slap|5=whip^(a)normal^" {
		t.Errorf("FrameSFX = %q, want the (b)normal tags sorted by frame", e.FrameSFX)
	}
	// Realization: only the idle "(a)normal" section.
	if e.FrameRealize != "leap^(b)normal^(a)normal|1=1^" {
		t.Errorf("FrameRealize = %q, want the (a)normal tag", e.FrameRealize)
	}
	// The second emote authors no frame sections at all → all three empty (wire
	// unchanged; KFOCompat still fills its template downstream).
	p := ini.Emotes[1]
	if p.FrameShake != "" || p.FrameSFX != "" || p.FrameRealize != "" {
		t.Errorf("emote with no frame sections must leave FRAME_* empty, got shake=%q sfx=%q realize=%q", p.FrameShake, p.FrameSFX, p.FrameRealize)
	}
}
