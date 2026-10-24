package themepack

// THE THIRD CASE-FOLD DOOR (v1.90.0 W9 warm-up).
// On a case-sensitive filesystem that is a silent data loss with a delayed fuse:

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestFoundSidecarRelReportsTheSpellingTheFolderActuallyHas drives the probe
// directly, because it is the only half of this that can be measured on EVERY
// platform. The integration arm below can only fail where the two spellings are
// two files.
func TestFoundSidecarRelReportsTheSpellingTheFolderActuallyHas(t *testing.T) {
	for _, tc := range []struct {
		name string
		rels []string
		want string
	}{
		{"the canonical spelling", []string{"courtroom_design.ini", sidecarName}, sidecarName},
		{"a shouted spelling", []string{"courtroom_design.ini", forgedSidecarName}, forgedSidecarName},
		{"a mixed spelling", []string{"Asyncao_Theme.INI"}, "Asyncao_Theme.INI"},
		{"no native tier at all", []string{"courtroom_design.ini", "fonts/Court.ttf"}, ""},
		// A sidecar-looking name in a SUBFOLDER is somebody's ordinary file, exactly
		// as canonicalSidecarRel's own comment says. It must not be adopted as the
		// theme's tier — that would stamp provenance into a stranger's data file.
		{"a lookalike in a subfolder", []string{"sub/asyncao_theme.ini"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entries := make([]srcEntry, 0, len(tc.rels))
			for _, r := range tc.rels {
				entries = append(entries, srcEntry{rel: r})
			}
			if got := foundSidecarRel(entries); got != tc.want {
				t.Fatalf("foundSidecarRel = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestCopyStampsTheSidecarTheThemeActuallyHas is the integration arm: a theme
// whose ONLY native tier is shouted must come out of a copy with its metadata
// intact and exactly one sidecar.
func TestCopyStampsTheSidecarTheThemeActuallyHas(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "paper")
	mustWrite(t, filepath.Join(dir, "courtroom_design.ini"), "courtroom = 0, 0, 256, 192\nviewport = 0, 0, 256, 192\n")
	mustWrite(t, filepath.Join(dir, forgedSidecarName), honestSidecar)

	m, err := CopyForEditing(dir, emptyRoot(t), "paper (edited)")
	if err != nil {
		t.Fatalf("CopyForEditing: %v", err)
	}

	tree := treeOf(t, m.Dir)
	var sidecars []string
	for rel := range tree {
		if strings.EqualFold(rel, sidecarName) {
			sidecars = append(sidecars, rel)
		}
	}
	if len(sidecars) != 1 {
		t.Fatalf("the copy holds %d native tiers (%v) — the stamp created a second one beside the author's",
			len(sidecars), sidecars)
	}
	body := tree[sidecars[0]]
	if !strings.Contains(body, "author = A Friend") {
		t.Errorf("the copy's %s lost its author:\n%s", sidecars[0], body)
	}
	for _, key := range []string{"derived_from", "derived_at", "derived_hash"} {
		if !strings.Contains(body, key) {
			t.Errorf("the copy's %s carries no %s — the provenance went somewhere else", sidecars[0], key)
		}
	}
	// The directory walk above is the whole check for "was a second file made":
	// treeOf lists real entries, so a stamp that created the canonical name beside
	// a shouted one is two entries in `sidecars`. An os.Stat on the canonical name
	// would NOT be — on a folding filesystem it resolves to the same file and would
	// fail this test for the wrong reason, which it did on the first run here.
}
