package ui

import (
	"strings"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
)

// TestSubtitleLine pins the plain-text extraction: zero-width wire frames,
// the "~~" centre prefix, and typewriter markup all vanish; the speaker is the
// showname, falling back to the character folder.
func TestSubtitleLine(t *testing.T) {
	styled := courtroom.SpriteStyle{Tint: true, R: 255, Opacity: 50}
	msg := &protocol.ChatMessage{
		CharName: "Phoenix",
		Showname: "Nick",
		Message:  "~~\\c2Hold {it}!" + styled.EncodeMarker(),
	}
	speaker, text := subtitleLine(msg)
	if speaker != "Nick" {
		t.Errorf("speaker = %q, want the showname", speaker)
	}
	if text != "Hold it!" {
		t.Errorf("text = %q, want markup-free %q", text, "Hold it!")
	}
	msg.Showname = ""
	if speaker, _ = subtitleLine(msg); speaker != "Phoenix" {
		t.Errorf("empty showname must fall back to the char folder, got %q", speaker)
	}
	if _, text = subtitleLine(&protocol.ChatMessage{Message: "   "}); text != "" {
		t.Errorf("a blankpost must extract empty text, got %q", text)
	}
}

// TestSubtitleCueFlow drives the feed/anchor/close state machine like the video
// export does: a cue starts on the first frame with visible text (preanim lead
// isn't subtitled), ends when the next message replaces it, and blank posts
// never produce a cue.
func TestSubtitleCueFlow(t *testing.T) {
	j := &gifExportJob{subsOn: true}
	msgA := &protocol.ChatMessage{CharName: "Franziska", Message: "Foolish fool!"}
	msgBlank := &protocol.ChatMessage{CharName: "Franziska", Message: " "}
	msgB := &protocol.ChatMessage{CharName: "Edgeworth", Message: "I object."}

	j.subFeed(msgA, 0)      // fed at frame 0…
	j.subAnchor(0, 0)       // …preanim frames show no text
	j.subAnchor(0, 1)       //
	j.subAnchor(3, 2)       // text starts typing on frame 2 → cue anchors here
	j.subFeed(msgBlank, 10) // blankpost replaces it at frame 10 (closes A, pends nothing visible)
	j.subAnchor(0, 10)      // a blank never shows runes…
	j.subAnchor(0, 11)      // …so it never anchors
	j.subFeed(msgB, 12)
	j.subAnchor(1, 12)
	j.subClose(20) // export end

	if len(j.subs) != 2 {
		t.Fatalf("cues = %d, want 2 (the blankpost must not cue)", len(j.subs))
	}
	if a := j.subs[0]; a.startFrame != 2 || a.endFrame != 10 || a.speaker != "Franziska" {
		t.Errorf("cue A = %+v, want [2,10) Franziska", a)
	}
	if b := j.subs[1]; b.startFrame != 12 || b.endFrame != 20 || b.text != "I object." {
		t.Errorf("cue B = %+v, want [12,20) %q", b, "I object.")
	}

	// Off = the whole machine is inert.
	off := &gifExportJob{}
	off.subFeed(msgA, 0)
	off.subAnchor(5, 1)
	off.subClose(9)
	if off.subHasPend || off.subHasOpen || len(off.subs) != 0 {
		t.Error("subtitles off must track nothing")
	}
}

// TestFormatSubtitles pins the SRT/VTT documents: numbering, the separator
// (comma vs dot), the header, and speaker prefixes.
func TestFormatSubtitles(t *testing.T) {
	cues := []subCue{
		{startFrame: 0, endFrame: 24, speaker: "Nick", text: "Hold it!"},
		{startFrame: 24, endFrame: 72, speaker: "", text: "…"},
	}
	srt := formatSubtitles(cues, 1000/24, false)
	wantSRT := "1\n00:00:00,000 --> 00:00:00,984\nNick: Hold it!\n\n2\n00:00:00,984 --> 00:00:02,952\n…\n\n"
	if srt != wantSRT {
		t.Errorf("SRT:\n%q\nwant:\n%q", srt, wantSRT)
	}
	vtt := formatSubtitles(cues, 1000/24, true)
	if !strings.HasPrefix(vtt, "WEBVTT\n\n") || strings.Contains(vtt, ",") {
		t.Errorf("VTT must carry the header and dot decimals:\n%q", vtt)
	}
	if !strings.Contains(vtt, "00:00:00.000 --> 00:00:00.984") {
		t.Errorf("VTT timing wrong:\n%q", vtt)
	}
}

// TestSubTimestamp covers the hour rollover (long recordings) and negatives.
func TestSubTimestamp(t *testing.T) {
	if got := subTimestamp(3723456, true); got != "01:02:03,456" {
		t.Errorf("timestamp = %q", got)
	}
	if got := subTimestamp(-5, false); got != "00:00:00.000" {
		t.Errorf("negative ms must clamp to zero, got %q", got)
	}
}
