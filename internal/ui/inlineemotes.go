package ui

import "strings"

// Inline emotes (#18), slice 1: Discord-style :shortcode: tokens render as a colour emoji.
// The shortcode text travels LITERALLY on the wire (no marker, no expansion before send),
// so AO2 / webAO show the readable ":joy:" while AsyncAO substitutes the emoji at DISPLAY
// time — it routes through the existing colour-emoji label path, so there's no new render
// machinery here. This slice covers the IC LOG (plain rendered text); the live typewriter
// chatbox + custom (image-URL) emotes are later slices, because the chatbox path has to keep
// the per-rune reveal and the effect-span alignment consistent with the substitution.
//
// Substitution is KNOWN-shortcode-only: an unknown ":foo:" (or a stray colon like a URL's
// "http://" or a "12:30") is left exactly as typed, so the feature can't garble normal text.

const (
	// maxShortcodeLen bounds the :name: scan so a lone ':' in a long line can't make the
	// parser wander far looking for a closing colon. The longest built-in stem is well under.
	maxShortcodeLen = 24
)

// inlineEmotes maps a shortcode stem (between the colons, lower-case) to its emoji. A
// curated, widely-shipped set (≤ Unicode 11, like the picker) so nothing renders as tofu; a
// BMP symbol carries its U+FE0F variation selector so it reaches the colour-emoji font.
// Extend freely — the keys are the user-facing vocabulary.
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

// inlineEmoteFor resolves a shortcode stem to its emoji (ok=false when unknown).
func inlineEmoteFor(stem string) (string, bool) {
	e, ok := inlineEmotes[stem]
	return e, ok
}

// isShortcodeChar reports a byte allowed inside a :stem: (lower-case ASCII letters, digits,
// and the joiners Discord allows). Excludes ':' so the closing colon ends the scan.
func isShortcodeChar(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= '0' && b <= '9') || b == '_' || b == '+' || b == '-'
}

// expandInlineEmotes replaces every KNOWN :shortcode: in s with its emoji, leaving unknown
// tokens and any other text untouched. It is allocation-free unless it actually substitutes:
// a string with no ':' returns immediately, and a string whose colons never form a known
// shortcode returns the original (the builder is only grown on the first real match). Pure,
// so the log-line builders that call it stay testable.
func expandInlineEmotes(s string) string {
	if !strings.ContainsRune(s, ':') {
		return s
	}
	var b strings.Builder
	started := false
	last, i := 0, 0
	for i < len(s) {
		if s[i] != ':' {
			i++
			continue
		}
		j := i + 1
		for j < len(s) && j-(i+1) < maxShortcodeLen && isShortcodeChar(s[j]) {
			j++
		}
		if j < len(s) && j > i+1 && s[j] == ':' {
			if e, ok := inlineEmoteFor(s[i+1 : j]); ok {
				if !started {
					b.Grow(len(s))
					started = true
				}
				b.WriteString(s[last:i])
				b.WriteString(e)
				i = j + 1
				last = i
				continue
			}
		}
		i++ // a ':' that didn't open a known shortcode — leave it literal
	}
	if !started {
		return s
	}
	b.WriteString(s[last:])
	return b.String()
}
