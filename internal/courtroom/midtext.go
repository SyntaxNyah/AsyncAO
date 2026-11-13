package courtroom

import "strings"

// Mid-text markers play an emote or sound mid-message; a typed :stem: becomes one on send and travels literally:
//
//	<e:<pre>:<emote>:<sfx>>   a mid-text emote  (pre/sfx "-" or "" = none)
//	<s:<sfx>>                 a mid-text sound only
//
// AsyncAO strips these with StripChatMarkup; others show the text. Fields must not contain '<', '>' or ':'.

const (
	midEmoteOpen = "<e:"
	midSFXOpen   = "<s:"
	midNone      = "-"
)

type MidEmote struct {
	Pre   string // preanim name ("" = none)
	Emote string // emote name
	SFX   string // sound name ("" = none)
}

type MidMark struct {
	Emote *MidEmote // non-nil for an <e:...> marker
	SFX   string    // non-empty for an <s:...> marker
}

func EncodeMidEmote(pre, emote, sfx string) string {
	if pre == "" {
		pre = midNone
	}
	if sfx == "" {
		sfx = midNone
	}
	return midEmoteOpen + pre + ":" + emote + ":" + sfx + ">"
}

func EncodeMidSFX(sfx string) string { return midSFXOpen + sfx + ">" }

// cleanNone maps the wire's "-" none value to "".
func cleanNone(s string) string {
	if s == midNone {
		return ""
	}
	return s
}

// readMidFieldRunes reads a field up to ':' or '>', returning the index past it; ok=false leaves the marker literal.
func readMidFieldRunes(rs []rune, i int) (string, int, bool) {
	j := i
	for j < len(rs) {
		switch rs[j] {
		case ':', '>':
			return string(rs[i:j]), j + 1, true
		case '<':
			return "", 0, false
		}
		j++
	}
	return "", 0, false
}

// parseMidEmoteRunes parses an <e:pre:emote:sfx> marker at rs[i], returning the mark and runes consumed.
func parseMidEmoteRunes(rs []rune, i int) (MidMark, int, bool) {
	const prefixLen = 3 // "<e:"
	if i+prefixLen >= len(rs) || rs[i] != '<' || rs[i+1] != 'e' || rs[i+2] != ':' {
		return MidMark{}, 0, false
	}
	pre, j, ok := readMidFieldRunes(rs, i+prefixLen)
	if !ok {
		return MidMark{}, 0, false
	}
	emote, j, ok := readMidFieldRunes(rs, j)
	if !ok {
		return MidMark{}, 0, false
	}
	sfx, j, ok := readMidFieldRunes(rs, j)
	if !ok {
		return MidMark{}, 0, false
	}
	return MidMark{Emote: &MidEmote{Pre: cleanNone(pre), Emote: emote, SFX: cleanNone(sfx)}}, j - i, true
}

func parseMidSFXRunes(rs []rune, i int) (MidMark, int, bool) {
	const prefixLen = 3 // "<s:"
	if i+2 >= len(rs) || rs[i] != '<' || rs[i+1] != 's' || rs[i+2] != ':' {
		return MidMark{}, 0, false
	}
	sfx, j, ok := readMidFieldRunes(rs, i+prefixLen)
	if !ok {
		return MidMark{}, 0, false
	}
	return MidMark{SFX: cleanNone(sfx)}, j - i, true
}

// ScanMidText returns the markers in order; ok=false with nil marks when none, allocating nothing.
func ScanMidText(message string) (marks []MidMark, ok bool) {
	if !strings.ContainsRune(message, '<') {
		return nil, false
	}
	rs := []rune(message)
	for i := 0; i < len(rs); i++ {
		if rs[i] != '<' {
			continue
		}
		if m, n, ok := parseMidEmoteRunes(rs, i); ok {
			marks = append(marks, m)
			i += n - 1
			continue
		}
		if m, n, ok := parseMidSFXRunes(rs, i); ok {
			marks = append(marks, m)
			i += n - 1
		}
	}
	return marks, len(marks) > 0
}

// StripMidText removes every marker; zero-alloc when none is present.
func StripMidText(message string) string {
	if !strings.ContainsRune(message, '<') {
		return message
	}
	rs := []rune(message)
	var b strings.Builder
	last := 0
	changed := false
	for i := 0; i < len(rs); {
		if rs[i] != '<' {
			i++
			continue
		}
		if _, n, ok := parseMidEmoteRunes(rs, i); ok {
			b.WriteString(string(rs[last:i]))
			i += n
			last = i
			changed = true
			continue
		}
		if _, n, ok := parseMidSFXRunes(rs, i); ok {
			b.WriteString(string(rs[last:i]))
			i += n
			last = i
			changed = true
			continue
		}
		i++
	}
	if !changed {
		return message
	}
	b.WriteString(string(rs[last:]))
	return b.String()
}

// isMidStemChar reports a byte allowed inside a :stem:; ':' is excluded so the closing colon ends the scan.
func isMidStemChar(b byte) bool {
	return isShortcodeChar(b) || (b >= 'A' && b <= 'Z')
}

// ExpandMidTextShortcodes replaces resolved :stem: tokens with markers; allocation-free unless it substitutes.
func ExpandMidTextShortcodes(text string, resolve func(stem string) (MidEmote, bool)) string {
	if resolve == nil || !strings.ContainsRune(text, ':') {
		return text
	}
	var b strings.Builder
	started := false
	last, i := 0, 0
	for i < len(text) {
		if text[i] != ':' {
			i++
			continue
		}
		j := i + 1
		for j < len(text) && j-(i+1) < maxShortcodeLen && isMidStemChar(text[j]) {
			j++
		}
		if j < len(text) && j > i+1 && text[j] == ':' {
			if e, ok := resolve(text[i+1 : j]); ok {
				if !started {
					b.Grow(len(text))
					started = true
				}
				b.WriteString(text[last:i])
				b.WriteString(EncodeMidEmote(e.Pre, e.Emote, e.SFX))
				i = j + 1
				last = i
				continue
			}
		}
		i++
	}
	if !started {
		return text
	}
	b.WriteString(text[last:])
	return b.String()
}
