package protocol

import (
	"strings"
	"testing"

	aolib "github.com/AO-Underground/aolib/go/v2"
)

// nyathenaFeatures mirrors Nyathena's advertised FL feature set.
func nyathenaFeatures() FeatureSet {
	return ParseFeatures([]string{
		FeatureNoEncryption, FeatureYellowText, FeaturePrezoom, FeatureFlipping,
		FeatureCustomObjections, FeatureFastLoading, FeatureDeskMod, FeatureEvidence,
		FeatureCCCCIC, FeatureARUP, FeatureCasingAlerts, FeatureModcallReason,
		FeatureLoopingSFX, FeatureAdditive, FeatureEffects, FeatureYOffset,
		FeatureExpandedDeskMods, FeatureAuthPacket,
	})
}

func TestOutgoingMSToAolibMSToServer(t *testing.T) {
	feats := nyathenaFeatures()

	t.Run("unpaired is -1 and JSON validates", func(t *testing.T) {
		msg := OutgoingMS{
			DeskMod: DeskShow, PreEmote: "-", CharName: "Phoenix", Emote: "normal",
			Message: "Objection!", Side: "def", SFXName: "1", EmoteMod: EmoteModIdle,
			CharID: 0, PairWith: UnpairedCharID,
		}
		typed := msg.ToAolibMSToServer(feats)
		if typed.PairedCharID != -1 {
			t.Fatalf("PairedCharID = %d, want -1", typed.PairedCharID)
		}
		if typed.Side != aolib.SideDef {
			t.Fatalf("Side = %q, want def", typed.Side)
		}
		raw, err := aolib.Encode(&typed, aolib.WireJSON)
		if err != nil {
			t.Fatalf("Encode(JSON) failed: %v", err)
		}
		if !strings.Contains(string(raw), `"paired_charid":-1`) {
			t.Fatalf("JSON missing unpaired marker: %s", raw)
		}
	})

	t.Run("custom objection maps to custom", func(t *testing.T) {
		msg := OutgoingMS{
			DeskMod: DeskShow, PreEmote: "-", CharName: "Phoenix", Emote: "point",
			Message: "Hold it!", Side: "def", SFXName: "1", CharID: 0,
			Objection: ShoutCustom, CustomShout: "Objection!",
			PairWith: UnpairedCharID,
		}
		typed := msg.ToAolibMSToServer(feats)
		if typed.ShoutModifier != aolib.ShoutModifierCustom {
			t.Fatalf("ShoutModifier = %q, want custom", typed.ShoutModifier)
		}
		if _, err := aolib.Encode(&typed, aolib.WireJSON); err != nil {
			t.Fatalf("Encode(JSON) failed: %v", err)
		}
	})

	t.Run("out-of-range enums fall back to defaults", func(t *testing.T) {
		msg := OutgoingMS{
			DeskMod: 99, EmoteMod: 99, Objection: 99, TextColor: 99,
			CharName: "Phoenix", Emote: "normal", Message: "hi", Side: "wit",
			CharID: 0, PairWith: UnpairedCharID,
		}
		typed := msg.ToAolibMSToServer(feats)
		if typed.DeskModifier != aolib.DeskModifierShown {
			t.Fatalf("DeskModifier = %q, want shown", typed.DeskModifier)
		}
		if typed.EmoteModifier != aolib.EmoteModifierNoPreanim {
			t.Fatalf("EmoteModifier = %q, want no_preanim", typed.EmoteModifier)
		}
		if typed.ShoutModifier != aolib.ShoutModifierNone {
			t.Fatalf("ShoutModifier = %q, want none", typed.ShoutModifier)
		}
		if typed.TextColor != aolib.TextColorWhite {
			t.Fatalf("TextColor = %q, want white", typed.TextColor)
		}
		// Out-of-range values must still produce valid JSON (no disconnect).
		if _, err := aolib.Encode(&typed, aolib.WireJSON); err != nil {
			t.Fatalf("Encode(JSON) failed: %v", err)
		}
	})
}

