package courtroom

// Blip chain, each rung lowercase then authored case: sounds/blips (webAO handleICSpeaking.ts:403-405),
// legacy sfx-blip general file, plain general sound (DRO blip_player.cpp:22-25 probes in this order).
// Order only picks a file when a base ships a set twice; misses are 404-cached, so a settled chain costs nothing.

import (
	"strconv"
	"strings"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
)

const (
	// legacyBlipPrefix joins the blip name with no separator (get_blips, text_file_functions.cpp:524).
	legacyBlipPrefix = "sfx-blip"

	// defaultBlipSet is get_blipname's last resort (text_file_functions.cpp:510) and webAO's default.
	defaultBlipSet = "male"

	// blipCandidateCap is AO2's three spellings times two casings; identical spellings collapse.
	blipCandidateCap = 6
)

// validBlipName rejects names that must not become a URL (text_file_functions.cpp:487-514): numbers are never blips,
// and KFO reuses MS index 30 for third_charid ("-1", "<id>^0").
func validBlipName(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	// '^' is KFO's triplex shape, the rest traversal or wire-escape characters; Unicode stays for custom set names.
	if strings.ContainsAny(name, `/\^:?#%&$`) || strings.Contains(name, "..") || name[0] == '.' {
		return false
	}
	if _, err := strconv.Atoi(name); err == nil {
		return false // "-1", "0", "7": a sentinel or an id, never a blip set
	}
	return true
}

// BlipCandidates returns de-duplicated URL bases; [0] is the asset's identity, the rest PrefetchChain alts.
func (u URLBuilder) BlipCandidates(name string) []string {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil
	}
	lower := strings.ToLower(name)
	out := make([]string, 0, blipCandidateCap)
	add := func(url string) {
		if len(out) >= blipCandidateCap {
			return // named cap (§17.4); the rungs below are the whole set
		}
		for _, have := range out {
			if have == url {
				return // an already-lowercase name collapses its two casings
			}
		}
		out = append(out, url)
	}
	// Rung 1: the modern set folder (get_blips' "cool kids variant", :519-522).
	add(u.Blip(name))
	add(u.BlipAuthored(name))
	// Rung 2: AO1's legacy general file sounds/general/sfx-blip<name> (:524).
	add(u.origin + soundsGeneral + segRaw(legacyBlipPrefix+lower))
	add(u.origin + soundsGeneral + segRaw(legacyBlipPrefix+name))
	// Rung 3: canon's first probe, the name as a plain general sound (:517); last here because it is rare on a mirror.
	add(u.origin + soundsGeneral + segRaw(lower))
	add(u.origin + soundsGeneral + segRaw(name))
	return out
}

// BlipRef is the only source of blip assets. Returns the zero AssetRef for a non-blip-set name; a
// caller skips it rather than probing. // AssetType: Blip
func (u URLBuilder) BlipRef(name string) AssetRef {
	if !validBlipName(name) {
		return AssetRef{}
	}
	cands := u.BlipCandidates(name)
	if len(cands) == 0 {
		return AssetRef{}
	}
	return AssetRef{Base: cands[0], Alts: cands[1:], Type: assets.AssetTypeBlip}
}
