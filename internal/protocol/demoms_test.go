package protocol

import "testing"

// TestBuildServerMSRoundTrip pins the .demo export serializer: a maxed-out
// 2.8/2.9 message survives BuildServerMS → wire → ParsePacket → ParseMS
// byte-for-byte — including the tricky composite fields (custom shout "4&name",
// pair "id^order", "x&y" offsets) and characters the wire must escape.
func TestBuildServerMSRoundTrip(t *testing.T) {
	src := &ChatMessage{
		DeskMod:     DeskEmoteOnly,
		PreEmote:    "pre-flip",
		CharName:    "Franziska",
		Emote:       "whip",
		Message:     "Foolish fool! #escape% test & more$",
		Side:        "pro",
		SFXName:     "whip_crack",
		EmoteMod:    EmoteModPreanim,
		CharID:      7,
		SFXDelay:    120,
		Objection:   ShoutCustom,
		CustomShout: "Gotcha",
		EvidenceID:  3,
		Flip:        true,
		Realization: true,
		TextColor:   5,
		Showname:    "Franzy",
		Pair: PairInfo{
			CharID: 4, Order: PairSpeakerBehind, HasOrder: true,
			Name: "Edgeworth", Emote: "desk", OffsetX: -20, OffsetY: 10, Flip: true,
		},
		SelfOffsetX:  15,
		SelfOffsetY:  -5,
		Immediate:    true,
		LoopingSFX:   true,
		Screenshake:  true,
		FrameShake:   "pre-flip^3",
		FrameRealize: "whip^1",
		FrameSFX:     "whip^2&snap",
		Additive:     true,
		Effects:      "realization||sfx",
		Blipname:     "female",
		Slide:        true,
	}
	wire := BuildServerMS(src).String()
	pkt, err := ParsePacket(wire)
	if err != nil {
		t.Fatalf("ParsePacket(%q): %v", wire, err)
	}
	if pkt.Header != "MS" || len(pkt.Fields) != MSMaximum {
		t.Fatalf("header/fields = %q/%d, want MS/%d", pkt.Header, len(pkt.Fields), MSMaximum)
	}
	got, err := ParseMS(pkt.Fields, ParseFeatures([]string{FeatureCCCCIC}), 0)
	if err != nil {
		t.Fatal(err)
	}
	if *got != *src {
		t.Fatalf("round-trip mismatch:\n got %+v\nwant %+v", got, src)
	}
}

// TestBuildServerMSMinimal pins the zero-ish message: an unpaired, plain line
// keeps the legacy-compatible spellings (pair "-1" with no ^, shout "0").
func TestBuildServerMSMinimal(t *testing.T) {
	src := &ChatMessage{CharName: "Phoenix", Emote: "normal", Message: "hi", Side: "def", CharID: 0, Pair: PairInfo{CharID: UnpairedCharID}}
	pkt := BuildServerMS(src)
	if pkt.Fields[MSOtherCharID] != "-1" {
		t.Errorf("unpaired other id = %q, want -1", pkt.Fields[MSOtherCharID])
	}
	if pkt.Fields[MSObjectionMod] != "0" {
		t.Errorf("no-shout objection = %q, want 0", pkt.Fields[MSObjectionMod])
	}
	got, err := ParseMS(pkt.Fields, ParseFeatures([]string{FeatureCCCCIC}), 0)
	if err != nil {
		t.Fatal(err)
	}
	if *got != *src {
		t.Fatalf("round-trip mismatch:\n got %+v\nwant %+v", got, src)
	}
}
