package ui

import "github.com/SyntaxNyah/AsyncAO/internal/config"

// Auto-mounted asset packages: the packages/ folder beside the executable, discovered at startup (main.go) and passed
// in via Deps.AutoMounts. They are appended after the manual mounts so an explicit folder wins the first-hit-wins
// search. Nil for tests and installs with no packages/ folder.

// localAssets is prefs.LocalAssets with the packages in use appended.
func (a *App) localAssets() (enabled bool, mounts []string) {
	enabled, manual := a.d.Prefs.LocalAssets()
	return enabled, config.CombineMounts(manual, a.packagesInUse())
}

// layeredAssets is prefs.LayeredAssets with the packages in use appended.
func (a *App) layeredAssets() (on bool, mounts []string) {
	on, manual := a.d.Prefs.LayeredAssets()
	return on, config.CombineMounts(manual, a.packagesInUse())
}

// packagesInUse is the auto-mounted packages, or none while the player has switched them off.
func (a *App) packagesInUse() []string {
	if a.d.Prefs.Load().PackagesOff {
		return nil
	}
	return a.d.AutoMounts
}

// haveLocalSources reports whether anything local can answer: the player's folders or packages in use. The folder
// and stream switches need one, or switching streaming off would leave nothing to read.
func (a *App) haveLocalSources() bool {
	_, mounts := a.localAssets()
	return len(mounts) > 0
}
