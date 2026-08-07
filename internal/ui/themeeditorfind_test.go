package ui

// FINDABILITY (v1.90.0 W7a) — docs/wip/THEME-EDITOR-DESIGN.md §Q7's
// "Entry points + settings findability".
//
// This closes a CLASS, not an instance. The recon found that no findability census
// existed at all, and v1.89.1 duly shipped a settings row nobody could search for.
// The flagship feature of the release cannot be the second one.

import (
	"os"
	"strings"
	"testing"
)

// themeEditorQueries are the words someone looking for the editor actually types.
// Each must land on the Theme tab.
var themeEditorQueries = []string{
	"theme editor", "editor", "make a theme", "build a theme", "create a theme",
	"decorate", "element", "elements", "inspector", "stacking",
	"shapes", "masks", "split screen", "procedural", "halftone", "generator",
	"animated background",
}

// TestEveryThemeEditorSettingIsFindable is the findability census.
//
// TWO HALVES, because there are two ways to be unfindable and they fail
// independently:
//
//   - the CONCEPT search (settingsSearchMatch over the keyword table) must send
//     every phrase above to the Theme tab;
//   - the ROW must register with the gather-search at all, i.e. it must call
//     c.onRow. A row that draws without that is invisible to the search box even
//     though its label is right there on screen — which is exactly how v1.89.1's
//     row went missing.
func TestEveryThemeEditorSettingIsFindable(t *testing.T) {
	for _, q := range themeEditorQueries {
		got := settingsSearchMatch(q)
		if got == tabTheme {
			continue
		}
		if got < 0 {
			t.Errorf("searching %q resolves to NOTHING — the flagship feature of the release would be "+
				"reachable only by scrolling to it", q)
			continue
		}
		// An earlier tab answering is legitimate only if it genuinely covers the term.
		if got < tabTheme && tabClaims(got, q) {
			continue
		}
		t.Errorf("searching %q resolves to %s, not Theme", q, tabNameOrNone(got))
	}

	// The structural half: the editor row registers with the gather index.
	src, err := os.ReadFile("settings.go")
	if err != nil {
		t.Fatalf("reading settings.go: %v", err)
	}
	body := string(src)
	const rowRegistration = `c.onRow("Theme editor", y)`
	if !strings.Contains(body, rowRegistration) {
		t.Errorf("the Theme-editor settings row no longer calls %s — a row that skips the gather hook is "+
			"invisible to the settings search even though its label is on screen, which is precisely the "+
			"v1.89.1 defect this census exists to make impossible", rowRegistration)
	}
	// …and it opens the editor rather than being a label with no verb.
	if !strings.Contains(body, "a.openThemeEditor(ScreenSettings)") {
		t.Error("the Theme-editor settings row no longer opens the editor from Settings — Settings is the " +
			"design's headline entry point, and the one reachable with no server attached")
	}
}

// TestThemeEditorKeywordsDoNotShadowLaterTabs is the whole-keyword shadow gate,
// aimed at this wave's additions specifically.
//
// The package-wide TestSettingsSearchKeywordsDoNotShadowLaterTabs already runs over
// the whole table; this one exists so a failure introduced HERE names this wave,
// rather than being reported as a mystery about a table with 300 entries in it.
func TestThemeEditorKeywordsDoNotShadowLaterTabs(t *testing.T) {
	for _, kw := range settingsSearchKeywords[tabTheme] {
		got := settingsSearchMatch(kw)
		if got < 0 || got >= tabTheme || tabClaims(got, kw) || preexistingSearchShadows[kw] {
			continue
		}
		t.Errorf("Theme keyword %q resolves to %s, which never lists it", kw, tabNameOrNone(got))
	}
}
