package config

// Canonical asset-type names: keys of AssetTypes and learned-format entries ("<host>|<type name>").
const (
	TypeCharIcon    = "CharIcon"
	TypeCharSprite  = "CharSprite"
	TypeBackground  = "Background"
	TypeDeskOverlay = "DeskOverlay"
	TypeShoutBubble = "ShoutBubble"
	TypeMisc        = "Misc"
	TypeSFX         = "SFX"
	TypeMusic       = "Music"
	TypeBlip        = "Blip"
	// TypeEmoteButton is the emote-picker art (characters/<char>/emotions/button<N>_off|_on).
	TypeEmoteButton = "EmoteButton"
	// TypeEffect is AO2 screen-effect art (effects/<name> in a theme, misc/<folder>/<name> at the origin).
	TypeEffect = "Effect"
)

// TypeNames lists every asset-type name in internal/assets.AssetType enum order.
var TypeNames = []string{
	TypeCharIcon,
	TypeCharSprite,
	TypeBackground,
	TypeDeskOverlay,
	TypeShoutBubble,
	TypeMisc,
	TypeSFX,
	TypeMusic,
	TypeBlip,
	TypeEmoteButton,
	TypeEffect,
}

// Extensions carry the leading dot so candidate URLs are plain concatenation.
const (
	ExtPNG  = ".png"
	ExtWebP = ".webp"
	ExtAPNG = ".apng"
	ExtGIF  = ".gif"
	ExtJPG  = ".jpg"
	ExtAVIF = ".avif"
	ExtOpus = ".opus"
	ExtOgg  = ".ogg"
	ExtWAV  = ".wav"
	ExtMP3  = ".mp3"
)

// OptionalImageFormats are the opt-in probe formats Settings offers, all off by default (zero-fallback policy).
var OptionalImageFormats = []string{ExtWebP, ExtAVIF, ExtAPNG, ExtGIF, ExtPNG, ExtJPG}

func cloneStrings(s []string) []string {
	if s == nil {
		return nil
	}
	out := make([]string, len(s))
	copy(out, s)
	return out
}
