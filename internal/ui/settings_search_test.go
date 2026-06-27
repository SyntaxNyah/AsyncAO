package ui

import "testing"

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
