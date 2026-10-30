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

// defaultFormatOrders is the entire probe list per type while fallbacks are off, so a cold asset costs one probe.
// Misc has two: chatbox skins have no extensions.json key and one mirror can mix chat.png with chatbox.webp.
// There is no ".webp.animated": animation is the VP8X ANIM flag, detected at decode time.
var defaultFormatOrders = map[string][]string{
	TypeCharIcon:    {ExtPNG},
	TypeCharSprite:  {ExtWebP},
	TypeBackground:  {ExtWebP},
	TypeMisc:        {ExtPNG, ExtWebP}, // png = AO2's stock misc convention; webp = the modern packs
	TypeDeskOverlay: {ExtWebP},
	TypeShoutBubble: {ExtWebP},
	TypeSFX:         {ExtOpus},
	TypeMusic:       {ExtOpus},
	TypeBlip:        {ExtOpus},
	TypeEmoteButton: {ExtWebP},
	// Effect packs mix .webp and .png with no extensions.json key, so the learned list and promote limit probes.
	// .webp is get_image_suffix's first choice.
	TypeEffect: {ExtWebP},
}

// legacyFallbackChains is appended, in order and deduplicated, to the format order when fallbacks are on for a type.
var legacyFallbackChains = map[string][]string{
	TypeCharIcon:    {ExtWebP},
	TypeCharSprite:  {ExtAPNG, ExtGIF, ExtPNG},
	TypeBackground:  {ExtAPNG, ExtGIF, ExtPNG},
	TypeDeskOverlay: {ExtAPNG, ExtGIF, ExtPNG},
	TypeShoutBubble: {ExtAPNG, ExtGIF, ExtPNG},
	TypeMisc:        {ExtAPNG, ExtGIF}, // png + webp are both in the default order already
	TypeSFX:         {ExtOgg, ExtWAV, ExtMP3},
	TypeMusic:       {ExtOgg, ExtMP3},
	TypeBlip:        {ExtOgg, ExtWAV, ExtMP3},
	// Legacy packs ship PNG buttons; APNG/GIF cover animated button packs.
	TypeEmoteButton: {ExtAPNG, ExtGIF, ExtPNG},
	// get_image_suffix's exact order (text_file_functions.cpp:381-404).
	TypeEffect: {ExtAPNG, ExtGIF, ExtPNG},
}

// DefaultFormatOrder returns a copy of a type's probe list, or nil for an unknown name.
func DefaultFormatOrder(typeName string) []string {
	return cloneStrings(defaultFormatOrders[typeName])
}

// LegacyFallbackChain returns a copy of a type's legacy fallback chain.
func LegacyFallbackChain(typeName string) []string {
	return cloneStrings(legacyFallbackChains[typeName])
}

// defaultAssetTypes builds the per-type table with fallbacks off and default format orders.
func defaultAssetTypes() map[string]AssetTypePrefs {
	m := make(map[string]AssetTypePrefs, len(TypeNames))
	for _, name := range TypeNames {
		m[name] = AssetTypePrefs{
			FormatOrder:      DefaultFormatOrder(name),
			FallbacksEnabled: false,
		}
	}
	return m
}

func cloneStrings(s []string) []string {
	if s == nil {
		return nil
	}
	out := make([]string, len(s))
	copy(out, s)
	return out
}
