package ui

import (
	"strings"
	"testing"
)

// The embedded changelog must ship non-empty and well-formed enough for the
// renderer to find at least one version section (the "What's New" screen is
// useless without it, and a missing assets/CHANGELOG.md would fail the build).
func TestChangelogEmbedded(t *testing.T) {
	if strings.TrimSpace(changelogMD) == "" {
		t.Fatal("changelogMD is empty: assets/CHANGELOG.md did not embed")
	}
	if !strings.Contains(changelogMD, "\n## ") && !strings.HasPrefix(changelogMD, "## ") {
		t.Error("changelog has no '## version' header — the version-history view would render flat")
	}
}

// changelogHeaderMatches drives the "installed" tag: it must match on the first
// token regardless of a leading v or trailing "— date", and never match a blank
// running version (an unstamped dev build must not tag any row).
func TestChangelogHeaderMatches(t *testing.T) {
	cases := []struct {
		title, cur string
		want       bool
	}{
		{"v1.0.0 — 2026-06-27", "1.0.0", true}, // header keeps its v; cur is v-stripped
		{"v1.0.0 — upcoming", "1.0.0", true},
		{"1.0.0", "1.0.0", true}, // header without a v still matches
		{"v1.0.0 — 2026-06-27", "1.0.1", false},
		{"v0.1.0 – v0.1.1 — withdrawn previews", "1.0.0", false},
		{"v1.0.0", "", false}, // dev build (no current version) tags nothing
		{"", "1.0.0", false},
	}
	for _, tc := range cases {
		if got := changelogHeaderMatches(tc.title, tc.cur); got != tc.want {
			t.Errorf("changelogHeaderMatches(%q, %q) = %v, want %v", tc.title, tc.cur, got, tc.want)
		}
	}
}
