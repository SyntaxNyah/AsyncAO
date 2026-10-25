package ui

// Inline emotes (#18): Discord-style :shortcode: tokens render as a colour emoji, in BOTH
// the IC log and the live chatbox. The shortcode text travels LITERALLY on the wire (no
// marker, no expansion before send), so AO2 / webAO show the readable ":joy:" while AsyncAO
// substitutes the emoji at display time — it routes through the existing colour-emoji path,
// so there's no new render machinery. Substitution is KNOWN-shortcode-only, so a stray colon
// (a URL's "http://", a "12:30") or an unknown ":foo:" is left exactly as typed.

// inlineEmotes maps a shortcode stem (between the colons, lower-case) to its emoji. A
// curated, widely-shipped set (≤ Unicode 11, like the picker) so nothing renders as tofu; a
// BMP symbol carries its U+FE0F variation selector so it reaches the colour-emoji font.
var inlineEmotes = map[string]string{
	"joy":        "😂",
	"sob":        "😭",
	"smile":      "🙂",
	"grin":       "😀",
	"wink":       "😉",
	"heart_eyes": "😍",
	"sunglasses": "😎",
	"thinking":   "🤔",
	"cry":        "😢",
	"rage":       "😡",
	"sweat":      "😅",
	"smirk":      "😏",
	"pleading":   "🥺",
	"eyes":       "👀",
	"skull":      "💀",
	"heart":      "❤️",
	"fire":       "🔥",
	"100":        "💯",
	"tada":       "🎉",
	"clap":       "👏",
	"thumbsup":   "👍",
	"thumbsdown": "👎",
	"pray":       "🙏",
	"wave":       "👋",
	"ok":         "👌",
	"muscle":     "💪",
	"star":       "⭐️",
	"scales":     "⚖️",
	"gavel":      "🔨",
	"objection":  "❗️",
	"question":   "❓️",
}

// inlineEmoteFor resolves a shortcode stem to its emoji (ok=false when unknown). A plain
// package func (not a closure), so passing it as courtroom.ExpandInlineEmotes' resolver — on
// every IC log line and every chatbox message — allocates nothing.
func inlineEmoteFor(stem string) (string, bool) {
	e, ok := inlineEmotes[stem]
	return e, ok
}
