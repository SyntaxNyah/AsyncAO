// Package courtroom implements the AO2 courtroom: session handshake, message
// lifecycle (shout → preanim → talk → idle), pairing, typewriter pacing, and
// the URL conventions assets live under. Everything here is SDL-free and
// headless-testable; rendering lives in render.go behind the cgo build tag.
package courtroom

import (
	"net/url"
	"strconv"
	"strings"

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
	origin string
}

// NewURLBuilder normalizes the origin (exactly one trailing slash).
func NewURLBuilder(origin string) URLBuilder {
	return URLBuilder{origin: strings.TrimRight(origin, "/") + "/"}
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

// seg escapes one path segment, webAO-style: lowercased (AO asset hosts are
// case-sensitive; packs ship lowercase) and percent-encoded exactly like
// encodeURI (parentheses and friends stay literal).
func seg(name string) string {
	return encodeURIRestores.Replace(url.PathEscape(strings.ToLower(name)))
}

// CharIcon returns the char-select icon base. // AssetType: CharIcon
func (u URLBuilder) CharIcon(character string) string {
	return u.origin + charactersDir + seg(character) + "/" + charIconStem
}

// Emote returns a character sprite base for the given kind.
// AssetType: CharSprite
func (u URLBuilder) Emote(character, emote string, kind EmoteKind) string {
	prefix := ""
	switch kind {
	case EmoteIdle:
		prefix = idlePrefix
	case EmoteTalk:
		prefix = talkPrefix
	}
	return u.origin + charactersDir + seg(character) + "/" + seg(prefix+emote)
}

// EmoteBare returns the unprefixed sprite base — the second spelling
// AO2-Client probes for idle/talk sprites (CharLayer::load_image tries
// the "(a)"/"(b)" path, then the bare one; many packs ship only bare
// files like "1.webp"). Pass as the fallback to PrefetchWithFallback.
// AssetType: CharSprite
func (u URLBuilder) EmoteBare(character, emote string) string {
	return u.origin + charactersDir + seg(character) + "/" + seg(emote)
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
	return u.origin + charactersDir + seg(character) + "/" + emotionsDir + emoteButtonStem + strconv.Itoa(n) + state
}

// ShoutBubble returns the per-character shout bubble base ("holdit_bubble",
// custom shouts use "custom"). // AssetType: ShoutBubble
func (u URLBuilder) ShoutBubble(character, shoutName string, custom bool) string {
	stem := shoutName + shoutBubbleSuff
	if custom {
		stem = customShoutStem
	}
	return u.origin + charactersDir + seg(character) + "/" + seg(stem)
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
	return u.origin + charactersDir + seg(character) + "/" + customObjDir + seg(name)
}

// DefaultShoutBubble returns the misc/default fallback bubble base.
// AssetType: ShoutBubble
func (u URLBuilder) DefaultShoutBubble(shoutName string) string {
	return u.origin + miscDefaultDir + seg(shoutName+shoutBubbleSuff)
}

// ShoutSFX returns the per-character shout sound base. // AssetType: SFX
func (u URLBuilder) ShoutSFX(character, shoutName string) string {
	return u.origin + charactersDir + seg(character) + "/" + seg(shoutName)
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

// SFX returns a general sound base. // AssetType: SFX
func (u URLBuilder) SFX(name string) string {
	return u.origin + soundsGeneral + seg(name)
}

// Blip returns a blip set base. // AssetType: Blip
func (u URLBuilder) Blip(name string) string {
	return u.origin + soundsBlips + seg(name)
}

// MusicURL returns the FULL music URL: AO music lists carry the extension in
// the track name, and tracks starting with http(s):// are direct URLs
// (AO2-Client get_music_path). Use Manager.PrefetchExact with this.
// AssetType: Music
func (u URLBuilder) MusicURL(track string) string {
	lower := strings.ToLower(track)
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
		return track
	}
	return u.origin + soundsMusic + escapePreservingSlashes(lower)
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
