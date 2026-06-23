package courtroom

// Status is a player's cross-client presence flag (#M1) — AFK / Busy / Writing /
// LFRP — shown as a badge on the AsyncAO player list. It rides the SAME invisible
// zero-width IC channel as the sprite style and the character profile, told apart by a
// leading magic byte, so standard AO2/webAO clients see clean text. Transmitted ONLY on
// change (a status changes rarely), so the invisible run rides at most the message where
// you changed it. Like the profile, the channel is IC-only, so other players see your
// new status when you NEXT speak.
type Status uint8

const (
	StatusNone    Status = iota // no badge (default / a clear)
	StatusAFK                   // away from keyboard
	StatusBusy                  // do-not-disturb
	StatusWriting               // composing a post
	StatusLFRP                  // looking for RP
	StatusCount                 // number of statuses, for cycling
)

const (
	// statusFrameMagic is the first payload byte of a status frame, distinct from the
	// sprite-style version (1) and the profile magic (0x70) so the shared zero-width
	// frame scanner tells the three codecs' frames apart by their leading byte.
	statusFrameMagic  = 0x71
	statusWireVersion = 1
)

// Valid reports whether s is a known status (an unknown byte from a newer peer reads as
// None — benign).
func (s Status) Valid() bool { return s < StatusCount }

// EncodeStatusChangeMarker returns the zero-width run to append to an outgoing message
// given prev (the status last TRANSMITTED this session): "" when unchanged, else the
// marker for the new status. StatusNone encodes a clear (receivers drop the badge).
func EncodeStatusChangeMarker(s, prev Status) string {
	if s == prev {
		return ""
	}
	return packZeroWidth([]byte{statusFrameMagic, statusWireVersion, byte(s)})
}

// DecodeStatusMarker pulls a Status from a message's text if a status frame is present.
// It scans every zero-width frame for the status magic (so it coexists with a style /
// profile frame). ok=false means no status frame; ok=true with StatusNone is a CLEAR
// (drop the badge). A frame from a newer peer carrying an unknown status reads None.
func DecodeStatusMarker(text string) (Status, bool) {
	if !hasMarker(text) {
		return StatusNone, false
	}
	for _, fr := range scanZeroWidthFrames(text) {
		if len(fr) >= 3 && fr[0] == statusFrameMagic && fr[1] == statusWireVersion {
			s := Status(fr[2])
			if !s.Valid() {
				s = StatusNone
			}
			return s, true
		}
	}
	return StatusNone, false
}

// HasStatusMarker reports whether text carries a status frame specifically.
func HasStatusMarker(text string) bool {
	_, ok := DecodeStatusMarker(text)
	return ok
}

// --- per-character status memory (send-on-change) ----------------------------

// maxRememberedStatuses bounds the per-character status memory (hard rule §17.4). A
// clear (StatusNone) frees its entry, so it stays near the active-status count.
const maxRememberedStatuses = 512

// rememberStatus records a speaker's transmitted status, keyed by bare character name.
// StatusNone is a clear (frees the entry). A blank name is ignored. The key is the raw
// character name (no case-fold) so the player-list lookup stays allocation-free.
func (c *Courtroom) rememberStatus(charName string, s Status) {
	if charName == "" {
		return
	}
	if s == StatusNone {
		delete(c.statusByName, charName)
		return
	}
	if c.statusByName == nil {
		c.statusByName = map[string]Status{}
	}
	if _, had := c.statusByName[charName]; !had && len(c.statusByName) >= maxRememberedStatuses {
		return // at the cap — don't admit a new character
	}
	c.statusByName[charName] = s
}

// RemoteStatus returns a character's last transmitted status (ok=false when none). The
// player list looks it up by the row's character name; a plain map read, so it's
// allocation-free per row per frame.
func (c *Courtroom) RemoteStatus(charName string) (Status, bool) {
	s, ok := c.statusByName[charName]
	return s, ok
}
