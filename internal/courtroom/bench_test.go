package courtroom

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
)

const (
	benchFrame     = 16 * time.Millisecond
	benchEmotes    = 60
	benchCharID    = 5
	benchTintRed   = 200
	benchFramesRun = 64
)

// benchMarkedText carries every marker a real message can: style, profile and status.
func benchMarkedText() string {
	style := SpriteStyle{Tint: true, R: benchTintRed, Glow: true}
	return "Take that! This is an ordinary line of chat." + style.EncodeMarker() +
		WireProfile{Pronouns: "they/them", Tag: "attorney"}.EncodeMarker()
}

func benchMessage(text string) *protocol.ChatMessage {
	return &protocol.ChatMessage{
		CharID: benchCharID, CharName: "Phoenix", Emote: "normal",
		Side: "wit", Message: text, EmoteMod: protocol.EmoteModIdle,
	}
}

func benchBegin(b *testing.B, text string) {
	room, _, _, _ := newCourtroomRig(b)
	msg := benchMessage(text)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		room.HandleEvent(Event{Kind: EventMessage, Message: msg})
		room.SkipToIdle()
	}
}

func BenchmarkMessageBeginPlain(b *testing.B) {
	benchBegin(b, "Take that! This is an ordinary line of chat.")
}

func BenchmarkMessageBeginMarked(b *testing.B) { benchBegin(b, benchMarkedText()) }

// BenchmarkDecodeMarkers is what begin spends on chat extras, on the game thread, for each message.
func BenchmarkDecodeMarkers(b *testing.B) {
	text := benchMarkedText()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = DecodeSpriteStyle(text)
		_, _ = DecodeProfileMarker(text)
		_, _ = DecodeStatusMarker(text)
		_, _ = DecodeEffectsMarker(text)
	}
}

func BenchmarkUpdateIdle(b *testing.B) {
	room, _, _, _ := newCourtroomRig(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		room.Update(benchFrame)
	}
}

func BenchmarkUpdateTalking(b *testing.B) {
	room, _, _, _ := newCourtroomRig(b)
	msg := benchMessage(strings.Repeat("A long line of chat text. ", benchFramesRun))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if i%benchFramesRun == 0 {
			room.SkipToIdle()
			room.HandleEvent(Event{Kind: EventMessage, Message: msg})
		}
		room.Update(benchFrame)
	}
}

func BenchmarkParseCharINI(b *testing.B) {
	var sb strings.Builder
	fmt.Fprintf(&sb, "[Options]\nshowname=Phoenix\nside=def\nblips=male\n[Emotions]\nnumber=%d\n", benchEmotes)
	for i := 1; i <= benchEmotes; i++ {
		fmt.Fprintf(&sb, "%d=emote%d#-#normal%d#0#0\n", i, i, i)
	}
	data := []byte(sb.String())
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := ParseCharINI(data); err != nil {
			b.Fatal(err)
		}
	}
}
