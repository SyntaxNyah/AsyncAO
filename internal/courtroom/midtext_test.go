package courtroom

import (
	"strings"
	"testing"
)

func TestEncodeMidEmote(t *testing.T) {
	if got := EncodeMidEmote("", "angry", ""); got != "<e:-:angry:->" {
		t.Errorf("all-none = %q, want %q", got, "<e:-:angry:->")
	}
	if got := EncodeMidEmote("flourish", "pointing", "whip"); got != "<e:flourish:pointing:whip>" {
		t.Errorf("full = %q", got)
	}
	if got := EncodeMidSFX("bang"); got != "<s:bang>" {
		t.Errorf("sfx = %q, want <s:bang>", got)
	}
}

func TestScanMidText(t *testing.T) {
	marks, ok := ScanMidText(`before <e:-:angry:-> and <s:bang> after`)
	if !ok {
		t.Fatal("markers not found")
	}
	if len(marks) != 2 {
		t.Fatalf("marks = %d, want 2", len(marks))
	}
	if marks[0].Emote == nil || marks[0].Emote.Emote != "angry" || marks[0].Emote.Pre != "" {
		t.Errorf("first = %+v, want angry emote", marks[0])
	}
	if marks[1].SFX != "bang" {
		t.Errorf("second = %+v, want bang sfx", marks[1])
	}

	// No markers → ok=false and nil (0-alloc fast path).
	if _, ok := ScanMidText("no markers here"); ok {
		t.Error("no-marker text must report ok=false")
	}
	// A stray '<' that never forms a marker stays literal.
	if m, ok := ScanMidText("a < b and 3 < 4"); ok || len(m) != 0 {
		t.Errorf("stray '<' parsed markers: %+v ok=%v", m, ok)
	}
	// A malformed marker (no closing '>') stays literal.
	if m, ok := ScanMidText("x <e:-:angry"); ok || len(m) != 0 {
		t.Errorf("malformed marker parsed: %+v ok=%v", m, ok)
	}
}

func TestStripMidText(t *testing.T) {
	in := "hi <e:-:angry:-> there <s:bang>!"
	if got := StripMidText(in); got != "hi  there !" {
		t.Errorf("StripMidText = %q, want %q", got, "hi  there !")
	}
	if got := StripMidText("no markers"); got != "no markers" {
		t.Errorf("plain text changed: %q", got)
	}
	// A lone '<' (e.g. a comparison) is not a marker and stays.
	if got := StripMidText("a < b"); got != "a < b" {
		t.Errorf("stray '<' mangled: %q", got)
	}
}

func TestExpandMidTextShortcodes(t *testing.T) {
	resolve := func(stem string) (MidEmote, bool) {
		switch stem {
		case "angry":
			return MidEmote{Pre: "flourish", Emote: "angry", SFX: "whip"}, true
		case "normal":
			return MidEmote{Emote: "normal"}, true
		}
		return MidEmote{}, false
	}

	cases := []struct{ in, want string }{
		{"I'm :angry: now", "I'm <e:flourish:angry:whip> now"},
		{":normal:!", "<e:-:normal:->!"},
		{"say :unknown: here", "say :unknown: here"}, // unknown shortcode stays literal
		{"a URL http://x and 12:30", "a URL http://x and 12:30"},
		{"plain, no colons", "plain, no colons"},
	}
	for _, tc := range cases {
		if got := ExpandMidTextShortcodes(tc.in, resolve); got != tc.want {
			t.Errorf("ExpandMidTextShortcodes(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	if got := ExpandMidTextShortcodes(":angry:", nil); got != ":angry:" {
		t.Errorf("nil resolver must leave text untouched, got %q", got)
	}
}

func TestTypewriterConsumesMidTextMarkers(t *testing.T) {
	tw := NewTypewriter()
	tw.Start("hi" + EncodeMidEmote("", "angry", "") + "bye")

	if got := tw.Text(); got != "hibye" {
		t.Fatalf("Text() = %q, want %q (markers are zero-width)", got, "hibye")
	}
	// Not due before the reveal reaches position 2.
	if _, ok := tw.NextMidEmote(); ok {
		t.Fatal("mid-emote fired before its position was revealed")
	}
	// Reveal everything, then the marker is due.
	tw.Update(DefaultCharInterval * 100)
	m, ok := tw.NextMidEmote()
	if !ok || m.Mark.Emote == nil || m.Mark.Emote.Emote != "angry" || m.At != 2 {
		t.Fatalf("marker = %+v ok=%v, want {At:2 angry}", m, ok)
	}
	if _, ok := tw.NextMidEmote(); ok {
		t.Fatal("only one marker expected")
	}
}

func TestStripChatMarkupStripsMidTextMarkers(t *testing.T) {
	msg := "hi" + EncodeMidEmote("-", "pointing", "whip") + EncodeMidSFX("bang") + "!"
	if got := StripChatMarkup(msg); got != "hi!" {
		t.Errorf("StripChatMarkup = %q, want %q", got, "hi!")
	}
}

func TestTypewriterMidMarkersCapped(t *testing.T) {
	tw := NewTypewriter()
	var sb strings.Builder
	for i := 0; i < midMarksMax+5; i++ {
		sb.WriteString(EncodeMidEmote("", "angry", ""))
		sb.WriteString("x")
	}
	tw.Start(sb.String())
	if len(tw.midMarks) != midMarksMax {
		t.Errorf("%d markers recorded, want the cap %d", len(tw.midMarks), midMarksMax)
	}
}
