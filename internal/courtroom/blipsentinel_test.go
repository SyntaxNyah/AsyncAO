package courtroom

import (
	"strings"
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
)

// TestValidBlipNameRejectsSentinels pins the prefetch-boundary guard. There is no
// such thing as a NUMERIC blip in AO: get_blipname/get_blips resolve a NAME
// through char.ini [Options] blips → legacy gender → "male"
// (AO2-Client text_file_functions.cpp:487-514), so anything that parses as a bare
// number is by definition not a blip set — and that is exactly what the KFO family
// puts in the slot AO2 reads BLIPNAME from ("-1" idle, "<id>^0" when a third pair
// is confirmed).
func TestValidBlipNameRejectsSentinels(t *testing.T) {
	for _, bad := range []string{
		"",     // absent
		"-1",   // KFO third_charid, idle
		"0",    // the other family's "no value" spelling
		"7",    // a bare char id
		"5^0",  // KFO third_charid once a triplex pair is confirmed
		"../x", // traversal
		"a/b",  // not a single path segment
		`a\b`,
		".hidden",
		"a#b", // wire-escape characters have no business in a name
		"a&b",
	} {
		if validBlipName(bad) {
			t.Errorf("validBlipName(%q) = true, want false", bad)
		}
	}
	for _, good := range []string{"male", "female", "typewriter", "YTTD", "blip2", "deep voice", "sfx-blip_2"} {
		if !validBlipName(good) {
			t.Errorf("validBlipName(%q) = false, want true", good)
		}
	}
}

// TestSentinelBlipnameNeverBecomesAURL is the end-to-end half: a message carrying
// the sentinel must fall through to the char.ini set (and then the AO default)
// instead of minting sounds/blips/-1 and walking the whole audio ladder on a
// guaranteed miss, once per message, forever.
func TestSentinelBlipnameNeverBecomesAURL(t *testing.T) {
	room, _, _, _ := newCourtroomRig(t)
	room.BlipNameFor = func(char string) string {
		if char == "dorothy" {
			return "deep"
		}
		return ""
	}
	msg := &protocol.ChatMessage{
		CharName: "dorothy", Emote: "normal", Message: "hello", Side: "wit",
		EmoteMod: protocol.EmoteModIdle,
		Blipname: "-1", // what a KFO server puts in slot 30
	}
	room.HandleEvent(Event{Kind: EventMessage, Message: msg})
	room.SkipToIdle()
	if strings.Contains(room.blipBase, "/-1") {
		t.Fatalf("the sentinel became a blip URL: %q", room.blipBase)
	}
	if !strings.HasSuffix(room.blipBase, "sounds/blips/deep") {
		t.Errorf("blipBase = %q, want the char.ini set (deep)", room.blipBase)
	}

	// With no char.ini answer either, the AO default plays — never the sentinel.
	room.BlipNameFor = func(string) string { return "" }
	msg2 := &protocol.ChatMessage{
		CharName: "stranger", Emote: "normal", Message: "hi", Side: "wit",
		EmoteMod: protocol.EmoteModIdle,
		Blipname: "5^0", // triplex-paired KFO client
	}
	room.HandleEvent(Event{Kind: EventMessage, Message: msg2})
	room.SkipToIdle()
	if !strings.HasSuffix(room.blipBase, "sounds/blips/male") {
		t.Errorf("blipBase = %q, want the AO default set (male)", room.blipBase)
	}
}

// TestEffectiveBlipRateMatchesAO2SpamPrevention pins AO2's crawl-adaptive clamp
// (courtroom.cpp:4528-4537). At the base cadence the user's rate stands; once the
// per-character delay drops to AO2's threshold (25 ms against its 40 ms base
// crawl, carried here as a fraction of OUR base) the period rises with the crawl,
// so a `}}`-sped message thins its blips instead of machine-gunning.
func TestEffectiveBlipRateMatchesAO2SpamPrevention(t *testing.T) {
	tw := NewTypewriter()
	base := tw.Interval // DefaultCharInterval
	for _, tc := range []struct {
		name string
		need time.Duration
		want int
	}{
		{"base cadence (1.0x): the user's rate stands", base, DefaultBlipRate},
		{"slowest step (2.25x)", time.Duration(float64(base) * 2.25), DefaultBlipRate},
		{"0.625x sits exactly on AO2's threshold", time.Duration(float64(base) * 0.625), DefaultBlipRate},
		{"0.25x (a `}}` message) thins to 1-in-4", time.Duration(float64(base) * 0.25), 4},
		{"a \\p pause is not a fast crawl", base * 50, DefaultBlipRate},
		{"instant runes take AO2's msg_delay != 0 exit", 0, DefaultBlipRate},
	} {
		if got := tw.effectiveBlipRate(tc.need); got != tc.want {
			t.Errorf("%s: effectiveBlipRate(%v) = %d, want %d", tc.name, tc.need, got, tc.want)
		}
	}
}

// TestInstantRevealDoesNotMachineGunBlips pins the one place AsyncAO has to differ
// from AO2 by construction: AO2's chat_tick is a Qt timer that fires once per
// character, so even at speed step 0 it walks the string one shot at a time. Our
// Update reveals the whole zero-delay remainder inside ONE tick, and firing a blip
// per rune there is the literal machine-gun.
func TestInstantRevealDoesNotMachineGunBlips(t *testing.T) {
	tw := NewTypewriter()
	tw.Start("}}}" + strings.Repeat("a", 40)) // }}} → speed step 0 → zero per-char delay
	_, blips := tw.Update(DefaultCharInterval)
	if !tw.Done() {
		t.Fatalf("an instant message must finish in one tick (visible=%d)", tw.Visible())
	}
	if blips > instantRevealBlipCap {
		t.Errorf("blips = %d, want at most %d from a single instant tick", blips, instantRevealBlipCap)
	}
}
