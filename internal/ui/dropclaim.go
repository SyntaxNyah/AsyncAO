package ui

// Who owns a dropped file.
//
// TWO consumers see every drop, and they used to disagree. SDL hands the path
// to HandleFileDrop (the global owner, demofile.go) AND the visible screen sees
// it as c.dropped in the same frame — and the Settings screen's default arm
// pointed the THEME FOLDER at whatever it was handed. So a theme bundle dropped
// on the settings page silently repointed the user's theme root at, typically,
// their Downloads folder, while the global handler was busy warning them the
// file "isn't a recording". Two owners, two wrong answers, one drop.
//
// The classification therefore lives HERE, once, and both owners call it. Each
// keeps its own fallback — that part is genuinely per-screen — but neither can
// claim something the other already owns.

import (
	"path/filepath"
	"strings"
)

// themePackExt is the AsyncAO theme bundle: a zip whose single root entry is
// the theme folder (docs/THEME-FORMAT.md §1). Declared here rather than in a
// themepack package because the EXTRACTOR does not exist yet — the routing
// does, and shipping the routing first is what stops the silent repoint above.
// When internal/themepack lands, this becomes an alias of its PackExt.
const themePackExt = ".aotheme"

// settingsImportExt is the whole-settings bundle the Data tab exports.
const settingsImportExt = ".json"

// dropClaim names the owner of a dropped path.
type dropClaim uint8

const (
	// dropClaimNone: nothing global claims it. Each screen applies its own
	// fallback — the Settings screen treats it as "point the theme folder
	// here" (a dropped file means its folder), everywhere else it is ignored.
	dropClaimNone dropClaim = iota
	// dropClaimRecording: .aorec / AO2 .demo — HandleFileDrop imports and plays
	// it (or routes it to the Studio video export).
	dropClaimRecording
	// dropClaimThemeBundle: .aotheme — HandleFileDrop owns it. It must NEVER
	// reach the Settings screen's theme-folder arm: a bundle is a FILE, so that
	// arm would repoint the theme root at the file's parent directory, which is
	// not a theme root and was never chosen by anyone.
	dropClaimThemeBundle
	// dropClaimSettingsImport: a .json while the Settings screen has an import
	// armed. Listed here so the global handler stays quiet about it instead of
	// warning about a file the user deliberately dropped.
	dropClaimSettingsImport
)

// claimDroppedFile classifies a dropped path by NAME alone — no disk access, so
// it is safe on any thread and both owners get the same answer for the same
// string. importArmed is the Settings screen's arming state, which is the one
// piece of context that changes the answer.
func claimDroppedFile(path string, importArmed bool) dropClaim {
	ext := strings.ToLower(filepath.Ext(strings.TrimSpace(path)))
	switch {
	case ext == recordingExt || ext == demoExt:
		return dropClaimRecording
	case ext == themePackExt:
		return dropClaimThemeBundle
	case importArmed && ext == settingsImportExt:
		return dropClaimSettingsImport
	default:
		return dropClaimNone
	}
}
