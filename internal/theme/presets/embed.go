// Package presets is the SHIPPED PRESET DATA and nothing else.
//
// It holds the two preset axes of the v1.90.0 theme editor — 21 layout
// fragments and 14 style fragments, 294 combinations — as `go:embed`ed bytes.
// There is no parser here, no model, no cap and no behaviour: the parent
// package (internal/theme) owns every one of those, and this package exists
// only so that `go:embed` has a directory it can reach.
//
// WHY A SEPARATE PACKAGE AT ALL. `go:embed` can only name files at or below
// the embedding file's own directory, so embedding from internal/theme would
// pull the whole subtree — including a future presets/README — into the parent
// package's file set and make the data's shape a property of the parser's
// directory listing. Splitting it also fixes the dependency direction once and
// for all: DATA NEVER IMPORTS CODE. This package imports "embed" and nothing
// else, which is what makes an import cycle impossible by construction rather
// than by discipline (internal/theme imports this; this can never import back).
//
// Pinned by TestPresetDataPackageShipsDataOnly — the encapsulation gate that
// fails the moment this file grows a second concern.
package presets

import "embed"

// FS is the embedded fragment tree: layout/<id>.ini and style/<id>.ini.
//
// The two directories are named EXPLICITLY rather than embedded as `presets`
// wholesale, so a stray file dropped beside them (an editor backup, a README, a
// half-finished draft) cannot silently become a shipped preset. A new axis is a
// new pattern line here and a reviewer sees it in the diff.
//
// NO `all:` PREFIX, deliberately: it would sweep in dotfiles and _-prefixed
// files, which is the same silent-inclusion hazard one layer down.
//
//go:embed layout/*.ini style/*.ini
var FS embed.FS

const (
	// LayoutDir / StyleDir are the two axis directories inside FS. Named
	// because the parser walks them and the gates assert over them: two
	// string literals in two packages are two strings that can disagree.
	LayoutDir = "layout"
	StyleDir  = "style"

	// FileExt is the fragment extension. The fragments are byte-identical
	// asyncao_theme.ini grammar plus a [preset] header, so the extension is
	// .ini and not a private one — a user can open, diff and copy one.
	FileExt = ".ini"
)
