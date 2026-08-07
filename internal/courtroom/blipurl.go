package courtroom

// blipurl.go — the ONE place a blip-set NAME becomes blip URLs.
//
// AO2 resolves a blip in two steps, and both are canon:
//
//  1. The NAME — AOApplication::get_blipname
//     (../AO2-Client/src/text_file_functions.cpp:487-514): char.ini [Options]
//     blips, then the per-emote [Options<N>] blips override (:491-503), then the
//     LEGACY [Options] gender key (:507), then "male" (:510). Both keys yield the
//     same bare string: get_blipname returns a NAME, and the key the author used
//     is gone by the time anything looks for a file. The key therefore does NOT
//     select a folder — see the ordering note below.
//
//  2. The FILE — AOApplication::get_blips (:515-527), which CHECKS two
//     spellings against a LOCAL, case-insensitive filesystem and falls back
//     to a third it never checks:
//
//     sounds/general/<name>          get_sounds_path (path_functions.cpp:53-56), :517
//     sounds/blips/<name>            "../blips/" + name, :519-522 ("the cool kids variant")
//     sounds/general/sfx-blip<name>  :524 ("Return legacy variant") — an
//                                    UNCHECKED return, no file_exists guard
//
//     The legacy spelling is one token — "sfx-blip" + p_blipname, a DASH and no
//     separator, so gender=male reads sounds/general/sfx-blipmale.<ext>. The DRO
//     fork kept the same two-rung ladder and spells out the file it means:
//     "sounds/general/sfx-blipmale.wav style"
//     (../DRO-Client/src/dro/system/audio/blip_player.cpp:21-25).
//
//     AOBlipPlayer::setBlip feeds get_blips' answer back through get_sounds_path
//     (../AO2-Client/src/aoblipplayer.cpp:19-21), which is why the middle rung is
//     written as a "../blips/" escape out of sounds/general — it is exactly
//     sounds/blips/<name>.
//
// Why AsyncAO's rung ORDER differs from AO2's: AO2 stats a local disk, where a
// probe is free and looking in sounds/general/ first costs nothing. We speak HTTP
// to a case-sensitive mirror, where every miss is a request, so the rungs are
// ordered by what mirrors actually serve. webAO — this client's URL reference —
// only ever asks for sounds/blips/<name>.opus
// (../webAO/src/viewport/utils/handleICSpeaking.ts:403-405), and that is where
// every modern base keeps its sets; it also collapses the legacy gender key into
// the SAME blips name (../webAO/src/client/handleCharacterInfo.ts:97-113), which
// is the direct evidence that the key does not pick a path. And the order is
// not merely webAO-motivated: DRO's shipping AOBlipPlayer::set_blips
// (../DRO-Client/src/dro/system/audio/blip_player.cpp:22-25) probes
// sounds/blips/ FIRST, then sfx-blip, and never probes the plain general
// name at all — rungs 1→2 here are a live AO client's own ladder, and the
// last rung is a pure superset of it. The legacy general spellings follow so
// an AO1-era base (sfx-blipmale.wav) still blips instead of falling silent,
// and canon's own first probe — the name as a plain general sound — is the
// last rung. Re-ordering can only change WHICH file plays for a
// base that ships the same set twice; otherwise it just changes how many probes
// it takes to find the one file that exists, and every miss is 404-cached
// (rule §17.6), so a settled chain costs nothing.
//
// Each rung splits by CASE, lowercase first. AO2 read a case-insensitive local
// filesystem and never had to choose; HTTP mirrors do — webAO-convention mirrors
// lowercase every path, raw content folders keep the author's case. Same shape
// and same order as the chatbox skin (MiscChatboxCandidates) and the screen-effect
// overlay (OverlayEffectMisc): the lowercase spelling is the asset's identity
// everywhere, the authored spelling is an alt, and identical spellings collapse.

import (
	"strconv"
	"strings"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
)

const (
	// legacyBlipPrefix is AO1's blip filename prefix, concatenated with the blip
	// name and NO separator: get_blips returns "sfx-blip" + p_blipname
	// (../AO2-Client/src/text_file_functions.cpp:524) and setBlip resolves that
	// under sounds/general/. Dash, not underscore.
	legacyBlipPrefix = "sfx-blip"

	// defaultBlipSet is get_blipname's last resort when neither char.ini key is
	// present (../AO2-Client/src/text_file_functions.cpp:510) and webAO's default
	// (../webAO/src/client/handleCharacterInfo.ts:84).
	defaultBlipSet = "male"

	// blipCandidateCap bounds one blip chain (rule §17.4): AO2's three spellings
	// (blips folder, legacy general file, plain general sound) × the two casings a
	// case-sensitive mirror splits them into. Identical spellings collapse, so a
	// lowercase name like "male" spends only three of the six.
	blipCandidateCap = 6
)

// validBlipName reports whether a name is a usable blip-set name, i.e. something
// we are willing to mint a URL (and therefore a network probe) from. There is no
// such thing as a numeric blip in AO: get_blipname/get_blips resolve a NAME
// through char.ini [Options] blips → legacy gender → "male"
// (AO2-Client text_file_functions.cpp:487-514), so anything that parses as a bare
// number is by definition not a blip. That is exactly what leaks off the KFO
// family, whose MS reuses index 30 for its "triplex" third_charid and idles it at
// the sentinel "-1" (and sends "<id>^0" once a third pair is confirmed) — probing
// it burned the whole audio ladder × every spelling per message, forever, on a
// guaranteed miss. The parse-side feature gate (protocol.ParseMS's getBlips) is
// the real fix; this is the belt at the prefetch boundary so no server, hostile or
// merely odd, can mint asset URLs out of that field.
//
// It is not exported and it has no second copy: every blip URL in the client is
// minted by BlipRef, which applies this guard first. The live funnel and the
// Studio content report used to carry two different versions of it — the report's
// checked only for "" and "-1", so a KFO "<id>^0" or a bare "0" became a probed
// URL there while the funnel dropped it.
func validBlipName(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	// Not a plain path segment. '^' is in the list because that is the shape a
	// triplex-paired KFO client puts in slot 30 ("<id>^0"); the rest are the
	// traversal / wire-escape characters a name has no business carrying. Unicode
	// is deliberately NOT restricted — custom blip sets are named by their authors.
	if strings.ContainsAny(name, `/\^:?#%&$`) || strings.Contains(name, "..") || name[0] == '.' {
		return false
	}
	if _, err := strconv.Atoi(name); err == nil {
		return false // "-1", "0", "7": a sentinel or an id, never a blip set
	}
	return true
}

// BlipCandidates returns the ordered, de-duplicated URL bases for a blip-set
// name: AO2's three get_blips spellings, each in the lowercase identity casing
// and then the authored one. [0] is the asset's identity everywhere (T1 key,
// audio lookup); the rest are alts for PrefetchChain, which probes them in order
// only while every format of each earlier base 404s. Bounded by
// blipCandidateCap. Blip names are a single path segment (validBlipName rejects
// slashes), so each candidate escapes as one segment — a two-word set like
// "deep voice" becomes .../sounds/blips/deep%20voice and
// .../sounds/general/sfx-blipdeep%20voice.
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
	// Rung 1 — the modern set folder: webAO's only convention and where every
	// modern base ships its sets (get_blips' "cool kids variant", :519-522).
	add(u.Blip(name))
	add(u.BlipAuthored(name))
	// Rung 2 — AO1's legacy general file: sounds/general/sfx-blip<name> (:524).
	add(u.origin + soundsGeneral + segRaw(legacyBlipPrefix+lower))
	add(u.origin + soundsGeneral + segRaw(legacyBlipPrefix+name))
	// Rung 3 — canon's own FIRST probe: the name as a plain general sound (:517).
	// Last here because a name that IS a general sound is the rare case on a
	// mirror, and this rung is only ever paid when the set is missing anyway.
	add(u.origin + soundsGeneral + segRaw(lower))
	add(u.origin + soundsGeneral + segRaw(name))
	return out
}

// BlipRef is the ONE mint for blip assets: name → guarded, ordered chain tagged
// AssetTypeBlip. Every site that wants a blip URL — the live message funnel and
// the Studio content report — goes through it, so the sentinel/path guard and the
// candidate ladder cannot drift apart again (they already had, in two different
// ways). Returns the zero AssetRef for anything that is not a blip-set name; a
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
