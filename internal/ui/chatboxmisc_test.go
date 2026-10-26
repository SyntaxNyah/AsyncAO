package ui

// THE CHATBOX LADDER, AND THE RUNG THAT IS NOT THERE YET (v1.90.0 W9).
//
// Design §W8 scoped "FindAssetMisc wired into the chatbox ladder" and named this
// gate. W9 DEFERS the wiring — the reason is written at themeImageStems, and it
// is that a per-character chatbox is a per-SPEAKER asset tier with its own cap and
// lifetime, not a rung on a table whose whole contract is "one per theme, pinned".
//
// A deferral with no test is a deferral nobody can act on, so this gate pins the
// two things the future wave needs pinned:
//
//  1. THE STEM ORDER, which the misc tier must not change. AO2 inserts
//     `misc/<p_misc>/` in front of the theme rungs (get_asset_paths,
//     path_functions.cpp:181-189); it does not add, remove or reorder a stem
//     (courtroom.cpp:3323-3330). So the answer to "what does the ladder look like
//     with misc" is "exactly this, looked up in more places".
//  2. THE HELPER IS REAL AND UNUSED. theme.FindAssetMisc ships gated
//     (TestFindAssetMiscProbeOrderMatchesAO2) with one caller — the effects
//     overlay. If a later wave deletes it as dead code the chatbox tier loses its
//     resolver, and if a later wave wires it into this table without building the
//     per-speaker lifetime, the texture budget is the thing that finds out.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// TestChatboxLadderOrderIsUnchangedWithMisc is the §W8 gate name, kept honest
// about what it can and cannot assert today.
func TestChatboxLadderOrderIsUnchangedWithMisc(t *testing.T) {
	stems := themeImageStems()[themeStemChatbox]
	want := []string{"chat", "chatbox", "chatblank"}
	if len(stems) != len(want) {
		t.Fatalf("the chatbox ladder is %v, want %v (courtroom.cpp:3323-3330)", stems, want)
	}
	for i := range want {
		if stems[i] != want[i] {
			t.Fatalf("the chatbox ladder is %v, want %v — AO2 probes `chat` then `chatbox`, "+
				"and `chatblank` is the last resort for a theme that ships only the blank skin", stems, want)
		}
	}
	// A misc pack changes the DIRECTORY, never the stem. If a future wave adds
	// "misc" or a per-character name to this list it has misread get_asset_paths,
	// and every theme in the reference corpus loses its chatbox.
	for _, s := range stems {
		if strings.Contains(s, "misc") || strings.Contains(s, "/") {
			t.Fatalf("stem %q looks like a PATH — the misc tier is a directory prefix, not a stem", s)
		}
	}
}

// TestTheMiscChatboxResolverStaysAvailableWhileItIsDeferred is the deferral's own
// gate. It fails if theme.FindAssetMisc stops resolving, which is the one way a
// deferred plan quietly becomes an abandoned one.
//
// It deliberately does NOT assert that the chatbox ladder calls it: it does not,
// on purpose, and a test that demanded the call would be a test of the thing that
// was declined rather than of the thing that was kept.
func TestTheMiscChatboxResolverStaysAvailableWhileItIsDeferred(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "themes", "packless")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "courtroom_design.ini"),
		[]byte("courtroom = 0, 0, 714, 579\nviewport = 0, 0, 256, 192\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	th, err := theme.Load("packless", "", []string{root})
	if err != nil {
		t.Fatal(err)
	}
	// A pack nobody shipped: the answer must be a clean "no", never a panic and
	// never a path outside the theme.
	if p, ok := th.FindAssetMisc("no-such-pack", "chat", themeImageExts); ok {
		t.Fatalf("FindAssetMisc invented %q for a pack that does not exist", p)
	}
	// A hostile pack name is REFUSED rather than resolved — the property that makes
	// it safe to point at a server-supplied char.ini value when the tier is built.
	if p, ok := th.FindAssetMisc("../../../etc", "chat", themeImageExts); ok {
		t.Fatalf("FindAssetMisc escaped the theme folder: %q", p)
	}
}
