// Package courtroom implements the AO2 courtroom: session handshake, message
// lifecycle (shout → preanim → talk → idle), pairing, typewriter pacing, and
// the URL conventions assets live under. Everything here is SDL-free and
// headless-testable; rendering lives in render.go behind the cgo build tag.
package courtroom

import (
	"net/url"
	"strconv"
	"strings"
	"unicode"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
)

// Asset path conventions, mirroring webAO (URL reference) and AO2-Client
// (semantics reference).
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

// EmoteKind selects the sprite layer for a character emote.
type EmoteKind int

const (
	// EmoteIdle is the looping (a) animation.
	EmoteIdle EmoteKind = iota
	// EmoteTalk is the looping (b) animation.
	EmoteTalk
	// EmotePreanim is the one-shot unprefixed preanimation.
	EmotePreanim
)

// URLBuilder turns AO asset names into URL bases (no extension — the
// resolver appends candidates). Origin is the server asset URL or the local
// mount origin, always with a trailing slash.
type URLBuilder struct {
	origin   string
	charCase uint8 // character-folder casing (CharCase*); 0 = lowercase (default)
}

// NewURLBuilder normalizes the origin (exactly one trailing slash).
func NewURLBuilder(origin string) URLBuilder {
	return URLBuilder{origin: strings.TrimRight(origin, "/") + "/"}
}

// Character-folder casing — a POWER-USER setting for the rare server whose character folders are
// capitalised. The vast majority ship lowercase, so lowercase is the default; the wrong choice
// makes every character asset 404. Applied ONLY to the character-folder segment, never to emotes.
const (
	CharCaseLower    uint8 = iota // lowercase (default — the safe, correct choice for almost every server)
	CharCaseFirstCap              // First-letter capital: "Phoenix wright"
	CharCaseTitle                 // Title Case: "Phoenix Wright"
	// CharCaseAuto asks the App to LEARN the casing per server (probe a character's icon in each
	// casing once, apply the winner). OFF unless the user picks it. The URLBuilder never applies it
	// directly — the App resolves Auto to a concrete lower/firstcap/title before WithCharCase, so a
	// stray Auto reaching charSeg falls through to lowercase (its default), never a broken URL.
	CharCaseAuto
	CharCaseCount
)

// WithCharCase returns a copy of u with the character-folder casing set (keeping the origin), so
// the App can rebuild the builder when the pref changes without re-resolving the origin.
func (u URLBuilder) WithCharCase(c uint8) URLBuilder {
	if c < CharCaseCount {
		u.charCase = c
	}
	return u
}

// Origin returns the normalized origin.
func (u URLBuilder) Origin() string { return u.origin }

// Evidence returns the FULL evidence image URL: LE image names carry their
// extension ("knife.png" — AO2-Client get_evidence_path serves them
// verbatim from base/evidence/); bare legacy names default to .png. Use
// Manager.PrefetchExact: no format probing, exactly one fetch.
// AssetType: Misc (exact URL)
func (u URLBuilder) Evidence(image string) string {
	if !strings.Contains(image, ".") {
		image += ".png"
	}
	return u.origin + evidenceDir + seg(image)
}

// encodeURIRestores maps Go's percent-escapes back to the literal marks
// JavaScript's encodeURI leaves untouched — webAO is the URL reference, and
// AO emote paths lean on literal "(a)"/"(b)" prefixes.
var encodeURIRestores = strings.NewReplacer(
	"%28", "(",
	"%29", ")",
	"%27", "'",
	"%21", "!",
	"%2A", "*",
	"%7E", "~",
)

// segRaw percent-encodes one path segment webAO-style (encodeURI: parentheses and friends stay
// literal) WITHOUT touching case — the caller decides the casing.
func segRaw(name string) string {
	return encodeURIRestores.Replace(url.PathEscape(name))
}

// seg escapes one path segment, lowercased (AO asset hosts are case-sensitive; packs ship
// lowercase). Used for EVERY segment except the character folder, which honours charSeg.
func seg(name string) string { return segRaw(strings.ToLower(name)) }

// segPath escapes a path VALUE that may span segments — emote anims nest
// ("emotes/Witch Standard Normal/normal1") and sfx names may too — lowercased
// like seg but with slashes kept as separators: webAO's encodeURI leaves '/'
// literal, and the old whole-value PathEscape produced %2F URLs that only
// worked where the edge happened to normalize them back (nginx/Cloudflare do;
// stricter origins don't). Empty parts survive (a leading '/' in a char.ini
// anim is verbatim AO2 concatenation), so the built URL mirrors AO2's paths
// byte for byte.
func segPath(name string) string {
	parts := strings.Split(strings.ToLower(name), "/")
	for i, part := range parts {
		parts[i] = segRaw(part)
	}
	return strings.Join(parts, "/")
}

// charSeg escapes the CHARACTER-folder segment honouring the builder's casing setting — lowercase
// by default (the correct choice for almost every server), or the first-cap / title-case forms for
// the rare capitalised-folder server. Emotes and every other segment stay lowercase (seg).
func (u URLBuilder) charSeg(name string) string {
	switch u.charCase {
	case CharCaseFirstCap:
		return segRaw(firstCap(name))
	case CharCaseTitle:
		return segRaw(titleCase(name))
	default:
		return seg(name)
	}
}

// firstCap lowercases s then upper-cases the first letter ("phoenix wright" → "Phoenix wright").
func firstCap(s string) string {
	r := []rune(strings.ToLower(s))
	if len(r) == 0 {
		return ""
	}
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

// titleCase lowercases s then upper-cases the first letter of each word ("phoenix wright" →
// "Phoenix Wright"); a word break is a space, underscore or hyphen.
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

// Emote returns a character sprite base for the given kind — the glued
// spelling "(a)<emote>", AO2-Client's FIRST candidate and the asset's
// identity everywhere. Nested emotes keep their slashes (segPath).
// AssetType: CharSprite
func (u URLBuilder) Emote(character, emote string, kind EmoteKind) string {
	return u.origin + charactersDir + u.charSeg(character) + "/" + segPath(emotePrefix(kind)+emote)
}

// EmoteFolder returns the prefix-as-FOLDER spelling — "(a)/<emote>" — the
// SECOND candidate AO2-Client probes (animationlayer.cpp:422 pathlist:
// "(a)X", "(a)/X", bare X): packs that group idle/talk art in literal
// "(a)"/"(b)" directories need it. A leading '/' on the emote doubles the
// slash exactly like AO2's string concatenation does. Chain it between
// Emote and EmoteBare. // AssetType: CharSprite
func (u URLBuilder) EmoteFolder(character, emote string, kind EmoteKind) string {
	return u.origin + charactersDir + u.charSeg(character) + "/" + segPath(emotePrefix(kind)) + "/" + segPath(emote)
}

// EmoteBare returns the unprefixed sprite base — the LAST spelling
// AO2-Client probes for idle/talk sprites (many packs ship only bare
// files like "1.webp"). Chain after Emote + EmoteFolder.
// AssetType: CharSprite
func (u URLBuilder) EmoteBare(character, emote string) string {
	return u.origin + charactersDir + u.charSeg(character) + "/" + segPath(emote)
}

// emotePrefix maps an EmoteKind to its AO sprite-name prefix ("" for
// preanims — they ship unprefixed).
func emotePrefix(kind EmoteKind) string {
	switch kind {
	case EmoteIdle:
		return idlePrefix
	case EmoteTalk:
		return talkPrefix
	}
	return ""
}

// EmoteAlts returns the alternate spellings behind Emote()'s identity: the
// bare unprefixed file, then the "(a)/" prefix FOLDER. Every sprite prefetch
// site feeds this to PrefetchChain after the glued Emote() base.
//
// Deliberate deviation from AO2-Client's pathlist ORDER (animationlayer.cpp
// probes "(a)X", "(a)/X", bare X): coverage is identical, but bare files
// outnumber prefix-folder packs in the wild by far, and a live origin was
// observed HANGING on folder-form misses (characters without an "(a)" dir
// timed out instead of 404ing, which aborted the chain before the bare file
// that existed and back-offed the host). Order only matters when a pack
// ships BOTH spellings of the same emote — effectively never.
// AssetType: CharSprite
func (u URLBuilder) EmoteAlts(character, emote string, kind EmoteKind) []string {
	return []string{
		u.EmoteBare(character, emote),
		u.EmoteFolder(character, emote, kind),
	}
}

// EmoteButton returns the emote-picker button art base for the 1-based
// emote number n; on selects the pressed (_on) variant. Convention shared
// by AO2-Client (emotion_button "emotions/button%1_off") and webAO.
// AssetType: EmoteButton
func (u URLBuilder) EmoteButton(character string, n int, on bool) string {
	state := "_off"
	if on {
		state = "_on"
	}
	return u.origin + charactersDir + u.charSeg(character) + "/" + emotionsDir + emoteButtonStem + strconv.Itoa(n) + state
}

// ShoutBubble returns the per-character shout bubble base ("holdit_bubble",
// custom shouts use "custom"). // AssetType: ShoutBubble
func (u URLBuilder) ShoutBubble(character, shoutName string, custom bool) string {
	stem := shoutName + shoutBubbleSuff
	if custom {
		stem = customShoutStem
	}
	return u.origin + charactersDir + u.charSeg(character) + "/" + seg(stem)
}

// NamedCustomShout returns a 2.10 named custom interjection base
// (characters/<char>/custom_objections/<stem>). Wire names from other
// clients may carry a file extension (their dir scan keeps it) — strip it,
// the resolver owns format probing.
// AssetType: ShoutBubble
func (u URLBuilder) NamedCustomShout(character, name string) string {
	if dot := strings.LastIndexByte(name, '.'); dot > 0 {
		name = name[:dot]
	}
	return u.origin + charactersDir + u.charSeg(character) + "/" + customObjDir + seg(name)
}

// DefaultShoutBubble returns the misc/default fallback bubble base.
// AssetType: ShoutBubble
func (u URLBuilder) DefaultShoutBubble(shoutName string) string {
	return u.origin + miscDefaultDir + seg(shoutName+shoutBubbleSuff)
}

// ShoutSFX returns the per-character shout sound base. // AssetType: SFX
func (u URLBuilder) ShoutSFX(character, shoutName string) string {
	return u.origin + charactersDir + u.charSeg(character) + "/" + seg(shoutName)
}

// Background returns a background part base (defenseempty, stand, ...).
// AssetType: Background
func (u URLBuilder) Background(bg, part string) string {
	return u.origin + backgroundDir + seg(bg) + "/" + seg(part)
}

// BackgroundsRoot returns the background/ directory URL (with trailing
// slash). On hosts that serve an autoindex it lists every background folder
// — the discovery source for the background picker, mirroring how iniswap.txt
// seeds the wardrobe. Not an asset: fetch its bytes, don't format-probe it.
func (u URLBuilder) BackgroundsRoot() string {
	return u.origin + backgroundDir
}

// CharFolder returns one character's folder URL (with trailing slash) — the
// recursive-download root and (on an autoindex host) its file listing.
func (u URLBuilder) CharFolder(character string) string {
	return u.origin + charactersDir + u.charSeg(character) + "/"
}

// BackgroundFolder returns one background's folder URL (with trailing slash).
func (u URLBuilder) BackgroundFolder(bg string) string {
	return u.origin + backgroundDir + seg(bg) + "/"
}

// SFX returns a general sound base. Names may nest in subfolders (AO2's
// get_sfx joins them as paths), so slashes survive. // AssetType: SFX
func (u URLBuilder) SFX(name string) string {
	return u.origin + soundsGeneral + segPath(name)
}

// Blip returns a blip set base (lowercased — the identity, matching the
// client-wide seg() convention and webAO-style mirrors). // AssetType: Blip
func (u URLBuilder) Blip(name string) string {
	return u.origin + soundsBlips + seg(name)
}

// BlipAuthored is Blip in the set's authored casing — the chain alt for
// case-preserving mirrors (blips=YTTD is sounds/blips/yttd.opus on a
// lowercase mirror but sounds/blips/YTTD.wav on a raw content folder; AO2's
// case-insensitive local FS never had to choose). // AssetType: Blip
func (u URLBuilder) BlipAuthored(name string) string {
	return u.origin + soundsBlips + segRaw(name)
}

// MiscChatboxCandidates returns the ordered spellings of a per-character
// chatbox skin (char.ini chat=<misc>), first entry = the asset's identity.
// Two axes multiply, both real in the wild:
//
//   - Stem: AO2-Client loads misc/<misc>/chat before misc/<misc>/chatbox
//     (courtroom.cpp:3328-3330; modern packs ship chat.png).
//   - Case: AO2 reads a case-INSENSITIVE local filesystem, but web mirrors
//     are case-sensitive and split two ways — webAO-convention mirrors
//     lowercase every path (miku.pizza ships misc/yttd/chat.png for
//     chat=YTTD), while raw content-folder mirrors keep the authored case
//     (misc/HallA/...). Lowercase leads (it's this client's URL convention,
//     seg() everywhere); the authored spelling follows when it differs.
//
// Slashes survive as path separators (nested values like "VA-11/Jill" are
// common; PathEscape's %2F was a dead URL), spaces still escape per segment.
// Feed [0] to the scene and the rest to PrefetchChain — ≤ 4 entries, each
// miss 404-cached. // AssetType: Misc
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

// MusicURL returns the FULL music URL: AO music lists carry the extension in
// the track name, and tracks starting with http(s):// are direct URLs
// (AO2-Client get_music_path). Use Manager.PrefetchExact with this.
// AssetType: Music
func (u URLBuilder) MusicURL(track string) string {
	if isMusicURL(track) {
		return track
	}
	return u.origin + soundsMusic + escapePreservingSlashes(strings.ToLower(track))
}

// isMusicURL reports whether a track is a direct http(s):// music URL (a DJ
// /play link) rather than a server-relative track name. Such a URL is ALWAYS
// real music — never an area transfer — even though its audio extension may sit
// before a query string (Discord CDN links carry a signed ?ex=&is=&hm= suffix).
func isMusicURL(track string) bool {
	lower := strings.ToLower(track)
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://")
}

// escapePreservingSlashes escapes each path segment of a track that may
// contain subdirectories ("songs/intro.opus").
func escapePreservingSlashes(p string) string {
	parts := strings.Split(p, "/")
	for i, part := range parts {
		parts[i] = url.PathEscape(part)
	}
	return strings.Join(parts, "/")
}

// PositionScene maps an AO side/pos code to its background and desk part
// stems (AO2-Client path_functions get_pos_path table). Unknown positions
// use the 2.8 unique-position convention: <pos> / <pos>_overlay.
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

// ShoutName maps an objection modifier to its asset stem.
func ShoutName(objection int) string {
	switch objection {
	case 1:
		return "holdit"
	case 2:
		return "objection"
	case 3:
		return "takethat"
	case 4:
		return customShoutStem
	default:
		return ""
	}
}

// SpriteAssetType is the asset type for every courtroom sprite layer.
const SpriteAssetType = assets.AssetTypeCharSprite
