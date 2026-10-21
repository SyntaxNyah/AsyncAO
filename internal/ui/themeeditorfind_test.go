package ui

// FINDABILITY (v1.90.0 W7a, retargeted in W10) —
// docs/wip/THEME-EDITOR-DESIGN.md §Q7's "Entry points + settings findability".

import (
	"strings"
	"testing"
)

// themeEditorQueries are the words someone looking for the editor actually types.
// Each must land on the Theme editor tab.
var themeEditorQueries = []string{
	"theme editor", "editor", "make a theme", "build a theme", "create a theme",
	"decorate", "element", "elements", "inspector", "stacking",
	"shapes", "masks", "split screen", "procedural", "halftone", "generator",
	"animated background",
	// W10's own vocabulary: the creator, the templates, the image intake and the
	// live-preview toggle. Each is a phrase a person hunting the feature types, and
	// each is on the tab that answers it.
	"theme builder", "theme creator", "edit theme", "start a theme", "from scratch",
	"blank theme", "template", "templates", "copy a theme", "duplicate a theme",
	"edit the layout", "design my own theme",
	"add an image", "upload an image", "upload image", "animated image", "theme art",
	"live preview", "aotheme", "theme bundle", "share a theme",
}

// TestEveryThemeEditorSettingIsFindable is the findability census.
// TWO HALVES, because there are two ways to be unfindable and they fail
// independently:
func TestEveryThemeEditorSettingIsFindable(t *testing.T) {
	for _, q := range themeEditorQueries {
		got := settingsSearchMatch(q)
		if got == tabThemeEditor {
			continue
		}
		if got < 0 {
			t.Errorf("searching %q resolves to NOTHING — the flagship feature of the release would be "+
				"reachable only by scrolling to it", q)
			continue
		}
		// An earlier tab answering is legitimate only if it genuinely covers the term.
		if got < tabThemeEditor && tabClaims(got, q) {
			continue
		}
		t.Errorf("searching %q resolves to %s, not Theme editor", q, tabNameOrNone(got))
	}

}

// TestThemeEditorKeywordsDoNotShadowLaterTabs is the whole-keyword shadow gate,
// aimed at this wave's additions specifically.
//
// The package-wide TestSettingsSearchKeywordsDoNotShadowLaterTabs already runs over
// the whole table; this one exists so a failure introduced HERE names this wave,
// rather than being reported as a mystery about a table with 300 entries in it.
func TestThemeEditorKeywordsDoNotShadowLaterTabs(t *testing.T) {
	for _, kw := range settingsSearchKeywords[tabThemeEditor] {
		got := settingsSearchMatch(kw)
		if got < 0 || got >= tabThemeEditor || tabClaims(got, kw) || preexistingSearchShadows[kw] {
			continue
		}
		t.Errorf("Theme editor keyword %q resolves to %s, which never lists it", kw, tabNameOrNone(got))
	}
}

// themeEditorReservedWords are short queries a LATER tab owns outright. A term on
// the Theme editor tab may not contain any of them.
// This tab sits at index 2, ahead of every tab from Assets on, so it is the one that
// can do the stealing. The list is the prose made executable.
var themeEditorReservedWords = []string{
	"custom", "maker", "scan", "order", "list", "export", "import", "layer",
	"download", "source", "grid", "glow", "webp", "gif", "new", "json", "stream",
}

// TestThemeEditorKeywordsDoNotStealShorterQueries is that trap, as a gate.
func TestThemeEditorKeywordsDoNotStealShorterQueries(t *testing.T) {
	for _, kw := range settingsSearchKeywords[tabThemeEditor] {
		for _, word := range themeEditorReservedWords {
			if !strings.Contains(kw, word) {
				continue
			}
			// It is only a theft if a LATER tab really answers the bare word.
			owner := settingsSearchMatch(word)
			if owner <= tabThemeEditor {
				continue
			}
			t.Errorf("Theme editor keyword %q contains %q, which %s answers — the whole-keyword shadow "+
				"gate cannot see this, and the search matches forward, so this tab would silently steal "+
				"that query", kw, word, tabNameOrNone(owner))
		}
	}
}
