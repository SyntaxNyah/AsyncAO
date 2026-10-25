package ui

import (
	"strings"
	"testing"
)

// The emote-grid spacing control shipped in v1.87.0 on the General tab, with a
// changelog path naming "Settings -> Interface" — a tab that has never existed —
// and no search keyword at all. So it was real, wired, and effectively
// unreachable: the written path led nowhere and searching for it found nothing.

// TestEmoteSpacingIsSearchable pins that every word someone would plausibly type
// for this control resolves, and resolves to the tab that actually draws it.
func TestEmoteSpacingIsSearchable(t *testing.T) {
	for _, q := range []string{
		"emote icon spacing", "emote spacing", "icon spacing", "emote gap",
		"spacing", "gap", "emote grid", "emote icons",
	} {
		tab := settingsSearchMatch(q)
		if tab < 0 {
			t.Errorf("settings search finds nothing for %q — the control is unreachable again", q)
			continue
		}
		if tab != tabTheme {
			t.Errorf("%q resolves to tab %d, want Theme (%d) — search would send the user to the wrong page",
				q, tab, tabTheme)
		}
	}
}

// TestChangelogNamesNoInventedSettingsTab pins the specific mistake that made
// this control unreachable: release notes told people to click a tab that has
// never existed, so no amount of looking could succeed.
func TestChangelogNamesNoInventedSettingsTab(t *testing.T) {
	changelog := readSourceFile(t, "assets/CHANGELOG.md")
	for _, invented := range []string{
		"Settings → Interface",
		"Settings > Interface",
		"Settings → Appearance",
		"Settings → UI",
	} {
		if strings.Contains(changelog, invented) {
			t.Errorf("changelog says %q, but that tab does not exist. Real tabs: %v",
				invented, settingsTabNames)
		}
	}
}
