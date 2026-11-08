package ui

import (
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
	"github.com/SyntaxNyah/AsyncAO/internal/update"
)

const (
	// updRowH is the pitch of a checkbox row.
	updRowH int32 = 26
	// updBadgeH and updBadgePadX size the release-kind pill; updBadgeGap separates it from the version beside it.
	updBadgeH    int32 = 22
	updBadgePadX int32 = 9
	updBadgeGap  int32 = 12
	// updBadgeDY centres the pill's text vertically.
	updBadgeDY int32 = 3
	// updHeadingH is the height of a Heading line; updHeadingBadgeDY drops the pill to the heading's baseline area.
	updHeadingH       int32 = 28
	updHeadingBadgeDY int32 = 3
	// updBtnW is the width of the tab's buttons; updBtnGap separates buttons and what follows them.
	updBtnW   int32 = 190
	updBtnGap int32 = 10
	// updDetailGap separates the secondary line from the heading above it.
	updDetailGap int32 = 4
	// updSectionGap closes a section before the next one.
	updSectionGap int32 = 10
	// updDateLayout shows the compile time in the viewer's zone, and updCheckedLayout the time of the last check.
	updDateLayout    = "2006-01-02 15:04"
	updCheckedLayout = "15:04"
)

func (a *App) drawSettingsUpdates(y, _ int32) int32 {
	c := a.ctx
	pad := a.formX
	w := a.formW2()
	build := update.Current()

	y = a.settingsSection(y, w, "Version")
	if update.IsDev() {
		c.Heading(pad, y, "Development build", ColText)
	} else {
		a.versionHeading(y, build.Version, update.IsPrerelease(build.Version))
	}
	y += updHeadingH + updDetailGap
	y = a.settingsDesc(pad, y, buildDetails(build), ColTextDim)
	if text, col := a.updateStatus(); text != "" {
		y = a.settingsDesc(pad, y, text, col)
	}
	y += updDetailGap
	x := pad
	if !update.IsDev() {
		label := "Check for updates"
		if a.updateChecking {
			label = "Checking…"
		}
		if c.Button(sdl.Rect{X: x, Y: y, W: updBtnW, H: btnH}, label) {
			a.startUpdateCheck()
		}
		x += updBtnW + updBtnGap
	}
	if c.Button(sdl.Rect{X: x, Y: y, W: updBtnW, H: btnH}, "Copy version info") {
		_ = sdl.SetClipboardText(versionReport(build))
		settings.statusLine = "Version info copied."
	}
	y += btnH + updBtnGap

	if rel := a.updateRel; rel != nil && !update.IsDev() {
		c.Fill(sdl.Rect{X: pad, Y: y, W: a.formW, H: 1}, ColPanelHi)
		y += updBtnGap + updDetailGap
		shown := rel.Tag
		if shown == "" {
			shown = rel.Version
		}
		a.versionHeading(y, shown, rel.Prerelease)
		y += updHeadingH + updDetailGap
		y = a.settingsDesc(pad, y, "New version available", ColTextDim)
		y += updDetailGap
		if c.Button(sdl.Rect{X: pad, Y: y, W: updBtnW, H: btnH}, "Patch notes & install…") {
			a.updateShow = true
		}
		y += btnH + updBtnGap
	}
	y += updSectionGap

	y = a.settingsSection(y, w, "Options")
	upd := a.d.Prefs.Load().UpdateCheck
	if next := c.Checkbox(pad, y, "Check for updates on launch", upd); next != upd {
		a.d.Prefs.Update(func(sn *config.Settings) { sn.UpdateCheck = next })
	}
	y += updRowH
	pre := a.d.Prefs.Load().UpdatePrerelease
	if next := c.Checkbox(pad, y, "Include pre-releases", pre); next != pre {
		a.d.Prefs.Update(func(sn *config.Settings) { sn.UpdatePrerelease = next })
		// Drop a stale offer; a check in flight is redone by pollUpdate.
		a.updateRel = nil
		a.updateShow = false
		a.updateCheckedAt = time.Time{}
		a.updateCheckErr = ""
		a.startUpdateCheck()
	}
	y += updRowH
	y = a.settingsDesc(pad, y, "Off: stable releases only.", ColTextDim)
	y += updSectionGap
	return y
}

// headingWidth measures text in the Heading face.
func (a *App) headingWidth(text string) int32 {
	w, _ := a.ctx.fontTextWidth(a.ctx.fontBig, text)
	return w
}

// updatesBadge draws a dark-on-colour pill at (x, y).
func (a *App) updatesBadge(x, y int32, text string, bg sdl.Color) {
	c := a.ctx
	c.Fill(sdl.Rect{X: x, Y: y, W: c.TextWidth(text) + 2*updBadgePadX, H: updBadgeH}, bg)
	c.Label(x+updBadgePadX, y+updBadgeDY, text, ColTierBlack)
}

// buildDetails is the secondary line under the version: commit and compile time, or what is unknown.
func buildDetails(b update.Build) string {
	commit := b.ShortCommit()
	switch {
	case commit == "":
		commit = "commit unknown"
	case b.Modified:
		commit += " (modified)"
	}
	compiled := "build date unknown"
	if !b.BuiltAt.IsZero() {
		compiled = "built " + b.BuiltAt.Local().Format(updDateLayout)
	}
	return commit + "  ·  " + compiled
}

// versionHeading draws a version in the Heading face with its Release or Pre-release pill.
func (a *App) versionHeading(y int32, version string, prerelease bool) {
	a.ctx.Heading(a.formX, y, version, ColText)
	kind, bg := "Release", ColAccent
	if prerelease {
		kind, bg = "Pre-release", ColTierYellow
	}
	a.updatesBadge(a.formX+a.headingWidth(version)+updBadgeGap, y+updHeadingBadgeDY, kind, bg)
}

// updateStatus is the dim line under the build details; "" while an offer is shown below the divider.
func (a *App) updateStatus() (string, sdl.Color) {
	checked := ""
	if !a.updateCheckedAt.IsZero() {
		checked = "  ·  checked " + a.updateCheckedAt.Format(updCheckedLayout)
	}
	switch {
	case update.IsDev():
		return "Development builds don't update.", ColTextDim
	case a.updateChecking:
		return "Checking for updates…", ColTextDim
	case a.updateRel != nil:
		return "", ColTextDim
	case a.updateCheckErr != "":
		return "Check failed: " + a.updateCheckErr, ColDanger
	case a.updateCheckedAt.IsZero():
		return "Not checked yet", ColTextDim
	default:
		return "Up to date" + checked, ColTextDim
	}
}

// versionReport is the one-line build description for bug reports.
func versionReport(b update.Build) string {
	s := "AsyncAO " + b.Version
	if c := b.ShortCommit(); c != "" {
		s += ", commit " + c
		if b.Modified {
			s += " (modified)"
		}
	}
	if !b.BuiltAt.IsZero() {
		s += ", built " + b.BuiltAt.UTC().Format(time.RFC3339)
	}
	return s
}
