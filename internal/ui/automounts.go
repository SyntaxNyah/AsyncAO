package ui

import "github.com/SyntaxNyah/AsyncAO/internal/config"

// Auto-mounted asset packages: the packages/ folder beside the executable, discovered at startup (main.go) and passed
// in via Deps.AutoMounts. They are appended after the manual mounts so an explicit folder wins the first-hit-wins
// search. Nil for tests and installs with no packages/ folder.

// searchedMounts is what is read from the player's machine: their folders while "Use my folders and packs" is on,
// then the packages while those are on. The two switches are independent.
func (a *App) searchedMounts() []string {
	var folders []string
	if a.d.Prefs.Load().LocalAssetsLayered {
		_, folders = a.d.Prefs.LocalAssets()
	}
	return config.CombineMounts(folders, a.packagesInUse())
}

// localAssets reports streaming switched off with something local to read, and what is read.
func (a *App) localAssets() (enabled bool, mounts []string) {
	streamOff, _ := a.d.Prefs.LocalAssets()
	mounts = a.searchedMounts()
	return streamOff && len(mounts) > 0, mounts
}

// layeredAssets reports local files read ahead of the stream, and which.
func (a *App) layeredAssets() (on bool, mounts []string) {
	streamOff, _ := a.d.Prefs.LocalAssets()
	mounts = a.searchedMounts()
	return !streamOff && len(mounts) > 0, mounts
}

// packagesInUse is the auto-mounted packages, or none while the player has switched them off.
func (a *App) packagesInUse() []string {
	if a.d.Prefs.Load().PackagesOff {
		return nil
	}
	return a.d.AutoMounts
}

// haveLocalSources reports whether anything local is read; with nothing, streaming stays on.
func (a *App) haveLocalSources() bool { return len(a.searchedMounts()) > 0 }
