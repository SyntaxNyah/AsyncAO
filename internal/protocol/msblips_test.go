package protocol

import "testing"

// kfoMS is a KFO-Server-shaped MS: 36 fields, where slots 30-35 are NOT AO2's
// blipname/slide pair but KFO's "triplex" third-pairing extension —
// third_charid (idle value the literal "-1"), third_folder, third_emote,
// third_offset, third_flip, video (server/area.py Area.send_ic, annotated inline
// in server/network/aoprotocol.py:1298-1303).
func kfoMS() []string {
	f := make([]string, MSMaximum)
	for i := range f {
		f[i] = ""
	}
	f[MSDeskMod] = "1"
	f[MSPreEmote] = "-"
	f[MSCharName] = "Phoenix"
	f[MSEmote] = "normal"
	f[MSMessage] = "Objection!"
	f[MSSide] = "def"
	f[MSSFXName] = "0"
	f[MSEmoteMod] = "0"
	f[MSCharID] = "3"
	f[MSSFXDelay] = "0"
	f[MSObjectionMod] = "0"
	f[MSEvidenceID] = "0"
	f[MSFlip] = "0"
	f[MSRealization] = "0"
	f[MSTextColor] = "0"
	f[MSOtherCharID] = "-1"
	f[MSBlipname] = "-1"  // KFO third_charid
	f[MSSlide] = "Franzy" // KFO third_folder — a CHARACTER FOLDER, not a flag
	return f
}

// TestParseMSKFOTriplexIsNotABlipname pins the KFO field-index collision that made
// every incoming message probe <origin>sounds/blips/-1 and burn the whole audio
// ladder on a guaranteed miss. AO2-Client reads BLIPNAME only when the server
// advertised custom_blips (courtroom.cpp:4249-4255); KFO advertises cccc_ic_support
// but NOT custom_blips (server/tsuserver.py supported_features), and reuses slots
// 30/31 for triplex. The gate is the whole fix.
func TestParseMSKFOTriplexIsNotABlipname(t *testing.T) {
	kfo := ParseFeatures([]string{
		FeatureCCCCIC, FeatureEffects, FeatureLoopingSFX, FeatureAdditive,
		FeatureYOffset, FeatureExpandedDeskMods, "triplex", "video_support",
	})
	msg, err := ParseMS(kfoMS(), kfo, 0)
	if err != nil {
		t.Fatal(err)
	}
	if msg.Blipname != "" {
		t.Errorf("Blipname = %q, want empty: index 30 is KFO's third_charid, not a blip set", msg.Blipname)
	}
	if msg.Slide {
		t.Errorf("Slide = true, want false: index 31 is KFO's third_folder")
	}
	// Everything BELOW the gate must still parse — the fix is a slice of the
	// packet, not a downgrade of the whole message.
	if msg.CharName != "Phoenix" || msg.Emote != "normal" || msg.CharID != 3 {
		t.Errorf("the rest of the message was damaged: %+v", msg)
	}
}

// TestParseMSCustomBlipsHonoursTheField is the other half: a server that really
// does advertise custom_blips gets its 2.9.1 blipname/slide pair read, exactly as
// before the gate.
func TestParseMSCustomBlipsHonoursTheField(t *testing.T) {
	f := kfoMS()
	f[MSBlipname] = "typewriter"
	f[MSSlide] = "1"
	msg, err := ParseMS(f, ParseFeatures([]string{FeatureCCCCIC, FeatureCustomBlips}), 0)
	if err != nil {
		t.Fatal(err)
	}
	if msg.Blipname != "typewriter" {
		t.Errorf("Blipname = %q, want typewriter", msg.Blipname)
	}
	if !msg.Slide {
		t.Error("Slide = false, want true")
	}
}

// TestParseMSBlipsNeedTheirOwnFeature pins that cccc_ic_support alone is NOT
// enough: it is what opens indices >= 15 in general, and reading 30/31 off the
// back of it is precisely what leaked.
func TestParseMSBlipsNeedTheirOwnFeature(t *testing.T) {
	f := kfoMS()
	f[MSBlipname] = "typewriter"
	f[MSSlide] = "1"
	msg, err := ParseMS(f, ParseFeatures([]string{FeatureCCCCIC}), 0)
	if err != nil {
		t.Fatal(err)
	}
	if msg.Blipname != "" || msg.Slide {
		t.Errorf("cccc_ic_support alone must not open 30/31: blip=%q slide=%v", msg.Blipname, msg.Slide)
	}
}
