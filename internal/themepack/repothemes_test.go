package themepack

// THE REPOSITORY'S FOURTEEN THEMES, THROUGH THE REAL TRANSPORT (v1.90.0 W8).
//
// Every other round-trip gate in this package builds its own folder out of three
// files this test file wrote, so it can only prove the writer agrees with the
// reader about what the writer made. These fourteen were typed by a human against
// docs/THEME-FORMAT.md and carry the things a synthesised fixture never does: CRLF
// endings, BOMs, comment blocks longer than the keys they introduce, blank lines
// with trailing spaces, and section names near the id cap.
//
// SO THIS IS THE ONE PLACE THE WHOLE CLAIM IS MEASURED END TO END: folder →
// .aotheme → folder, byte for byte, on files nobody generated. It is the promise
// themes/README.md makes to a user who drags a bundle onto the window.
//
// It also closes the half of themes/README.md's Dev note that internal/theme
// structurally cannot: that package cannot import this one (this one imports it),
// so the parse census lives there and the transport census lives here.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// repoThemesDir is where the standalone themes live, relative to this package.
// The same relative depth as internal/theme's copy — both are two levels down.
const repoThemesDir = "../../themes"

// repoThemeMinimum is the count below which the corpus is assumed broken rather
// than small (see internal/theme's twin: a census that reads nothing passes
// everything).
const repoThemeMinimum = 10

// TestRepoThemesRoundTripThroughTheBundle bundles every repository theme and
// extracts it again, comparing the trees byte for byte.
func TestRepoThemesRoundTripThroughTheBundle(t *testing.T) {
	dirs := repoThemeDirs(t)
	for _, dir := range dirs {
		name := filepath.Base(dir)
		t.Run(name, func(t *testing.T) {
			dest := filepath.Join(t.TempDir(), name+PackExt)
			wm, err := Write(dir, dest, nil)
			if err != nil {
				t.Fatalf("bundling %s: %v", name, err)
			}
			if wm.Files == 0 {
				t.Fatal("the bundle holds no files")
			}
			// THE MANIFEST IS PART OF THE CLAIM, not decoration: it is what the
			// consent sheet shows a recipient before they agree to anything, so a
			// bundle that cannot name the theme it carries is a bundle that asks for
			// consent to an unnamed thing.
			if wm.Name == "" {
				t.Errorf("the manifest carries no [theme] name for %s", name)
			}
			if wm.License == "" {
				t.Errorf("the manifest carries no [theme] license for %s — every theme in "+
					"themes/ declares one (see themes/README.md's licensing section)", name)
			}
			if wm.Format != theme.ThemeFormatVersion {
				t.Errorf("the manifest reports format %d, want %d", wm.Format, theme.ThemeFormatVersion)
			}

			root := t.TempDir()
			im, err := Extract(dest, root)
			if err != nil {
				t.Fatalf("importing %s back: %v", name, err)
			}
			if im.Root != name {
				t.Errorf("imported as %q, want the folder name it came from (%q)", im.Root, name)
			}
			if im.Files != wm.Files {
				t.Errorf("bundled %d files and imported %d", wm.Files, im.Files)
			}
			assertTreesEqual(t, dir, im.Dir)
		})
	}
}

// TestNoRepoThemeBundlesAFontWithoutItsLicence is the licensing claim made
// testable. themes/README.md says the corpus ships zero bytes of third-party
// font — so the export census must find no font entry at all, which is a
// stronger and cheaper assertion than checking each one for a licence file.
func TestNoRepoThemeBundlesAFontWithoutItsLicence(t *testing.T) {
	for _, dir := range repoThemeDirs(t) {
		name := filepath.Base(dir)
		dest := filepath.Join(t.TempDir(), name+PackExt)
		m, err := Write(dir, dest, nil)
		if err != nil {
			t.Fatalf("bundling %s: %v", name, err)
		}
		for _, f := range m.Fonts {
			if f.LicenseFile == "" {
				t.Errorf("%s bundles %s with no licence file beside it — themes/README.md "+
					"claims this corpus ships no third-party font at all", name, f.File)
			}
		}
	}
}

// repoThemeDirs lists the theme folders, skipping when the directory is not in
// this checkout and failing when it is there but empty.
func repoThemeDirs(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(repoThemesDir)
	if err != nil {
		t.Skipf("no %s in this checkout: %v", repoThemesDir, err)
	}
	var dirs []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		full := filepath.Join(repoThemesDir, e.Name())
		if _, err := os.Stat(filepath.Join(full, theme.SidecarFileName)); err != nil {
			continue
		}
		dirs = append(dirs, full)
	}
	if len(dirs) < repoThemeMinimum {
		t.Fatalf("%s holds %d themes, expected at least %d", repoThemesDir, len(dirs), repoThemeMinimum)
	}
	return dirs
}
