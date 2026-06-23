package courtroom

import (
	"strings"
	"testing"
)

// TestProfileWireRoundTrip pins the #101 slice-2 codec: a profile encodes to an
// invisible run, decodes back identically, and leaves the visible text clean.
func TestProfileWireRoundTrip(t *testing.T) {
	p := WireProfile{Pronouns: "they/them", Tag: "objection enjoyer"}
	marker := p.EncodeMarker()
	if marker == "" {
		t.Fatal("a non-empty profile encoded to nothing")
	}
	// Invisible: every rune is a zero-width codec symbol (so AO2/webAO show nothing).
	for _, r := range marker {
		if r != zwStart && octalIndex(r) < 0 {
			t.Fatalf("marker carries a visible rune U+%04X", r)
		}
	}
	got, ok := DecodeProfileMarker("hello world" + marker)
	if !ok {
		t.Fatal("DecodeProfileMarker found no profile")
	}
	if got != p {
		t.Errorf("round trip = %+v, want %+v", got, p)
	}
	if clean := stripZeroWidth("hello world" + marker); clean != "hello world" {
		t.Errorf("stripped text = %q, want %q", clean, "hello world")
	}
}

// TestProfileWireEmptyAndClear pins the empty/clear semantics: an empty profile sends
// nothing, and turning a profile off transmits a CLEAR that decodes ok=true+empty (the
// receiver uses that to drop the speaker's card).
func TestProfileWireEmptyAndClear(t *testing.T) {
	if (WireProfile{}).EncodeMarker() != "" {
		t.Error("an empty profile should encode to nothing")
	}
	clear := WireProfile{}.EncodeChangeMarker(WireProfile{Pronouns: "she/her"})
	if clear == "" {
		t.Fatal("turning a profile off should transmit a clear")
	}
	got, ok := DecodeProfileMarker("x" + clear)
	if !ok || !got.Empty() {
		t.Errorf("clear decoded to (%+v, %v), want (empty, true)", got, ok)
	}
}

// TestProfileWireChangeOnly pins send-on-change: an unchanged profile sends no marker,
// a newly-set one does.
func TestProfileWireChangeOnly(t *testing.T) {
	p := WireProfile{Pronouns: "he/him", Tag: "ace"}
	if p.EncodeChangeMarker(p) != "" {
		t.Error("an unchanged profile should send no marker")
	}
	if p.EncodeChangeMarker(WireProfile{}) == "" {
		t.Error("a newly-set profile should send a marker")
	}
}

// TestProfileStyleCoexist is the crux: the profile and sprite-style codecs share the
// zero-width channel. A style+profile message must decode BOTH, and a profile-ONLY
// message must NOT read as a style (a style clear would wipe an active style).
func TestProfileStyleCoexist(t *testing.T) {
	style := SpriteStyle{Tint: true, R: 10, G: 20, B: 30}
	prof := WireProfile{Pronouns: "they/them", Tag: "hi"}
	// Live send order: style marker first, then profile.
	msg := "I rest my case" + style.EncodeMarker() + prof.EncodeMarker()

	if gotStyle, clean := DecodeSpriteStyle(msg); gotStyle != style || clean != "I rest my case" {
		t.Errorf("style+profile message: style=%+v clean=%q", gotStyle, clean)
	}
	if !HasStyleMarker(msg) {
		t.Error("HasStyleMarker false on a style+profile message")
	}
	if gotProf, ok := DecodeProfileMarker(msg); !ok || gotProf != prof {
		t.Errorf("style+profile message: profile=(%+v,%v), want (%+v,true)", gotProf, ok, prof)
	}

	// A profile-ONLY message: not a style.
	pOnly := "hmm" + prof.EncodeMarker()
	if HasStyleMarker(pOnly) {
		t.Error("HasStyleMarker true on a profile-only message — would clear the speaker's style")
	}
	if s, _ := DecodeSpriteStyle(pOnly); s.Active() {
		t.Error("a profile-only message decoded to an active style")
	}
	if _, ok := DecodeProfileMarker(pOnly); !ok {
		t.Error("profile-only message: DecodeProfileMarker found nothing")
	}

	// A style-ONLY message: not a profile.
	sOnly := "x" + style.EncodeMarker()
	if HasProfileMarker(sOnly) {
		t.Error("a style-only message falsely reported a profile")
	}
}

// TestProfileWireClampAndCorrupt pins the defensive bounds: oversize fields are clamped
// to the wire caps and a truncated frame is benign (no profile, never a panic).
func TestProfileWireClampAndCorrupt(t *testing.T) {
	long := strings.Repeat("z", 200)
	got, ok := DecodeProfileMarker("x" + WireProfile{Pronouns: long, Tag: long}.EncodeMarker())
	if !ok {
		t.Fatal("oversize profile didn't round-trip")
	}
	if len(got.Pronouns) > wireProfilePronounsMax || len(got.Tag) > wireProfileTagMax {
		t.Errorf("fields not clamped: pron=%d tag=%d", len(got.Pronouns), len(got.Tag))
	}

	marker := WireProfile{Pronouns: "they/them", Tag: "hi"}.EncodeMarker()
	runes := []rune(marker)
	truncated := string(runes[:len(runes)/2]) // half the data → length prefixes unsatisfied
	if _, ok := DecodeProfileMarker("x" + truncated); ok {
		t.Error("a truncated profile frame should decode to no profile")
	}
}

// TestRememberProfile pins the per-character store: remember by name, recall, a clear
// (empty) frees the entry, and a blank name is ignored.
func TestRememberProfile(t *testing.T) {
	c := &Courtroom{}
	c.rememberProfile("Phoenix", WireProfile{Pronouns: "he/him", Tag: "lawyer"})
	if p, ok := c.RemoteProfile("Phoenix"); !ok || p.Pronouns != "he/him" {
		t.Errorf("RemoteProfile = (%+v,%v)", p, ok)
	}
	c.rememberProfile("Phoenix", WireProfile{}) // clear frees it
	if _, ok := c.RemoteProfile("Phoenix"); ok {
		t.Error("a cleared profile should be gone")
	}
	c.rememberProfile("", WireProfile{Pronouns: "x"}) // blank name ignored
	if _, ok := c.RemoteProfile(""); ok {
		t.Error("a blank character name should not be stored")
	}
}
