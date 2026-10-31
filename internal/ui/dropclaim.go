package ui

// Who owns a dropped file. SDL hands the path to HandleFileDrop (demofile.go) and the visible screen sees it as
// c.dropped in the same frame, so the classification lives here once and both call it. Each keeps its own per-screen
// fallback.

import (
	"path/filepath"
	"strings"

	"github.com/SyntaxNyah/AsyncAO/internal/themepack"
)

// themePackExt is the AsyncAO theme bundle, a zip whose single root entry is the theme folder (docs/THEME-FORMAT.md).
// The extension is only for routing; themepack sniffs the zip's magic bytes.
const themePackExt = themepack.PackExt

// settingsImportExt is the whole-settings bundle the Data tab exports.
const settingsImportExt = ".json"

// themeFontExts are the face files a native theme can carry. Claimed because the Settings default arm would otherwise
// point the theme root at the font's folder.
// Only what SDL_ttf opens: internFace reads a single face, so .ttc, .otc and .woff are left unclaimed.
var themeFontExts = [...]string{".ttf", ".otf"}

// dropClaim names the owner of a dropped path.
type dropClaim uint8

const (
	// dropClaimNone: nothing global claims it. Settings treats it as "point the theme folder here"; elsewhere it is
	// ignored.
	dropClaimNone dropClaim = iota
	// dropClaimRecording: .aorec / AO2 .demo; HandleFileDrop imports and plays it or routes it to the Studio video
	// export.
	dropClaimRecording
	// dropClaimThemeBundle: .aotheme, owned by HandleFileDrop. A bundle is a file, so the theme-folder arm would
	// repoint the theme root at its parent.
	dropClaimThemeBundle
	// dropClaimSettingsImport: a .json while the Settings screen has an import armed, so the global handler stays
	// quiet.
	dropClaimSettingsImport
	// dropClaimThemeFont: a .ttf / .otf, owned by HandleFileDrop; like a bundle it must never reach the theme-folder
	// arm.
	dropClaimThemeFont
	// dropClaimThemeImage: a .png / .webp / .gif / .apng / .avif, owned by HandleFileDrop and claimed by extension
	// alone, for the same reason as a font.
	dropClaimThemeImage
	// dropClaimCount sizes the name table; never a real claim.
	dropClaimCount
)

// dropClaimNames is what each claim is called in a failure line. A claim added without a name still compiles, because a
// short fixed-size array literal is legal Go and leaves "".
var dropClaimNames = [dropClaimCount]string{
	"none", "recording", "theme bundle", "settings import", "theme font", "theme image",
}

// String makes a claim printable in a refusal line.
func (c dropClaim) String() string {
	if int(c) >= len(dropClaimNames) {
		return "?"
	}
	return dropClaimNames[c]
}

// claimDroppedFile classifies a dropped path by name alone, with no disk access, so it is safe on any thread.
// importArmed is the one piece of context that changes the answer.
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

// settingsDropAct is what the Settings screen does with a dropped path, once claimDroppedFile has said who owns it.
type settingsDropAct uint8

const (
	// settingsDropIgnore: a global owner claimed it. The theme-folder arm would store the file's parent as the theme
	// root.
	settingsDropIgnore settingsDropAct = iota
	settingsDropImportSettings
	// settingsDropRepointThemeRoot: nothing global claimed it, so a dropped folder points AsyncAO at a base.
	settingsDropRepointThemeRoot
)

// settingsDropAction maps a claim onto the Settings screen's response; the default is the fallback.
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

func isThemeFontExt(ext string) bool {
	for _, e := range themeFontExts {
		if ext == e {
			return true
		}
	}
	return false
}
