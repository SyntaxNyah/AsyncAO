package protocol

import (
	"strconv"
	"strings"
	"testing"
)

// allFeatures advertises everything a 2.9+ server would.
func allFeatures() FeatureSet {
	return ParseFeatures([]string{
		FeatureNoEncryption, FeatureYellowText, FeaturePrezoom, FeatureFlipping,
		FeatureCustomObjections, FeatureFastLoading, FeatureDeskMod, FeatureEvidence,
		FeatureCCCCIC, FeatureARUP, FeatureCasingAlerts, FeatureModcallReason,
		FeatureLoopingSFX, FeatureAdditive, FeatureEffects, FeatureYOffset,
		FeatureExpandedDeskMods, FeatureCustomBlips,
	})
}

// --- packet framing ------------------------------------------------------------

func TestPacketRoundTripWithEscaping(t *testing.T) {
	original := NewPacket("MS", "1", "50% off #1 & more $$", "wit")
	wire := original.String()

	if !strings.HasSuffix(wire, PacketTerminator) {
		t.Fatalf("wire %q missing terminator", wire)
	}
	for _, escaped := range []string{"<percent>", "<num>", "<and>", "<dollar>"} {
		if !strings.Contains(wire, escaped) {
			t.Errorf("wire %q missing escape %s", wire, escaped)
		}
	}

	parsed, err := ParsePacket(wire)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Header != "MS" || parsed.Field(1) != "50% off #1 & more $$" || parsed.Field(2) != "wit" {
		t.Errorf("round trip lost data: %+v", parsed)
	}
}

func TestParsePacketRejectsUnterminated(t *testing.T) {
	if _, err := ParsePacket("MS#1#2"); err == nil {
		t.Error("unterminated packet accepted")
	}
}

func TestPacketFieldOutOfRangeIsEmpty(t *testing.T) {
	p := NewPacket("ID", "1")
	if p.Field(5) != "" || p.Field(-1) != "" {
		t.Error("out-of-range Field must read as empty")
	}
}

func TestParseFeaturesCaseInsensitive(t *testing.T) {
	fs := ParseFeatures([]string{"CCCC_IC_Support", " y_offset "})
	if !fs.Has(FeatureCCCCIC) || !fs.Has("Y_OFFSET") {
		t.Error("feature matching must be case-insensitive and trimmed")
	}
}

// --- MS fixtures -----------------------------------------------------------------

// fixture28Paired is a full 2.8/2.9 paired message: custom objection,
// pair order ^1 (speaker behind), x&y offsets, flip.
var fixture28Paired = []string{
	"1",                                   // DESK_MOD
	"-",                                   // PRE_EMOTE
	"Phoenix",                             // CHAR_NAME
	"thinking",                            // EMOTE
	"...Objection!",                       // MESSAGE
	"def",                                 // SIDE
	"1",                                   // SFX_NAME
	"0",                                   // EMOTE_MOD
	"2",                                   // CHAR_ID
	"0",                                   // SFX_DELAY
	"4&Gotcha",                            // OBJECTION_MOD (custom shout)
	"0",                                   // EVIDENCE_ID
	"1",                                   // FLIP
	"0",                                   // REALIZATION
	"3",                                   // TEXT_COLOR
	"Nick",                                // SHOWNAME
	"4^1",                                 // OTHER_CHARID ^order: speaker behind
	"Edgeworth",                           // OTHER_NAME
	"pointing",                            // OTHER_EMOTE
	"5&-10",                               // SELF_OFFSET x&y
	"12&8",                                // OTHER_OFFSET x&y
	"1",                                   // OTHER_FLIP
	"1",                                   // IMMEDIATE
	"0",                                   // LOOPING_SFX
	"1",                                   // SCREENSHAKE
	"",                                    // FRAME_SCREENSHAKE
	"",                                    // FRAME_REALIZATION
	"",                                    // FRAME_SFX
	"0",                                   // ADDITIVE
	"realization|default|sfx-realization", // EFFECTS
	"male",                                // BLIPNAME
	"0",                                   // SLIDE
}

// fixture26Paired is a 2.6-era paired message: no ^order, x-only offsets,
// 23 fields (through IMMEDIATE).
var fixture26Paired = []string{
	"0", "pre_anim", "Maya", "normal", "Nick!", "wit", "0", "1", "5", "120",
	"0", "0", "0", "1", "0",
	"",  // SHOWNAME (empty: use char name)
	"2", // OTHER_CHARID — no order suffix
	"Phoenix",
	"normal",
	"-15", // SELF_OFFSET x only
	"20",  // OTHER_OFFSET x only
	"0",   // OTHER_FLIP
	"0",   // IMMEDIATE
}

// fixtureVanilla15 is a pre-2.6 message: exactly MSMinimum fields.
var fixtureVanilla15 = []string{
	"chat", "-", "Gumshoe", "normal", "Hey pal!", "pro", "1", "0", "7", "0",
	"2", "0", "0", "0", "0",
}

func TestParseMS28PairedFixture(t *testing.T) {
	msg, err := ParseMS(fixture28Paired, allFeatures(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if msg.CharID != 2 || msg.CharName != "Phoenix" || msg.Emote != "thinking" {
		t.Errorf("speaker fields wrong: %+v", msg)
	}
	if msg.Objection != ShoutCustom || msg.CustomShout != "Gotcha" {
		t.Errorf("custom objection = %d %q", msg.Objection, msg.CustomShout)
	}
	if !msg.Flip || !msg.Immediate || !msg.Screenshake {
		t.Error("flag fields lost")
	}
	if msg.Showname != "Nick" || msg.TextColor != 3 {
		t.Errorf("showname/color = %q/%d", msg.Showname, msg.TextColor)
	}
	if msg.SelfOffsetX != 5 || msg.SelfOffsetY != -10 {
		t.Errorf("self offset = %d,%d want 5,-10", msg.SelfOffsetX, msg.SelfOffsetY)
	}
	if msg.Blipname != "male" {
		t.Errorf("blipname = %q", msg.Blipname)
	}

	p := msg.Pair
	if !p.Active() {
		t.Fatal("pair inactive")
	}
	if p.CharID != 4 || p.Name != "Edgeworth" || p.Emote != "pointing" {
		t.Errorf("pair = %+v", p)
	}
	if p.OffsetX != 12 || p.OffsetY != 8 || !p.Flip {
		t.Errorf("pair offsets/flip = %d,%d,%v", p.OffsetX, p.OffsetY, p.Flip)
	}
	// Golden z-order: ^1 means the SPEAKER renders behind (AO2-Client
	// display_pair_character case 1).
	if p.SpeakerInFront() {
		t.Error("^1 must put the speaker behind the pair")
	}
}

func TestParseMS26PairedFixture(t *testing.T) {
	msg, err := ParseMS(fixture26Paired, ParseFeatures([]string{FeatureCCCCIC}), 10)
	if err != nil {
		t.Fatal(err)
	}
	p := msg.Pair
	if !p.Active() || p.CharID != 2 || p.Name != "Phoenix" {
		t.Fatalf("2.6 pair = %+v", p)
	}
	if p.HasOrder {
		t.Error("2.6 packet has no ^order")
	}
	// Golden z-order: no explicit order → speaker in front.
	if !p.SpeakerInFront() {
		t.Error("default z-order must put the speaker in front")
	}
	if msg.SelfOffsetX != -15 || msg.SelfOffsetY != 0 {
		t.Errorf("x-only self offset = %d,%d want -15,0", msg.SelfOffsetX, msg.SelfOffsetY)
	}
	if p.OffsetX != 20 || p.OffsetY != 0 {
		t.Errorf("x-only pair offset = %d,%d want 20,0", p.OffsetX, p.OffsetY)
	}
	if p.Flip {
		t.Error("pair flip must be off")
	}
	if msg.EmoteMod != EmoteModPreanim {
		t.Errorf("emote mod = %d, want preanim", msg.EmoteMod)
	}
}

func TestParseMSVanilla15(t *testing.T) {
	msg, err := ParseMS(fixtureVanilla15, ParseFeatures(nil), 10)
	if err != nil {
		t.Fatal(err)
	}
	// Legacy "chat" desk mod reads as 0, like QString::toInt.
	if msg.DeskMod != DeskHide {
		t.Errorf("desk mod = %d, want 0", msg.DeskMod)
	}
	if msg.Pair.Active() {
		t.Error("vanilla message claims a pair")
	}
	if msg.Objection != ShoutObjection {
		t.Errorf("objection = %d, want %d", msg.Objection, ShoutObjection)
	}
}

func TestParseMSGatesExtendedFieldsWithoutCCCC(t *testing.T) {
	// A server without cccc_ic_support sending 2.6 fields is "japing us"
	// (AO2-Client comment) — extended fields must read as empty.
	msg, err := ParseMS(fixture28Paired, ParseFeatures([]string{FeatureLoopingSFX}), 10)
	if err != nil {
		t.Fatal(err)
	}
	if msg.Showname != "" {
		t.Errorf("showname = %q, want gated to empty", msg.Showname)
	}
	if msg.Pair.Active() {
		t.Error("pair must be gated off without cccc_ic_support")
	}
}

func TestParseMSRejectsShortAndInvalid(t *testing.T) {
	if _, err := ParseMS(fixtureVanilla15[:14], allFeatures(), 0); err == nil {
		t.Error("short packet accepted")
	}
	bad := append([]string{}, fixtureVanilla15...)
	bad[MSCharID] = "999"
	if _, err := ParseMS(bad, allFeatures(), 10); err == nil {
		t.Error("out-of-range char id accepted")
	}
	bad[MSCharID] = "-2"
	if _, err := ParseMS(bad, allFeatures(), 10); err == nil {
		t.Error("char id below -1 accepted")
	}
}

func TestEmoteModNormalization(t *testing.T) {
	cases := map[int]int{
		0: EmoteModIdle, 1: EmoteModPreanim, 5: EmoteModZoom, 6: EmoteModPreanimZoom,
		2: EmoteModPreanim, 4: EmoteModPreanimZoom, 3: EmoteModIdle, 99: EmoteModIdle,
	}
	for in, want := range cases {
		if got := normalizeEmoteMod(in); got != want {
			t.Errorf("normalizeEmoteMod(%d) = %d, want %d", in, got, want)
		}
	}
}

// --- pairing golden table ---------------------------------------------------------

func TestParsePairIDGolden(t *testing.T) {
	cases := []struct {
		raw      string
		id       int
		front    bool
		hasOrder bool
	}{
		{"4^0", 4, true, true},  // explicit: speaker in front
		{"4^1", 4, false, true}, // explicit: speaker behind
		{"4", 4, true, false},   // 2.6: default front
		{"-1", -1, true, false}, // unpaired
		{"", -1, true, false},   // absent
		{"junk", -1, true, false},
		{"7^junk", 7, true, true}, // bad order falls back to front
	}
	for _, tc := range cases {
		id, order, hasOrder := ParsePairID(tc.raw)
		front := !(hasOrder && order == PairSpeakerBehind)
		if id != tc.id || front != tc.front || hasOrder != tc.hasOrder {
			t.Errorf("ParsePairID(%q) = id %d front %v hasOrder %v; want %d %v %v",
				tc.raw, id, front, hasOrder, tc.id, tc.front, tc.hasOrder)
		}
	}
}

func TestPairActiveRequiresNameAndID(t *testing.T) {
	if (PairInfo{CharID: 3}).Active() {
		t.Error("pair with empty name must be inactive (AO2-Client is_pairing)")
	}
	if (PairInfo{CharID: -1, Name: "Edgeworth"}).Active() {
		t.Error("pair with charid -1 must be inactive")
	}
	if !(PairInfo{CharID: 0, Name: "Edgeworth"}).Active() {
		t.Error("charid 0 with a name is a valid pair")
	}
}

func TestParseOffsetForms(t *testing.T) {
	cases := map[string][2]int{
		"":       {0, 0},
		"25":     {25, 0},
		"-100":   {-100, 0},
		"5&-10":  {5, -10},
		"0&33":   {0, 33},
		"junk":   {0, 0},
		"7&junk": {7, 0},
	}
	for raw, want := range cases {
		x, y := ParseOffset(raw)
		if x != want[0] || y != want[1] {
			t.Errorf("ParseOffset(%q) = %d,%d want %d,%d", raw, x, y, want[0], want[1])
		}
	}
}

// --- outgoing MS -------------------------------------------------------------------

func baseOutgoing() OutgoingMS {
	return OutgoingMS{
		DeskMod: 1, PreEmote: "-", CharName: "Phoenix", Emote: "normal",
		Message: "Hold it!", Side: "def", SFXName: "1", EmoteMod: EmoteModIdle,
		CharID: 2, Objection: ShoutHoldIt, TextColor: 0,
		Showname: "Nick", PairWith: 4, PairOrder: PairSpeakerBehind,
		OffsetX: 10, OffsetY: -5, Immediate: true,
		Blipname: "male",
	}
}

// Outgoing MS is ASYMMETRIC to incoming: the client never sends other_name/
// other_emote/other_offset/other_flip — the server injects the partner's
// data when relaying (AO2-Client on_chat_return_pressed appends exactly
// showname, other_charid, offset, immediate for the CCCC block). Outgoing
// indices therefore differ from the incoming CHAT_MESSAGE enum.
const (
	outShowname  = 15
	outPairID    = 16
	outOffset    = 17
	outImmediate = 18

	// Full-feature outgoing length: 15 base + 4 CCCC + 5 looping_sfx
	// + 1 additive + 1 effects + 2 custom_blips.
	outFullFeatureLen = 28
)

func TestOutgoingMSFullFeatures(t *testing.T) {
	fields := baseOutgoing().Fields(allFeatures())
	if len(fields) != outFullFeatureLen {
		t.Fatalf("field count = %d, want %d", len(fields), outFullFeatureLen)
	}
	if fields[outPairID] != "4^1" {
		t.Errorf("pair field = %q, want 4^1", fields[outPairID])
	}
	if fields[outOffset] != "10&-5" {
		t.Errorf("offset field = %q, want 10&-5 (y_offset server)", fields[outOffset])
	}
	if fields[outShowname] != "Nick" || fields[outImmediate] != "1" {
		t.Error("CCCC fields wrong")
	}
	if fields[len(fields)-2] != "male" || fields[len(fields)-1] != "0" {
		t.Error("custom_blips tail fields wrong")
	}
}

func TestOutgoingMSVanillaServerGetsBareMinimum(t *testing.T) {
	fields := baseOutgoing().Fields(ParseFeatures(nil))
	if len(fields) != MSMinimum {
		t.Fatalf("field count = %d, want %d (no features → pre-2.6 shape)", len(fields), MSMinimum)
	}
}

func TestOutgoingMSWithoutEffectsOmitsPairOrder(t *testing.T) {
	feats := ParseFeatures([]string{FeatureCCCCIC}) // no effects, no y_offset
	fields := baseOutgoing().Fields(feats)
	if got := fields[outPairID]; got != "4" {
		t.Errorf("pair field = %q, want bare 4 (no effects feature)", got)
	}
	if got := fields[outOffset]; got != "10" {
		t.Errorf("offset field = %q, want x-only 10 (no y_offset feature)", got)
	}
	if want := outImmediate + 1; len(fields) != want {
		t.Errorf("field count = %d, want %d (through immediate)", len(fields), want)
	}
}

// TestOutgoingMSKFOCompat pins the KFO-Server fix: its MS validator rejects EMPTY
// strings for the STR-typed frame/effect fields (where AO2-Client always sends a
// non-empty value), so with KFOCompat the empty frame fields become AO2's frame
// template and an empty effect becomes "||" — while a NON-KFO server's wire is
// byte-identical (the empties stay).
func TestOutgoingMSKFOCompat(t *testing.T) {
	feats := ParseFeatures([]string{FeatureCCCCIC, FeatureLoopingSFX, FeatureEffects})
	msg := OutgoingMS{PreEmote: "-", Emote: "normal"} // FrameShake/Realize/SFX/Effects all empty

	normal := msg.Fields(feats)
	kfo := msg
	kfo.KFOCompat = true
	kfoFields := kfo.Fields(feats)

	if len(normal) != len(kfoFields) {
		t.Fatalf("KFO must not change the field count: normal=%d kfo=%d", len(normal), len(kfoFields))
	}
	const tmpl = "-^(b)normal^(a)normal^"
	nTmpl, hasEff := 0, false
	for _, f := range kfoFields {
		if f == tmpl {
			nTmpl++
		}
		if f == "||" {
			hasEff = true
		}
	}
	if nTmpl != 3 {
		t.Errorf("KFO: want 3 frame fields = %q, got %d (fields=%v)", tmpl, nTmpl, kfoFields)
	}
	if !hasEff {
		t.Errorf("KFO: an empty effect must become \"||\", fields=%v", kfoFields)
	}
	for _, f := range normal {
		if f == tmpl || f == "||" {
			t.Errorf("a non-KFO server's wire must be unchanged, found %q in %v", f, normal)
		}
	}
}

// TestOutgoingMSDefaultPairOrderStaysBare pins the LemmyAO-compat fix: a paired
// message with the DEFAULT order (speaker in front) emits a BARE id even on a
// full-feature/effects server — "4", not "4^0" — while the non-default "behind"
// order still carries the "^1" reorder suffix. "4" and "4^0" render identically
// on AO2-Client/webAO (missing "^" = front), but strict parsers accept only the
// bare integer (LemmyAO does Number("4^0") → NaN and drops the whole message).
func TestOutgoingMSDefaultPairOrderStaysBare(t *testing.T) {
	out := baseOutgoing()
	out.PairOrder = PairSpeakerInFront // the common/default pairing
	if got := out.Fields(allFeatures())[outPairID]; got != "4" {
		t.Errorf("front-order pair field = %q, want bare 4 (a ^0 suffix makes LemmyAO drop the message)", got)
	}
	out.PairOrder = PairSpeakerBehind // a real reorder still needs the suffix
	if got := out.Fields(allFeatures())[outPairID]; got != "4^1" {
		t.Errorf("behind-order pair field = %q, want 4^1 (reorder must survive)", got)
	}
}

func TestOutgoingMSUnpaired(t *testing.T) {
	o := baseOutgoing()
	o.PairWith = UnpairedCharID
	fields := o.Fields(allFeatures())
	if fields[outPairID] != "-1" {
		t.Errorf("unpaired field = %q, want -1", fields[outPairID])
	}
}

// TestOutgoingDeskModClamp pins #16: the expanded desk mods (2–5) collapse to the
// legacy hide/show values (0/1) when the server lacks expanded_desk_mods, and ride
// raw when it advertises the feature — AO2-Client's on_chat_return_pressed clamp
// (courtroom.cpp:2021-2031). A strict MS validator rejects an unknown 2–5 otherwise.
func TestOutgoingDeskModClamp(t *testing.T) {
	noExpanded := ParseFeatures([]string{FeatureCCCCIC}) // deliberately without expanded_desk_mods
	// Clamp table: 0→0, 1→1, 2→1 (SHOW), 3→0 (HIDE), 4→1 (SHOW), 5→0 (HIDE).
	clampCases := []struct{ in, want int }{
		{DeskHide, DeskHide},
		{DeskShow, DeskShow},
		{DeskEmoteOnly, DeskShow},
		{DeskPreOnly, DeskHide},
		{DeskEmoteOnlyEx, DeskShow},
		{DeskPreOnlyEx, DeskHide},
	}
	for _, tc := range clampCases {
		o := baseOutgoing()
		o.DeskMod = tc.in
		if got := o.Fields(noExpanded)[MSDeskMod]; got != strconv.Itoa(tc.want) {
			t.Errorf("no-feature clamp: DeskMod %d → %q, want %d", tc.in, got, tc.want)
		}
		// With the feature advertised the value rides raw.
		if got := o.Fields(allFeatures())[MSDeskMod]; got != strconv.Itoa(tc.in) {
			t.Errorf("feature-on: DeskMod %d shipped as %q, want it raw", tc.in, got)
		}
	}
}

// TestOutgoingWireEscapingSurvives checks the base-15 portion (which IS
// symmetric) round-trips through the wire with hostile characters. The
// extended portion is server-transformed in real AO and not round-trippable.
func TestOutgoingWireEscapingSurvives(t *testing.T) {
	noFeatures := ParseFeatures(nil)
	o := baseOutgoing()
	o.Message = "Special chars: #%&$ 100%"
	wire := o.Packet(noFeatures).String()

	parsed, err := ParsePacket(wire)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Header != "MS" {
		t.Fatalf("header = %q", parsed.Header)
	}
	msg, err := ParseMS(parsed.Fields, noFeatures, 0)
	if err != nil {
		t.Fatal(err)
	}
	if msg.Message != o.Message {
		t.Errorf("message round trip = %q, want %q", msg.Message, o.Message)
	}
	if msg.Objection != ShoutHoldIt {
		t.Errorf("objection round trip = %d", msg.Objection)
	}
}

// TestNormalizeOutgoingEmoteMod pins AO2-Client's on_chat_return_pressed
// overrides (courtroom.cpp "EMOTE MOD OVERRIDES"): legacy ini values
// 2/3/4 never reach the wire, and — when the "Pre" toggle wants it — a
// preanim upgrades idle/zoom. Raw legacy values made schema-strict
// receivers (LemmyAO) drop our messages entirely. The preanim=false rows
// pin the new "Pre unchecked" branch (courtroom.cpp:2080-2092): a preanim
// mod is forced back to idle/zoom so no intro plays.
func TestNormalizeOutgoingEmoteMod(t *testing.T) {
	full := ParseFeatures([]string{"prezoom"})
	none := ParseFeatures(nil)
	cases := []struct {
		mod        int
		hasPreanim bool
		preanim    bool // the "Pre" toggle
		features   FeatureSet
		want       int
	}{
		// Pre ON (checked): historical behavior — the emote's preanim rides.
		{2, false, true, none, EmoteModPreanim}, // objection-internal → preanim
		{3, false, true, none, EmoteModIdle},    // meaningless → idle
		{4, false, true, none, EmoteModZoom},    // legacy zoom alias
		{0, true, true, none, EmoteModPreanim},  // preanim upgrades idle
		{5, true, true, full, EmoteModPreanimZoom},
		{5, true, true, none, EmoteModZoom}, // prezoom feature gates the upgrade
		{0, false, true, none, EmoteModIdle},
		{1, false, true, none, EmoteModPreanim},
		{6, false, true, none, EmoteModPreanimZoom},
		// Pre OFF (unchecked): suppress the intro — a preanim mod downgrades.
		{1, true, false, none, EmoteModIdle}, // preanim → idle (intro suppressed)
		{6, true, false, full, EmoteModZoom}, // preanim-zoom → zoom
		{0, true, false, none, EmoteModIdle}, // idle stays idle, no upgrade
		{5, true, false, full, EmoteModZoom}, // zoom stays zoom, no upgrade
		{2, true, false, none, EmoteModIdle}, // legacy 2→preanim, then downgraded to idle
	}
	for _, tc := range cases {
		if got := NormalizeOutgoingEmoteMod(tc.mod, tc.hasPreanim, tc.preanim, false, tc.features); got != tc.want {
			t.Errorf("NormalizeOutgoingEmoteMod(%d, hasPre=%v, pre=%v) = %d, want %d", tc.mod, tc.hasPreanim, tc.preanim, got, tc.want)
		}
	}
}
