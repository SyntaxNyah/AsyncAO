package config

// Canonical asset-type names. These strings are the single source of truth
// for keying AssetPreferences.AssetTypes and learned-format entries
// ("<host>|<type name>"). internal/assets maps its AssetType enum onto these
// names; a cross-package test asserts the two stay in sync.
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
	// TypeEmoteButton is the courtroom emote-picker art
	// (characters/<char>/emotions/button<N>_off|_on).
	TypeEmoteButton = "EmoteButton"
)

// TypeNames lists every canonical asset-type name, in the same order as the
// internal/assets.AssetType enum declares its constants.
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
}

// File extensions probed for assets. Extensions always carry the leading dot
// so candidate URLs are a plain string concatenation.
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

// OptionalImageFormats are the extensions the Settings UI offers as opt-in
// probe formats (all disabled by default — zero-fallback policy).
var OptionalImageFormats = []string{ExtWebP, ExtAVIF, ExtAPNG, ExtGIF, ExtPNG, ExtJPG}

// defaultFormatOrders is the zero-fallback probe list per asset type: with
// fallbacks disabled this is the *entire* probe list (spec §4). Every bulk
// type defaults to a single format, so a cold asset costs exactly one probe.
//
// Misc is the deliberate exception (two formats): chatbox skins have no
// extensions.json key and no per-host convention — ONE live mirror serves
// misc/yttd/chat.png beside misc/helltaker/chatbox.webp, so a single
// per-host format can never cover the type. It's also the rarest probe in
// the client (one URL per chat=-declaring character, then cached), so the
// extra candidate costs nothing measurable. Per-host learning still
// front-runs the order; a learned miss falls back to this full list within
// the same pass (spec §4's stale-learned re-probe).
//
// Note there is no ".webp.animated": animation is a property of the .webp
// payload (VP8X ANIM flag), detected at decode time, never a separate probe.
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
}

// legacyFallbackChains is appended (order preserved, deduplicated) to the
// configured format order when fallbacks are enabled for a type, globally or
// per-type (spec §4).
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
}

// DefaultFormatOrder returns a copy of the zero-fallback probe list for the
// given asset-type name, or nil for an unknown name.
func DefaultFormatOrder(typeName string) []string {
	return cloneStrings(defaultFormatOrders[typeName])
}

// LegacyFallbackChain returns a copy of the legacy chain appended when
// fallbacks are enabled for the given asset-type name.
func LegacyFallbackChain(typeName string) []string {
	return cloneStrings(legacyFallbackChains[typeName])
}

// defaultAssetTypes builds the per-type preference table with every known
// asset type present, fallbacks disabled, default format orders.
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
