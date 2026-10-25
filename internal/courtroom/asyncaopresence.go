package courtroom

// AsyncAO-user detection. Any received zero-width frame — sprite style, profile,
// status, effects, reaction, animated text — means that speaker is running an
// AsyncAO client, because AO2 / webAO never emit the channel. We record the
// speaker's character name so the player list can badge them, and so a future
// messaging feature knows who can receive cross-client messages.
//
// It's necessarily PASSIVE: a peer becomes known only AFTER they emit a marker
// (speak with a style/profile, react, …). There's no way to enumerate every
// client in the room without a presence beacon (extra traffic), so "who's on
// AsyncAO" fills in as people participate. Bounded; not cleared within a session
// (a client's identity doesn't toggle mid-session).

const maxDetectedAsyncAO = 512

// rememberAsyncAO records that a character is driven by an AsyncAO client. A blank
// name (system / spectator with no character) is ignored. Bounded — past the cap a
// new name isn't admitted (the already-known stay badged).
func (c *Courtroom) rememberAsyncAO(charName string) {
	if charName == "" {
		return
	}
	if c.asyncAOByName == nil {
		c.asyncAOByName = map[string]struct{}{}
	}
	if _, had := c.asyncAOByName[charName]; !had && len(c.asyncAOByName) >= maxDetectedAsyncAO {
		return
	}
	c.asyncAOByName[charName] = struct{}{}
}

// RemoteIsAsyncAO reports whether a character has been seen emitting the AsyncAO
// cross-client channel. The player list looks it up per row; allocation-free map
// read, safe per row per frame.
func (c *Courtroom) RemoteIsAsyncAO(charName string) bool {
	_, ok := c.asyncAOByName[charName]
	return ok
}
