package ui

import (
	"os"
	"strings"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// TestThemeLoadRoots pins the #87 fix: a custom themes folder is searched first
// for a NAMED theme, but is dropped for the stock "default" so a custom
// themes/default can't shadow the built-in one (the bug — stock default became
// unreachable once a folder was set). The app directory is always present.
func TestThemeLoadRoots(t *testing.T) {
	const custom, exe = "C:/MyThemes", "C:/App"

	if got := themeLoadRoots("CoolTheme", custom, exe, ""); len(got) != 2 || got[0] != custom || got[1] != exe {
		t.Fatalf("named-theme roots = %v, want [%q %q] (custom first)", got, custom, exe)
	}
	if got := themeLoadRoots(theme.DefaultThemeName, custom, exe, ""); len(got) != 1 || got[0] != exe {
		t.Fatalf("default roots = %v, want [%q] only (custom root must be dropped)", got, exe)
	}
	if got := themeLoadRoots("CoolTheme", "", exe, ""); len(got) != 1 || got[0] != exe {
		t.Fatalf("no-folder roots = %v, want [%q]", got, exe)
	}
}

// TestThemeLoadRootOrderIsUnchangedForExistingInstalls is the W2 gate on the
// third root. Adding a search root is the one change in this wave that could
// silently swap which theme a user has been looking at for a year, so the
// property under test is not "the config root is searched" but "everything that
// resolved before still resolves the same way, in the same order, first".
//
// The config root therefore has to be LAST in every combination, and a caller
// that hands the same directory in twice must still get it once.
func TestThemeLoadRootOrderIsUnchangedForExistingInstalls(t *testing.T) {
	const (
		custom = "C:/MyThemes"
		exe    = "C:/App"
		cfg    = "C:/Users/x/AppData/Roaming/AsyncAO"
		// What ConfigBaseDir actually returns in a PORTABLE install: a config/
		// folder INSIDE the exe dir, never the exe dir itself (config/location.go,
		// resolveConfigBase). The write root there is <exe>/themes, which the exe
		// root already covers, so this third root probes <exe>/config/themes — a
		// normally-absent sibling. It is still listed, and still listed last.
		portableCfg = "C:/App/config"
	)

	cases := []struct {
		name              string
		theme             string
		custom, exe, base string
		want              []string
	}{
		// The pre-W2 answer was [custom exe]; the config root may only be appended
		// to it.
		{
			name:  "custom folder + config root: custom still first, config last",
			theme: "CoolTheme", custom: custom, exe: exe, base: cfg,
			want: []string{custom, exe, cfg},
		},
		{
			name:  "stock default still drops the custom root, and gains the config root",
			theme: theme.DefaultThemeName, custom: custom, exe: exe, base: cfg,
			want: []string{exe, cfg},
		},
		{
			name:  "no custom folder: exe dir stays the first root",
			theme: "CoolTheme", exe: exe, base: cfg,
			want: []string{exe, cfg},
		},
		{
			name:  "portable install: the config root is <exe>/config, listed last like any other",
			theme: "CoolTheme", exe: exe, base: portableCfg,
			want: []string{exe, portableCfg},
		},
		// DEFENSIVE, not a production state: no config policy returns the exe dir
		// (see themeSearchRoots' comment). The dedup is pinned anyway, because the
		// cost of a duplicated root is a directory walked twice for every theme.
		{
			name:  "a caller that passes the exe dir as the config root gets it once",
			theme: "CoolTheme", exe: exe, base: exe,
			want: []string{exe},
		},
		{
			name:  "no config root at all (unresolvable): the old list, unchanged",
			theme: "CoolTheme", custom: custom, exe: exe,
			want: []string{custom, exe},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := themeLoadRoots(tc.theme, tc.custom, tc.exe, tc.base)
			if len(got) != len(tc.want) {
				t.Fatalf("roots = %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("roots = %v, want %v (differs at %d)", got, tc.want, i)
				}
			}
			// The invariant behind the list: the config root is never ahead of a
			// root that existed before this wave.
			for i, r := range got {
				if r == tc.base && tc.base != tc.exe && i != len(got)-1 {
					t.Errorf("config root %q sits at position %d of %v — it must be last", r, i, got)
				}
			}
		})
	}
}

// TestThemePickerAndLoaderSearchTheSameRoots is the gate on the W2 defect this
// test file exists to prevent a second time: themeLoadRoots gained the config
// root, scanThemes did not, and on every NON-portable install (Flatpak, Program
// Files, the macOS tarball, and every user carrying an existing OS-config folder)
// a theme sitting in the write root the Settings screen prints — and that "Open
// folder" opens — loaded fine by name while being absent from the dropdown and
// unreachable by the ◀ ▶ arrows.
//
// The property is therefore not "three roots are searched" but "the list the user
// can PICK from covers every root the loader would resolve in".
func TestThemePickerAndLoaderSearchTheSameRoots(t *testing.T) {
	custom, exe, cfg := t.TempDir(), t.TempDir(), t.TempDir()
	writeCatalogTheme(t, custom, "from-custom", theme.DesignFileName)
	writeCatalogTheme(t, exe, "from-exe", theme.DesignFileName)
	// The regression case: a theme that exists ONLY in the config root.
	writeCatalogTheme(t, cfg, "from-config", theme.DesignFileName)

	roots := themeSearchRoots(custom, exe, cfg)
	names := scanThemeDirs(roots, "")
	for _, want := range []string{theme.DefaultThemeName, "from-custom", "from-exe", "from-config"} {
		if !containsString(names, want) {
			t.Errorf("theme %q is loadable but missing from the picker list %v", want, names)
		}
	}

	// And the loader searches exactly those roots, in that order, for each of
	// them — a name the picker offers must resolve where the picker found it.
	for _, name := range []string{"from-custom", "from-exe", "from-config"} {
		got := themeLoadRoots(name, custom, exe, cfg)
		if len(got) != len(roots) {
			t.Fatalf("loader roots for %q = %v, picker roots = %v", name, got, roots)
		}
		for i := range got {
			if got[i] != roots[i] {
				t.Fatalf("loader roots for %q = %v, picker roots = %v (differ at %d)", name, got, roots, i)
			}
		}
	}
}

// TestThemePickerBuildsItsRootsFromTheSharedList is the structural half of the
// gate above. That test compares two lists a maintainer could satisfy by
// hand-rolling the same three roots in scanThemes again — which is exactly how
// the drift happened — so this one pins the SOURCE: the picker's scan must call
// themeSearchRootsNow and must not gather roots of its own.
//
// A source census rather than a behavioural test because scanThemes resolves the
// real machine's executable and config directories, which a test cannot inject.
func TestThemePickerBuildsItsRootsFromTheSharedList(t *testing.T) {
	body := funcBodyInFile(t, "settings.go", "func (a *App) scanThemes(")
	if !strings.Contains(body, "themeSearchRootsNow(") {
		t.Errorf("scanThemes no longer builds its roots from themeSearchRootsNow — the picker and the loader can now disagree about which directories exist")
	}
	if strings.Contains(body, "os.Executable()") {
		t.Errorf("scanThemes gathers a root of its own again (os.Executable); the shared themeSearchRoots is the only list allowed to grow or shrink")
	}
}

// containsString is the local spelling of "is this name in the list" — the two
// tests above read better with it than with an inline loop.
func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// funcBodyInFile returns the source text of one function, from its declaration
// to the next top-level declaration. Deliberately crude: it is a census over a
// checked-in file in this same package, so a missing file or a renamed function
// is a failure (the thing being pinned moved), never a skip.
func funcBodyInFile(t *testing.T, file, decl string) string {
	t.Helper()
	src, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("reading %s: %v", file, err)
	}
	i := strings.Index(string(src), decl)
	if i < 0 {
		t.Fatalf("%s no longer contains %q — update this census to name the function that replaced it", file, decl)
	}
	rest := string(src)[i+len(decl):]
	if j := strings.Index(rest, "\nfunc "); j >= 0 {
		rest = rest[:j]
	}
	return rest
}

// The subtheme TIER order is pinned where Load lives
// (internal/theme/subtheme_test.go, TestSubthemeTierProbeOrderMatchesAO2); this
// file owns only the ROOTS the tiers are built over.
