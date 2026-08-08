package theme

// THE REPOSITORY'S OWN THEMES, RUN THROUGH THE REAL READER (v1.90.0 W8).
//
// themes/README.md has carried a "Dev note" asking for this gate since the theme
// factory shipped: fourteen hand-authored themes live in the repository, they are
// the format's only large corpus of files nobody generated, and until now NOTHING
// executed them. The audit that wrote that note found two of them carrying nine
// `gen_params` entries against a cap of EIGHT — a refusal, not a truncation, so
// those two themes would not have loaded at all.
//
// A HAND-AUTHORED CORPUS IS THE ONLY TEST THE WRITER CANNOT MAKE SELF-FULFILLING.
// Every other round-trip fixture in this package was produced by our own writer,
// so it can only prove the writer agrees with itself; these files were typed by a
// human against docs/THEME-FORMAT.md, which is the document the format actually
// promises.
//
// The directory may legitimately be absent (a source tree checked out without it,
// a vendored build), so the walk SKIPS when there is nothing there — but it fails
// loudly if the folder exists and holds no theme, because a census that read
// nothing passes everything.

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// repoThemesDir is where the standalone themes live, relative to this package.
const repoThemesDir = "../../themes"

// repoThemeMinimum is the count below which the corpus is assumed broken rather
// than small. Fourteen ship today; the floor is deliberately under that so adding
// or retiring one is not a test edit, and deliberately above zero so an empty or
// mis-pathed directory cannot pass.
const repoThemeMinimum = 10

// TestRepoThemesParse is themes/README.md's Dev note, points 1, 2 and 5, plus the
// property the note could not have asked for because the writer did not exist
// yet: every one of them survives its own writer byte-identically.
func TestRepoThemesParse(t *testing.T) {
	dirs := repoThemeDirs(t)
	for _, dir := range dirs {
		name := filepath.Base(dir)
		t.Run(name, func(t *testing.T) {
			// (1) The sidecar parses, with ZERO notes. A note is a degrade — an
			// unknown enum, a clamped number, a section this build ignored — and a
			// theme WE ship must not be degrading against our own reader.
			sc, err := LoadSidecar(filepath.Join(dir, SidecarFileName))
			if err != nil {
				t.Fatalf("%s will not load: %v", SidecarFileName, err)
			}
			if sc == nil {
				t.Fatalf("%s has no %s — every theme here is a native theme", name, SidecarFileName)
			}
			if notes := sc.Notes(); len(notes) > 0 {
				t.Errorf("%d degrade note(s), the first being %q — a theme we ship must read clean",
					len(notes), notes[0])
			}
			// The unknown census, MINUS the declared vendor namespace. `[x.*]` is the
			// format's own private-extension prefix and six of these themes use it on
			// purpose (docs/THEME-FORMAT.md: carried through untouched, named in the
			// import report), so counting those would be testing the feature as a
			// defect. Anything OUTSIDE `x.` in our own corpus is a typo'd key nobody
			// noticed — which is exactly the class the audit that asked for this gate
			// was finding by hand.
			if stray := strayUnknown(sc.Unknown()); len(stray) > 0 {
				t.Errorf("%d entr(y|ies) this build does not define and that are not in the "+
					"`x.` vendor namespace, the first being %q — in our own themes that is a typo, "+
					"not forward compatibility", len(stray), stray[0])
			}

			// (2) courtroom_design.ini exists and declares both keys
			// applyAO2DefaultRects gates the canvas on. Without the pair a standalone
			// theme gets no canvas, and then every [overrides] rect and every anchored
			// element is silently dropped.
			ini, err := LoadINI(filepath.Join(dir, DesignFileName))
			if err != nil {
				t.Fatalf("%s: %v", DesignFileName, err)
			}
			for _, key := range []string{"courtroom", "viewport"} {
				if _, ok := ini.Get(key); !ok {
					t.Errorf("%s declares no %q — the theme gets no canvas and every "+
						"anchored element is dropped", DesignFileName, key)
				}
			}

			// (5) [media] is empty in all of them. The README's originality claim
			// ("zero bytes of third-party art, font or audio") is TESTABLE, so it is
			// tested rather than asserted in prose.
			if len(sc.Media) > 0 {
				t.Errorf("declares %d [media] entr(y|ies), the first being %q — the "+
					"themes/README.md originality claim says every one of them is procedural",
					len(sc.Media), sc.Media[0].ID)
			}

			// AND IT SURVIVES ITS OWN WRITER. Load, write, compare: a save that
			// changed one byte of a file no one edited would be the comment-eating
			// regression INIDoc exists to prevent, caught on the only corpus in the
			// tree that a human wrote.
			src, err := os.ReadFile(filepath.Join(dir, SidecarFileName))
			if err != nil {
				t.Fatalf("re-reading %s: %v", SidecarFileName, err)
			}
			out, err := sc.Bytes()
			if err != nil {
				t.Fatalf("writing it back refused: %v", err)
			}
			if string(out) != string(src) {
				t.Errorf("a save that changed nothing rewrote the file:\n%s",
					firstDifference(string(src), string(out)))
			}
		})
	}
}

// strayUnknown drops the entries that are DELIBERATE — the `x.<vendor>.<name>`
// private-extension sections the format publishes and these themes use — and
// returns whatever is left, which in our own corpus can only be a mistake.
func strayUnknown(entries []string) []string {
	var out []string
	for _, e := range entries {
		if strings.HasPrefix(e, "x.") {
			continue
		}
		out = append(out, e)
	}
	return out
}

// repoThemeDirs lists the theme folders, skipping the whole gate when the
// directory is not in this checkout and failing when it is there but empty.
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
		if _, err := os.Stat(filepath.Join(full, SidecarFileName)); err != nil {
			continue
		}
		dirs = append(dirs, full)
	}
	if len(dirs) < repoThemeMinimum {
		t.Fatalf("%s holds %d themes, expected at least %d — a census that reads nothing "+
			"passes everything", repoThemesDir, len(dirs), repoThemeMinimum)
	}
	return dirs
}

// firstDifference reports where two versions of a file diverge, with the line, so
// a failure names the byte instead of dumping two files at the reader.
func firstDifference(a, b string) string {
	la, lb := strings.Split(a, "\n"), strings.Split(b, "\n")
	for i := 0; i < len(la) && i < len(lb); i++ {
		if la[i] != lb[i] {
			return "line " + strconv.Itoa(i+1) + ":\n  was  " + la[i] + "\n  now  " + lb[i]
		}
	}
	return "the files differ in length: " + strconv.Itoa(len(la)) + " lines became " + strconv.Itoa(len(lb))
}
