package ui

// COPY-FOR-EDITING GATES (v1.90.0 W8) — docs/wip/THEME-EDITOR-DESIGN.md §Q5.
//
// internal/themepack's own suite proves the COPY is safe and that the original is
// never mutated. These prove the CLIENT drives it — that the two surfaces which
// used to end in advice now perform the act, that a session's unsaved work follows
// the user into the copy, and that none of it can be deleted while the suite stays
// green.
//
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
//
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
	a := &App{}
	a.d.Prefs = newTestPrefs(t)
	a.d.Prefs.SetTheme("paper", "")
	a.themeCat.Store(&themeCatalog{
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

// TestTheCopyPrimitiveHasAProductionDriver is the rule-11 gate.
//
// themepack.CopyForEditing, its provenance stamp and its two Settings/editor
// sentences are one feature; a copy with no caller is the parsed-but-never-applied
// shape the hard rule names by example. This census fails the day the call site is
// deleted or moved back behind a test.
func TestTheCopyPrimitiveHasAProductionDriver(t *testing.T) {
	found := ""
	for _, name := range bundleGateGoFiles(t) {
		if strings.Contains(bundleGateSource(t, name), "themepack.CopyForEditing(") {
			found = name
			break
		}
	}
	if found == "" {
		t.Fatal("no non-test file in internal/ui calls themepack.CopyForEditing — the copy is 223 lines of " +
			"production code driven only by its own test, which is the shape hard rule 11 forbids. Either wire " +
			"it or delete it and state the deferral")
	}
	// AND BOTH SURFACES REACH IT. The Settings row is the first-run path (an AO2
	// theme cannot open the editor at all); the save refusal is the one that used to
	// cost a whole session's work.
	rows := funcBodySource(t, "settings.go", "drawThemeCatalogRows")
	if !containsCall(rows, "themeEditorRow") {
		t.Error("Settings ▸ Theme no longer draws the theme-editor row through its own builder — the row " +
			"that cannot open the editor is back to offering advice with no button")
	}
	if !containsCall(rows, "copyAppliedThemeForEditing") || !containsCall(rows, "openThemeEditor") {
		t.Error("the theme-editor row's button no longer performs BOTH acts — open what can be opened, " +
			"copy what cannot")
	}
	action := funcBodySource(t, "themecopy.go", "copyAppliedThemeForEditing")
	if !containsCall(action, "copyThemeForEditing") {
		t.Error("the row's copy action no longer starts a copy")
	}
	save := funcBodySource(t, "themeeditorsave.go", "editorSave")
	if !containsCall(save, "offerCopyOnSaveRefusal") {
		t.Error("editorSave no longer routes its refusal through the copy offer — a bundled theme takes a " +
			"session's work and then tells the user to go and copy a folder in Explorer")
	}
	offer := funcBodySource(t, "themeeditorsave.go", "offerCopyOnSaveRefusal")
	if !containsCall(offer, "copyThemeForEditing") {
		t.Error("the save refusal offers a copy it never starts")
	}
}

// TestTheCopyPollIsWiredIntoTheFrame — a job whose result nobody lands is a
// goroutine that copies a theme and tells no one.
func TestTheCopyPollIsWiredIntoTheFrame(t *testing.T) {
	frame := funcBodyInPackage(t, "func (a *App) Frame(")
	if !strings.Contains(frame, "pollThemeCopy()") {
		t.Error("App.Frame no longer polls the copy job — a finished copy would never land")
	}
	apply := funcBodyInPackage(t, "func (a *App) pollThemeApply(")
	if !strings.Contains(apply, "maybeOpenEditorAfterCopy(") {
		t.Error("pollThemeApply no longer performs the copy hand-off — \"Copy for editing\" would copy and " +
			"then stop, which is half of what the button offers")
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
	a.themeSidecar = theme.NewSidecar()
	if row := a.themeEditorRow(); !row.copies {
		t.Errorf("a bundled theme with a native tier offers %q — Save would refuse it after a whole "+
			"session's work", row.button)
	}
	// Ours: edited in place, no copy.
	cat := a.themeCatalogSnapshot()
	next := *cat
	next.Entries = []themeCatalogEntry{{Name: "paper", Dir: cat.Entries[0].Dir, Origin: themeOriginYours}}
	a.themeCat.Store(&next)
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
	if a.themeCopy == nil {
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
	if name, _ := a.d.Prefs.Theme(); name != "paper (edited)" {
		t.Errorf("the copy was not selected: pref says %q", name)
	}
	if a.themeGen.Load() == 0 {
		t.Error("the copy applied no theme — the editor opens over the APPLIED theme, so a copy that does " +
			"not apply cannot be edited")
	}
	// THE TWEAKS CAME ALONG.
	if got := a.d.Prefs.ThemeRectOverrides("paper (edited)"); got["ic_chat_message"] != [4]int{1, 2, 3, 4} {
		t.Errorf("the layout override did not follow the copy: %v", got)
	}
	// AND THE EDITOR IS ARMED to open over it once that apply lands.
	if a.themeEditArmGen == 0 {
		t.Error("nothing arms the editor — \"Copy for editing\" would copy and then leave the user on the " +
			"Settings screen with no editor")
	}
	drainThemeApply(t, a, a.themeGen.Load())
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
	first := a.themeCopy
	if first == nil {
		t.Fatalf("the copy started nothing: %s", a.warnLine)
	}
	a.copyThemeForEditing("paper", themeCopyThenOpen)
	if a.themeCopy != first {
		t.Fatal("a second press replaced the job in flight — the first goroutine's result would land on a " +
			"job nobody is holding")
	}
	if !strings.Contains(a.warnLine, "already") {
		t.Errorf("the refusal was silent: %q", a.warnLine)
	}
	if !a.awaitThemeCopy(copyGateWait) {
		t.Fatalf("the copy never finished: %s", a.warnLine)
	}
	drainThemeApply(t, a, a.themeGen.Load())
}

// TestATagalongCopyOfAThemeWeAlreadyOwnIsRefused. The offer and the act read ONE
// predicate, so a copy nobody needs cannot be started by a stale button, a hotkey
// or a future call site.
func TestATagalongCopyOfAThemeWeAlreadyOwnIsRefused(t *testing.T) {
	a, src, _ := copyRig(t, themeOriginYours)
	a.themeSidecar = theme.NewSidecar()
	a.copyThemeForEditing("paper", themeCopyThenOpen)
	if a.themeCopy != nil {
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
	if a.themeCopy != nil {
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
	a := &App{}
	a.themeEditArmGen, a.themeEditArmFrom = 7, ScreenSettings
	a.screen = ScreenSettings

	a.maybeOpenEditorAfterCopy(6) // an OLDER apply
	if a.te != nil {
		t.Fatal("an unrelated older apply opened the editor")
	}
	if a.themeEditArmGen != 7 {
		t.Fatal("an older apply spent the arming — the copy's own landing would never open the editor")
	}
	a.maybeOpenEditorAfterCopy(9) // a NEWER one has outraced it
	if a.te != nil {
		t.Fatal("an unrelated newer apply opened the editor")
	}
	if a.themeEditArmGen != 0 {
		t.Error("an outraced arming was left set — generations only increase, so it could never match again")
	}

	// And the user who walked away is never hijacked.
	a.themeEditArmGen, a.themeEditArmFrom = 11, ScreenSettings
	a.screen = ScreenCourtroom
	a.maybeOpenEditorAfterCopy(11)
	if a.te != nil {
		t.Error("the editor opened itself over a screen the user had moved to")
	}
	if a.themeEditArmGen != 0 {
		t.Error("the arming survived its own apply — it must be consumed either way")
	}
}

// ---------------------------------------------------------------------------
// The save refusal — the path that used to cost a session
// ---------------------------------------------------------------------------

// TestASaveRefusalOffersTheCopyAndTheSecondPressTakesIt is the wave's worst path,
// closed: a bundled theme opened, took a session's work, and refused to save.
//
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
	a.themeCat.Store(&next)
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
	if a.themeCopy != nil {
		t.Fatal("the FIRST press copied something — the offer is a confirmation, not an act")
	}

	a.editorSave() // the confirming press
	if a.themeCopy == nil {
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
	drainThemeApply(t, a, a.themeGen.Load())
}

// TestAnExpiredOfferDoesNotCopy — the confirmation means "yes, now", not "the next
// time you press Save". It expires with the chip that made the offer.
func TestAnExpiredOfferDoesNotCopy(t *testing.T) {
	a, _, _ := copyRig(t, themeOriginBundled)
	a.te = &themeEditor{doc: newThemeDoc("paper", theme.NewSidecar())}
	a.te.copyAt = time.Now().Add(-themeCopyConfirmWindow - time.Second)
	a.offerCopyOnSaveRefusal(errThemeNotYours)
	if a.themeCopy != nil {
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
	if a.themeCopy != nil {
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
