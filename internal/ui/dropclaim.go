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

	"github.com/SyntaxNyah/AsyncAO/internal/themepack"
)

// themePackExt is the AsyncAO theme bundle: a zip whose single root entry is
// the theme folder (docs/THEME-FORMAT.md §1).
//
// AN ALIAS OF themepack.PackExt (v1.90.0 W8), exactly as this comment promised
// when W2 shipped the routing ahead of the extractor. The router and the
// extractor cannot now disagree about what a bundle is called — and note that
// the extension is only ever the ROUTING question: themepack sniffs the zip's
// own magic bytes, so a .aotheme that is really a screenshot is refused by the
// importer and a friend's plain re-zipped .zip imports identically once it
// reaches one.
const themePackExt = themepack.PackExt

// settingsImportExt is the whole-settings bundle the Data tab exports.
const settingsImportExt = ".json"

// themeFontExts are the face files a native theme can carry (v1.90.0 W4). They are
// claimed for exactly the reason .aotheme is: a font dropped on the Settings screen
// fell through to the default arm, which pointed the user's THEME ROOT at the
// font's parent directory — typically Downloads — while the global handler was
// meanwhile warning them the file "isn't a recording". A .ttf is not a theme
// folder; it belongs INSIDE one.
//
// The two extensions SDL_ttf opens, and only those. .ttc / .otc (collections) and
// .woff are deliberately absent: internFace reads a single face out of a file, so
// claiming a container we cannot open would trade a wrong action for a wrong
// promise. They fall through to the folder arm exactly as they do today.
var themeFontExts = [...]string{".ttf", ".otf"}

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
	// dropClaimThemeFont: a .ttf / .otf — HandleFileDrop owns it (v1.90.0 W4's
	// font intake). Like a bundle it is a FILE, so it must never reach the
	// Settings screen's theme-folder arm.
	dropClaimThemeFont
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
	case isThemeFontExt(ext):
		return dropClaimThemeFont
	default:
		return dropClaimNone
	}
}

// settingsDropAct is what the SETTINGS SCREEN does with a dropped path, once
// claimDroppedFile has said who owns it.
//
// It is a type rather than three lines inside drawSettings because those three
// lines are a ship-blocker with no test on them: drawSettings is called by zero
// tests, so deleting the `case dropClaimRecording, dropClaimThemeBundle,
// dropClaimThemeFont:` arm left the whole suite green while a dropped .aotheme
// or .ttf silently repointed the user's theme root at their Downloads folder —
// the W2 bug, returning unnoticed. The gate beside claimDroppedFile now drives
// THIS function instead of restating its switch, so the arm cannot go missing.
type settingsDropAct uint8

const (
	// settingsDropIgnore: a global owner claimed it (HandleFileDrop imports the
	// recording, warns about the bundle, points the font at [fonts]/[fontbind]).
	// The Settings screen must do NOTHING — its theme-folder arm would turn the
	// dropped FILE into "its parent folder" and store that as the theme root.
	settingsDropIgnore settingsDropAct = iota
	// settingsDropImportSettings: the armed .json the Data tab is waiting for.
	settingsDropImportSettings
	// settingsDropRepointThemeRoot: nothing global claimed it, so the documented
	// folder-import path applies (#21) — a dropped folder IS how you point
	// AsyncAO at a base.
	settingsDropRepointThemeRoot
)

// settingsDropAction maps an ownership claim onto the Settings screen's response.
// TOTAL over dropClaim by construction: the default is the fallback, and every
// arm above it is a claim some other owner already made.
func settingsDropAction(claim dropClaim) settingsDropAct {
	switch claim {
	case dropClaimSettingsImport:
		return settingsDropImportSettings
	case dropClaimRecording, dropClaimThemeBundle, dropClaimThemeFont:
		return settingsDropIgnore
	default:
		return settingsDropRepointThemeRoot
	}
}

// isThemeFontExt reports whether an already-lowered extension is a theme face file.
func isThemeFontExt(ext string) bool {
	for _, e := range themeFontExts {
		if ext == e {
			return true
		}
	}
	return false
}
