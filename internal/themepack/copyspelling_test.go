package themepack

// THE THIRD CASE-FOLD DOOR (v1.90.0 W9 warm-up).
//
// Three functions in this package have to answer "which file in this folder is
// the theme's native tier?". Two of them fold case — readPackSidecar via
// canonicalSidecarRel, readFolderSidecar via strings.EqualFold — and the third,
// stampProvenance, used to open the CANONICAL name unconditionally.
//
// On a case-sensitive filesystem that is a silent data loss with a delayed fuse:
// copying a theme whose tier is spelled ASYNCAO_THEME.INI read nothing, created a
// SECOND empty sidecar beside the author's and wrote the provenance into that. The
// copy lost its name, author and licence; the editor showed an empty native tier;
// and the copy's own export was then refused by our one-file-per-name guard,
// which is the point at which the user finds out.

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
//
// ON A CASE-FOLDING FILESYSTEM (Windows, macOS by default) this arm cannot fail:
// there is one file either way and os.Open finds it under either spelling. It
// still runs — it costs nothing and it pins the metadata carry — but the defect it
// is named for is only observable where the pair can exist, so the honest thing is
// to say so here rather than to let a green run on this box read as proof. That is
// the same shape TestTheExporterRefusesWhatTheImporterRefuses already uses, with
// the skip inverted: there the FIXTURE cannot exist, here the BUG cannot.
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
