package courtroom

import "strings"

// :shortcode: tokens travel literally and become emoji at display; the parser is here because ui imports courtroom.

// maxShortcodeLen stops a lone ':' from scanning far along a long line for a closing colon.
const maxShortcodeLen = 24

// isShortcodeChar reports a byte allowed inside a :stem:; ':' is excluded so the closing colon ends the scan.
func isShortcodeChar(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= '0' && b <= '9') || b == '_' || b == '+' || b == '-'
}

// ExpandInlineEmotes replaces resolved :shortcode: tokens; allocation-free unless it substitutes.
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
