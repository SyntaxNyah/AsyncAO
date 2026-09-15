package courtroom

import "testing"

// TestParseAOLoopSidecarMatchesCanonLineOrder pins the SINGLE-FORWARD-PASS,
// order-dependent contract at the heart of the parser (aomusicplayer.cpp:71-123):
// a `loop_start` line that appears BEFORE a later `seconds=true` line was authored
// as SAMPLES, and must stay samples. A two-pass "scan for seconds first" parser
// would silently reinterpret this exact file as 44100 SECONDS instead of the 1
// second it actually is — the fatal-flaw regression this test exists to catch.
func TestParseAOLoopSidecarMatchesCanonLineOrder(t *testing.T) {
	data := []byte("loop_start=44100\nseconds=true\n")
	pts, ok := ParseAOLoopSidecar(data, 44100)
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if pts.StartSec != 1.0 {
		t.Errorf("StartSec = %v, want 1.0 (44100 SAMPLES at 44100Hz, not 44100 seconds)", pts.StartSec)
	}
}

// TestParseAOLoopSidecarLoopLengthIsOrderDependent pins loop_length's own
// order-dependent quirk (aomusicplayer.cpp:115-118): it adds to whatever
// loop_start holds AT THAT LINE, not to loop_start's final value. Here
// loop_length appears BEFORE loop_start, so it must resolve against loop_start's
// default of 0 — never against the 50 that loop_start is set to two lines later.
func TestParseAOLoopSidecarLoopLengthIsOrderDependent(t *testing.T) {
	data := []byte("loop_length=100\nloop_start=50\n")
	pts, ok := ParseAOLoopSidecar(data, 1) // sampleRate=1: samples convert 1:1 to seconds
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if pts.StartSec != 50 {
		t.Errorf("StartSec = %v, want 50", pts.StartSec)
	}
	if !pts.HaveEnd || pts.EndSec != 100 {
		t.Errorf("EndSec = %v (HaveEnd=%v), want 100 — loop_length must have used loop_start's "+
			"value AT ITS OWN LINE (0), not the 50 set afterwards (which would wrongly give 150)",
			pts.EndSec, pts.HaveEnd)
	}
}

// TestSecFormsWinOutrightOverLegacyKeysForTheSameBound pins binding decision D12:
// loop_start_sec, when present, overwrites the legacy loop_start OUTRIGHT — even
// a wildly different legacy sample count must not survive.
func TestSecFormsWinOutrightOverLegacyKeysForTheSameBound(t *testing.T) {
	data := []byte("loop_start=999999\nloop_start_sec=1.5\n")
	pts, ok := ParseAOLoopSidecar(data, 44100)
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if pts.StartSec != 1.5 {
		t.Errorf("StartSec = %v, want 1.5 (the _sec key must win outright)", pts.StartSec)
	}
}

// TestBareLoopStartIsNeverMistakenForSeconds pins the exact precedence rule §3
// states: a bare, unsuffixed legacy value is ALWAYS sample-or-seconds per the
// running seconds=true mode — never seconds by virtue of looking like a small
// number. Here loop_start=1 with NO seconds=true and NO _sec key must be 1 SAMPLE
// at 44100Hz (a tiny fraction of a second), never 1.0 second outright.
func TestBareLoopStartIsNeverMistakenForSeconds(t *testing.T) {
	data := []byte("loop_start=1\n")
	pts, ok := ParseAOLoopSidecar(data, 44100)
	if !ok {
		t.Fatal("ok = false, want true")
	}
	want := 1.0 / 44100.0
	if pts.StartSec != want {
		t.Errorf("StartSec = %v, want %v (1 SAMPLE, not 1 second)", pts.StartSec, want)
	}
}

// TestBareFractionStaysSamplesNeverAPercentageOfDuration pins binding decision
// D13's DECLINED form: "loop_start = 0.25" is samples-grammar, never "25% of the
// track". This parser has no duration/percentage concept anywhere in it, so a
// fractional value fails AO2's own toUInt() (aomusicplayer.cpp:109, a WHOLE-string
// unsigned-integer parse) and degrades to 0 samples — never 0.25 seconds, and
// never any duration-scaled value.
func TestBareFractionStaysSamplesNeverAPercentageOfDuration(t *testing.T) {
	data := []byte("loop_start=0.25\n")
	pts, ok := ParseAOLoopSidecar(data, 44100)
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if pts.StartSec != 0 {
		t.Errorf("StartSec = %v, want 0 (a fractional SAMPLE count is invalid input, "+
			"not 25%% of a duration this parser doesn't even know)", pts.StartSec)
	}
}

// TestSampleModeNeverInheritsAO2sChannelMultiplier pins recon 4/decision 14
// directly: AO2's own conversion is `bytes = value * sampleSize(2) * numChannels(2)`
// (aomusicplayer.cpp:96-109), a hardcoded-stereo assumption that is silently wrong
// for a mono source. ParseAOLoopSidecar's signature carries no channel-count
// parameter AT ALL — the conversion is exactly `value / sampleRate`, so the
// question "what if this were a mono file" cannot even be asked of it. This test
// pins the arithmetic outright: an erroneous latent *2/x2 factor would land on
// 1.0 or 4.0 here, never the correct 2.0.
func TestSampleModeNeverInheritsAO2sChannelMultiplier(t *testing.T) {
	data := []byte("loop_start=88200\n")
	pts, ok := ParseAOLoopSidecar(data, 44100)
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if pts.StartSec != 2.0 {
		t.Errorf("StartSec = %v, want exactly 2.0 (88200/44100, no channel factor) — a value of "+
			"1.0 or 4.0 would mean a stereo/mono multiplier crept back in", pts.StartSec)
	}
}

// TestLegacyKeysAreCaseSensitiveConvenienceKeysAreNot pins the asymmetry the
// assignment calls out explicitly: AO2's own literal keys are matched
// case-sensitively (a differently-cased line is invisible to the legacy grammar,
// exactly like AO2's QString::operator==), while AsyncAO's own *_sec keys fold
// case because no real file can already depend on their exact spelling.
func TestLegacyKeysAreCaseSensitiveConvenienceKeysAreNot(t *testing.T) {
	data := []byte("Loop_Start=100\nloop_start_SEC=2.5\n")
	pts, ok := ParseAOLoopSidecar(data, 1)
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if pts.StartSec != 2.5 {
		t.Errorf("StartSec = %v, want 2.5 — the wrongly-cased legacy key must be invisible "+
			"(if it weren't, and it also lost to the _sec key, this would still read 2.5, so "+
			"the real regression this catches is StartSec==100/1==100, meaning the miscased "+
			"legacy line was read anyway)", pts.StartSec)
	}
}

// TestExplicitSecondsUnitSuffixOnALegacyValue pins binding decision D13's ACCEPTED
// form: a trailing "s"/"sec" (case-insensitive) on a LEGACY key's value means "this
// number is already seconds", bypassing sample-rate math entirely regardless of
// the running seconds=true mode. A sample-rate of 44100 is deliberately supplied so
// a wrongly-still-sample-mode read would produce a tiny fraction instead of the
// intended 1.5/2.5 seconds.
func TestExplicitSecondsUnitSuffixOnALegacyValue(t *testing.T) {
	data := []byte("loop_start=1.5s\nloop_end=2.5SEC\n")
	pts, ok := ParseAOLoopSidecar(data, 44100)
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if pts.StartSec != 1.5 {
		t.Errorf("StartSec = %v, want 1.5 (the 's' suffix must mean seconds outright)", pts.StartSec)
	}
	if !pts.HaveEnd || pts.EndSec != 2.5 {
		t.Errorf("EndSec = %v (HaveEnd=%v), want 2.5 — the 'SEC' suffix is case-insensitive",
			pts.EndSec, pts.HaveEnd)
	}
}

// TestParseAOLoopSidecarLineTokenizing pins the raw line-splitting contract in one
// fixture: a leading blank line, a comment-shaped line with no '=' at all (which
// needs no dedicated comment syntax — it is simply unparseable, like AO2's own
// bare lines), CRLF endings (the trailing '\r' must be trimmed away, never leak
// into a value), and a line with SEVERAL '=' signs (AO2's own QString::split("=")
// keeps only args[1] — "1" here — and silently drops anything past the second
// '=', matching aomusicplayer.cpp:75-79 exactly).
func TestParseAOLoopSidecarLineTokenizing(t *testing.T) {
	data := []byte("\r\n; a bare comment with no equals sign\r\nloop_start=1=2\r\n\r\nloop_end=3\r\n")
	pts, ok := ParseAOLoopSidecar(data, 1)
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if pts.StartSec != 1 {
		t.Errorf("StartSec = %v, want 1 (only args[1] of \"1=2\" counts, matching AO2's own indexing)", pts.StartSec)
	}
	if !pts.HaveEnd || pts.EndSec != 3 {
		t.Errorf("EndSec = %v (HaveEnd=%v), want 3", pts.EndSec, pts.HaveEnd)
	}
}

// TestParseAOLoopSidecarGarbageAndExtremeValues pins AO2's own "no validation, no
// rejection" discipline (aomusicplayer.cpp:92-124: unchecked toUInt()/toDouble()):
// non-numeric, negative and enormous values never panic and never crash the scan —
// they degrade silently, exactly like the reference client.
func TestParseAOLoopSidecarGarbageAndExtremeValues(t *testing.T) {
	cases := []struct {
		name string
		data string
		rate int
		want float64
	}{
		{"non-numeric sample value", "loop_start=notanumber\n", 44100, 0},
		{"negative sample value (toUInt fails)", "loop_start=-100\n", 44100, 0},
		{"negative seconds value (toDouble allows it)", "seconds=true\nloop_start=-1.5\n", 44100, -1.5},
		{"overflows 64-bit (degrades to 0, never panics)", "loop_start=99999999999999999999\n", 1, 0},
		{"large but valid 64-bit sample count", "loop_start=1000000000000\n", 1, 1000000000000},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pts, ok := ParseAOLoopSidecar([]byte(c.data), c.rate)
			if !ok {
				t.Fatal("ok = false, want true")
			}
			if pts.StartSec != c.want {
				t.Errorf("StartSec = %v, want %v", pts.StartSec, c.want)
			}
		})
	}
}

// TestUnresolvedSampleRateDegradesSampleModeToUnset pins the sniffer-failure
// path (assets.SniffAudioSampleRate returning 0): a SAMPLE-mode bound must be
// left UNSET rather than divide by zero or fabricate a bogus 0-second bound,
// while a *_sec key (which never needs the rate at all) is unaffected.
func TestUnresolvedSampleRateDegradesSampleModeToUnset(t *testing.T) {
	pts, ok := ParseAOLoopSidecar([]byte("loop_start=100\nloop_end=200\n"), 0)
	if !ok {
		t.Fatal("ok = false, want true (the file itself parsed fine)")
	}
	if pts.HaveEnd {
		t.Errorf("HaveEnd = true, want false — loop_end is sample-mode and sampleRate is unusable")
	}
	if pts.StartSec != 0 {
		t.Errorf("StartSec = %v, want 0 (untouched default, not a fabricated conversion)", pts.StartSec)
	}

	pts2, ok := ParseAOLoopSidecar([]byte("loop_start=100\nloop_start_sec=5\n"), 0)
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if pts2.StartSec != 5 {
		t.Errorf("StartSec = %v, want 5 — the _sec key needs no sample rate at all", pts2.StartSec)
	}
}

// TestLoopLengthSecUsesTheFinalStartBound pins Part B's stated evaluation order:
// loop_length_sec is computed AFTER the start bound is fully resolved (legacy or
// _sec-overridden), using WHICHEVER value is final — never the legacy value that
// was overridden away.
func TestLoopLengthSecUsesTheFinalStartBound(t *testing.T) {
	data := []byte("loop_start=999\nloop_start_sec=10\nloop_length_sec=5\n")
	pts, ok := ParseAOLoopSidecar(data, 1)
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if pts.StartSec != 10 {
		t.Fatalf("StartSec = %v, want 10 (the _sec override)", pts.StartSec)
	}
	if !pts.HaveEnd || pts.EndSec != 15 {
		t.Errorf("EndSec = %v (HaveEnd=%v), want 15 (10+5) — using the overridden start, "+
			"not the legacy 999 (which would wrongly give 1004)", pts.EndSec, pts.HaveEnd)
	}
}

// TestLoopEndSecBeatsLoopLengthSec pins the stated precedence: loop_length_sec is
// only consulted when loop_end_sec is ABSENT. Both present here — loop_end_sec
// must win.
func TestLoopEndSecBeatsLoopLengthSec(t *testing.T) {
	data := []byte("loop_start=0\nloop_end_sec=7\nloop_length_sec=100\n")
	pts, ok := ParseAOLoopSidecar(data, 1)
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if !pts.HaveEnd || pts.EndSec != 7 {
		t.Errorf("EndSec = %v (HaveEnd=%v), want 7 (loop_end_sec must beat loop_length_sec)",
			pts.EndSec, pts.HaveEnd)
	}
}

// TestParseAOLoopSidecarAbsentEndIsHaveEndFalseNotOkFalse pins §3 step 4: an
// absent end is reported through HaveEnd == false, never through ok == false —
// the file itself was perfectly readable, it simply never defined an end.
func TestParseAOLoopSidecarAbsentEndIsHaveEndFalseNotOkFalse(t *testing.T) {
	pts, ok := ParseAOLoopSidecar([]byte("loop_start=1\n"), 1)
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if pts.HaveEnd {
		t.Errorf("HaveEnd = true, want false — no loop_end/loop_length/*_sec key was ever present")
	}
}

// TestParseAOLoopSidecarRejectsEmptyOrUnparseableData pins §3 step 4's other
// half: ok is false only when there is genuinely nothing to report — an empty
// payload, or a file with no '=' anywhere at all.
func TestParseAOLoopSidecarRejectsEmptyOrUnparseableData(t *testing.T) {
	if _, ok := ParseAOLoopSidecar(nil, 44100); ok {
		t.Error("nil data: ok = true, want false")
	}
	if _, ok := ParseAOLoopSidecar([]byte{}, 44100); ok {
		t.Error("empty data: ok = true, want false")
	}
	if _, ok := ParseAOLoopSidecar([]byte("just some prose\nwith no equals signs at all\n"), 44100); ok {
		t.Error("no '=' anywhere: ok = true, want false")
	}
}

// droManifestFixture is a small two-track DRO manifest reused across the DRO
// tests below (../DRO-Client/src/draudiotrackmetadata.cpp:57-90 shape): mixed key
// casing, a quoted and an unquoted section, matched by filename.
const droManifestFixture = `[0]
Filename = "Intro.opus"
Title = "Ignored On Purpose"
loop_start = 0
loop_end = 100

[1]
FILENAME=Boss Fight.opus
PLAY_ONCE=true
loop_start=200
loop_end=300
`

// TestParseDROLoopSidecarMatchesByFilenameCaseInsensitively pins DRO's own
// case-insensitive cache key (draudiotrackmetadata.cpp:82-89, l_lower_track_name)
// against SEVERAL groups: the requested trackRel is cased differently from the
// manifest's own `filename`/`Filename` key spelling in BOTH directions.
func TestParseDROLoopSidecarMatchesByFilenameCaseInsensitively(t *testing.T) {
	meta, ok := ParseDROLoopSidecar([]byte(droManifestFixture), "INTRO.OPUS", 100)
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if meta.Loop.StartSec != 0 || !meta.Loop.HaveEnd || meta.Loop.EndSec != 1.0 {
		t.Errorf("[0] loop = %+v, want start=0 end=1.0", meta.Loop)
	}

	meta2, ok := ParseDROLoopSidecar([]byte(droManifestFixture), "boss fight.opus", 100)
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if meta2.Loop.StartSec != 2.0 || !meta2.Loop.HaveEnd || meta2.Loop.EndSec != 3.0 {
		t.Errorf("[1] loop = %+v, want start=2.0 end=3.0", meta2.Loop)
	}
}

// TestParseDROLoopSidecarPlayOnce pins that play_once is BOTH parsed and reported
// truthfully in both directions — hard rule 11's own named failure mode is
// "parsed but never applied", and this test only pins the PARSE half; the render
// package is responsible for the apply half (suppressing the arm).
func TestParseDROLoopSidecarPlayOnce(t *testing.T) {
	meta, ok := ParseDROLoopSidecar([]byte(droManifestFixture), "Boss Fight.opus", 100)
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if !meta.PlayOnce {
		t.Error("PlayOnce = false, want true")
	}
	if !meta.Loop.HaveEnd {
		t.Error("HaveEnd = false, want true — the loop window is still reported even though " +
			"play_once is set; suppression is the render side's job, not the parser's")
	}

	meta2, ok := ParseDROLoopSidecar([]byte(droManifestFixture), "Intro.opus", 100)
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if meta2.PlayOnce {
		t.Error("PlayOnce = true, want false — section [0] never sets it")
	}
}

// TestParseDROLoopSidecarSecondsMode pins the AsyncAO extension that DRO itself
// lacks: a section with seconds=true reads loop_start/loop_end as float seconds
// instead of sample counts, so the sample rate is irrelevant to those values.
func TestParseDROLoopSidecarSecondsMode(t *testing.T) {
	data := []byte(`[0]
filename = "Track.opus"
seconds = true
loop_start = 1.5
loop_end = 9.75
`)
	meta, ok := ParseDROLoopSidecar(data, "Track.opus", 100)
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if meta.Loop.StartSec != 1.5 || !meta.Loop.HaveEnd || meta.Loop.EndSec != 9.75 {
		t.Errorf("loop = %+v, want start=1.5 end=9.75 haveEnd=true (seconds, not samples)", meta.Loop)
	}
}

// TestParseDROLoopSidecarQuotedAndUnquotedValuesAgree pins that QSettings-style
// quoting is transparent: the same logical manifest, written with and without
// surrounding double quotes, must parse identically.
func TestParseDROLoopSidecarQuotedAndUnquotedValuesAgree(t *testing.T) {
	quoted := []byte(`[0]
filename = "Track.opus"
play_once = "true"
loop_start = "441000"
loop_end = "882000"
`)
	unquoted := []byte(`[0]
filename = Track.opus
play_once = true
loop_start = 441000
loop_end = 882000
`)
	q, qOK := ParseDROLoopSidecar(quoted, "Track.opus", 441000)
	u, uOK := ParseDROLoopSidecar(unquoted, "Track.opus", 441000)
	if !qOK || !uOK {
		t.Fatalf("ok = (%v, %v), want (true, true)", qOK, uOK)
	}
	if q != u {
		t.Errorf("quoted %+v != unquoted %+v", q, u)
	}
	if q.Loop.StartSec != 1.0 || q.Loop.EndSec != 2.0 || !q.PlayOnce {
		t.Errorf("parsed = %+v, want start=1.0 end=2.0 play_once=true", q)
	}
}

// TestParseDROLoopSidecarNoMatchReturnsNotOK pins the miss path: a manifest that
// exists but names no matching track, and an entirely empty manifest, both
// report ok=false rather than a zero-value meta that could be mistaken for a
// genuine (if empty) match.
func TestParseDROLoopSidecarNoMatchReturnsNotOK(t *testing.T) {
	if _, ok := ParseDROLoopSidecar([]byte(droManifestFixture), "Nonexistent.opus", 100); ok {
		t.Error("unmatched filename: ok = true, want false")
	}
	if _, ok := ParseDROLoopSidecar([]byte{}, "Intro.opus", 100); ok {
		t.Error("empty manifest: ok = true, want false")
	}
}

// TestClampLoopPointsIndependentBounds pins binding decision D19 exactly: start
// and end are clamped INDEPENDENTLY, so a bad bound on one side never discards a
// good, author-intended bound on the other — the flaw one earlier design shipped
// by collapsing both to 0 together.
func TestClampLoopPointsIndependentBounds(t *testing.T) {
	cases := []struct {
		name            string
		start, end, dur float64
		wantS, wantE    float64
	}{
		{"bad end alone leaves a good start untouched", 5, 999, 100, 5, 100},
		{"bad start alone leaves a good end untouched", 150, 50, 100, 0, 50},
		{"both bad", 150, 999, 100, 0, 100},
		{"both fine, untouched", 10, 50, 100, 10, 50},
		{"end exactly at duration is fine, not reset", 0, 100, 100, 0, 100},
		{"start exactly at duration resets to 0", 100, 50, 100, 0, 50},
		{"end at or before start is reset to duration", 20, 20, 100, 20, 100},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotS, gotE := ClampLoopPoints(c.start, c.end, c.dur)
			if gotS != c.wantS || gotE != c.wantE {
				t.Errorf("ClampLoopPoints(%v, %v, %v) = (%v, %v), want (%v, %v)",
					c.start, c.end, c.dur, gotS, gotE, c.wantS, c.wantE)
			}
		})
	}
}

// TestDROLoopManifestURLMatchesTheIssuesOwnExample pins decision D26 against the
// issue's own worked example: a track's manifest lives at
// sounds/music/<folder>/<folder>.ini, named after the folder it ships in.
func TestDROLoopManifestURLMatchesTheIssuesOwnExample(t *testing.T) {
	u := NewURLBuilder("https://miku.pizza/base/")
	track := u.MusicURL("dro_castlevania/Belmont The Legend.opus")
	got, ok := DROLoopManifestURL(track)
	if !ok {
		t.Fatal("ok = false, want true")
	}
	want := "https://miku.pizza/base/sounds/music/dro_castlevania/dro_castlevania.ini"
	if got != want {
		t.Errorf("DROLoopManifestURL(%q) = %q, want %q", track, got, want)
	}
}

// TestDROLoopManifestURLRefusesATrackWithNoFolder pins that DRO's per-folder
// convention has nothing to approximate for a bare filename.
func TestDROLoopManifestURLRefusesATrackWithNoFolder(t *testing.T) {
	u := NewURLBuilder("https://miku.pizza/base/")
	track := u.MusicURL("Trial.opus")
	if _, ok := DROLoopManifestURL(track); ok {
		t.Errorf("DROLoopManifestURL(%q): ok = true, want false (no folder segment)", track)
	}
}

// TestDirectMusicURLNeverGetsEitherSidecarLookup pins Part D against the
// adversarial case a bare scheme check cannot solve: a direct http(s) URL whose
// PATH happens to contain "sounds/music/" (a same-convention mirror, or a CDN
// link shaped like one) and carries a trailing query string — exactly how a
// Discord /play link signs itself. IsDirectMusicURL alone cannot refuse this
// once it has been resolved (an origin-built URL is ALSO http(s)-prefixed), so
// MusicRelPath/DROLoopManifestURL fall back to the query-string/fragment signal
// (hasRawQueryOrFragment) that only a raw, unescaped direct link can carry.
func TestDirectMusicURLNeverGetsEitherSidecarLookup(t *testing.T) {
	const direct = "https://miku.pizza/base/sounds/music/Trial.opus?ex=1&is=2&hm=3&"
	if !IsDirectMusicURL(direct) {
		t.Fatal("fixture is not even recognized as a direct URL — the rest of this test proves nothing")
	}
	if rel, ok := MusicRelPath(direct); ok {
		t.Errorf("MusicRelPath(%q) = (%q, true), want ok=false", direct, rel)
	}
	if manifest, ok := DROLoopManifestURL(direct); ok {
		t.Errorf("DROLoopManifestURL(%q) = (%q, true), want ok=false", direct, manifest)
	}
}

// TestMusicRelPathAndDROLoopManifestURLAcceptALegitimateResolvedURL is the
// regression guard for the bug the previous test's fix could otherwise
// reintroduce in the other direction: a NORMAL, non-adversarial resolved
// server-relative track (no query string, no fragment) must still succeed. An
// over-eager "reject anything http(s)-prefixed" gate would break this — every
// resolved MusicURL is http(s)-prefixed, since the asset origin itself is.
func TestMusicRelPathAndDROLoopManifestURLAcceptALegitimateResolvedURL(t *testing.T) {
	u := NewURLBuilder("https://miku.pizza/base/")
	track := u.MusicURL("dro_castlevania/Belmont The Legend.opus")
	// track is http(s)-prefixed too (the origin is), which is exactly why
	// MusicRelPath/DROLoopManifestURL cannot use IsDirectMusicURL as their gate —
	// see hasRawQueryOrFragment's own doc comment.
	if _, ok := MusicRelPath(track); !ok {
		t.Fatalf("MusicRelPath(%q): ok = false, want true", track)
	}
	if _, ok := DROLoopManifestURL(track); !ok {
		t.Fatalf("DROLoopManifestURL(%q): ok = false, want true", track)
	}
}

// TestIsDirectMusicURLIsAThinWrapperNotASecondCopy is the encapsulation test for
// this package's one exported seam onto the direct-URL gate: it drives the REAL
// exported IsDirectMusicURL against the package's own internal isMusicURL over a
// table designed to catch drift at the edges (empty string, bare scheme, a
// near-miss prefix, mixed case) — not just the easy middle cases both a correct
// implementation AND a subtly-wrong reimplementation would agree on. A future
// edit that turns IsDirectMusicURL into its own hardcoded prefix check (instead
// of delegating) risks forgetting to keep every edge case in sync with
// isMusicURL — for example if a new scheme were added to one list and not the
// other — and THAT is what this test would catch; it cannot catch both being
// wrong in the same way, which is not this test's job (isMusicURL's own
// correctness is pinned by MusicURL's callers elsewhere in this package).
func TestIsDirectMusicURLIsAThinWrapperNotASecondCopy(t *testing.T) {
	cases := []string{
		"",
		"http://",
		"https://",
		"http://x",
		"HTTPS://X",
		"HtTp://mixed.example/song.opus",
		"http:/one-slash-is-not-a-scheme",
		"ahttp://not-actually-http",
		"ftp://not-a-supported-scheme",
		"songs/intro.opus",
		"999/songs/that/slap/[999] Tranquility.ogg",
		"https://cdn.example.com/x.OGG?q=1.5",
	}
	for _, track := range cases {
		if got, want := IsDirectMusicURL(track), isMusicURL(track); got != want {
			t.Errorf("IsDirectMusicURL(%q) = %v, isMusicURL(%q) = %v — the wrapper has drifted "+
				"from the function it is supposed to be a thin alias for", track, got, track, want)
		}
	}
}
