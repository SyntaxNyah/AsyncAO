package courtroom

import (
	"strings"
	"testing"
	"time"
)

// The blip cadence, and the line between the two halves of a reveal.
//
// AO2's chat_tick is a one-shot QTimer re-armed from the HANDLING instant
// (../AO2-Client/src/courtroom.cpp:4271 stop, :4586 `chat_tick_timer->start(msg_delay)`),
// so one delivery emits one grapheme and sounds at most one blip
// (`blip_player->playBlip()`, :4545) — its blips are physically spaced in real time.
//
// AsyncAO reveals from a dt-driven accumulator, and one Update is one delivery — but its
// dt is the CALLER's step, which is not the crawl. The frame loop passes real elapsed
// time (clamped to cmd/asyncao/main.go's maxFrameDelta, 100 ms); the scene/GIF/WebP/video
// exporters step the room at 1s/fps with presets of 8/12/15/24 (internal/ui/gifexport.go).
// So a coarse step legitimately carries several runes.
//
// The split these tests pin: the REVEAL is proportional to dt (anything else is a hidden
// second speed knob keyed to the caller's frame rate — it slowed the exporters to a third
// of the requested crawl and truncated their lines), while the BLIPS carry a per-delivery
// budget (blipsPerUpdate), because N blips submitted in one instant are one louder blip,
// not a faster rhythm.

// crawl40 is AO2's own text_crawl default (options.cpp:180-183, `config.value("text_crawl", 40)`)
// and AsyncAO's shipped default (config.DefaultTextCrawlMs) — the speed the field report was
// made at.
const crawl40 = 40 * time.Millisecond

// frame60 is a healthy 60 Hz frame. It is NOT derived from the crawl on purpose: with idle
// rendering off (config.IdleFPSDefault == FPSOff, the shipped default) FramePace's talk tier
// hands back the ACTIVE cap, not min(staticTalkFPS, Interval) — internal/ui/app.go, the
// `case idleOff || idleUnlimited: talk = full` arm — so on defaults a message plays at
// whatever vsync gives. Nothing may assume frames arrive at least as often as runes are due.
const frame60 = 16667 * time.Microsecond

// ao2BlipsPerDelivery is AO2's own per-delivery blip count, read off the AO2 source
// rather than off our constant. chat_tick reaches `blip_player->playBlip()` at most once
// per invocation: one unconditional call inside one guarded branch, no loop
// (../AO2-Client/src/courtroom.cpp:4540-4548), and the timer is a one-shot re-armed at
// :4586. Every assertion below measures against THIS, never against blipsPerUpdate —
// a test that reads the constant it pins moves its goalposts along with a regression.
const ao2BlipsPerDelivery = 1

// TestTheBlipBudgetIsAO2sPerDeliveryCount is the direct pin on the constant itself, so
// widening it is a red test and not a silent policy change.
func TestTheBlipBudgetIsAO2sPerDeliveryCount(t *testing.T) {
	if blipsPerUpdate != ao2BlipsPerDelivery {
		t.Errorf("blipsPerUpdate = %d, want %d — one AsyncAO Update is one AO2 chat_tick "+
			"delivery, and a delivery sounds at most one blip (courtroom.cpp:4545). Raising this "+
			"hands the mixer N chunks in the same instant, which is one louder blip, not a faster "+
			"rhythm; lowering it silences the line.", blipsPerUpdate, ao2BlipsPerDelivery)
	}
}

// stepTypewriter drives n frames of `step` and returns the totals plus the worst single
// frame — the number that actually matters for "did it burst".
func stepTypewriter(tw *Typewriter, step time.Duration, n int) (revealed, blips, worstRunes, worstBlips int) {
	for i := 0; i < n; i++ {
		r, b := tw.Update(step)
		revealed += r
		blips += b
		if r > worstRunes {
			worstRunes = r
		}
		if b > worstBlips {
			worstBlips = b
		}
	}
	return
}

// TestALaggedFrameNeverBurstsTheBlips is THE defect test for the audible half: stall the
// clock mid-message and assert the frame that absorbs the stall sounds like ONE AO2 timer
// delivery, and that the frames after it do not machine-gun either. The reveal is
// deliberately NOT capped here — see TestTheCrawlRateIsIndependentOfTheCallersStepSize —
// so the stall's characters do appear; they just do not all speak at once.
func TestALaggedFrameNeverBurstsTheBlips(t *testing.T) {
	tw := NewTypewriter()
	tw.Interval = crawl40
	tw.Start(strings.Repeat("ab", 40)) // 80 blipping runes: far more than any burst could want

	// Settle into a normal cadence first, so the stall lands mid-message the way the
	// field report describes.
	stepTypewriter(&tw, frame60, 6)
	before := tw.Visible()

	// ONE frame that took 400 ms — a real hitch (asset decode, GC pause, window drag).
	// 400/40 = 10 runes are genuinely due, and at rate 2 that is 5 blips wanting the
	// same instant.
	const stall = 400 * time.Millisecond
	revealed, blips := tw.Update(stall)
	if blips > ao2BlipsPerDelivery {
		t.Errorf("a %v stall fired %d blips in ONE instant; AO2 sounds one per timer delivery, "+
			"physically spaced (courtroom.cpp:4545/:4586)", stall, blips)
	}
	if want := int(stall / crawl40); revealed < want-1 {
		t.Errorf("a %v stall revealed %d runes, want ~%d — the TEXT must stay proportional to dt "+
			"(a rune budget here re-times every caller with a coarse step; the live loop already "+
			"bounds a hitch at cmd/asyncao/main.go maxFrameDelta)", stall, revealed, want)
	}

	// …and the recovery must not machine-gun either: no banked blip debt.
	_, _, _, worstBlips := stepTypewriter(&tw, frame60, 12)
	if worstBlips > ao2BlipsPerDelivery {
		t.Errorf("post-stall catch-up fired %d blips in one frame", worstBlips)
	}
	if tw.Visible() <= before {
		t.Error("the crawl must keep advancing across a stall")
	}
}

// TestTheCrawlRateIsIndependentOfTheCallersStepSize is THE defect test for the visible
// half, and the one the exporters live on. The same message, the same crawl, driven at
// every step size a real caller uses — 60 Hz frames and the four export presets
// (internal/ui/gifexport.go exportFPSPresets, each stepping j.room.Update(1s/fps)) —
// must reveal the same number of runes per second of SCENE time. A per-Update rune
// budget makes this fail by construction: at 8 fps it capped the crawl at 8 runes/s
// against the 25/s the user asked for, so a 4 s line needed 3× the frames and hit the
// exporter's maxFrames truncation mid-sentence.
func TestTheCrawlRateIsIndependentOfTheCallersStepSize(t *testing.T) {
	const window = 4 * time.Second
	want := int(window / crawl40) // 100 runes are due in 4 s at the default crawl

	for _, step := range []time.Duration{
		frame60,              // the live frame loop
		time.Second / 8,      // exportFPSPresets[0]
		time.Second / 12,     // exportFPSPresets[1]
		time.Second / 15,     // exportFPSPresets[2]
		time.Second / 24,     // exportFPSPresets[3]
		crawl40,              // exactly one rune per call
		5 * time.Millisecond, // finer than the crawl: several calls per rune
	} {
		tw := NewTypewriter()
		tw.Interval = crawl40
		tw.Start(strings.Repeat("a", 2*want)) // long enough that it cannot simply finish
		revealed, _, _, worstBlips := stepTypewriter(&tw, step, int(window/step))
		if revealed < want*9/10 || revealed > want*11/10 {
			t.Errorf("step %v: revealed %d runes over %v, want ~%d — the crawl must not ride the "+
				"caller's step rate (%d%% of the requested speed)", step, revealed, window, want,
				revealed*100/want)
		}
		if worstBlips > ao2BlipsPerDelivery {
			t.Errorf("step %v: one call sounded %d blips", step, worstBlips)
		}
	}
}

// TestTheCatchUpCapDoesNotSlowTheOrdinaryCrawl is the fractional-accumulator half:
// reveal-and-reset would quantise a 40 ms crawl onto the 16.7 ms frame grid and run 25%
// slow, so the leftover must carry.
func TestTheCatchUpCapDoesNotSlowTheOrdinaryCrawl(t *testing.T) {
	tw := NewTypewriter()
	tw.Interval = crawl40
	tw.Start(strings.Repeat("a", 200))

	const frames = 120
	revealed, _, _, worstBlips := stepTypewriter(&tw, frame60, frames)
	want := int(time.Duration(frames) * frame60 / crawl40) // 120 × 16.667 ms / 40 ms = 50
	if revealed < want-1 || revealed > want+1 {
		t.Errorf("revealed %d runes over %v, want %d±1 at a %v crawl",
			revealed, time.Duration(frames)*frame60, want, crawl40)
	}
	if worstBlips > ao2BlipsPerDelivery {
		t.Errorf("a healthy frame sounded %d blips", worstBlips)
	}
}

// TestTheSpeedPrefStillDrivesTheCrawl pins that the user-facing ms knob keeps working
// across its whole range (config.MinTextCrawlMs..MaxTextCrawlMs) — the other half of the
// reported defect being that moving that knob changed nothing.
//
// The frame step is a FIXED 60 Hz for every crawl — deliberately not min(1s/30, crawl),
// which is what the first version of this test computed. Deriving the step from the crawl
// assumes exactly the property under test (that frames arrive at least as often as runes
// are due) and so cannot fail; and it is not what ships, because with idle rendering off
// FramePace's talk tier returns the active cap (see frame60). At a 5 ms crawl 200 runes
// are due per second and no display refreshes that often, so this is also the case a
// per-Update rune budget silently capped at the refresh rate.
func TestTheSpeedPrefStillDrivesTheCrawl(t *testing.T) {
	for _, crawl := range []time.Duration{
		5 * time.Millisecond,   // config.MinTextCrawlMs
		40 * time.Millisecond,  // config.DefaultTextCrawlMs
		100 * time.Millisecond, // config.MaxTextCrawlMs
	} {
		tw := NewTypewriter()
		tw.Interval = crawl
		const window = time.Second
		want := int(window / crawl)
		tw.Start(strings.Repeat("a", 2*want+8))

		revealed, blips, _, worstBlips := stepTypewriter(&tw, frame60, int(window/frame60))
		if revealed < want*9/10 || revealed > want*11/10 {
			t.Errorf("crawl %v: revealed %d runes/s at a fixed 60 Hz step, want ~%d — the speed "+
				"pref must reach the reveal even when it is faster than the frame rate", crawl, revealed, want)
		}
		if blips == 0 {
			t.Errorf("crawl %v: no blips over %v", crawl, window)
		}
		if worstBlips > ao2BlipsPerDelivery {
			t.Errorf("crawl %v: one frame sounded %d blips", crawl, worstBlips)
		}
	}
}

// TestBlipsRideTheRevealedCharactersLikeAO2 pins AO2's counting rules themselves —
// Courtroom::chat_tick, ../AO2-Client/src/courtroom.cpp:4540-4555:
//
//	if ((blip_rate <= 0 && blip_ticker < 1) || (b_rate > 0 && blip_ticker % b_rate == 0)) {
//	  if (!formatting_char && (f_character != ' ' || blank_blip)) { playBlip(); ++blip_ticker; }
//	} else { ++blip_ticker; }
//
// with blip_ticker reset to 0 at the start of every message (:4233). The ticker is tested
// BEFORE it advances, so the FIRST printed character blips; a whitespace on a due tick is
// silent AND holds the tick (:4550-4553); a whitespace on a non-due tick still advances it.
// Every expectation below was traced against those lines, not against our implementation.
func TestBlipsRideTheRevealedCharactersLikeAO2(t *testing.T) {
	// One rune per call, so the per-delivery blip budget cannot mask a counting bug.
	for _, tc := range []struct {
		text    string
		spaces  bool // blank_blip
		want    int
		tracing string
	}{
		// ticker 0 'a' blip(1) → 1 'b' tick → 2 ' ' due+silent, HOLDS → 2 'c' blip(2) →
		// 3 'd' tick → 4 ' ' holds → 4 'e' blip(3) → 5 'f' tick → 6 ' ' holds →
		// 6 'g' blip(4) → 7 'h' tick.
		{"ab cd ef gh", false, 4, "held ticks: the space waits for a letter to sound on"},
		// ticker 0 'a' blip(1) → 1 'b' tick → 2 ' ' due AND blank_blip → blip(2) →
		// 3 'c' tick → 4 'd' blip(3) → 5 ' ' tick → 6 'e' blip(4) → 7 'f' tick →
		// 8 ' ' blip(5) → 9 'g' tick → 10 'h' blip(6).
		{"ab cd ef gh", true, 6, "blank_blip: the spaces sound too"},
		// ticker 0 'a' blip(1) → 1 ' ' NOT due → ++ticker → 2 'b' blip(2) → 3 ' ' →
		// 4 'c' blip(3) → 5 ' ' → 6 'd' blip(4). Silent spaces, shifted phase.
		{"a b c d", false, 4, "a non-due space still shifts the phase (:4549-4555)"},
		// The first-character rule on its own: three letters at rate 2 blip on #1 and #3.
		{"abc", false, 2, "the ticker starts at 0, so character ONE blips"},
		// A line break is not whitespace: AO2 makes it a formatting_char (:4436-4439) and
		// the :4479 guard diverts it past playBlip (:4545) AND past both ++blip_ticker
		// (:4546,:4554). So ticker 0 'a' blip(1) → 1 '\n' SKIPPED ENTIRELY → 1 'b' tick →
		// 2 'c' blip(2) → 3 'd' tick: blips on a and c. Counting it as a space instead
		// ticks on the non-due break and shifts to a/b/d — three blips, one extra, and
		// every later blip off by one character. The tail past the break is what makes
		// that a different TOTAL and not just a different phase, so this row can fail.
		{"a\\nbcd", false, 2, "a line break neither sounds nor ticks (:4479 diverts it)"},
		// Same break with blank_blip ON: AO2 tests formatting_char BEFORE blank_blip, so
		// the setting cannot resurrect it. ticker 0 'a' blip(1) → 1 '\n' skipped → 1 'b'
		// tick → 2 'c' blip(2) → 3 'd' tick.
		{"a\\nbcd", true, 2, "blank_blip does not reach a formatting_char (:4544)"},
	} {
		tw := NewTypewriter()
		tw.Interval = crawl40
		tw.BlipOnSpaces = tc.spaces
		tw.Start(tc.text)
		_, blips, _, worstBlips := stepTypewriter(&tw, crawl40, 4*len(tc.text)+8)
		if !tw.Done() {
			t.Fatalf("fixture %q: the message should have finished", tc.text)
		}
		if blips != tc.want {
			t.Errorf("%q blank_blip=%v: blips = %d, want %d — %s",
				tc.text, tc.spaces, blips, tc.want, tc.tracing)
		}
		if worstBlips > ao2BlipsPerDelivery {
			t.Errorf("%q: worst call sounded %d blips", tc.text, worstBlips)
		}
	}
}

// TestTheBlipBudgetDoesNotShiftTheRhythm is the encapsulation half of blipsPerUpdate: a
// blip dropped for being simultaneous with another must not move the ones after it. The
// ticker is AO2's phase, so it advances on the DUE tick whether or not the sound was
// budgeted — drive the same message one-rune-at-a-time and in coarse batches and the
// blips must land on the same characters.
func TestTheBlipBudgetDoesNotShiftTheRhythm(t *testing.T) {
	const text = "the quick brown fox jumps over the lazy dog"

	// Where do the blips fall when every delivery carries exactly one rune (the budget
	// can never bind)? Record the visible-rune index of each.
	fine := NewTypewriter()
	fine.Interval = crawl40
	fine.Start(text)
	var atFine []int
	for !fine.Done() {
		if _, b := fine.Update(crawl40); b > 0 {
			atFine = append(atFine, fine.Visible())
		}
	}
	if len(atFine) == 0 {
		t.Fatal("fixture: no blips at all")
	}

	// Now a coarse caller (an 8 fps export step, 3+ runes per delivery). It must sound
	// FEWER blips — one per delivery — but every one it does sound must be a blip the
	// fine run also had: a dropped sound may not re-phase the rest of the line.
	coarse := NewTypewriter()
	coarse.Interval = crawl40
	coarse.Start(text)
	fineSet := map[int]bool{}
	for _, i := range atFine {
		fineSet[i] = true
	}
	total := 0
	for !coarse.Done() {
		before := coarse.Visible()
		_, b := coarse.Update(time.Second / 8)
		if b > ao2BlipsPerDelivery {
			t.Fatalf("a coarse delivery sounded %d blips", b)
		}
		total += b
		if b == 0 {
			continue
		}
		// The sounded blip belongs to one of the runes revealed by this call.
		hit := false
		for i := before + 1; i <= coarse.Visible(); i++ {
			if fineSet[i] {
				hit = true
				break
			}
		}
		if !hit {
			t.Errorf("a blip sounded over runes %d..%d, which the un-budgeted run never blips on — "+
				"the budget re-phased the rhythm", before+1, coarse.Visible())
		}
	}
	if total == 0 {
		t.Error("the coarse caller went completely silent")
	}
	if total > len(atFine) {
		t.Errorf("the coarse caller sounded %d blips, more than the %d AO2 counts for this line",
			total, len(atFine))
	}
}
