package courtroom

import "github.com/SyntaxNyah/AsyncAO/internal/helper/text"

// WireProfile is the profile part sent on the zero-width channel, only on change; larger fields stay local.
type WireProfile struct {
	Pronouns string
	Tag      string
}

const (
	// Field caps in bytes, deliberately tight: others blip on each invisible rune, and it eats the IC length limit.
	wireProfilePronounsMax = 16
	wireProfileTagMax      = 24

	// profileFrameMagic is the first byte, distinct from spriteStyleVersion (1); an unknown version decodes to nothing.
	profileFrameMagic  = 0x70
	profileWireVersion = 1
)

// sanitized strips codec runes, so a pasted invisible can't corrupt the frame, and clamps on a UTF-8 boundary.
func (p WireProfile) sanitized() WireProfile {
	return WireProfile{
		Pronouns: text.TruncateBytes(stripZeroWidth(p.Pronouns), wireProfilePronounsMax),
		Tag:      text.TruncateBytes(stripZeroWidth(p.Tag), wireProfileTagMax),
	}
}

func (p WireProfile) Empty() bool {
	q := p.sanitized()
	return q.Pronouns == "" && q.Tag == ""
}

// payloadBytes packs magic, version, then each field with a one-byte length prefix.
func (p WireProfile) payloadBytes() []byte {
	q := p.sanitized()
	b := make([]byte, 0, 4+len(q.Pronouns)+len(q.Tag))
	b = append(b, profileFrameMagic, profileWireVersion)
	b = append(b, byte(len(q.Pronouns)))
	b = append(b, q.Pronouns...)
	b = append(b, byte(len(q.Tag)))
	b = append(b, q.Tag...)
	return b
}

// EncodeMarker always builds a marker and exists for codec tests; the live send path uses EncodeChangeMarker.
func (p WireProfile) EncodeMarker() string {
	if p.Empty() {
		return ""
	}
	return packZeroWidth(p.payloadBytes())
}

// EncodeChangeMarker returns "" when unchanged from prev, the new marker, or a clear (empty profile) when turned off.
func (p WireProfile) EncodeChangeMarker(prev WireProfile) string {
	cur := p.sanitized()
	if cur == prev.sanitized() {
		return ""
	}
	// Active gets its marker; empty gets a clear (payloadBytes is magic,version,0,0).
	return packZeroWidth(cur.payloadBytes())
}

// DecodeProfileMarker finds a profile frame among any others; ok=true with an empty profile is a clear.
func DecodeProfileMarker(text string) (WireProfile, bool) {
	if !hasMarker(text) {
		return WireProfile{}, false
	}
	for _, fr := range scanZeroWidthFrames(text) {
		if len(fr) >= 4 && fr[0] == profileFrameMagic && fr[1] == profileWireVersion {
			return parseProfilePayload(fr)
		}
	}
	return WireProfile{}, false
}

// maxRememberedProfiles bounds the per-character profile memory (hard rule 4); a clear frees its entry.
const maxRememberedProfiles = 512

// rememberProfile keys by raw character name so the lookup stays allocation-free; empty clears, blank is skipped.
func (c *Courtroom) rememberProfile(charName string, p WireProfile) {
	if charName == "" {
		return
	}
	if p.Empty() {
		delete(c.profileByName, charName)
		return
	}
	if c.profileByName == nil {
		c.profileByName = map[string]WireProfile{}
	}
	if _, had := c.profileByName[charName]; !had && len(c.profileByName) >= maxRememberedProfiles {
		return // at the cap — don't admit a new character
	}
	c.profileByName[charName] = p
}

// RemoteProfile returns a character's last transmitted profile; allocation-free, safe per row per frame.
func (c *Courtroom) RemoteProfile(charName string) (WireProfile, bool) {
	p, ok := c.profileByName[charName]
	return p, ok
}

// parseProfilePayload re-clamps fields so a peer can't push oversize text; truncation yields ok=false.
func parseProfilePayload(b []byte) (WireProfile, bool) {
	i := 2 // past magic + version (already checked by the caller)
	read := func() (string, bool) {
		if i >= len(b) {
			return "", false
		}
		n := int(b[i])
		i++
		if i+n > len(b) {
			return "", false
		}
		s := string(b[i : i+n])
		i += n
		return s, true
	}
	pron, ok1 := read()
	tag, ok2 := read()
	if !ok1 || !ok2 {
		return WireProfile{}, false
	}
	return WireProfile{
		Pronouns: text.TruncateBytes(stripZeroWidth(pron), wireProfilePronounsMax),
		Tag:      text.TruncateBytes(stripZeroWidth(tag), wireProfileTagMax),
	}, true
}
