package courtroom

import "strings"

// Mid-text emotes (#130) — a marker embedded in the IC Message text that plays an
// emote (and optionally a sound) at that point in the message, instead of the KFO
// "just send two packets" hack that silently depends on the queue. The user types a
// Discord-style :stem: (or right-clicks an emote in the grid); on send the shortcode
// is converted to the INTERNAL marker, which travels literally on the wire:
//
//	<e:<pre>:<emote>:<sfx>>   a mid-text emote  (pre/sfx "-" or "" = none)
//	<s:<sfx>>                 a mid-text sound only
//
// AsyncAO strips these markers from the displayed text (typewriter.go, in lock-step
// with StripChatMarkup) and renders the sprite / plays the sound at that point in the
// crawl; non-AsyncAO clients show the readable "<e:...>" text. Fields are matched
// case-sensitively against char.ini and must not contain '<', '>' or ':' (those are
// the marker's own delimiters).

const (
	midEmoteOpen = "<e:"
	midSFXOpen   = "<s:"
	midNone      = "-"
)

// MidEmote is one parsed mid-text emote marker.
type MidEmote struct {
	Pre   string // preanim name ("" = none)
	Emote string // emote name
	SFX   string // sound name ("" = none)
}

// MidMark is one marker found in a message: either an emote or a bare sound.
type MidMark struct {
	Emote *MidEmote // non-nil for an <e:...> marker
	SFX   string    // non-empty for an <s:...> marker
}

// EncodeMidEmote renders a mid-text emote marker.
func EncodeMidEmote(pre, emote, sfx string) string {
	if pre == "" {
		pre = midNone
	}
	if sfx == "" {
		sfx = midNone
	}
	return midEmoteOpen + pre + ":" + emote + ":" + sfx + ">"
}

// EncodeMidSFX renders a mid-text sound-only marker.
func EncodeMidSFX(sfx string) string { return midSFXOpen + sfx + ">" }

// cleanNone maps the wire's "-" none-sentinel back to the empty string.
func cleanNone(s string) string {
	if s == midNone {
		return ""
	}
	return s
}

// readMidFieldRunes reads a marker field starting at rs[i], up to the next ':' or
// '>'. It returns the field text, the index just PAST the delimiter, and ok=false
// when the field runs off the end without a delimiter or contains a '<' (a stray
// marker opener — malformed, so the whole thing stays literal).
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

// parseMidEmoteRunes parses an <e:pre:emote:sfx> marker whose '<' is at rs[i]. It
// returns the mark and the number of runes consumed (through the closing '>').
func parseMidEmoteRunes(rs []rune, i int) (MidMark, int, bool) {
	if i+3 >= len(rs) || rs[i] != '<' || rs[i+1] != 'e' || rs[i+2] != ':' {
		return MidMark{}, 0, false
	}
	pre, j, ok := readMidFieldRunes(rs, i+3)
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

// parseMidSFXRunes parses an <s:sfx> marker whose '<' is at rs[i].
func parseMidSFXRunes(rs []rune, i int) (MidMark, int, bool) {
	if i+2 >= len(rs) || rs[i] != '<' || rs[i+1] != 's' || rs[i+2] != ':' {
		return MidMark{}, 0, false
	}
	sfx, j, ok := readMidFieldRunes(rs, i+3)
	if !ok {
		return MidMark{}, 0, false
	}
	return MidMark{SFX: cleanNone(sfx)}, j - i, true
}

// ScanMidText returns the markers embedded in message in left-to-right order.
// ok=false (marks nil) when there are none, so the common case allocates nothing.
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

// StripMidText removes every mid-text marker from message, returning the visible
// text. Zero-alloc when no marker is present.
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

// isMidStemChar reports a byte allowed inside a :stem: mid-text shortcode (letters
// of either case, digits, and the usual joiners). Excludes ':' so the closing colon
// ends the scan.
func isMidStemChar(b byte) bool {
	return isShortcodeChar(b) || (b >= 'A' && b <= 'Z')
}

// ExpandMidTextShortcodes replaces every :stem: that resolve maps to a MidEmote with
// its encoded <e:...> marker, leaving unknown tokens (and all other text) untouched.
// It is allocation-free unless it actually substitutes. This is the send-side half of
// #130: what the user types stays readable, and the wire carries the internal marker.
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
