package ui

import "github.com/SyntaxNyah/AsyncAO/internal/sdl"

// The layout the Updates and Asset source cards share: a Heading with a pill, dim detail lines, a button row.
const (
	// settRowH is the pitch of a checkbox row.
	settRowH int32 = 26
	// settBadgeH and settBadgePadX size a pill; settBadgeGap separates it from the heading beside it.
	settBadgeH    int32 = 22
	settBadgePadX int32 = 9
	settBadgeGap  int32 = 12
	// settBadgeDY centres the pill's text vertically.
	settBadgeDY int32 = 3
	// settHeadingH is the height of a Heading line; settHeadingBadgeDY drops the pill to the heading's baseline area.
	settHeadingH       int32 = 28
	settHeadingBadgeDY int32 = 3
	// settBtnGap separates buttons and what follows them.
	settBtnGap int32 = 10
	// settDetailGap separates the secondary line from the heading above it.
	settDetailGap int32 = 4
	// settSectionGap closes a section before the next one.
	settSectionGap int32 = 10
)

// headingWidth measures text in the Heading face.
func (a *App) headingWidth(text string) int32 {
	w, _ := a.ctx.fontTextWidth(a.ctx.fontBig, text)
	return w
}

// settingsBadge draws a dark-on-colour pill at (x, y) and returns its width.
func (a *App) settingsBadge(x, y int32, text string, bg sdl.Color) int32 {
	c := a.ctx
	w := c.TextWidth(text) + 2*settBadgePadX
	c.Fill(sdl.Rect{X: x, Y: y, W: w, H: settBadgeH}, bg)
	c.Label(x+settBadgePadX, y+settBadgeDY, text, ColTierBlack)
	return w
}

// badgeHeading draws text in the Heading face at the card's left edge with a pill after it.
func (a *App) badgeHeading(y int32, text, kind string, bg sdl.Color) {
	a.ctx.Heading(a.formX, y, text, ColText)
	a.settingsBadge(a.formX+a.headingWidth(text)+settBadgeGap, y+settHeadingBadgeDY, kind, bg)
}
