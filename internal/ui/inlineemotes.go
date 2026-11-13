package ui

// Inline emotes: known :shortcode: tokens display as colour emoji, but the text goes over the wire literally so other
// clients show the readable form. Unknown codes and stray colons stay as typed. The parser is
// courtroom.ExpandInlineEmotes, because the chatbox path in courtroom can't import ui.

// inlineEmotes maps a lower-case shortcode stem to its emoji, at most Unicode 11 so nothing renders as tofu.
// BMP symbols carry U+FE0F so they reach the colour-emoji font.
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

// inlineEmoteFor resolves a shortcode stem; a plain func, not a closure, so passing it as resolver allocates nothing.
func inlineEmoteFor(stem string) (string, bool) {
	e, ok := inlineEmotes[stem]
	return e, ok
}
