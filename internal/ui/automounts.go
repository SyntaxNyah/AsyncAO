package ui

import "github.com/SyntaxNyah/AsyncAO/internal/config"

// Auto-mounted asset packages (issue #128): the packages/ folder beside the
// executable, discovered once at startup (main.go) and passed in via
// Deps.AutoMounts. They are appended after the user's manual mounts so an
// explicit folder always wins the first-hit-wins search. Nil for tests and for
// installs with no packages/ folder, so the effective mount list collapses to
// the manual list and no existing call site changes meaning.

// localAssets is prefs.LocalAssets with the auto-mounted packages appended.
func (a *App) localAssets() (enabled bool, mounts []string) {
	enabled, manual := a.d.Prefs.LocalAssets()
	return enabled, config.CombineMounts(manual, a.d.AutoMounts)
}

// layeredAssets is prefs.LayeredAssets with the auto-mounted packages appended.
func (a *App) layeredAssets() (on bool, mounts []string) {
	on, manual := a.d.Prefs.LayeredAssets()
	return on, config.CombineMounts(manual, a.d.AutoMounts)
}
