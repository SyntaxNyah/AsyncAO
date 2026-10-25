package ui

import (
	"strings"
	"testing"
)

// TestSettingsSearchMatch pins the #102 fix: every settings SECTION is reachable
// by a natural search term (not just the old curated handful), so the search
// "works" — each term lands on the tab that actually holds it.
func TestSettingsSearchMatch(t *testing.T) {
	cases := []struct {
		q   string
		tab int
	}{
		// Previously-missing sections that the report was about.
		{"friends", tabChat},
		{"ignore", tabChat},
		{"dnd", tabChat},
		{"music history", tabChat},
		{"mod tools", tabChat},
		{"callword", tabChat},
		{"blip", tabAudio},
		{"volume", tabAudio},
		{"reset to defaults", tabReset},
		{"zstd", tabAssets},
		{"download", tabAssets},
		{"cache", tabAssets},
		{"loop preanim", tabAssets},
		{"preanimation", tabAssets},
		{"window", tabGeneral},
		{"dyslexia", tabGeneral},
		{"sprite style", tabGeneral},
		{"opacity", tabGeneral},
		{"hide desk", tabGeneral},
		{"macros", tabHotkeys},
		{"keybind", tabHotkeys},
		{"discord", tabAccount},
		{"password", tabAccount},
		{"video", tabStudio},
		{"replay", tabStudio},
		// Tab-name direct hits.
		{"theme", tabTheme},
		{"lobby", tabTheme},
		{"audio", tabAudio},
		// Formats now owns every "which file type is asked for" term. These used
		// to split between Assets and Power user, which is why nobody found them.
		{"format", tabFormats},
		{"image format", tabFormats},
		// "extensions" and not "extensions.json": the literal filename is found by
		// the gather-search's ROW-LABEL pass (the auto-detect checkbox says it in
		// full), and a keyword carrying the ".json" tail would swallow the Data
		// tab's "json" — see TestSettingsSearchKeywordsDoNotShadowLaterTabs.
		{"extensions", tabFormats},
		{"manifest", tabFormats},
		{"fallback", tabFormats},
		{"autodetect", tabFormats},
		{"probe", tabFormats},
		{"learned", tabFormats},
		{"apng", tabFormats},
		{"opus", tabFormats},
		{"format profile", tabFormats},
		{"stuck images", tabFormats},
		{"emotes not loading", tabFormats},
	}
	for _, c := range cases {
		if got := settingsSearchMatch(c.q); got != c.tab {
			t.Errorf("settingsSearchMatch(%q) = %d (%s), want %d (%s)",
				c.q, got, tabNameOrNone(got), c.tab, settingsTabNames[c.tab])
		}
	}
	if got := settingsSearchMatch(""); got != -1 {
		t.Errorf("empty query = %d, want -1", got)
	}
	if got := settingsSearchMatch("   "); got != -1 {
		t.Errorf("whitespace query = %d, want -1", got)
	}
	if got := settingsSearchMatch("zzzznotarealsetting"); got != -1 {
		t.Errorf("unknown query = %d, want -1", got)
	}
}

func tabNameOrNone(i int) string {
	if i < 0 || i >= len(settingsTabNames) {
		return "none"
	}
	return settingsTabNames[i]
}

// preexistingSearchShadows are the keyword collisions that already existed
// before the Formats tab was added: General's "text size"/"log colours" swallow
// Chat's "text"/"log", and Audio's "alert volume"/"sfx volume" swallow Chat's
// "alert"/"sfx". They are listed rather than silently tolerated so the invariant
// below stays exact — and so that fixing them later is a deletion from this map,
// not a rediscovery of the problem.
var preexistingSearchShadows = map[string]bool{
	"text": true, "log": true, "alert": true, "sfx": true,
}

// TestSettingsSearchKeywordsDoNotShadowLaterTabs pins the trap that inserting a
// settings tab walks straight into: settingsSearchMatch scans the keyword table
// in TAB ORDER and matches forward (the query must be a SUBSTRING of a term), so
// a term on an EARLY tab silently swallows every query a LATER tab claims as its
// own — including queries buried inside a longer term, which is the part that is
// easy to miss ("gif format" answers "gif"; "extensions.json" answers "json").
// Adding Formats at index 3 put a block of new terms in front of nine tabs, so
// the invariant is checked across the whole table, not just that one.
func TestSettingsSearchKeywordsDoNotShadowLaterTabs(t *testing.T) {
	for tab, kws := range settingsSearchKeywords {
		for _, kw := range kws {
			// A tab must be reachable by its OWN terms. An earlier tab answering
			// instead is legitimate only when it genuinely covers that term too;
			// an earlier tab that never mentions the word is pure theft.
			got := settingsSearchMatch(kw)
			if got < 0 || got >= tab || tabClaims(got, kw) || preexistingSearchShadows[kw] {
				continue
			}
			t.Errorf("keyword %q belongs to tab %s but resolves to %s, which never lists it",
				kw, settingsTabNames[tab], tabNameOrNone(got))
		}
	}
}

// tabClaims reports whether tab i lists term verbatim, or its NAME matches it —
// the two ways a tab legitimately wins a query someone else also uses.
func tabClaims(i int, term string) bool {
	if i < 0 || i >= len(settingsTabNames) {
		return false
	}
	if strings.Contains(strings.ToLower(settingsTabNames[i]), term) {
		return true
	}
	for _, kw := range settingsSearchKeywords[i] {
		if kw == term {
			return true
		}
	}
	return false
}
