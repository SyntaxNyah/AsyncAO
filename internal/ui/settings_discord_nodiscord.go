//go:build nodiscord

package ui

// drawDiscordSection is a no-op in the Discord-free build (-tags nodiscord): the Rich
// Presence integration is compiled out, so the Settings screen has no Discord section
// and none of its rendering code is in the binary.
func (a *App) drawDiscordSection(y, w int32) int32 { return y }
