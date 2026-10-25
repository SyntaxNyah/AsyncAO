package ui

// Who owns a dropped file.
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
const themePackExt = themepack.PackExt

// settingsImportExt is the whole-settings bundle the Data tab exports.
const settingsImportExt = ".json"

// themeFontExts are the face files a native theme can carry (v1.90.0 W4). They are
// claimed for exactly the reason .aotheme is: a font dropped on the Settings screen
// fell through to the default arm, which pointed the user's THEME ROOT at the
// font's parent directory — typically Downloads — while the global handler was
// meanwhile warning them the file "isn't a recording". A .ttf is not a theme
// folder; it belongs INSIDE one.
// The two extensions SDL_ttf opens, and only those. .ttc / .otc (collections) and
// .woff are deliberately absent: internFace reads a single face out of a file, so
// claiming a container we cannot open would trade a wrong action for a wrong
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
	// dropClaimThemeImage: a .png / .webp / .gif / .apng / .avif — HandleFileDrop
	// owns it (v1.90.0 W10's image intake). Claimed by EXTENSION ALONE, with no
	// "is the editor open" context, and that is the same ruling dropClaimThemeFont
	// made, for the same two reasons: a picture is a FILE, so the Settings screen's
	// theme-folder arm would otherwise repoint the user's theme root at whatever
	// folder it came out of; and with no editor open the answer is a sentence saying
	// where the feature is, which beats a silent "isn't a recording" warning about a
	// file the user dropped on purpose.
	dropClaimThemeImage
	// dropClaimCount sizes the name table and BOUNDS THE CENSUS; never a real
	// claim. The gate beside settingsDropAction used to walk the enum by naming
	// its last member, which made every future wave responsible for remembering
	// to move the bound — and W10 did not: dropClaimThemeImage shipped outside a
	// census whose own comment promised that "a new claim appended to the iota
	// block joins this gate the moment it exists". A sentinel is the only form of
	// that promise the next wave cannot break by omission.
	dropClaimCount
)

// dropClaimNames is what each claim is called in a failure line. It is sized against
// dropClaimCount, the same shape themeCreateTemplateNames and editOpKindNames use.
// WHAT THAT SIZING ACTUALLY BUYS, stated exactly, because the opposite was written here
// first and a false safety promise in this file is worse than none: a claim appended to
// the block above is NOT a compile error. A short fixed-size array literal is legal Go —
// The compiler catches only the REVERSE, a name with no claim behind it, which is how a
// The claim-with-no-name direction is caught loudly at run time, by the empty-name loop
var dropClaimNames = [dropClaimCount]string{
	"none", "recording", "theme bundle", "settings import", "theme font", "theme image",
}

// String makes a claim printable in a refusal or a test failure without a second
// table — an integer in that line tells the reader nothing about which owner lapsed.
func (c dropClaim) String() string {
	if int(c) >= len(dropClaimNames) {
		return "?"
	}
	return dropClaimNames[c]
}

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
	case isThemeImageExt(ext):
		return dropClaimThemeImage
	default:
		return dropClaimNone
	}
}

// settingsDropAct is what the SETTINGS SCREEN does with a dropped path, once
// claimDroppedFile has said who owns it.
// It is a type rather than three lines inside drawSettings because those three
// lines are a ship-blocker with no test on them: drawSettings is called by zero
// tests, so deleting the `case dropClaimRecording, dropClaimThemeBundle,
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
	case dropClaimRecording, dropClaimThemeBundle, dropClaimThemeFont, dropClaimThemeImage:
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
