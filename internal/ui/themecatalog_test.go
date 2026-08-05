package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// writeCatalogTheme fabricates a theme folder under <root>/themes/<name>.
// kindFiles names the INIs it ships, which is exactly what the badge reads.
func writeCatalogTheme(t *testing.T, root, name string, kindFiles ...string) string {
	t.Helper()
	dir := filepath.Join(root, theme.ThemesDirName, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range kindFiles {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("chatbox = 1, 2, 3, 4\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// TestThemeCatalogIsBoundedAndPublishedAtomically is the W2 gate on the scan.
// Three properties, all of them rules rather than preferences:
//
//   - BOUNDED (hard rule 4): a user who points the theme folder at a directory
//     holding a thousand entries gets themeCatalogCap of them and a flag saying
//     so — never an unbounded slice behind a pointer the draw path reads.
//   - PUBLISHED, not mutated: the snapshot the UI holds is immutable, so a scan
//     landing mid-frame swaps a pointer instead of growing a slice under it.
//   - BADGED: the kind comes from the files actually present, which is what
//     makes "this folder holds no theme files" a visible state instead of a
//     theme that silently does nothing.
func TestThemeCatalogIsBoundedAndPublishedAtomically(t *testing.T) {
	root := t.TempDir()

	// Well past the cap, so the bound is exercised rather than asserted.
	const overCap = themeCatalogCap + 24
	for i := 0; i < overCap; i++ {
		writeCatalogTheme(t, root, fmt.Sprintf("bulk%03d", i), theme.DesignFileName)
	}
	cat := scanThemeCatalog(root, nil, false)
	if cat == nil {
		t.Fatal("scan returned no catalog")
	}
	if len(cat.Entries) > themeCatalogCap {
		t.Errorf("catalog holds %d entries, cap is %d", len(cat.Entries), themeCatalogCap)
	}
	if !cat.Truncated {
		t.Errorf("catalog filled to the cap but Truncated is false — the UI would claim these are all the themes there are")
	}

	// Badges, on a fresh root small enough to enumerate.
	root2 := t.TempDir()
	writeCatalogTheme(t, root2, "classic", theme.DesignFileName)
	writeCatalogTheme(t, root2, "native", theme.SidecarFileName)
	writeCatalogTheme(t, root2, "both", theme.DesignFileName, theme.SidecarFileName)
	writeCatalogTheme(t, root2, "empty")
	// A subtheme folder (its own design INI) and a plain asset folder that must
	// NOT be offered as a variant.
	sub := filepath.Join(root2, theme.ThemesDirName, "classic", "night")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, theme.DesignFileName), []byte("chatbox = 9, 9, 9, 9\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root2, theme.ThemesDirName, "classic", "misc"), 0o755); err != nil {
		t.Fatal(err)
	}

	cat2 := scanThemeCatalog(root2, nil, false)
	if cat2 == nil {
		t.Fatal("scan returned no catalog")
	}
	// Truncated is not a mood: it means the cap was actually reached. (The scan
	// also sees this machine's real theme roots, so the assertion is the
	// invariant rather than a hard count.)
	if cat2.Truncated && len(cat2.Entries) < themeCatalogCap {
		t.Errorf("catalog claims truncation with only %d entries", len(cat2.Entries))
	}
	want := map[string]themeKind{
		"classic": themeKindAO2,
		"native":  themeKindNative,
		"both":    themeKindBoth,
		"empty":   themeKindBroken,
	}
	for name, kind := range want {
		e, ok := cat2.Entry(name)
		if !ok {
			t.Errorf("theme %q missing from the catalog", name)
			continue
		}
		if e.Kind != kind {
			t.Errorf("theme %q badged %q, want %q", name, themeKindLabel(e.Kind), themeKindLabel(kind))
		}
	}
	if e, _ := cat2.Entry("classic"); len(e.Subthemes) != 1 || e.Subthemes[0] != "night" {
		t.Errorf("classic subthemes = %v, want exactly [night] (misc/ is not a variant)", e.Subthemes)
	}
	// Entry() is case-insensitive because a folder name is, on this platform.
	if _, ok := cat2.Entry("CLASSIC"); !ok {
		t.Errorf("Entry is case-sensitive; folder names are not")
	}

	// The snapshot is published whole. Nothing mutates a live one: swapping
	// the pointer is the only way it changes.
	//
	// The count to compare against is CAPTURED, not hardcoded: this scan also
	// walks this machine's real theme roots (exe dir, config base), so the
	// number of entries depends on what the developer happens to have in
	// <config>/themes. The property under test is "publishing cat left cat2
	// alone", and a baseline taken before the publish tests exactly that
	// without going red the day someone uses the folder this wave ships.
	cat2Len := len(cat2.Entries)
	a := &App{}
	a.themeCat.Store(cat2)
	if got := a.themeCatalogSnapshot(); got != cat2 {
		t.Errorf("snapshot read back a different pointer")
	}
	a.themeCat.Store(cat)
	if got := a.themeCatalogSnapshot(); got != cat || len(cat2.Entries) != cat2Len {
		t.Errorf("publishing a new catalog disturbed the old one (cat2 held %d entries, now %d)",
			cat2Len, len(cat2.Entries))
	}
	// A nil snapshot answers instead of panicking — it is the state of the very
	// first frame after boot.
	var none *themeCatalog
	if _, ok := none.Entry("classic"); ok {
		t.Errorf("nil catalog claimed to hold a theme")
	}
}

// TestThemeBadgeAgreesWithWhatTheClientAcceptsAsATheme pins the badge against
// the app's OWN definition of a theme folder. themeINIFiles is that definition:
// normalizeThemeRoot uses it to decide a dropped folder is a theme, and
// theme.Load merges the design, fonts and sounds INIs independently, so a folder
// holding any ONE of them loads and does something. Telling the user such a
// folder holds no theme files would have the browser contradict the client that
// had just accepted it on drop — which is what this test exists to stop coming
// back (it did once: the badge read only the design INI and the sidecar).
func TestThemeBadgeAgreesWithWhatTheClientAcceptsAsATheme(t *testing.T) {
	for _, ini := range themeINIFiles {
		dir := writeCatalogTheme(t, t.TempDir(), "solo", ini)
		if got := themeKindOf(dir); got != themeKindAO2 {
			t.Errorf("a folder holding only %s badges %q, want %q — the client loads it and accepts it on drop",
				ini, themeKindLabel(got), themeKindLabel(themeKindAO2))
		}
		if _, pick := normalizeThemeRoot(dir); pick != "solo" {
			t.Errorf("normalizeThemeRoot no longer accepts a folder holding only %s — it and themeKindOf have drifted apart", ini)
		}
	}

	// The zero value still means one testable thing: NONE of the four files this
	// client reads out of a theme folder is present.
	if got := themeKindOf(writeCatalogTheme(t, t.TempDir(), "nothing")); got != themeKindBroken {
		t.Errorf("an empty folder badges %q, want the zero-value badge", themeKindLabel(got))
	}

	// Same rule one tier down: AO2 probes the subtheme path per element, so a
	// variant that only overrides the fonts INI is a real variant.
	themeDir := writeCatalogTheme(t, t.TempDir(), "classic", theme.DesignFileName)
	if err := os.MkdirAll(filepath.Join(themeDir, "night"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(themeDir, "night", theme.FontsFileName), []byte("message = 12\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if subs := scanSubthemes(themeDir); len(subs) != 1 || subs[0] != "night" {
		t.Errorf("subthemes = %v, want [night] — a fonts-only variant is one AO2 resolves", subs)
	}
}

// TestThemeCatalogFocusStatOnlyRescansWhenTheRootMoved pins the cheap trigger:
// focus-gain must cost one os.Stat and nothing else unless the write root's
// mtime actually changed. A rescan on every alt-tab is exactly the polling the
// no-watcher decision exists to avoid.
func TestThemeCatalogFocusStatOnlyRescansWhenTheRootMoved(t *testing.T) {
	root := t.TempDir()
	writeCatalogTheme(t, root, "one", theme.DesignFileName)

	prev := scanThemeCatalog(root, nil, false)
	if prev == nil {
		t.Fatal("scan returned no catalog")
	}
	// Unchanged root: the stat-only pass declines to publish.
	if got := scanThemeCatalog(root, prev, true); got != nil {
		t.Errorf("stat-only scan republished with nothing changed")
	}
	// With no previous snapshot there is nothing to compare against, so it
	// declines too rather than doing a full walk on every focus event.
	if got := scanThemeCatalog(root, nil, true); got != nil {
		t.Errorf("stat-only scan walked the disk with no baseline to compare")
	}
	// A moved write root republishes. RootMod is what the comparison reads, so
	// the test moves that directly — filesystem mtime granularity is a second
	// on some hosts, and this gate is about the COMPARISON, not the clock.
	stale := *prev
	stale.RootMod = prev.RootMod.Add(-time.Hour)
	if got := scanThemeCatalog(root, &stale, true); got == nil {
		t.Errorf("stat-only scan ignored a write root whose mtime moved")
	}
}

// TestThemeCatalogTriggerFloorIsOnlyForTheAutomaticOnes pins which of the four
// triggers themeScanMinInterval applies to. Getting this backwards makes the
// Refresh button feel broken for five seconds after the screen opens.
func TestThemeCatalogTriggerFloorIsOnlyForTheAutomaticOnes(t *testing.T) {
	prefs, err := config.New(filepath.Join(t.TempDir(), config.PrefsFileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = prefs.Close() })

	a := &App{d: Deps{Prefs: prefs}}
	a.themeCat.Store(&themeCatalog{ScannedAt: time.Now()}) // "just scanned"

	// On-open is floored: it must bail without starting anything.
	a.requestThemeCatalog(themeScanOnOpen)
	waitForCatalogScan(t, a)
	if a.themeCatBusy.Load() {
		t.Errorf("floored request left the single-flight latched")
	}

	// Explicit refresh ignores the floor.
	a.requestThemeCatalog(themeScanExplicit)
	waitForCatalogScan(t, a)

	// And an aged snapshot lets the automatic trigger through again.
	a.themeCat.Store(&themeCatalog{ScannedAt: time.Now().Add(-2 * themeScanMinInterval)})
	a.requestThemeCatalog(themeScanOnOpen)
	waitForCatalogScan(t, a)

	// The focus latch is one-shot: it must not re-arm itself.
	a.NoteFocusGained()
	if !a.consumeThemeFocusStat() {
		t.Errorf("focus-gain latch did not survive to its consumer")
	}
	if a.consumeThemeFocusStat() {
		t.Errorf("focus-gain latch fired twice for one focus event")
	}
}

// waitForCatalogScan drains any in-flight scan so the test's next assertion is
// about a settled state (and so -race sees the goroutine finish).
func waitForCatalogScan(t *testing.T, a *App) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for a.themeCatBusy.Load() {
		if time.Now().After(deadline) {
			t.Fatal("theme catalog scan never finished")
		}
		time.Sleep(time.Millisecond)
	}
}
