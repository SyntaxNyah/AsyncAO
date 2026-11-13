package courtroom

// Any zero-width frame marks the speaker as AsyncAO; AO2 and webAO never emit the channel. Detection is passive.

const maxDetectedAsyncAO = 512

// rememberAsyncAO records an AsyncAO character; blank names are ignored and names past the cap are not admitted.
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

// RemoteIsAsyncAO reports whether a character has emitted the channel; allocation-free, safe per row per frame.
func (c *Courtroom) RemoteIsAsyncAO(charName string) bool {
	_, ok := c.asyncAOByName[charName]
	return ok
}
