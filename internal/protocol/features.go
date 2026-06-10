package protocol

import "strings"

// Server feature flags from the FL packet, wire names exactly as AO servers
// advertise them (mirrors AO2-Client BASE_FEATURE_SET; matching is
// case-insensitive like ServerData::get_feature).
const (
	FeatureYellowText       = "yellowtext"         // 2.1
	FeatureFlipping         = "flipping"           // 2.1
	FeatureCustomObjections = "customobjections"   // 2.1
	FeatureFastLoading      = "fastloading"        // 2.1
	FeatureNoEncryption     = "noencryption"       // 2.1
	FeatureDeskMod          = "deskmod"            // 2.3–2.5
	FeatureEvidence         = "evidence"           // 2.3–2.5
	FeatureCCCCIC           = "cccc_ic_support"    // 2.6: pairing, shownames, immediate
	FeatureARUP             = "arup"               // 2.6
	FeatureCasingAlerts     = "casing_alerts"      // 2.6
	FeatureModcallReason    = "modcall_reason"     // 2.6
	FeatureLoopingSFX       = "looping_sfx"        // 2.8
	FeatureAdditive         = "additive"           // 2.8
	FeatureEffects          = "effects"            // 2.8: also gates pair ^order
	FeatureYOffset          = "y_offset"           // 2.9: vertical offsets
	FeatureExpandedDeskMods = "expanded_desk_mods" // 2.9: desk mods 2–5
	FeatureAuthPacket       = "auth_packet"        // 2.9.1
	FeaturePrezoom          = "prezoom"
	FeatureCustomBlips      = "custom_blips"
)

// FeatureSet is the set of features a server advertised.
type FeatureSet map[string]struct{}

// ParseFeatures builds a FeatureSet from FL packet fields.
func ParseFeatures(fields []string) FeatureSet {
	fs := make(FeatureSet, len(fields))
	for _, f := range fields {
		fs[strings.ToLower(strings.TrimSpace(f))] = struct{}{}
	}
	return fs
}

// Has reports whether the server advertised the feature (case-insensitive).
func (fs FeatureSet) Has(feature string) bool {
	_, ok := fs[strings.ToLower(feature)]
	return ok
}
