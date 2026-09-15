package courtroom

import (
	"bufio"
	"bytes"
	"net/url"
	"strconv"
	"strings"
)

// LoopPoints is a resolved loop window in SECONDS — the one portable unit both AO2's
// byte math and DRO's sample math independently reduce to once a channel/byte-size
// factor is removed (../DRO-Client/src/draudiostream.cpp:319 confirms
// "m_loop_start / double(l_sample_rate)", no byte or channel multiplier at all).
// AsyncAO targets a time-addressed seek API (Mix_SetMusicPosition takes a double)
// instead of BASS's byte-addressed one, so it never reintroduces AO2's own latent
// bug of hardcoding stereo (aomusicplayer.cpp:99 num_channels = 2, silently wrong for
// a mono source).
type LoopPoints struct {
	StartSec float64 // 0 unless overridden — AO2's own m_loop_start zero-default
	EndSec   float64
	HaveEnd  bool // false = no end was ever defined; nothing to loop back FROM
}

// DROTrackMeta is one [N] section of a DRO sounds/music/<folder>/<folder>.ini
// manifest, matched to a track by its `filename` key (draudiotrackmetadata.cpp:64).
//
// title is deliberately NOT a field. Wiring it into the zero-allocation
// MusicDisplayName path (courtroom.go) is a separate design problem, and parsing a
// value nothing ever reads is precisely the "parsed but never applied" failure
// CLAUDE.md names by example (the sidecar [overrides] tier that shipped inert
// through four waves behind exactly such a mirror). DEFERRED, not forgotten
// (binding decision D18) — MusicDisplayName keeps computing the Now-Playing text
// purely and synchronously from the wire track name alone.
type DROTrackMeta struct {
	PlayOnce bool
	Loop     LoopPoints
}

// The AO legacy sidecar's own literal keys (aomusicplayer.cpp:73-122), matched
// CASE-SENSITIVELY exactly as AO2 matches them (QString::operator== is
// case-sensitive) — a differently-cased line is invisible to the legacy grammar,
// by design. Only the NEW *_sec keys below fold case, because they are AsyncAO's
// own addition and no AO2-authored file can already depend on their exact casing.
const (
	aoSecondsKey    = "seconds"
	aoLoopStartKey  = "loop_start"
	aoLoopEndKey    = "loop_end"
	aoLoopLengthKey = "loop_length"
	// aoSecondsTrueValue is the one value that flips secondsMode on. AO2 tests it
	// with == "true" specifically (aomusicplayer.cpp:83) — any other value,
	// including "True" or "1", leaves the running mode unchanged; no validation,
	// no rejection, byte-for-byte parity with the reference client.
	aoSecondsTrueValue = "true"
)

// The additive convenience keys (Part B / binding decision D12): folded to
// lowercase before comparison, so any casing an author types just works. Safe to
// fold because AO2 itself never emits or reads a key with this exact spelling —
// there is no real, already-authored file whose meaning this could flip.
const (
	secStartKey  = "loop_start_sec"
	secEndKey    = "loop_end_sec"
	secLengthKey = "loop_length_sec"
)

// unitSuffixSec / unitSuffixS are the two trailing spellings a legacy-key VALUE may
// carry to mean "this number is already seconds" (binding decision D13: an explicit
// "loop_start = 1.5s" or "1.5sec"). Checked case-insensitively. AO2's own
// toUInt()/toDouble() would simply fail closed to 0 on a value shaped like "1.5s"
// (aomusicplayer.cpp:92-124, no partial parse), so accepting the suffix can never
// reinterpret a real AO2-authored file — no file AO2 already accepts contains a
// trailing letter on one of these values.
const (
	unitSuffixSec = "sec" // checked BEFORE unitSuffixS below: "sec" itself ends in 's'
	unitSuffixS   = "s"
)

// The DRO manifest's own literal keys (draudiotrackmetadata.cpp:63-67), matched
// case-insensitively via parseDROManifest's lowercased storage — mirroring DRO's own
// utils::QSettingsKeyFetcher (utils.cpp:7-14), which looks a requested key up against
// the file's real keys case-insensitively.
const (
	droFilenameKey  = "filename"
	droPlayOnceKey  = "play_once"
	droLoopStartKey = "loop_start"
	droLoopEndKey   = "loop_end"
	droSecondsKey   = "seconds"
)

// droPlayOnceTrue / droPlayOnceTrueNumeric are the two spellings accepted for a
// string-backed DRO play_once value: "true" is what QSettings::IniFormat writes for
// its own bool serialization, "1" is what a hand-authored manifest may reasonably
// write instead. Anything else (including absent) is false.
const (
	droPlayOnceTrue        = "true"
	droPlayOnceTrueNumeric = "1"
)

// splitEqualsLine tokenizes one line of either sidecar format the SAME way AO2's own
// QString::split("=") + args.size()<2 check does (aomusicplayer.cpp:75-79): split on
// EVERY "=", keep only the first two parts — a THIRD "=" on the line is simply
// discarded, never re-joined into the value — then trim both sides. ok=false for a
// line with no "=" at all (a blank line, a bare comment, or plain prose), which is
// also how both parsers skip a comment line with no key at all: there is nothing to
// split, so it is invisible without any dedicated comment syntax.
func splitEqualsLine(line string) (key, val string, ok bool) {
	parts := strings.Split(line, "=")
	if len(parts) < 2 {
		return "", "", false
	}
	return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]), true
}

// trimUnitSuffix strips an AsyncAO-only trailing "s"/"sec" unit marker
// (case-insensitive) off a legacy-key value, reporting whether one was present.
// "sec" is checked first because it itself ends in "s" — checking the short form
// first would strip only the final letter and leave "1.5se" behind.
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

// legacySampleValue mirrors AO2's own args[1].trimmed().toUInt() (aomusicplayer.cpp:
// 109): the WHOLE string must be a valid unsigned integer or the conversion fails and
// AO2 silently uses 0 — no partial parse, no negative sign, no decimal point (this is
// the reason a bare fraction like "0.25" is samples-grammar garbage, never "25% of
// the track": there is no percentage concept anywhere in this parser to fall back
// to). 64 bits is a superset of Qt's own 32-bit uint — real files never approach
// either limit — so no value AO2 already accepts is reinterpreted differently here.
func legacySampleValue(s string) float64 {
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0
	}
	return float64(n)
}

// legacySecondsValue mirrors AO2's own args[1].trimmed().toDouble() in seconds_mode
// (aomusicplayer.cpp:105): a real float, sign and fraction both allowed, 0 on any
// parse failure — no validation, matching the reference client exactly.
func legacySecondsValue(s string) float64 {
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return v
}

// resolveLegacySeconds converts ONE legacy-key value into seconds for the running
// single-pass scan, in the precedence Part B requires:
//  1. an explicit trailing unit suffix on THIS value (D13) always wins — seconds,
//     regardless of secondsMode;
//  2. otherwise the running seconds=true mode (aomusicplayer.cpp:81-90) — the value
//     is already seconds;
//  3. otherwise it is a sample count, converted via /sampleRate exactly as AO2's own
//     byte math reduces once its channel/byte-size factor is removed (recon 4/14) —
//     but ok=false when sampleRate is unusable (<=0): a garbage 0-second bound would
//     be worse than leaving the caller's running value untouched, so the caller must
//     skip the update entirely rather than accept a fabricated zero.
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

// parseFloat is strconv.ParseFloat with the ok idiom the *_sec keys need. Unlike the
// legacy grammar's unconditional degrade-to-0 (required for byte-for-byte AO2
// parity), an unparsable *_sec value is simply treated as ABSENT: it is AsyncAO's own
// key, so there is no existing file whose meaning must be preserved by silently
// substituting 0 for "not present".
func parseFloat(s string) (float64, bool) {
	v, err := strconv.ParseFloat(s, 64)
	return v, err == nil
}

// ParseAOLoopSidecar parses an AO music-loop sidecar (webAO/AO2-Client convention:
// "<track-url>.txt" alongside the track itself) into a resolved loop window in
// SECONDS.
//
// The legacy grammar below replicates ../AO2-Client/src/aomusicplayer.cpp:71-123
// LINE FOR LINE, IN FILE ORDER, CASE-SENSITIVELY: it is a SINGLE FORWARD PASS, not a
// two-pass scan. `seconds=true` is a running flag that affects ONLY the lines below
// it — a real, already-authored file with `loop_start=306704` followed later by
// `seconds=true` means 306704 SAMPLES to AO2, and reinterpreting it as 306704 SECONDS
// (~85 hours) the moment a second pass "helpfully" looked ahead would be a
// five-order-of-magnitude silent corruption of a file that already works today.
// Never "fix" this into a two-pass scan: a superset may accept MORE files, it must
// never change what an existing one means.
//
// Layered ON TOP, never folded into that ordered scan, is Part B's additive grammar
// (binding decisions D12/D13): the case-INSENSITIVE `loop_start_sec` / `loop_end_sec`
// / `loop_length_sec` keys (plain seconds, no sample-rate math, evaluated in a
// SEPARATE pass because their names can never collide with anything AO2 itself
// emits — order among themselves does not matter) and a trailing "s"/"sec" unit
// suffix directly on a legacy value (resolveLegacySeconds). Each bound resolves
// independently: one bound may come from a *_sec key while the other comes from the
// legacy grammar in the very same file.
//
// sampleRate is the track's own real sample rate (internal/assets.SniffAudioSampleRate
// — read by the render-side caller, never re-derived here). sampleRate <= 0
// (unresolved) degrades any SAMPLE-mode bound to "unset" rather than dividing by zero
// or fabricating a bogus 0-second bound; a *_sec-suffixed or seconds-mode value is
// unaffected, since neither needs the rate at all.
//
// ok is false only when data is empty or every line was unparseable (nothing to
// report either way) — an absent end is reported via HaveEnd == false, never via
// ok == false.
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
			// AO2's own order-dependent quirk (aomusicplayer.cpp:115-118): adds to
			// whatever loop_start holds RIGHT NOW, not whatever it ends up as by
			// the end of the file. A loop_length line appearing BEFORE loop_start
			// resolves against loop_start's default (0) — a known, disclosed
			// authoring trap, not something this parser "fixes".
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

	// The additive overlay (Part B): case-insensitive, order-independent among
	// themselves, applied AFTER the legacy scan above has fully settled.
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
		// Evaluated AFTER the start bound above is fully resolved (legacy or
		// _sec-overridden) — "using whichever loop_start is final by this point".
		loopEnd, haveEnd = loopStart+lengthSec, true
	}

	return LoopPoints{StartSec: loopStart, EndSec: loopEnd, HaveEnd: haveEnd}, sawKV
}

// droSection is one tokenized `[N]` group from a DRO manifest: lowercased keys
// (mirroring DRO's own case-insensitive utils::QSettingsKeyFetcher, utils.cpp:7-14)
// mapped to their raw, already-unquoted values.
type droSection struct {
	kv map[string]string
}

// unquoteDROValue strips ONE layer of surrounding double quotes. QSettings wraps a
// string value in quotes when the raw text needs escaping; DRO reads such a value
// back unquoted via QSettings itself, so a manifest entry is meaningful whether or
// not it happens to be quoted, and this reader must accept both spellings.
func unquoteDROValue(v string) string {
	if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
		return v[1 : len(v)-1]
	}
	return v
}

// parseDROManifest tokenizes a DRO audiotrack manifest: QSettings::IniFormat [N]
// groups of key=value pairs, values optionally wrapped in double quotes.
//
// This is a SEPARATE small scanner from internal/theme's INI reader
// (theme.ParseINI), not a second general-purpose one: theme.INI stores everything as
// a flat "section/key" map with no way to ENUMERATE the sections it read, and this
// parser's whole job is to walk each [N] group in file order looking for the one
// whose filename matches — a real capability gap in the shared reader, not a style
// choice, and internal/theme/ini.go is not a file this task may touch. What IS
// reused is theme.ParseINI's own discipline: blank lines skipped, keys matched
// case-insensitively, one map of keys per section.
//
// A key appearing before any [section] header is skipped (not a shape DRO ever
// writes); a section with no readable filename can never match a track and needs no
// warning (hard rule 6 — a missing/unusable sidecar stays silent).
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

// isTruthyDROBool mirrors QVariant::toBool() closely enough for a string-backed ini
// value: DRO's own bool serialization writes "true"/"false" (QSettings::IniFormat),
// and a hand-authored manifest may reasonably write "1" instead — both accepted,
// case-insensitively; anything else (including absent) is false.
func isTruthyDROBool(v string) bool {
	v = strings.ToLower(strings.TrimSpace(v))
	return v == droPlayOnceTrue || v == droPlayOnceTrueNumeric
}

// droSampleSeconds converts a DRO loop_start/loop_end sample count to seconds — the
// same raw/sampleRate reduction as AO2's legacy grammar (recon 4), confirmed against
// DRO's own mirror of it at ../DRO-Client/src/draudiostream.cpp:319
// ("m_loop_start / double(l_sample_rate)"). DRO's format has no seconds=true toggle
// at all — every loop_start/loop_end value is always a sample count. ok=false only
// when sampleRate is unusable, matching resolveLegacySeconds' own guard; an
// unparsable value degrades to 0 the same unchecked way DRO's own quint64
// loop_start()/loop_end() getters do (draudiotrackmetadata.cpp:66-67, .toULongLong(),
// no validation).
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

// droLoopSeconds converts one DRO loop_start/loop_end value into seconds, honouring
// the section's seconds=true toggle (AsyncAO's own extension: DRO itself has no such
// toggle). In seconds mode the value is already a float in seconds; otherwise it is
// a sample count reduced by /sampleRate exactly as DRO's own math does.
func droLoopSeconds(v string, secondsMode bool, sampleRate int) (seconds float64, ok bool) {
	if secondsMode {
		return legacySecondsValue(v), true
	}
	return droSampleSeconds(v, sampleRate)
}

// ParseDROLoopSidecar parses a DRO sounds/music/<folder>/<folder>.ini manifest
// (../DRO-Client/src/draudiotrackmetadata.cpp:57-90, update_cache) and returns the
// metadata for the section whose `filename` matches trackRel, case-insensitively —
// DRO itself keys its whole cache by the lowercased filename
// (draudiotrackmetadata.cpp:82-89). The first matching section wins; a manifest with
// two sections claiming the same filename is a malformed manifest DRO itself only
// warns about (draudiotrackmetadata.cpp:83-87), not something this parser needs to
// adjudicate further. ok=false when no section's filename matches at all.
//
// title is read by nobody — see DROTrackMeta's own doc comment (decision D18).
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

// ClampLoopPoints applies DRO's own two-part clamp
// (../DRO-Client/src/draudiostream.cpp:319-329, init_loop) to a resolved loop
// window, checking start and end INDEPENDENTLY so a bad end can never discard a
// good, author-intended start (binding decision D19 — the flaw one earlier design
// shipped by collapsing both bounds to 0 together). This function does not decide
// whether the resulting window is wide enough to arm a watcher at all — that is
// render.Audio's own minLoopWindowSec floor, applied after this clamp.
func ClampLoopPoints(start, end, dur float64) (float64, float64) {
	if start >= dur {
		start = 0
	}
	if end <= start || end > dur {
		end = dur
	}
	return start, end
}

// IsDirectMusicURL is the ONE export of this package's direct-URL gate — a thin
// wrapper around isMusicURL (urlbuilder.go), never a second implementation, so a
// future edit to the http(s)-prefix rule can only ever happen in one place
// (TestIsDirectMusicURLIsAThinWrapperNotASecondCopy pins the two functions
// together). A direct http(s) link (a Discord /play URL, which may carry a query
// string after the extension) never gets a sidecar lookup of either kind (binding
// decision D25): both AO2-Client and DRO-Client bypass their own sidecar/manifest
// metadata for a URL-shaped track name — aomusicplayer.cpp has no sidecar path for
// one at all, and DRAudiotrackMetadata's own constructor short-circuits on the URL
// regex before ever touching s_audiotrack_cache (draudiotrackmetadata.cpp:98-109).
//
// IsDirectMusicURL is only unambiguous on the track's ORIGINAL, pre-resolution
// wire name (exactly how isMusicURL is used everywhere else in this file:
// MusicURL itself, courtroom.go:799/831). A caller holding only the fully
// resolved URLBuilder.MusicURL() output cannot fall back to this gate: an asset
// origin is itself an http(s) URL, so a legitimate origin-built track ALSO starts
// with "http(s)://" once resolved, and this function has no way to tell that
// apart from a direct link passed through verbatim. Callers that only have the
// resolved string (MusicRelPath, DROLoopManifestURL, below) use a different,
// narrower signal for exactly that reason — see hasRawQueryOrFragment.
func IsDirectMusicURL(url string) bool {
	return isMusicURL(url)
}

// hasRawQueryOrFragment reports whether s contains a literal, unescaped '?' or
// '#'. This is the ONE signal a fully RESOLVED musicURL string can still carry
// that distinguishes "a direct link, kept verbatim by MusicURL's own http(s)
// passthrough" from "a URL this package built" — a bare scheme check
// (IsDirectMusicURL) cannot do this, because it is equally true of a legitimate
// origin-built URL once the origin itself is http(s) (virtually always: an asset
// origin IS a network URL). URLBuilder's own output can never contain either
// character raw: segPath/escapePreservingSlashes route every segment through
// url.PathEscape, which percent-encodes both as structurally significant
// ("%3F"/"%23"). A genuinely direct link that happens to sign itself with a
// query string — precisely how a Discord CDN /play link is shaped
// ("...song.mp3?ex=&is=&hm=", already documented on MusicDisplayName) — keeps
// that '?' raw, because MusicURL returns such a track completely unchanged.
//
// This cannot, on its own, catch a direct link that carries NEITHER a query
// string nor a fragment (a plain "https://host/sounds/music/x.opus" masquerading
// as a resolved track) — a caller that still has the ORIGINAL wire text available
// should prefer IsDirectMusicURL on that instead, before ever building a resolved
// URL from it. That gap is disclosed, not hidden: both functions below only ever
// receive the resolved string, so this is the strongest signal available to them.
func hasRawQueryOrFragment(s string) bool {
	return strings.ContainsAny(s, "?#")
}

// MusicRelPath strips the "sounds/music/" segment a URLBuilder.MusicURL was built
// with and per-segment URL-unescapes the remainder back to the track's AUTHORED
// spelling (the inverse of escapePreservingSlashes) — the shape DRO's own manifest
// stores under `filename` (draudiotrackmetadata.cpp:64), which is never URL-escaped.
//
// ok=false for a musicURL carrying a raw query string or fragment
// (hasRawQueryOrFragment — a direct link's own signature, Part D) even when its
// path coincidentally CONTAINS "sounds/music/" (a Discord CDN mirror or a
// same-convention origin can be shaped exactly like that), checked BEFORE the
// substring search for exactly this reason. ok=false too for a URL with no such
// segment at all (a local-mount or differently-laid-out origin).
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
		// An unescapable segment is left exactly as it arrived — the best
		// available answer, matching escapePreservingSlashes' own per-segment
		// (never whole-value) scheme.
	}
	return strings.Join(parts, "/"), true
}

// DROLoopManifestURL derives the DRO-convention manifest URL for a track. DRO scans
// sounds/music/ as a real local directory (draudiotrackmetadata.cpp:26-41,
// get_all_package_and_base_paths) and would find EVERY *.ini it ships there; a
// streaming client cannot list a directory it never downloads, so AsyncAO
// approximates the common authoring pattern instead — the folder a track lives in
// names its own manifest (binding decision D26, matching the issue's own worked
// example: sounds/music/dro_castlevania/dro_castlevania.ini for a track at
// sounds/music/dro_castlevania/<file>).
//
// ok=false for a musicURL carrying a raw query string or fragment
// (hasRawQueryOrFragment, checked before the "sounds/music/" search for the same
// coincidental-substring reason MusicRelPath guards against) or for a track with
// no folder segment at all — DRO's per-folder convention has nothing to
// approximate for a bare filename.
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
