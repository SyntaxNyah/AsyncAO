package courtroom

import "unicode/utf8"

// WireProfile is the small, cross-client slice of a character profile (#101 slice 2)
// that travels between AsyncAO clients: just the identity glance — pronouns and a
// one-line tagline. It rides the SAME invisible zero-width channel as the sprite style
// (a marker appended to an IC message), so standard AO2/webAO clients render nothing
// and are unaffected. The larger profile fields (bio, art image, theme song) stay
// local: they're too big for the message channel and the roster card doesn't show art
// yet. The card's title uses the speaker's live showname, so the name isn't sent here.
//
// Like the sprite style it is transmitted ONLY on change (a profile rarely changes), so
// the invisible run rides at most the speaker's first post-join message, not every line.
type WireProfile struct {
	Pronouns string
	Tag      string
}

const (
	// Wire field caps in BYTES (not runes) — the cap bounds the zero-width run's length
	// on the wire. Kept DELIBERATELY tight: other clients typewriter the invisible run and
	// blip on each character, and the payload also eats into the server's IC
	// message-length limit (a tsuserver default is ~256 chars). The codec packs 3 bits
	// per invisible rune, so the worst case here (16+24 fields + ~4 overhead = 44 bytes)
	// is ~117 invisible runes on ONE message; send-on-change means it rides at most a
	// player's first post-join message, not every line. The local profile fields can be
	// longer — only this subset transmits, clamped. (Tightening these shrinks the blip
	// and the length cost; loosening them grows both.)
	wireProfilePronounsMax = 16
	wireProfileTagMax      = 24

	// profileFrameMagic is the first payload byte of a profile frame, deliberately
	// distinct from spriteStyleVersion (1) so the shared zero-width frame scanner tells
	// the two codecs' frames apart by their leading byte: a sprite-style decoder reads a
	// profile frame as "not my version" and ignores it (benign), and the profile decoder
	// skips style frames. profileWireVersion follows, so a later format change is
	// detectable (an unknown version decodes to no profile).
	profileFrameMagic  = 0x70
	profileWireVersion = 1
)

// sanitized returns the profile with each field stripped of any codec runes (so a
// pasted invisible can't corrupt the frame) and clamped to its wire byte budget on a
// UTF-8 boundary.
func (p WireProfile) sanitized() WireProfile {
	return WireProfile{
		Pronouns: clampWireBytes(stripZeroWidth(p.Pronouns), wireProfilePronounsMax),
		Tag:      clampWireBytes(stripZeroWidth(p.Tag), wireProfileTagMax),
	}
}

// Empty reports whether the (sanitized) profile carries nothing to transmit.
func (p WireProfile) Empty() bool {
	q := p.sanitized()
	return q.Pronouns == "" && q.Tag == ""
}

// clampWireBytes truncates s to at most n bytes without splitting a UTF-8 rune.
func clampWireBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// payloadBytes packs the profile into its wire form: magic, version, then each field as
// a one-byte length prefix followed by its UTF-8 bytes (fields are byte-capped well
// under 255).
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

// EncodeMarker returns the invisible zero-width run that carries this profile, to append
// to an outgoing IC message. "" for an empty profile (nothing to send).
func (p WireProfile) EncodeMarker() string {
	if p.Empty() {
		return ""
	}
	return packZeroWidth(p.payloadBytes())
}

// EncodeChangeMarker returns the run to append to an outgoing message given prev (the
// profile this client last TRANSMITTED): "" when nothing changed, the new marker when
// it changed, or a CLEAR marker (an empty profile, which tells receivers to drop this
// speaker's card) when a profile was turned off. Mirrors the sprite-style send-on-change
// so the invisible run rides only the change message — clients track the last-sent
// profile and pass it here.
func (p WireProfile) EncodeChangeMarker(prev WireProfile) string {
	cur := p.sanitized()
	if cur == prev.sanitized() {
		return ""
	}
	// Active → its marker; empty → a clear (payloadBytes is magic,version,0,0).
	return packZeroWidth(cur.payloadBytes())
}

// DecodeProfileMarker pulls a WireProfile out of an IC message's text, if one is
// present. It scans every zero-width frame for the profile magic, so it coexists with a
// sprite-style frame on the same message. ok=false means "no profile frame" (or a
// short/corrupt one — benign, no card); ok=true with an empty profile is a CLEAR, which
// the receiver uses to drop this speaker's card.
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

// HasProfileMarker reports whether text carries a profile frame specifically.
func HasProfileMarker(text string) bool {
	_, ok := DecodeProfileMarker(text)
	return ok
}

// --- per-character profile memory (send-on-change) ---------------------------

// maxRememberedProfiles bounds the per-character profile memory (#101, hard rule §17.4)
// — like the style memory, capped so a malformed stream can't grow the map without
// bound. A clear frees its entry, so it stays near the active-profile count.
const maxRememberedProfiles = 512

// rememberProfile records a speaker's transmitted profile from an explicit marker,
// keyed by their bare character name. An EMPTY profile is a clear — it frees the entry.
// A blank name (system / spectator, no character) is not stored. The key is the raw
// character name (no case-folding) so the player-list lookup stays allocation-free.
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

// RemoteProfile returns a character's last transmitted WireProfile (ok=false when none).
// The player list looks it up by the row's character name. Allocation-free (a plain map
// read) so it can run per row per frame.
func (c *Courtroom) RemoteProfile(charName string) (WireProfile, bool) {
	p, ok := c.profileByName[charName]
	return p, ok
}

// parseProfilePayload reads the length-prefixed fields after the magic+version header.
// A truncated field yields ok=false (benign). Fields are re-clamped on decode so a peer
// can't push oversize text into the card.
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
		Pronouns: clampWireBytes(stripZeroWidth(pron), wireProfilePronounsMax),
		Tag:      clampWireBytes(stripZeroWidth(tag), wireProfileTagMax),
	}, true
}
