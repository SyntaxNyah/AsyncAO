package courtroom

// Status is a presence flag, sent on the zero-width IC channel only on change, so others see it at the next line.
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
	// statusFrameMagic is the frame's first byte, distinct from sprite style (1) and profile (0x70).
	statusFrameMagic  = 0x71
	statusWireVersion = 1
)

// Valid reports whether s is a known status; an unknown byte from a newer peer reads as None.
func (s Status) Valid() bool { return s < StatusCount }

// EncodeStatusChangeMarker returns "" when unchanged from prev, else the marker; StatusNone encodes a clear.
func EncodeStatusChangeMarker(s, prev Status) string {
	if s == prev {
		return ""
	}
	return packZeroWidth([]byte{statusFrameMagic, statusWireVersion, byte(s)})
}

// DecodeStatusMarker finds a status frame among any others; ok=true with StatusNone is a clear.
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

// maxRememberedStatuses bounds the per-character status memory (hard rule 4); a clear frees its entry.
const maxRememberedStatuses = 512

// rememberStatus keys by raw character name (no case-fold) so the player-list lookup stays allocation-free.
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

// RemoteStatus returns a character's last transmitted status; allocation-free, safe per row per frame.
func (c *Courtroom) RemoteStatus(charName string) (Status, bool) {
	s, ok := c.statusByName[charName]
	return s, ok
}
