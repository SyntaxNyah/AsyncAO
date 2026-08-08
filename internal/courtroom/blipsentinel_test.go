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
	if strings.Contains(room.blipRef.Base, "/-1") {
		t.Fatalf("the sentinel became a blip URL: %q", room.blipRef.Base)
	}
	if !strings.HasSuffix(room.blipRef.Base, "sounds/blips/deep") {
		t.Errorf("blip base = %q, want the char.ini set (deep)", room.blipRef.Base)
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
	if !strings.HasSuffix(room.blipRef.Base, "sounds/blips/male") {
		t.Errorf("blip base = %q, want the AO default set (male)", room.blipRef.Base)
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

// TestZeroDelayRunesAreSilentLikeAO2 pins the `}}}` (speed step 0) case against AO2's
// own fast path rather than against our budget. chat_tick's guard —
// `if ((msg_delay <= 0 && tick_pos < f_message.size() - 1) || formatting_char)`,
// ../AO2-Client/src/courtroom.cpp:4479-4484 — re-arms the timer with start(0) and
// RETURNS, so an instant character reaches neither `blip_player->playBlip()` (:4545) nor
// either `++blip_ticker` (:4546, :4554). Only the message's last character escapes the
// clause. So AO2 sounds exactly ONE blip for an all-instant message, and a `}}}` span in
// the middle of a line is silent AND phase-neutral.
//
// Both halves are asserted, because the count alone is satisfiable by accident: a
// per-delivery budget of one also yields "one blip" for the all-instant fixture while
// ticking on every instant rune, which moves every blip after an odd-length span.
func TestZeroDelayRunesAreSilentLikeAO2(t *testing.T) {
	tw := NewTypewriter()
	tw.Start("}}}" + strings.Repeat("a", 40)) // }}} → speed step 0 → zero per-char delay
	_, blips := tw.Update(DefaultCharInterval)
	if !tw.Done() {
		t.Fatalf("an instant message must finish in one tick (visible=%d)", tw.Visible())
	}
	if blips != 1 {
		t.Errorf("an all-instant message sounded %d blips, want exactly 1 — every character but "+
			"the last takes AO2's start(0) fast path and reaches no blip code (courtroom.cpp:4479-4484), "+
			"and the last one falls through the `tick_pos < size - 1` clause and blips", blips)
	}

	// Phase: an ODD-length instant span must not move the blips that follow it. `}}}`
	// drops to speed step 0, `{{{` climbs back to the default, so the three 'b's crawl
	// normally. AO2 enters them with blip_ticker still 0 → b1 and b3 blip. If the span
	// had ticked (three runes at rate 2 → ticker 3) the blip would land on b2 instead.
	phase := NewTypewriter()
	phase.Start("}}}aaa{{{bbb")
	// One nanosecond: far less than a character's worth of crawl, yet the zero-delay
	// runes are not gated on the accumulator at all, so this call reveals exactly the
	// span and stops at the first 'b'. Anything it sounds came from the span.
	if r, b := phase.Update(time.Nanosecond); b != 0 || r != 3 {
		t.Errorf("the instant span revealed %d runes and sounded %d blips, want 3 and 0 — it is not "+
			"the end of the message, so AO2 reaches no blip code for any of it", r, b)
	}
	var at []int
	for !phase.Done() {
		if _, b := phase.Update(DefaultCharInterval); b > 0 {
			at = append(at, phase.Visible()) // 1-based index of the rune just revealed
		}
	}
	// Runes are "aaabbb": the b's are visible-counts 4, 5, 6.
	if len(at) != 2 || at[0] != 4 || at[1] != 6 {
		t.Errorf("blips landed at visible-counts %v, want [4 6] — the crawl after a `}}}` span must "+
			"resume on AO2's phase (blip_ticker untouched by the instant runes)", at)
	}
}
