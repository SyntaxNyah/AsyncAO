package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// TestThemePickerDedupesFoldedNames — the dropdown and the catalog answer the
// same question about the same directories, so they have to agree on what "the
// same theme" is. The catalog folds case (a folder name does, on both platforms
// this ships on); the picker keyed case-SENSITIVELY, so a user with "Nocturne"
// beside a "nocturne" got two dropdown rows for the one catalog entry — and the
// second row applied a theme name that resolves to the first row's folder.
func TestThemePickerDedupesFoldedNames(t *testing.T) {
	// Two roots, because one directory cannot hold both spellings on a folding
	// filesystem — and two roots is the shape that produces this in the wild
	// (a theme shipped beside the exe and re-downloaded into the config base).
	rootA, rootB := t.TempDir(), t.TempDir()
	writeCatalogTheme(t, rootA, "Nocturne", theme.DesignFileName)
	writeCatalogTheme(t, rootB, "nocturne", theme.DesignFileName)

	names := scanThemeDirs([]string{rootA, rootB}, "")
	got := 0
	for _, n := range names {
		if strings.EqualFold(n, "nocturne") {
			got++
		}
	}
	if got != 1 {
		t.Errorf("the picker listed %q %d times: %v — folder names fold, and the catalog holds one entry for them", "nocturne", got, names)
	}
	// The spelling kept is the one the FIRST root actually holds, so the label
	// matches the directory the loader will open.
	for _, n := range names {
		if strings.EqualFold(n, "nocturne") && n != "Nocturne" {
			t.Errorf("kept %q, want the first root's own spelling %q", n, "Nocturne")
		}
	}
	// The built-in default is still first and still exactly once.
	if len(names) == 0 || names[0] != theme.DefaultThemeName {
		t.Fatalf("names = %v, want %q first", names, theme.DefaultThemeName)
	}
}

// TestThemePickerListIsBounded — hard rule 4 applies to the dropdown as much as
// to the catalog it mirrors, and the bound has to be honest: the row says the
// list is a prefix rather than implying these are all the themes there are.
func TestThemePickerListIsBounded(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < themePickerCap+16; i++ {
		writeCatalogTheme(t, root, fmt.Sprintf("bulk%03d", i), theme.DesignFileName)
	}

	names := scanThemeDirs([]string{root}, "")
	if len(names) > themePickerCap {
		t.Errorf("picker list holds %d names, cap is %d", len(names), themePickerCap)
	}
	if !themeListTruncated(names) {
		t.Error("a list filled to the cap must report as truncated — the row would otherwise claim these are all the themes there are")
	}
	if text := themeFoundTextFresh(names); !strings.Contains(text, "still load by name") {
		t.Errorf("the count line reads %q — at the bound it must say the rest are still on disk", text)
	}

	// The bare-folder import (#21) is appended even AT the cap: it is the theme
	// the user just picked, and dropping it makes the pick unreachable the moment
	// it is made — the vanishing-theme bug the pick argument exists to prevent.
	withPick := scanThemeDirs([]string{root}, "dropped-in")
	if last := withPick[len(withPick)-1]; last != "dropped-in" {
		t.Errorf("the just-imported folder was dropped by the cap (list ends %q) — the pick must survive the bound", last)
	}
	// And it is NOT appended twice when a root already produced it.
	dup := scanThemeDirs([]string{root}, "BULK000")
	seen := 0
	for _, n := range dup {
		if strings.EqualFold(n, "bulk000") {
			seen++
		}
	}
	if seen != 1 {
		t.Errorf("the pick was listed %d times — the append must fold case like the walk", seen)
	}
}

// TestThemePickerFitsAWholeCatalogsWorthOfThemes is the off-by-one guard on that
// bound. themePickerCap's whole promise is that the dropdown and the browser
// answer the same question about the same directories — so a collection of
// exactly themeCatalogCap real themes, which the catalog holds in full and
// reports as NOT truncated, must be listed in full by the picker too. It was
// not: scanThemeDirs seeds the built-in "default" NAME first, so at a cap of
// themeCatalogCap the seed ate one directory's slot and the 128th theme became
// the one the browser shows and the dropdown cannot reach.
func TestThemePickerFitsAWholeCatalogsWorthOfThemes(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < themeCatalogCap; i++ {
		writeCatalogTheme(t, root, fmt.Sprintf("bulk%03d", i), theme.DesignFileName)
	}

	names := scanThemeDirs([]string{root}, "")
	for i := 0; i < themeCatalogCap; i++ {
		if want := fmt.Sprintf("bulk%03d", i); !containsString(names, want) {
			t.Fatalf("theme %q is in the catalog but missing from the %d-name picker list — the seed ate a directory's slot", want, len(names))
		}
	}
	// The built-in seed is the only extra: one name per directory, plus "default".
	if len(names) != themeCatalogCap+1 {
		t.Errorf("picker list holds %d names, want %d themes + the %q seed", len(names), themeCatalogCap, theme.DefaultThemeName)
	}
}

// themeFoundTextFresh calls themeFoundText with the package-level memo reset and
// restored, so a test can read the text without leaking cache state into
// whatever else touches settingsState.
func themeFoundTextFresh(names []string) string {
	text, forN := settings.themeCountText, settings.themeCountFor
	settings.themeCountText, settings.themeCountFor = "", -1
	out := themeFoundText(names)
	settings.themeCountText, settings.themeCountFor = text, forN
	return out
}

// TestSubthemeRowShowsAStalePickInsteadOfClaimingNone — AO2 keeps the subtheme
// preference across theme changes, so "the pref names a folder THIS theme does
// not have" is a normal state, not an error. The dropdown used to fall back to
// row 0 for it, which reads as "no subtheme is set" over a preference that is
// set — and the pick would silently reappear on switching back. Worse, the Clear
// button only existed on the other arm, so in a theme that DOES ship variants the
// stale pick could not be seen or cleared at all.
func TestSubthemeRowShowsAStalePickInsteadOfClaimingNone(t *testing.T) {
	cat := &themeCatalog{
		WriteRoot: filepath.Join("t", "themes"),
		Entries: []themeCatalogEntry{{
			Name:      "classic",
			Dir:       filepath.Join("t", "themes", "classic"),
			Kind:      themeKindAO2,
			Origin:    themeOriginYours,
			Subthemes: []string{"night", "sepia"},
		}},
	}

	var rows themeRowText
	rows.rebuild(cat, "classic", "night") // a pick this theme really has
	if rows.subStale != -1 {
		t.Errorf("subStale = %d, want -1 — the theme has this variant", rows.subStale)
	}
	if want := 1; rows.subSel != want {
		t.Errorf("subSel = %d, want %d (row 0 is %q)", rows.subSel, want, subthemeNoneLabel)
	}

	rows.rebuild(cat, "classic", "aurora") // a pick made for ANOTHER theme
	if rows.subStale < 0 {
		t.Fatalf("no synthesized row for a pick this theme lacks: opts = %v", rows.subOpts)
	}
	if rows.subSel != rows.subStale {
		t.Errorf("subSel = %d, want the synthesized row %d — falling back to 0 reads as \"(none)\" over a set preference", rows.subSel, rows.subStale)
	}
	got := rows.subOpts[rows.subStale]
	if got != "aurora"+subthemeMissingSuffix {
		t.Errorf("synthesized row = %q, want %q", got, "aurora"+subthemeMissingSuffix)
	}
	// The DECORATED label must never become the preference: the draw maps that row
	// back to the undecorated name (drawThemeCatalogRows' next == subStale arm).
	if strings.TrimSuffix(got, subthemeMissingSuffix) != "aurora" {
		t.Error("the synthesized label cannot be mapped back to the folder name it stands for")
	}
	// "(none)" is still row 0, so clearing is one pick away on this arm.
	if rows.subOpts[0] != subthemeNoneLabel {
		t.Errorf("row 0 = %q, want %q — it is how the stale pick gets cleared here", rows.subOpts[0], subthemeNoneLabel)
	}

	// A theme with NO variants has no list to carry the pick, so that arm keeps
	// its words-and-a-Clear-button shape.
	bare := &themeCatalog{Entries: []themeCatalogEntry{{Name: "flat", Kind: themeKindAO2}}, WriteRoot: "t"}
	rows.rebuild(bare, "flat", "aurora")
	if len(rows.subOpts) != 0 {
		t.Errorf("a theme with no variants drew a dropdown: %v", rows.subOpts)
	}
	if !strings.Contains(rows.subMsg, "aurora") {
		t.Errorf("subMsg = %q, want the set pick named in it", rows.subMsg)
	}
}

// TestThemeRowTextRebuildsOnlyWhenItsInputsMove — the memo's whole point. Each of
// the three inputs must invalidate it, and nothing else may: a per-frame rebuild
// would put the concatenations, the options slice and the fmt.Sprintf back on the
// draw path (the settings-text doctrine).
func TestThemeRowTextRebuildsOnlyWhenItsInputsMove(t *testing.T) {
	cat := &themeCatalog{
		WriteRoot: "t",
		Entries:   []themeCatalogEntry{{Name: "classic", Kind: themeKindBoth, Origin: themeOriginBundled, Subthemes: []string{"night"}}},
	}
	other := &themeCatalog{WriteRoot: "t"}

	var rows themeRowText
	if !rows.stale(cat, "classic", "") {
		t.Fatal("a zero-value memo must be stale — it holds no rows at all")
	}
	rows.rebuild(cat, "classic", "")
	if rows.stale(cat, "classic", "") {
		t.Error("the memo rebuilt with nothing changed — that is a per-frame allocation")
	}
	if !rows.stale(other, "classic", "") {
		t.Error("a republished catalog did not invalidate the rows")
	}
	if !rows.stale(cat, "nocturne", "") {
		t.Error("a theme change did not invalidate the rows")
	}
	if !rows.stale(cat, "classic", "night") {
		t.Error("a subtheme change did not invalidate the rows")
	}
	// The badge is built from the entry, not re-derived per draw.
	if want := themeKindLabel(themeKindBoth) + " · " + themeOriginLabel(themeOriginBundled); rows.badge != want {
		t.Errorf("badge = %q, want %q", rows.badge, want)
	}
	// A nil snapshot is the first-frame state and must be a valid memo key, not a
	// permanent rebuild.
	rows.rebuild(nil, "classic", "")
	if rows.stale(nil, "classic", "") {
		t.Error("the pre-first-scan (nil catalog) state rebuilds every frame")
	}
	if rows.have {
		t.Error("a nil catalog cannot hold an entry")
	}
}

// TestThemeCatalogNoteIsTruncationAware — the line under the write root is the
// only place the browser admits its list is a prefix.
func TestThemeCatalogNoteIsTruncationAware(t *testing.T) {
	var rows themeRowText
	rows.rebuild(&themeCatalog{WriteRoot: "t"}, "default", "")
	if rows.note != themeDropNote {
		t.Errorf("note = %q, want the drop hint", rows.note)
	}
	rows.rebuild(&themeCatalog{WriteRoot: "t", Truncated: true}, "default", "")
	if !strings.Contains(rows.note, fmt.Sprint(themeCatalogCap)) || !strings.Contains(rows.note, "still load by name") {
		t.Errorf("truncated note = %q — it must name the cap and say the rest still load", rows.note)
	}
}

// TestThemeWriteRootFallbackIsResolvedInTheMemo pins that the config lookup the
// row falls back to happens at REBUILD time, never per frame (hard rule 2 keeps
// syscalls off a draw path, and UserThemesDir's memos are only warm because boot
// filled them).
func TestThemeWriteRootFallbackIsResolvedInTheMemo(t *testing.T) {
	var rows themeRowText
	rows.rebuild(&themeCatalog{}, "default", "") // published, but no write root in it
	// Either a real path or "" (a host where the location cannot be resolved) —
	// what matters is that the memo holds the answer, so the draw reads a field.
	if rows.root != "" {
		if !filepath.IsAbs(rows.root) {
			t.Errorf("fallback root %q is not absolute", rows.root)
		}
		if _, err := os.Stat(filepath.Dir(rows.root)); err != nil && !os.IsNotExist(err) {
			t.Errorf("fallback root %q is not stat-able: %v", rows.root, err)
		}
	}
	// The published root always wins over the fallback.
	rows.rebuild(&themeCatalog{WriteRoot: filepath.Join("published", "themes")}, "default", "")
	if rows.root != filepath.Join("published", "themes") {
		t.Errorf("root = %q, want the snapshot's own WriteRoot", rows.root)
	}
}
