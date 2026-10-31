package ui

import "github.com/SyntaxNyah/AsyncAO/internal/config"

// Auto-mounted asset packages: the packages/ folder beside the executable, discovered at startup (main.go) and passed
// in via Deps.AutoMounts. They are appended after the manual mounts so an explicit folder wins the first-hit-wins
// search. Nil for tests and installs with no packages/ folder.

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
