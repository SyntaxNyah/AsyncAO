package ui

// COPY-FOR-EDITING GATES (v1.90.0 W8) — docs/wip/THEME-EDITOR-DESIGN.md §Q5.
// The class they close is named in CLAUDE.md rule 11: a primitive whose only
// caller is a test. themepack.CopyForEditing shipped with 223 lines of production
// code and one driver, and that driver was TestCopyForEditingLeavesTheOriginal-
// Untouched. TestTheCopyPrimitiveHasAProductionDriver is the census that makes the
// wiring undeletable.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/theme"
	"github.com/SyntaxNyah/AsyncAO/internal/themepack"
)

// copyGateWait bounds every await here. Generous rather than tight: the work
// behind it is a real recursive copy on a real temp directory.
const copyGateWait = 10 * time.Second

// copyRig stages an App over an AO2-only theme that lives somewhere this client
// may NOT write — the state every AO2 user is in on first run — and returns the
// source folder and the write root.
// The catalog is built by hand rather than scanned: the scan reads the developer's
// real theme roots, and a gate that depended on those would pass or fail on what
// happens to be installed.
func copyRig(t *testing.T, origin themeOrigin) (*App, string, string) {
	t.Helper()
	base := t.TempDir()
	src := filepath.Join(base, "bundled", theme.ThemesDirName, "paper")
	root := filepath.Join(base, "mine", theme.ThemesDirName)
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatalf("making the source theme: %v", err)
	}
	design := "; the author's own file\ncourtroom = 0, 0, 256, 192\nviewport = 0, 0, 256, 192\n"
	if err := os.WriteFile(filepath.Join(src, theme.DesignFileName), []byte(design), 0o644); err != nil {
		t.Fatalf("writing %s: %v", theme.DesignFileName, err)
	}
	a := &App{env: env{}}
	a.d.Prefs = newTestPrefs(t)
	a.d.Prefs.SetTheme("paper", "")
	a.skin.themeCat.Store(&themeCatalog{
		Entries: []themeCatalogEntry{{
			Name: "paper", Dir: src, Kind: themeKindAO2, Origin: origin,
		}},
		WriteRoot: root,
		ScannedAt: time.Now(),
	})
	return a, src, root
}

// ---------------------------------------------------------------------------
// The census — the wiring cannot be deleted
// ---------------------------------------------------------------------------

// TestSaveRefusalOffersTheCopy drives the real editorSave on a bundled theme and
// asserts the refusal: the first Save arms the copy offer (the press-again chip),
// and the note names the act that fixes it — so a bundled theme can't take a
// session's work and then strand the user with advice to copy a folder by hand.
func TestSaveRefusalOffersTheCopy(t *testing.T) {
	a, _, _ := copyRig(t, themeOriginBundled)
	a.elemBake.themeSidecar = theme.NewSidecar()
	if !a.openThemeEditorForTest(ScreenLobby) {
		t.Fatal("a bundled theme with a native tier would not open in the editor")
	}
	a.editorSave()
	if a.te.copyAt.IsZero() {
		t.Error("Save on a bundled theme did not arm the copy offer — a second Save never starts the copy")
	}
	if !strings.Contains(a.te.status, "Press Save again") {
		t.Errorf("the refusal note %q does not name the act that fixes it", a.te.status)
	}
}

// ---------------------------------------------------------------------------
// The row
// ---------------------------------------------------------------------------

// TestTheThemeEditorRowAlwaysOffersAnAct is the defect, made mechanical: the row
// drew "copy it for editing first" with NO button beside it.
func TestTheThemeEditorRowAlwaysOffersAnAct(t *testing.T) {
	cases := []struct {
		reason themeEditReason
		busy   bool
		copies bool
		why    string
	}{
		{themeEditInPlace, false, false, "a theme that is ours opens directly"},
		{themeEditNoNativeTier, false, true, "a plain AO2 theme has nothing to open — the copy makes the tier"},
		{themeEditNotYours, false, true, "a bundled theme must be copied before it can be saved"},
		{themeEditNoNativeTier, true, true, "a copy in flight still owns the button"},
	}
	seen := map[string]bool{}
	for _, tc := range cases {
		row := buildThemeEditorRow(tc.reason, tc.busy)
		if row.button == "" {
			t.Errorf("reason %d busy=%v draws NO button — %s", tc.reason, tc.busy, tc.why)
		}
		if row.note == "" {
			t.Errorf("reason %d busy=%v says nothing", tc.reason, tc.busy)
		}
		if row.copies != tc.copies {
			t.Errorf("reason %d busy=%v copies=%v, want %v — %s", tc.reason, tc.busy, row.copies, tc.copies, tc.why)
		}
		seen[row.note] = true
	}
	if len(seen) != len(cases) {
		t.Errorf("the four states share %d sentences — a row that says the same thing in two states cannot "+
			"tell the user which one they are in", len(seen))
	}
}

// TestTheRowFollowsTheAppliedTheme drives the App-level reader, so the wording and
// the act cannot be right in the pure builder and wrong on screen.
func TestTheRowFollowsTheAppliedTheme(t *testing.T) {
	a, _, _ := copyRig(t, themeOriginBundled)
	if row := a.themeEditorRow(); !row.copies {
		t.Fatalf("an AO2 theme with no native tier offers %q — there is nothing for the editor to open", row.button)
	}
	// A native tier, but somebody else's folder: still a copy, and offered BEFORE
	// the session rather than after it.
	a.elemBake.themeSidecar = theme.NewSidecar()
	if row := a.themeEditorRow(); !row.copies {
		t.Errorf("a bundled theme with a native tier offers %q — Save would refuse it after a whole "+
			"session's work", row.button)
	}
	// Ours: edited in place, no copy.
	cat := a.themeCatalogSnapshot()
	next := *cat
	next.Entries = []themeCatalogEntry{{Name: "paper", Dir: cat.Entries[0].Dir, Origin: themeOriginYours}}
	a.skin.themeCat.Store(&next)
	if row := a.themeEditorRow(); row.copies {
		t.Errorf("a theme that is already ours offers %q — copying it would duplicate a folder for nothing", row.button)
	}
	// THE SETTINGS-TEXT DOCTRINE (themeRowText): this row draws every frame the
	// Theme tab is open, and the label cache keys on the string — so a formatted
	// sentence here would rasterise a fresh texture sixty times a second.
	if n := testing.AllocsPerRun(200, func() { _ = a.themeEditorRow() }); n != 0 {
		t.Errorf("the theme-editor row allocates %.1f/op, want 0 — its four sentences are constants for "+
			"exactly this reason", n)
	}
}

// ---------------------------------------------------------------------------
// The act
// ---------------------------------------------------------------------------

// TestCopyingAnAO2ThemeMakesItEditableAndLeavesTheOriginalAlone is the first-run
// path end to end, through the client's own button.
func TestCopyingAnAO2ThemeMakesItEditableAndLeavesTheOriginalAlone(t *testing.T) {
	a, src, root := copyRig(t, themeOriginBundled)
	before := treeUnder(t, src)
	// A layout tweak on the ORIGINAL: design §Q5 step 5 says it comes along, or the
	// copy looks wrong the moment it applies.
	a.d.Prefs.SetThemeRectOverride("paper", "ic_chat_message", [4]int{1, 2, 3, 4})

	if row := a.themeEditorRow(); !row.copies {
		t.Fatalf("the row offers %q for a theme with no native tier", row.button)
	}
	a.copyAppliedThemeForEditing(themeCopyThenOpen)
	if a.skin.themeCopy == nil {
		t.Fatalf("the button started nothing: %s", a.warnLine)
	}
	if !a.awaitThemeCopy(copyGateWait) {
		t.Fatalf("the copy never finished: %s", a.warnLine)
	}

	dir := filepath.Join(root, "paper (edited)")
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("no copy at %s: %v", dir, err)
	}
	// THE NATIVE TIER NOW EXISTS — that is what makes an AO2 theme editable at all —
	// and it records where it came from.
	body, err := os.ReadFile(filepath.Join(dir, theme.SidecarFileName))
	if err != nil {
		t.Fatalf("the copy has no %s: %v", theme.SidecarFileName, err)
	}
	if !strings.Contains(string(body), "derived_from = paper") {
		t.Errorf("the copy does not record where it came from:\n%s", body)
	}
	// THE ORIGINAL IS UNTOUCHED — the highest-blast-radius promise in the feature.
	after := treeUnder(t, src)
	if len(after) != len(before) {
		t.Fatalf("the ORIGINAL folder now holds %d files, was %d — a copy must never write into it",
			len(after), len(before))
	}
	for rel, want := range before {
		if after[rel] != want {
			t.Errorf("the ORIGINAL's %s was modified by the copy", rel)
		}
	}
	// THE COPY IS APPLIED: you can only edit what is on screen.
	if name, _ := a.d.Prefs.Load().Theme(); name != "paper (edited)" {
		t.Errorf("the copy was not selected: pref says %q", name)
	}
	if a.skin.themeGen.Load() == 0 {
		t.Error("the copy applied no theme — the editor opens over the APPLIED theme, so a copy that does " +
			"not apply cannot be edited")
	}
	// THE TWEAKS CAME ALONG.
	if got := a.d.Prefs.Load().ThemeRectOverrides("paper (edited)"); got["ic_chat_message"] != [4]int{1, 2, 3, 4} {
		t.Errorf("the layout override did not follow the copy: %v", got)
	}
	// AND THE EDITOR IS ARMED to open over it once that apply lands.
	if a.skin.themeEditArmGen == 0 {
		t.Error("nothing arms the editor — \"Copy for editing\" would copy and then leave the user on the " +
			"Settings screen with no editor")
	}
	drainThemeApply(t, a, a.skin.themeGen.Load())
}

// TestTheCopyIsInTheCatalogBeforeAnyRescan. The editor's Save resolves its write
// root THROUGH the catalog, and requestThemeCatalog is single-flight and floored —
// so a copy that waited for a scan could be invisible for seconds, and a Save in
// that window refuses a theme that is sitting on disk.
func TestTheCopyIsInTheCatalogBeforeAnyRescan(t *testing.T) {
	a, _, root := copyRig(t, themeOriginBundled)
	dir := filepath.Join(root, "paper (edited)")
	a.noteCopiedTheme("paper", &themepack.Manifest{Root: "paper (edited)", Dir: dir})

	cat := a.themeCatalogSnapshot()
	entry, ok := cat.Entry("paper (edited)")
	if !ok {
		t.Fatal("the copy is not in the catalog — the very next Save would refuse a theme sitting on disk")
	}
	if entry.Dir != dir {
		t.Errorf("the entry points at %q, want the folder that was written", entry.Dir)
	}
	if entry.Origin != themeOriginYours {
		t.Errorf("the copy is badged %q — it was written into the snapshot's own write root",
			themeOriginLabel(entry.Origin))
	}
	if entry.Kind != themeKindBoth {
		t.Errorf("the copy is badged %q, want AO2 + native — it carries the source's INIs and the sidecar "+
			"the provenance stamp wrote", themeKindLabel(entry.Kind))
	}
	// The SOURCE is untouched in the snapshot too: a seed that rewrote it would make
	// the original look editable and the editor would write into somebody else's folder.
	if src, _ := cat.Entry("paper"); src.Origin != themeOriginBundled {
		t.Errorf("the source is now badged %q", themeOriginLabel(src.Origin))
	}
	// SORTED as the scanner publishes, so the picker does not jump when the real
	// scan lands.
	if len(cat.Entries) != 2 || cat.Entries[0].Name != "paper" {
		t.Errorf("the seeded snapshot is %v — it must be in the scanner's own order", cat.Entries)
	}
	// IDEMPOTENT: a second seed of the same folder must not double the row.
	a.noteCopiedTheme("paper", &themepack.Manifest{Root: "paper (edited)", Dir: dir})
	if got := len(a.themeCatalogSnapshot().Entries); got != 2 {
		t.Errorf("seeding twice produced %d entries", got)
	}
}

// TestASecondCopyIsRefusedRatherThanRaced — hard rule 4 applied to the copy: the
// job pointer IS the flag.
func TestASecondCopyIsRefusedRatherThanRaced(t *testing.T) {
	a, _, _ := copyRig(t, themeOriginBundled)
	a.copyThemeForEditing("paper", themeCopyThenOpen)
	first := a.skin.themeCopy
	if first == nil {
		t.Fatalf("the copy started nothing: %s", a.warnLine)
	}
	a.copyThemeForEditing("paper", themeCopyThenOpen)
	if a.skin.themeCopy != first {
		t.Fatal("a second press replaced the job in flight — the first goroutine's result would land on a " +
			"job nobody is holding")
	}
	if !strings.Contains(a.warnLine, "already") {
		t.Errorf("the refusal was silent: %q", a.warnLine)
	}
	if !a.awaitThemeCopy(copyGateWait) {
		t.Fatalf("the copy never finished: %s", a.warnLine)
	}
	drainThemeApply(t, a, a.skin.themeGen.Load())
}

// TestATagalongCopyOfAThemeWeAlreadyOwnIsRefused. The offer and the act read ONE
// predicate, so a copy nobody needs cannot be started by a stale button, a hotkey
// or a future call site.
func TestATagalongCopyOfAThemeWeAlreadyOwnIsRefused(t *testing.T) {
	a, src, _ := copyRig(t, themeOriginYours)
	a.elemBake.themeSidecar = theme.NewSidecar()
	a.copyThemeForEditing("paper", themeCopyThenOpen)
	if a.skin.themeCopy != nil {
		t.Fatal("a theme that is already ours was duplicated — the editor can write it where it is")
	}
	if !strings.Contains(a.warnLine, "already yours") {
		t.Errorf("the refusal is %q — it must say why nothing happened", a.warnLine)
	}
	if entries, _ := os.ReadDir(filepath.Dir(src)); len(entries) != 1 {
		t.Errorf("%d folders beside the source, want 1 — the refusal wrote something", len(entries))
	}
}

// TestARefusedCopySaysWhyAndWritesNothing — every refusal names the offender and
// the fix (design §3.3); "failed" answers nothing.
func TestARefusedCopySaysWhyAndWritesNothing(t *testing.T) {
	a, _, root := copyRig(t, themeOriginBundled)
	a.copyThemeForEditing("a theme that is not installed", themeCopyThenOpen)
	if a.skin.themeCopy != nil {
		t.Fatal("a copy of a theme the catalog does not know started anyway")
	}
	if !strings.Contains(a.warnLine, "not in the theme list") {
		t.Errorf("the refusal is %q", a.warnLine)
	}
	if _, err := os.Stat(root); err == nil {
		if entries, _ := os.ReadDir(root); len(entries) > 0 {
			t.Errorf("a refused copy created %d things in the themes folder", len(entries))
		}
	}
}

// TestTheEditorOnlyOpensItselfForTheApplyThatArmedIt. The arming names ONE
// generation, exactly as the #35 window snap does, so it cannot leak onto
// healTheme's eviction re-kick, the texture-filter apply or a boot apply.
func TestTheEditorOnlyOpensItselfForTheApplyThatArmedIt(t *testing.T) {
	a := &App{env: env{}}
	a.skin.themeEditArmGen, a.skin.themeEditArmFrom = 7, ScreenSettings
	a.screen = ScreenSettings

	a.maybeOpenEditorAfterCopy(6) // an OLDER apply
	if a.te != nil {
		t.Fatal("an unrelated older apply opened the editor")
	}
	if a.skin.themeEditArmGen != 7 {
		t.Fatal("an older apply spent the arming — the copy's own landing would never open the editor")
	}
	a.maybeOpenEditorAfterCopy(9) // a NEWER one has outraced it
	if a.te != nil {
		t.Fatal("an unrelated newer apply opened the editor")
	}
	if a.skin.themeEditArmGen != 0 {
		t.Error("an outraced arming was left set — generations only increase, so it could never match again")
	}

	// And the user who walked away is never hijacked.
	a.skin.themeEditArmGen, a.skin.themeEditArmFrom = 11, ScreenSettings
	a.screen = ScreenCourtroom
	a.maybeOpenEditorAfterCopy(11)
	if a.te != nil {
		t.Error("the editor opened itself over a screen the user had moved to")
	}
	if a.skin.themeEditArmGen != 0 {
		t.Error("the arming survived its own apply — it must be consumed either way")
	}
}

// ---------------------------------------------------------------------------
// The save refusal — the path that used to cost a session
// ---------------------------------------------------------------------------

// TestASaveRefusalOffersTheCopyAndTheSecondPressTakesIt is the wave's worst path,
// closed: a bundled theme opened, took a session's work, and refused to save.
// It drives the real buttons — editorSave twice — and asserts on the FILE the
// second press produces, on the DOCUMENT following the user into the copy, and on
// the provenance surviving the save that lands on top of it.
func TestASaveRefusalOffersTheCopyAndTheSecondPressTakesIt(t *testing.T) {
	a, cleanup, _ := stageEditorCanvas(t, [2]int{0, 0})
	defer cleanup()
	base := t.TempDir()
	src := filepath.Join(base, "bundled", theme.ThemesDirName, a.te.doc.name)
	root := filepath.Join(base, "mine", theme.ThemesDirName)
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatalf("making the source theme: %v", err)
	}
	design := "; the author's own file\ncourtroom = 0, 0, 256, 192\nviewport = 0, 0, 256, 192\n"
	if err := os.WriteFile(filepath.Join(src, theme.DesignFileName), []byte(design), 0o644); err != nil {
		t.Fatalf("writing %s: %v", theme.DesignFileName, err)
	}
	editorFixtureCatalog(t, a, src, themeOriginBundled)
	// The catalog's write root is where a copy may land; the fixture points it at
	// the theme's own folder, which is not where somebody else's theme goes.
	cat := a.themeCatalogSnapshot()
	next := *cat
	next.WriteRoot = root
	a.skin.themeCat.Store(&next)
	from := a.te.doc.name

	// A session's worth of work, unsaved.
	a.te.doc.dirty = true

	a.editorSave()
	if a.te.save != nil {
		t.Fatal("a bundled theme was written in place")
	}
	if !strings.Contains(a.te.status, "Press Save again") {
		t.Fatalf("the refusal is %q — it must offer the act that fixes it, not homework", a.te.status)
	}
	if a.skin.themeCopy != nil {
		t.Fatal("the FIRST press copied something — the offer is a confirmation, not an act")
	}

	a.editorSave() // the confirming press
	if a.skin.themeCopy == nil {
		t.Fatalf("the second press started no copy: %s", a.te.status)
	}
	if !a.awaitThemeCopy(copyGateWait) {
		t.Fatalf("the copy never finished: %s", a.te.status)
	}
	want := from + " (edited)"
	if a.te.doc.name != want {
		t.Fatalf("the open document still points at %q — the session's unsaved work would be saved into "+
			"somebody else's theme, or lost", a.te.doc.name)
	}
	if !a.editorAwaitSave(copyGateWait) {
		t.Fatalf("the retargeted save did not land: %s", a.te.status)
	}
	body, err := os.ReadFile(filepath.Join(root, want, theme.SidecarFileName))
	if err != nil {
		t.Fatalf("nothing was saved into the copy: %v", err)
	}
	// PROVENANCE SURVIVES THE SAVE THAT LANDS ON TOP OF IT. The open model came from
	// the original theme and carries no [import] of its own, so a retarget that did
	// not lift the stamp would delete the credit the copy had just earned.
	if !strings.Contains(string(body), "derived_from = "+from) {
		t.Errorf("the first save after the copy wiped its provenance:\n%s", body)
	}
	if a.te.doc.dirty {
		t.Error("the document is still dirty after a landed save")
	}
	// AND THE ORIGINAL NEVER GREW A SIDECAR.
	if _, err := os.Stat(filepath.Join(src, theme.SidecarFileName)); err == nil {
		t.Error("the editor wrote into the bundled theme it was refused — that is the whole point of the copy")
	}
	drainThemeApply(t, a, a.skin.themeGen.Load())
}

// TestAnExpiredOfferDoesNotCopy — the confirmation means "yes, now", not "the next
// time you press Save". It expires with the chip that made the offer.
func TestAnExpiredOfferDoesNotCopy(t *testing.T) {
	a, _, _ := copyRig(t, themeOriginBundled)
	a.te = &themeEditor{doc: newThemeDoc("paper", theme.NewSidecar())}
	a.te.copyAt = time.Now().Add(-themeCopyConfirmWindow - time.Second)
	a.offerCopyOnSaveRefusal(errThemeNotYours)
	if a.skin.themeCopy != nil {
		t.Fatal("an offer the user can no longer see still authorised a copy")
	}
	if !strings.Contains(a.te.status, "Press Save again") {
		t.Errorf("the lapsed offer did not re-arm: %q", a.te.status)
	}
}

// TestOnlyTheNotYoursRefusalOffersACopy. Every other reason a save has nowhere to
// go — an unscanned catalog, a theme that vanished, a folder we cannot resolve —
// is reported as itself; offering to copy them would be an act that fixes nothing.
func TestOnlyTheNotYoursRefusalOffersACopy(t *testing.T) {
	a, _, _ := copyRig(t, themeOriginBundled)
	a.te = &themeEditor{doc: newThemeDoc("paper", theme.NewSidecar())}
	a.offerCopyOnSaveRefusal(errThemeNotYours)
	if a.te.copyAt.IsZero() {
		t.Fatal("the not-yours refusal did not arm the offer")
	}
	a.te.copyAt = time.Time{}
	a.offerCopyOnSaveRefusal(os.ErrNotExist)
	if !a.te.copyAt.IsZero() {
		t.Error("an unrelated refusal armed the copy offer — pressing Save again would copy a theme for a " +
			"reason a copy does not fix")
	}
	if a.skin.themeCopy != nil {
		t.Error("an unrelated refusal started a copy")
	}
}

// TestTheCopiedThemeKeepsTheNameTheDesignPreFills. The identity is the FOLDER
// name, sanitized — and themepack.SanitizeName keeps parentheses legal precisely
// so this name survives it. A sanitizer that dropped them would rename every copy
// to the source's own name and then refuse it as taken.
func TestTheCopiedThemeKeepsTheNameTheDesignPreFills(t *testing.T) {
	if got := themepack.SanitizeName("paper" + themeCopySuffix); got != "paper (edited)" {
		t.Errorf("the copy would be called %q — the design pre-fills \"<name> (edited)\"", got)
	}
}
