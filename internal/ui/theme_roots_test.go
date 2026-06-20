package ui

import (
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// TestThemeLoadRoots pins the #87 fix: a custom themes folder is searched first
// for a NAMED theme, but is dropped for the stock "default" so a custom
// themes/default can't shadow the built-in one (the bug — stock default became
// unreachable once a folder was set). The app directory is always present.
func TestThemeLoadRoots(t *testing.T) {
	const custom, exe = "C:/MyThemes", "C:/App"

	if got := themeLoadRoots("CoolTheme", custom, exe); len(got) != 2 || got[0] != custom || got[1] != exe {
		t.Fatalf("named-theme roots = %v, want [%q %q] (custom first)", got, custom, exe)
	}
	if got := themeLoadRoots(theme.DefaultThemeName, custom, exe); len(got) != 1 || got[0] != exe {
		t.Fatalf("default roots = %v, want [%q] only (custom root must be dropped)", got, exe)
	}
	if got := themeLoadRoots("CoolTheme", "", exe); len(got) != 1 || got[0] != exe {
		t.Fatalf("no-folder roots = %v, want [%q]", got, exe)
	}
}
