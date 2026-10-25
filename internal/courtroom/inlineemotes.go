package courtroom

import "strings"

// Inline emotes (#18): Discord-style :shortcode: tokens render as a colour emoji. The
// shortcode text travels LITERALLY on the wire (no marker, no expansion before send), so
// AO2 / webAO show the readable ":joy:" while AsyncAO substitutes the emoji at display time.
//
// This is just the SDL-free PARSER. The shortcode→emoji REGISTRY lives in the ui package
// (the user-facing vocabulary); the parser takes a resolver so both the IC log (ui) and the
// live chatbox (courtroom begin) share one implementation — ui imports courtroom, not the
// other way round, so the parser must live here for the chatbox to reach it.

// maxShortcodeLen bounds the :name: scan so a lone ':' in a long line can't make the parser
// wander far looking for a closing colon. Every built-in stem is well under this.
const maxShortcodeLen = 24

// isShortcodeChar reports a byte allowed inside a :stem: (lower-case ASCII letters, digits,
// and the joiners Discord allows). Excludes ':' so the closing colon ends the scan.
func isShortcodeChar(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= '0' && b <= '9') || b == '_' || b == '+' || b == '-'
}

// ExpandInlineEmotes replaces every :shortcode: that resolve maps to a replacement, leaving
// unknown tokens and any other text untouched. It is allocation-free unless it actually
// substitutes: a string with no ':' returns immediately, and a string whose colons never
// form a resolved shortcode returns the original (the builder is only grown on the first real
// match). resolve must be a plain func value (not a closure over per-call state) to stay
// alloc-free. Known-shortcode-only substitution means a URL's "http://" or a "12:30" is never
// touched.
func ExpandInlineEmotes(s string, resolve func(stem string) (string, bool)) string {
	if resolve == nil || !strings.ContainsRune(s, ':') {
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
			if e, ok := resolve(s[i+1 : j]); ok {
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
		i++ // a ':' that didn't open a resolved shortcode — leave it literal
	}
	if !started {
		return s
	}
	b.WriteString(s[last:])
	return b.String()
}
