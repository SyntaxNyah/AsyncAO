package courtroom

import (
	"bufio"
	"bytes"
	"net/url"
	"strconv"
	"strings"
)

// LoopPoints is a window in seconds (draudiostream.cpp:319); AO2 assumes stereo (aomusicplayer.cpp:99).
type LoopPoints struct {
	StartSec float64 // 0 unless overridden — AO2's own m_loop_start zero-default
	EndSec   float64
	HaveEnd  bool // false = no end was ever defined; nothing to loop back FROM
}

// DROTrackMeta is one [N] section of a DRO manifest, matched by its filename key (draudiotrackmetadata.cpp:64).
type DROTrackMeta struct {
	PlayOnce bool
	Loop     LoopPoints
}

// AO sidecar keys (aomusicplayer.cpp:73-122), case-sensitive as in AO2; only our *_sec keys fold case.
const (
	aoSecondsKey    = "seconds"
	aoLoopStartKey  = "loop_start"
	aoLoopEndKey    = "loop_end"
	aoLoopLengthKey = "loop_length"
	// aoSecondsTrueValue is the only value that sets secondsMode (aomusicplayer.cpp:83); "True" or "1" do not.
	aoSecondsTrueValue = "true"
)

// Our additive keys, folded to lowercase; AO2 never reads these spellings, so no authored file changes meaning.
const (
	secStartKey  = "loop_start_sec"
	secEndKey    = "loop_end_sec"
	secLengthKey = "loop_length_sec"
)

// Suffixes meaning "already seconds"; AO2 parses such values as 0 (aomusicplayer.cpp:92-124), so no file changes.
const (
	unitSuffixSec = "sec" // checked BEFORE unitSuffixS below: "sec" itself ends in 's'
	unitSuffixS   = "s"
)

// DRO manifest keys (draudiotrackmetadata.cpp:63-67), case-insensitive like QSettingsKeyFetcher (utils.cpp:7-14).
const (
	droFilenameKey  = "filename"
	droPlayOnceKey  = "play_once"
	droLoopStartKey = "loop_start"
	droLoopEndKey   = "loop_end"
	droSecondsKey   = "seconds"
)

// Accepted spellings of a true DRO play_once: QSettings writes "true", hand-authored files may write "1".
const (
	droPlayOnceTrue        = "true"
	droPlayOnceTrueNumeric = "1"
)

// splitEqualsLine splits like AO2 (aomusicplayer.cpp:75-79): a third "=" part is discarded; ok=false without "=".
func splitEqualsLine(line string) (key, val string, ok bool) {
	parts := strings.Split(line, "=")
	if len(parts) < 2 {
		return "", "", false
	}
	return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]), true
}

// trimUnitSuffix strips a trailing "s" or "sec"; "sec" is checked first because it also ends in "s".
func trimUnitSuffix(val string) (rest string, isSeconds bool) {
	lower := strings.ToLower(val)
	switch {
	case strings.HasSuffix(lower, unitSuffixSec):
		return strings.TrimSpace(val[:len(val)-len(unitSuffixSec)]), true
	case strings.HasSuffix(lower, unitSuffixS):
		return strings.TrimSpace(val[:len(val)-len(unitSuffixS)]), true
	default:
		return val, false
	}
}

// legacySampleValue mirrors toUInt (aomusicplayer.cpp:109): anything but an unsigned integer is 0, never a percentage.
func legacySampleValue(s string) float64 {
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0
	}
	return float64(n)
}

// legacySecondsValue mirrors AO2's toDouble() in seconds_mode (aomusicplayer.cpp:105): 0 on any parse failure.
func legacySecondsValue(s string) float64 {
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return v
}

// resolveLegacySeconds converts one legacy-key value to seconds for the running single-pass scan.
func resolveLegacySeconds(val string, secondsMode bool, sampleRate int) (seconds float64, ok bool) {
	if rest, isSeconds := trimUnitSuffix(val); isSeconds {
		return legacySecondsValue(rest), true
	}
	if secondsMode {
		return legacySecondsValue(val), true
	}
	if sampleRate <= 0 {
		return 0, false
	}
	return legacySampleValue(val) / float64(sampleRate), true
}

// parseFloat treats an unparsable *_sec value as absent; only the legacy grammar must degrade to 0 for AO2 parity.
func parseFloat(s string) (float64, bool) {
	v, err := strconv.ParseFloat(s, 64)
	return v, err == nil
}

// ParseAOLoopSidecar replicates aomusicplayer.cpp:71-123 as one forward pass: seconds=true affects only later lines.
// Never make it two-pass, or loop_start=306704 before seconds=true turns samples into 85 hours.
func ParseAOLoopSidecar(data []byte, sampleRate int) (LoopPoints, bool) {
	if len(data) == 0 {
		return LoopPoints{}, false
	}
	lines := strings.Split(string(data), "\n")

	var secondsMode bool
	var loopStart, loopEnd float64 // AO2's own zero defaults (m_loop_start/m_loop_end[streamId] = 0)
	var haveEnd bool
	var sawKV bool

	for _, raw := range lines {
		key, val, ok := splitEqualsLine(raw)
		if !ok {
			continue // AO2: args.size() < 2 -> continue
		}
		sawKV = true
		if key == aoSecondsKey {
			if val == aoSecondsTrueValue {
				secondsMode = true // affects only lines processed AFTER this one
			}
			continue
		}
		switch key {
		case aoLoopStartKey:
			if v, ok := resolveLegacySeconds(val, secondsMode, sampleRate); ok {
				loopStart = v
			}
		case aoLoopLengthKey:
			// AO2's quirk (aomusicplayer.cpp:115-118): loop_length before loop_start resolves against 0; keep it.
			if v, ok := resolveLegacySeconds(val, secondsMode, sampleRate); ok {
				loopEnd = loopStart + v
				haveEnd = true
			}
		case aoLoopEndKey:
			if v, ok := resolveLegacySeconds(val, secondsMode, sampleRate); ok {
				loopEnd = v
				haveEnd = true
			}
		}
	}

	// Our additive keys: case-insensitive, order-independent, applied after the legacy scan settles.
	var startSec, endSec, lengthSec float64
	var haveStartSec, haveEndSec, haveLengthSec bool
	for _, raw := range lines {
		key, val, ok := splitEqualsLine(raw)
		if !ok {
			continue
		}
		switch strings.ToLower(key) {
		case secStartKey:
			if v, pOk := parseFloat(val); pOk {
				startSec, haveStartSec = v, true
			}
		case secEndKey:
			if v, pOk := parseFloat(val); pOk {
				endSec, haveEndSec = v, true
			}
		case secLengthKey:
			if v, pOk := parseFloat(val); pOk {
				lengthSec, haveLengthSec = v, true
			}
		}
	}
	if haveStartSec {
		loopStart = startSec // wins outright over the legacy value for this bound
	}
	switch {
	case haveEndSec:
		loopEnd, haveEnd = endSec, true // wins outright over the legacy value for this bound
	case haveLengthSec:
		// Evaluated after the start bound is final.
		loopEnd, haveEnd = loopStart+lengthSec, true
	}

	return LoopPoints{StartSec: loopStart, EndSec: loopEnd, HaveEnd: haveEnd}, sawKV
}

// droSection is one [N] group: lowercased keys (like QSettingsKeyFetcher, utils.cpp:7-14) mapped to unquoted values.
type droSection struct {
	kv map[string]string
}

// unquoteDROValue strips one layer of quotes; QSettings quotes values needing escapes, so accept both spellings.
func unquoteDROValue(v string) string {
	if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
		return v[1 : len(v)-1]
	}
	return v
}

// parseDROManifest tokenizes QSettings [N] groups; keys before any header are skipped.
func parseDROManifest(data []byte) []droSection {
	var sections []droSection
	var cur *droSection
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			sections = append(sections, droSection{kv: map[string]string{}})
			cur = &sections[len(sections)-1]
			continue
		}
		if cur == nil {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		cur.kv[strings.ToLower(strings.TrimSpace(key))] = unquoteDROValue(strings.TrimSpace(val))
	}
	return sections
}

// isTruthyDROBool approximates QVariant::toBool for a string value: "true" or "1", case-insensitively.
func isTruthyDROBool(v string) bool {
	v = strings.ToLower(strings.TrimSpace(v))
	return v == droPlayOnceTrue || v == droPlayOnceTrueNumeric
}

// droSampleSeconds converts samples to seconds (draudiostream.cpp:319); bad is 0 (draudiotrackmetadata.cpp:66-67).
func droSampleSeconds(v string, sampleRate int) (seconds float64, ok bool) {
	if sampleRate <= 0 {
		return 0, false
	}
	n, err := strconv.ParseUint(v, 10, 64)
	if err != nil {
		return 0, true
	}
	return float64(n) / float64(sampleRate), true
}

// droLoopSeconds converts one loop value, honouring seconds=true, our own extension that DRO lacks.
func droLoopSeconds(v string, secondsMode bool, sampleRate int) (seconds float64, ok bool) {
	if secondsMode {
		return legacySecondsValue(v), true
	}
	return droSampleSeconds(v, sampleRate)
}

// ParseDROLoopSidecar returns the section matching trackRel case-insensitively
// (draudiotrackmetadata.cpp:57-90, :82-89); the first match wins, as DRO only warns (draudiotrackmetadata.cpp:83-87).
func ParseDROLoopSidecar(data []byte, trackRel string, sampleRate int) (DROTrackMeta, bool) {
	for _, sec := range parseDROManifest(data) {
		fn, present := sec.kv[droFilenameKey]
		if !present || !strings.EqualFold(fn, trackRel) {
			continue
		}
		var meta DROTrackMeta
		meta.PlayOnce = isTruthyDROBool(sec.kv[droPlayOnceKey])
		secondsMode := isTruthyDROBool(sec.kv[droSecondsKey])
		if ls, present := sec.kv[droLoopStartKey]; present {
			if v, convOk := droLoopSeconds(ls, secondsMode, sampleRate); convOk {
				meta.Loop.StartSec = v
			}
		}
		if le, present := sec.kv[droLoopEndKey]; present {
			if v, convOk := droLoopSeconds(le, secondsMode, sampleRate); convOk {
				meta.Loop.EndSec = v
				meta.Loop.HaveEnd = true
			}
		}
		return meta, true
	}
	return DROTrackMeta{}, false
}

// ClampLoopPoints applies DRO's clamp (draudiostream.cpp:319-329), so a bad end can't discard a good start.
func ClampLoopPoints(start, end, dur float64) (float64, float64) {
	if start >= dur {
		start = 0
	}
	if end <= start || end > dur {
		end = dur
	}
	return start, end
}

// IsDirectMusicURL wraps isMusicURL. Direct links get no sidecar lookup, as in aomusicplayer.cpp and DRO
// (draudiotrackmetadata.cpp:98-109). Pass the raw wire name: a resolved URL also starts with http(s).
func IsDirectMusicURL(url string) bool {
	return isMusicURL(url)
}

// hasRawQueryOrFragment detects a direct link in a resolved URL: we encode '?' and '#', signed links keep them.
// It misses a direct link with neither.
func hasRawQueryOrFragment(s string) bool {
	return strings.ContainsAny(s, "?#")
}

// MusicRelPath unescapes back to the authored spelling DRO manifests store (draudiotrackmetadata.cpp:64).
func MusicRelPath(musicURL string) (rel string, ok bool) {
	if hasRawQueryOrFragment(musicURL) {
		return "", false
	}
	i := strings.Index(musicURL, soundsMusic)
	if i < 0 {
		return "", false
	}
	parts := strings.Split(musicURL[i+len(soundsMusic):], "/")
	for idx, p := range parts {
		if unescaped, err := url.PathUnescape(p); err == nil {
			parts[idx] = unescaped
		}
		// An unescapable segment is left as it arrived, matching the per-segment scheme.
	}
	return strings.Join(parts, "/"), true
}

// DROLoopManifestURL assumes a track's folder names its manifest; DRO scans locally (draudiotrackmetadata.cpp:26-41).
func DROLoopManifestURL(musicURL string) (string, bool) {
	if hasRawQueryOrFragment(musicURL) {
		return "", false
	}
	i := strings.Index(musicURL, soundsMusic)
	if i < 0 {
		return "", false
	}
	base := musicURL[:i+len(soundsMusic)]
	rest := musicURL[i+len(soundsMusic):]
	slash := strings.IndexByte(rest, '/')
	if slash <= 0 {
		return "", false
	}
	folder := rest[:slash] // already escaped exactly as MusicURL emitted it — reused verbatim, never re-escaped
	return base + folder + "/" + folder + ".ini", true
}
