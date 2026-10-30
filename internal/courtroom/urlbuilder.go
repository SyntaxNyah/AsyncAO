// Package courtroom implements the AO2 courtroom: session handshake, message lifecycle, pairing and URLs; SDL-free.
package courtroom

import (
	"net/url"
	"path"
	"strconv"
	"strings"
	"unicode"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
)

// Asset path conventions, mirroring webAO (URLs) and AO2-Client (semantics).
const (
	charactersDir   = "characters/"
	backgroundDir   = "background/"
	soundsGeneral   = "sounds/general/"
	soundsBlips     = "sounds/blips/"
	soundsMusic     = "sounds/music/"
	miscDefaultDir  = "misc/default/"
	evidenceDir     = "evidence/"
	customObjDir    = "custom_objections/"
	emotionsDir     = "emotions/"
	emoteButtonStem = "button"
	charIconStem    = "char_icon"
	idlePrefix      = "(a)"
	talkPrefix      = "(b)"
	shoutBubbleSuff = "_bubble"
	customShoutStem = "custom"
)

type EmoteKind int

const (
	// EmoteIdle is the looping (a) animation.
	EmoteIdle EmoteKind = iota
	// EmoteTalk is the looping (b) animation.
	EmoteTalk
	// EmotePreanim is the one-shot unprefixed preanimation.
	EmotePreanim
)

// URLBuilder turns AO asset names into URL bases with no extension; the resolver appends candidates.
type URLBuilder struct {
	origin   string
	charCase uint8 // character-folder casing (CharCase*); 0 = lowercase (default)
}

// NewURLBuilder normalizes the origin (exactly one trailing slash).
func NewURLBuilder(origin string) URLBuilder {
	return URLBuilder{origin: strings.TrimRight(origin, "/") + "/"}
}

// Character-folder casing, for rare servers with capitalised folders; the wrong choice 404s every character asset.
const (
	CharCaseLower    uint8 = iota // lowercase (default — the safe, correct choice for almost every server)
	CharCaseFirstCap              // First-letter capital: "Phoenix wright"
	CharCaseTitle                 // Title Case: "Phoenix Wright"
	// CharCaseAuto asks the App to learn the casing; a stray Auto in charSeg falls through to lowercase.
	CharCaseAuto
	CharCaseCount
)

// WithCharCase returns a copy with the casing set, keeping the origin.
func (u URLBuilder) WithCharCase(c uint8) URLBuilder {
	if c < CharCaseCount {
		u.charCase = c
	}
	return u
}

func (u URLBuilder) Origin() string { return u.origin }

// Evidence returns the full URL: LE names carry their extension (get_evidence_path), bare names default to .png.
// AssetType: Misc (exact URL)
func (u URLBuilder) Evidence(image string) string {
	if !strings.Contains(image, ".") {
		image += ".png"
	}
	// A nested name keeps its separators; AO allows ".." ("../characters/Ridelle/char_icon.png") for asset reuse.
	return u.origin + segPath(cleanRel(evidenceDir+image))
}

// encodeURIRestores restores marks JavaScript's encodeURI leaves literal: emote paths need literal "(a)"/"(b)".
var encodeURIRestores = strings.NewReplacer(
	"%28", "(",
	"%29", ")",
	"%27", "'",
	"%21", "!",
	"%2A", "*",
	"%7E", "~",
)

// segRaw percent-encodes one segment webAO-style without touching case.
func segRaw(name string) string {
	return encodeURIRestores.Replace(url.PathEscape(name))
}

// seg escapes one lowercased segment, as hosts are case-sensitive; the character folder uses charSeg.
func seg(name string) string { return segRaw(strings.ToLower(name)) }

// segPath escapes a multi-segment value, lowercased; %2F is a dead path on strict origins, so slashes stay.
func segPath(name string) string {
	parts := strings.Split(strings.ToLower(name), "/")
	for i, part := range parts {
		parts[i] = segRaw(part)
	}
	return strings.Join(parts, "/")
}

// segPathRaw is segPath keeping the authored case, the chain alt for case-preserving mirrors.
func segPathRaw(name string) string {
	parts := strings.Split(name, "/")
	for i, part := range parts {
		parts[i] = segRaw(part)
	}
	return strings.Join(parts, "/")
}

// charSeg escapes the character path in the configured casing. '/' is a real directory (path_functions.cpp:43-46),
// and %2F 404s where the origin doesn't decode it (Apache's default).
func (u URLBuilder) charSeg(name string) string {
	if !strings.ContainsRune(name, '/') {
		return u.charSegOne(name) // the 99% case: one segment, unchanged output, no allocation
	}
	// Only nesting pays the traversal clamp; cleanRel is rooted, and the mount fetcher refuses escapes.
	parts := strings.Split(cleanRel(name), "/")
	for i, part := range parts {
		parts[i] = u.charSegOne(part)
	}
	return strings.Join(parts, "/")
}

// charSegOne escapes one segment; nested folders case independently, as the server named each one.
func (u URLBuilder) charSegOne(name string) string {
	switch u.charCase {
	case CharCaseFirstCap:
		return segRaw(firstCap(name))
	case CharCaseTitle:
		return segRaw(titleCase(name))
	default:
		return seg(name)
	}
}

func firstCap(s string) string {
	r := []rune(strings.ToLower(s))
	if len(r) == 0 {
		return ""
	}
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

// titleCase upper-cases each word's first letter; a word break is a space, underscore or hyphen.
func titleCase(s string) string {
	r := []rune(strings.ToLower(s))
	capNext := true
	for i, c := range r {
		switch {
		case capNext && unicode.IsLetter(c):
			r[i] = unicode.ToUpper(c)
			capNext = false
		case c == ' ' || c == '_' || c == '-':
			capNext = true
		}
	}
	return string(r)
}

// CharIcon returns the char-select icon base. // AssetType: CharIcon
func (u URLBuilder) CharIcon(character string) string {
	return u.origin + charactersDir + u.charSeg(character) + "/" + charIconStem
}

// Emote returns the glued "(a)<emote>" base, AO2's first candidate and the asset's identity.
// AssetType: CharSprite
func (u URLBuilder) Emote(character, emote string, kind EmoteKind) string {
	return u.origin + charactersDir + u.charSeg(character) + "/" + segPath(emotePrefix(kind)+emote)
}

// EmoteFolder returns "(a)/<emote>", also probed by AO2 (animationlayer.cpp:422). Chain it between
// Emote and EmoteBare. // AssetType: CharSprite
func (u URLBuilder) EmoteFolder(character, emote string, kind EmoteKind) string {
	return u.origin + charactersDir + u.charSeg(character) + "/" + segPath(emotePrefix(kind)) + "/" + segPath(emote)
}

// EmoteBare returns the unprefixed base, the last spelling AO2 probes. Chain after Emote and EmoteFolder.
// AssetType: CharSprite
func (u URLBuilder) EmoteBare(character, emote string) string {
	return u.origin + charactersDir + u.charSeg(character) + "/" + segPath(emote)
}

// emotePrefix maps an EmoteKind to its sprite prefix ("" for preanims, which ship unprefixed).
func emotePrefix(kind EmoteKind) string {
	switch kind {
	case EmoteIdle:
		return idlePrefix
	case EmoteTalk:
		return talkPrefix
	}
	return ""
}

// EmoteAlts returns the spellings behind Emote(): bare, then prefix folder; for PrefetchChain.
func (u URLBuilder) EmoteAlts(character, emote string, kind EmoteKind) []string {
	return []string{
		u.EmoteBare(character, emote),
		u.EmoteFolder(character, emote, kind),
	}
}

// emotePrefixLen is len("(a)") == len("(b)").
const emotePrefixLen = 3

// SpriteHealAlts rebuilds EmoteAlts' chain from an Emote() base, for holders of only a live URL, keeping this out of
// internal/ui. // AssetType: CharSprite
func SpriteHealAlts(active string) []string {
	return []string{bareSpriteBase(active), folderSpriteBase(active)}
}

// bareSpriteBase strips a leading "(a)"/"(b)" from the final segment; unprefixed bases pass through unchanged.
func bareSpriteBase(active string) string {
	i := strings.LastIndexByte(active, '/')
	seg := active[i+1:]
	if strings.HasPrefix(seg, "(a)") || strings.HasPrefix(seg, "(b)") {
		return active[:i+1] + seg[emotePrefixLen:]
	}
	return active
}

// folderSpriteBase turns a leading "(a)"/"(b)" on the final segment into a folder; others pass through.
func folderSpriteBase(active string) string {
	i := strings.LastIndexByte(active, '/')
	seg := active[i+1:]
	if strings.HasPrefix(seg, "(a)") || strings.HasPrefix(seg, "(b)") {
		return active[:i+1] + seg[:emotePrefixLen] + "/" + seg[emotePrefixLen:]
	}
	return active
}

// EmoteButton returns the picker art for 1-based emote n; on selects _on (AO2 "emotions/button%1_off").
// AssetType: EmoteButton
func (u URLBuilder) EmoteButton(character string, n int, on bool) string {
	state := "_off"
	if on {
		state = "_on"
	}
	return u.origin + charactersDir + u.charSeg(character) + "/" + emotionsDir + emoteButtonStem + strconv.Itoa(n) + state
}

// ShoutBubble returns the per-character bubble base ("holdit_bubble";
// custom shouts use "custom"). // AssetType: ShoutBubble
func (u URLBuilder) ShoutBubble(character, shoutName string, custom bool) string {
	stem := shoutName + shoutBubbleSuff
	if custom {
		stem = customShoutStem
	}
	return u.origin + charactersDir + u.charSeg(character) + "/" + seg(stem)
}

// NamedCustomShout returns a custom_objections/<stem> base; a wire name's extension is stripped for the resolver.
// AssetType: ShoutBubble
func (u URLBuilder) NamedCustomShout(character, name string) string {
	if dot := strings.LastIndexByte(name, '.'); dot > 0 {
		name = name[:dot]
	}
	return u.origin + charactersDir + u.charSeg(character) + "/" + customObjDir + seg(name)
}

// AssetType: ShoutBubble
func (u URLBuilder) DefaultShoutBubble(shoutName string) string {
	return u.origin + miscDefaultDir + seg(shoutName+shoutBubbleSuff)
}

// ShoutSFX returns the per-character shout sound base. // AssetType: SFX
func (u URLBuilder) ShoutSFX(character, shoutName string) string {
	return u.origin + charactersDir + u.charSeg(character) + "/" + seg(shoutName)
}

// Background returns a part base. Names may carry "../" (get_background_path, DRIO), so slashes stay. Flat names are
// unchanged. // AssetType: Background
func (u URLBuilder) Background(bg, part string) string {
	return u.origin + segPath(cleanRel(backgroundDir+bg+"/"+part))
}

// cleanRel collapses "." and ".." as QFile does, rooted so ".." can't escape the origin; use on raw names.
func cleanRel(p string) string {
	if p == "" {
		return ""
	}
	return strings.TrimPrefix(path.Clean("/"+p), "/")
}

// BackgroundsRoot returns the background/ URL for the picker's autoindex; fetch bytes, don't format-probe.
func (u URLBuilder) BackgroundsRoot() string {
	return u.origin + backgroundDir
}

// EvidenceRoot returns the evidence/ URL for the picker's autoindex; fetch bytes, don't format-probe.
func (u URLBuilder) EvidenceRoot() string {
	return u.origin + evidenceDir
}

// CharFolder returns a character's folder URL, the recursive-download root.
func (u URLBuilder) CharFolder(character string) string {
	return u.origin + charactersDir + u.charSeg(character) + "/"
}

// Speedlines returns <character>/<side>_speedlines, drawn behind the speaker during a zoom emote.
func (u URLBuilder) Speedlines(character, side string) string {
	return u.origin + charactersDir + u.charSeg(character) + "/" + side + "_speedlines"
}

// BackgroundFolder returns a background's folder URL; nesting matches Background.
func (u URLBuilder) BackgroundFolder(bg string) string {
	return u.origin + backgroundDir + segPath(bg) + "/"
}

// SFX returns a general sound base; names may nest in subfolders (AO2's
// get_sfx joins them as paths), so slashes survive. // AssetType: SFX
func (u URLBuilder) SFX(name string) string {
	return u.origin + soundsGeneral + segPath(name)
}

// Blip returns a lowercased blip set base, the identity (matching the
// client-wide seg() convention and webAO-style mirrors). // AssetType: Blip
func (u URLBuilder) Blip(name string) string {
	return u.origin + soundsBlips + seg(name)
}

// BlipAuthored is Blip in the authored casing, the chain alt for case-preserving mirrors (AO2's
// case-insensitive local FS never had to choose). // AssetType: Blip
func (u URLBuilder) BlipAuthored(name string) string {
	return u.origin + soundsBlips + segRaw(name)
}

// MiscChatboxCandidates returns a chat=<misc> skin's spellings, first = identity; spaces escape per segment.
func (u URLBuilder) MiscChatboxCandidates(misc string) []string {
	misc = strings.TrimSpace(misc)
	lower := escapePreservingSlashes(strings.ToLower(misc))
	cands := []string{
		u.origin + "misc/" + lower + "/chat",
		u.origin + "misc/" + lower + "/chatbox",
	}
	if authored := escapePreservingSlashes(misc); authored != lower {
		cands = append(cands,
			u.origin+"misc/"+authored+"/chat",
			u.origin+"misc/"+authored+"/chatbox",
		)
	}
	return cands
}

// miscDir holds every per-character misc asset (AO2 get_misc_path, path_functions.cpp:48-51).
const miscDir = "misc/"

// overlayEffectsManifest is the roster a misc pack ships beside its art (get_effects, text_file_functions.cpp:772).
const overlayEffectsManifest = "effects.ini"

// overlayIconsDir prefixes AO2's dropdown icon element (courtroom.cpp:5608).
const overlayIconsDir = "icons/"

// OverlayEffectMisc returns an overlay's origin candidates, the only network step; each segment escapes alone.
func (u URLBuilder) OverlayEffectMisc(folder, name string) []string {
	return u.overlayEffectCandidates(folder, name)
}

// OverlayEffectMiscIcon is OverlayEffectMisc for the dropdown icon
// ("icons/<name>", courtroom.cpp:5608). // AssetType: Effect
func (u URLBuilder) OverlayEffectMiscIcon(folder, name string) []string {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil
	}
	return u.overlayEffectCandidates(folder, overlayIconsDir+name)
}

// OverlayEffectManifest is the exact effects.ini URL. Lowercase only, as a second casing only buys a second 404.
func (u URLBuilder) OverlayEffectManifest(folder string) string {
	folder = strings.TrimSpace(folder)
	if folder == "" {
		return ""
	}
	return u.origin + segPath(miscDir+folder+"/"+overlayEffectsManifest)
}

// Fixed names of a chatbox's colour INIs: set_font reads them against get_chat(p_char).
const (
	chatboxFontsIni      = "courtroom_fonts.ini"
	chatboxChatConfigIni = "chat_config.ini"
)

// ChatboxChatConfigIni returns the spellings of chat_config.ini; fetch with FetchRaw, never format-probe.
func (u URLBuilder) ChatboxChatConfigIni(misc string) []string {
	return u.chatboxIniCandidates(misc, chatboxChatConfigIni, "")
}

// ChatboxFontsIni returns the spellings of courtroom_fonts.ini; real packs also ship it capitalised.
func (u URLBuilder) ChatboxFontsIni(misc string) []string {
	return u.chatboxIniCandidates(misc, chatboxFontsIni, "Courtroom_Fonts.ini")
}

// chatboxIniCandidates builds up to 4 spellings: each file spelling times each folder spelling.
func (u URLBuilder) chatboxIniCandidates(misc, file, authoredFile string) []string {
	misc = strings.TrimSpace(misc)
	if misc == "" {
		return nil
	}
	files := []string{file}
	if authoredFile != "" && authoredFile != file {
		files = append(files, authoredFile)
	}
	lower := escapePreservingSlashes(strings.ToLower(misc))
	authored := escapePreservingSlashes(misc)
	out := make([]string, 0, len(files)*2)
	for _, f := range files {
		out = append(out, u.origin+"misc/"+lower+"/"+f)
	}
	if authored != lower {
		for _, f := range files {
			out = append(out, u.origin+"misc/"+authored+"/"+f)
		}
	}
	return out
}

// overlayEffectCandidates builds up to 2 spellings of misc/<folder>/<element>.
func (u URLBuilder) overlayEffectCandidates(folder, element string) []string {
	folder = strings.TrimSpace(folder)
	element = strings.TrimSpace(element)
	if folder == "" || element == "" {
		return nil
	}
	rel := miscDir + folder + "/" + element
	lower := segPath(rel)
	cands := []string{u.origin + lower}
	if authored := segPathRaw(rel); authored != lower {
		cands = append(cands, u.origin+authored)
	}
	return cands
}

// MusicURL returns the full URL; tracks carry the extension, and http(s):// tracks are direct (get_music_path).
// AssetType: Music
func (u URLBuilder) MusicURL(track string) string {
	if isMusicURL(track) {
		return track
	}
	return u.origin + soundsMusic + escapePreservingSlashes(strings.ToLower(track))
}

// The direct-link prefixes get_music_path special-cases.
const (
	httpScheme  = "http://"
	httpsScheme = "https://"
)

// isMusicURL reports a direct http(s) link, always music, though its extension may precede a query string.
func isMusicURL(track string) bool {
	return hasPrefixFold(track, httpScheme) || hasPrefixFold(track, httpsScheme)
}

// hasPrefixFold allocates nothing, as MusicDisplayName calls isMusicURL per visible row per frame.
func hasPrefixFold(s, prefix string) bool {
	return len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix)
}

// escapePreservingSlashes escapes each segment of a track that may contain subdirectories.
func escapePreservingSlashes(p string) string {
	parts := strings.Split(p, "/")
	for i, part := range parts {
		parts[i] = url.PathEscape(part)
	}
	return strings.Join(parts, "/")
}

// AO2's witness-view spellings, the fallback without art (path_functions.cpp:108-117, courtroom.cpp:4613-4626).
const (
	defaultPosBackground = "witnessempty"
	legacyPosBackground  = "wit"
)

// BackgroundFallbacks returns further stems, in AO2's order. Desks get none (courtroom.cpp:4628-4634):
// "stand" would resurrect the previous room's desk.
func BackgroundFallbacks(bgPart string) []string {
	alts := make([]string, 0, 2)
	for _, stem := range [...]string{defaultPosBackground, legacyPosBackground} {
		if stem != bgPart {
			alts = append(alts, stem)
		}
	}
	return alts
}

// PositionScene maps a position to background and desk stems (get_pos_path); unknown uses <pos> and <pos>_overlay.
func PositionScene(pos string) (bgPart, deskPart string) {
	switch pos {
	case "def":
		return "defenseempty", "defensedesk"
	case "pro":
		return "prosecutorempty", "prosecutiondesk"
	case "wit":
		return "witnessempty", "stand"
	case "jud":
		return "judgestand", "judgedesk"
	case "hld":
		return "helperstand", "helperdesk"
	case "hlp":
		return "prohelperstand", "prohelperdesk"
	case "jur":
		return "jurystand", "jurydesk"
	case "sea":
		return "seancestand", "seancedesk"
	case "":
		return "witnessempty", "stand" // AO defaults to witness
	default:
		return pos, pos + "_overlay"
	}
}

// ZoomSide returns prosecution for pro, hlp and wit, defense otherwise, as AO2 hardcodes (courtroom.cpp:3463-3471).
func ZoomSide(pos string) string {
	switch pos {
	case "pro", "hlp", "wit":
		return "prosecution"
	}
	return "defense"
}

// SpeedlinesCandidates returns <pos>_speedlines, then AO2's side stem; the <pos> arm is our extension (jud, jur, sea).
func (u URLBuilder) SpeedlinesCandidates(character, pos string) []string {
	// ZoomSide is never "", so the slice always ends with a real candidate.
	out := make([]string, 0, 2)
	if pos != "" {
		out = append(out, u.Speedlines(character, pos))
	}
	return append(out, u.Speedlines(character, ZoomSide(pos)))
}

// Objection modifiers, as the MS packet carries them.
const (
	objectionHoldIt = 1 + iota
	objectionObjection
	objectionTakeThat
	objectionCustom
)

func ShoutName(objection int) string {
	switch objection {
	case objectionHoldIt:
		return "holdit"
	case objectionObjection:
		return "objection"
	case objectionTakeThat:
		return "takethat"
	case objectionCustom:
		return customShoutStem
	default:
		return ""
	}
}

const SpriteAssetType = assets.AssetTypeCharSprite
