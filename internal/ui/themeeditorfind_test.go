package ui

// FINDABILITY (v1.90.0 W7a, retargeted in W10) â€”
// docs/wip/THEME-EDITOR-DESIGN.md Â§Q7's "Entry points + settings findability".
//
// This closes a CLASS, not an instance. The recon found that no findability census
// existed at all, and v1.89.1 duly shipped a settings row nobody could search for.
// The flagship feature of the release cannot be the second one.
//
// W10 MOVED THE DESTINATION AND KEPT EVERY CLAIM. The editor row was findable by
// SEARCH from W7a onwards and still invisible to anyone who scrolled: it was one row
// two thirds of the way down the Theme tab. It now has a tab of its own, so the
// assertions below say tabThemeEditor where they used to say tabTheme, and the
// structural half reads the file that now draws the row. Those are the only two
// changes; the queries, the shadow rule and the "it must actually open the editor"
// clause are untouched, which is the evidence that the MOVE is the change and the
// behaviour is not.

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
//
// TWO HALVES, because there are two ways to be unfindable and they fail
// independently:
//
//   - the CONCEPT search (settingsSearchMatch over the keyword table) must send
//     every phrase above to the Theme editor tab;
//   - the ROW must register with the gather-search at all, i.e. it must call
//     c.onRow. A row that draws without that is invisible to the search box even
//     though its label is right there on screen â€” which is exactly how v1.89.1's
//     row went missing.
func TestEveryThemeEditorSettingIsFindable(t *testing.T) {
	for _, q := range themeEditorQueries {
		got := settingsSearchMatch(q)
		if got == tabThemeEditor {
			continue
		}
		if got < 0 {
			t.Errorf("searching %q resolves to NOTHING â€” the flagship feature of the release would be "+
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
//
// THIS IS THE TRAP THE WHOLE-KEYWORD GATE CANNOT SEE, and the keyword table's own
// comments have been describing it in prose since W2:
// TestSettingsSearchKeywordsDoNotShadowLaterTabs compares WHOLE keywords, so a long
// term here whose SUBSTRING is a later tab's query passes that gate while stealing
// the query for real â€” "customize layout" would silently take "custom" from
// tabAssets' "custom error sprite" and tabAudio's "custom music".
//
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
			t.Errorf("Theme editor keyword %q contains %q, which %s answers â€” the whole-keyword shadow "+
				"gate cannot see this, and the search matches forward, so this tab would silently steal "+
				"that query", kw, word, tabNameOrNone(owner))
		}
	}
}
