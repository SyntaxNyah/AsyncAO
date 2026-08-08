package ui

// THE TRANSPORT'S UI GATES (v1.90.0 W8).
//
// internal/themepack's own suite proves the archive is safe. These prove the
// CLIENT drives it: that a dropped bundle is described before it is written, that
// the second drop is what writes, that the import applies live, and that the
// pieces cannot be deleted while the suite stays green.

import (
	"archive/zip"
	"go/ast"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
	"github.com/SyntaxNyah/AsyncAO/internal/themepack"
)

// bundleGateWait bounds every await in this file. Generous rather than tight: the
// work behind it is a real zip read on a real temp directory, and a timeout flake
// in a suite about extraction safety reads as a security regression.
const bundleGateWait = 10 * time.Second

// dropRig is an App a drop can be delivered to, plus a themes root that is a
// temp directory rather than the developer's real AppData.
//
// THE ROOT IS A PARAMETER, which is the whole reason bundleDrop is split out of
// handleThemeBundleDrop: a gate that had to go through config.UserThemesDir()
// would either write into the developer's own AppData or re-implement the
// portability policy it is not testing.
func dropRig(t *testing.T) (*App, string) {
	t.Helper()
	return &App{}, filepath.Join(t.TempDir(), "themes")
}

// newTestPrefs is a preferences value the gates may write to. A bare struct, as
// internal/config's own tests use: nothing here needs the debounced saver, and a
// gate that started one would be writing files nobody asked for.
func newTestPrefs(t *testing.T) *config.AssetPreferences {
	t.Helper()
	return &config.AssetPreferences{}
}

// bundleGateSource reads one of this package's files as text.
func bundleGateSource(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(b)
}

// bundleGateGoFiles lists this package's non-test sources.
func bundleGateGoFiles(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading the package directory: %v", err)
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		out = append(out, name)
	}
	return out
}

// fixtureBundle writes a small, legal .aotheme and returns its path.
func fixtureBundle(t *testing.T, folder string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), folder+themepack.PackExt)
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("creating the bundle: %v", err)
	}
	zw := zip.NewWriter(f)
	write := func(name, body string) {
		w, err := zw.Create(folder + "/" + name)
		if err != nil {
			t.Fatalf("adding %s: %v", name, err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
	}
	write(theme.SidecarFileName, "[theme]\nformat = 1\nname = Paper\nauthor = Nyah\n")
	write(theme.DesignFileName, "courtroom = 0, 0, 256, 192\nviewport = 0, 0, 256, 192\n")
	write("fonts/Court.ttf", "not really a font")
	if err := zw.Close(); err != nil {
		t.Fatalf("closing the bundle: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("closing the file: %v", err)
	}
	return path
}

// awaitBundle drains the in-flight stage THROUGH landThemeBundle, so the gate
// asserts on state the shipped landing produced rather than on a goroutine having
// been started.
func awaitBundle(t *testing.T, a *App) {
	t.Helper()
	if a.themeImp.job == nil {
		t.Fatal("no bundle job is in flight — the drop started nothing")
	}
	select {
	case res := <-a.themeImp.job.done:
		a.landThemeBundle(res)
	case <-time.After(bundleGateWait):
		t.Fatal("the bundle job never finished")
	}
}

// TestABundleIsDescribedBeforeAnythingIsWritten is the consent contract. The
// design specifies a sheet rendered before extraction; the client's answer is a
// two-drop confirmation on the warn line, and the property that matters is the
// same either way: the FIRST drop writes nothing and says what is inside.
func TestABundleIsDescribedBeforeAnythingIsWritten(t *testing.T) {
	a, root := dropRig(t)
	pack := fixtureBundle(t, "paper")

	a.bundleDrop(pack, root)
	awaitBundle(t, a)

	if _, err := os.Stat(root); err == nil {
		if entries, _ := os.ReadDir(root); len(entries) > 0 {
			t.Fatalf("the FIRST drop wrote %d things into the themes folder — consent means "+
				"nothing lands until the user agrees", len(entries))
		}
	}
	for _, want := range []string{"Paper", "3 files", "font"} {
		if !strings.Contains(a.warnLine, want) {
			t.Errorf("the consent line does not mention %q:\n  %s", want, a.warnLine)
		}
	}
	// THE CALL TO ACTION SURVIVES THE CLAMP. warnLine is one line and clampLine
	// truncates it, so a consent prompt that puts "drop it again" last loses it on
	// any bundle with two fonts — a prompt that never says how to consent.
	if !strings.Contains(a.warnLine, "Drop it again") {
		t.Errorf("the consent line does not say how to confirm:\n  %s", a.warnLine)
	}
	if !strings.Contains(a.warnLine, "Court.ttf") {
		t.Errorf("a bundled font with no licence file beside it was not named:\n  %s\n"+
			"OFL requires the licence to travel with the face, so the person re-sharing this "+
			"bundle is the one breaking the terms", a.warnLine)
	}
	if strings.Contains(a.warnLine, "0.0 MB") {
		t.Errorf("a small bundle reads as empty:\n  %s\n"+
			"\"0.0 MB packed / 0.0 MB on disk\" reads as a broken import rather than a small one", a.warnLine)
	}
	if a.warnAt.IsZero() {
		t.Error("warnLine was set without warnAt — the line would never disappear")
	}
}

// TestTheSecondDropImports is the other half: consent given, the theme lands.
func TestTheSecondDropImports(t *testing.T) {
	a, root := dropRig(t)
	a.d.Prefs = newTestPrefs(t)
	pack := fixtureBundle(t, "paper")

	a.bundleDrop(pack, root) // inspect
	awaitBundle(t, a)
	a.bundleDrop(pack, root) // confirm
	awaitBundle(t, a)

	dir := filepath.Join(root, "paper")
	if _, err := os.Stat(filepath.Join(dir, theme.SidecarFileName)); err != nil {
		t.Fatalf("the theme did not land at %s: %v", dir, err)
	}
	name, _ := a.d.Prefs.Theme()
	if name != "paper" {
		t.Errorf("the imported theme was not selected: pref says %q", name)
	}
	if !strings.Contains(a.warnLine, "Imported") {
		t.Errorf("no receipt after the import:\n  %s", a.warnLine)
	}
	// LIVE, NOT NEXT LAUNCH (design §Q4): the import kicks an apply itself, which
	// is the whole "two drops later you are looking at it" experience.
	if a.themeGen.Load() == 0 {
		t.Error("the import applied no theme — this is deliberately not ImportSettings' " +
			"freeze-and-apply-next-launch semantics")
	}
	drainThemeApply(t, a, a.themeGen.Load())
	// AND THE CONSENT IS SPENT. A third drop must describe again rather than
	// import again — otherwise "drop it twice" quietly becomes "every drop from
	// now on imports".
	a.bundleDrop(pack, root)
	if a.themeImp.job == nil || a.themeImp.job.stage != themeImportInspect {
		t.Error("a third drop went straight to extraction — consent must be spent by the act it authorises")
	}
}

// TestAnotherBundleDoesNotInheritTheFirstsConsent — the confirmation means "this
// thing", not "the last thing you dropped".
func TestAnotherBundleDoesNotInheritTheFirstsConsent(t *testing.T) {
	a, root := dropRig(t)
	first := fixtureBundle(t, "paper")
	second := fixtureBundle(t, "ink")

	a.bundleDrop(first, root)
	awaitBundle(t, a)
	a.bundleDrop(second, root)
	if a.themeImp.job == nil || a.themeImp.job.stage != themeImportInspect {
		t.Fatal("dropping a DIFFERENT bundle imported it on the strength of the previous one's consent")
	}
	awaitBundle(t, a)
	if entries, _ := os.ReadDir(root); len(entries) > 0 {
		t.Errorf("something was written after two first-drops: %d entries", len(entries))
	}
}

// TestAnExpiredConsentDoesNotImport — a bundle inspected and forgotten must not be
// imported by an unrelated drop ten minutes later.
func TestAnExpiredConsentDoesNotImport(t *testing.T) {
	a, root := dropRig(t)
	pack := fixtureBundle(t, "paper")
	a.bundleDrop(pack, root)
	awaitBundle(t, a)
	a.themeImp.armedAt = time.Now().Add(-themeImportConsentWindow - time.Second)
	a.bundleDrop(pack, root)
	if a.themeImp.job == nil || a.themeImp.job.stage != themeImportInspect {
		t.Fatal("an expired consent still authorised an import")
	}
}

// TestARefusedBundleSaysWhyAndWritesNothing. The user's next question after a
// refusal is always "what do I do about it?", and "failed" answers nothing.
func TestARefusedBundleSaysWhyAndWritesNothing(t *testing.T) {
	a, root := dropRig(t)
	bad := filepath.Join(t.TempDir(), "screenshot"+themepack.PackExt)
	if err := os.WriteFile(bad, []byte("\x89PNG\r\n\x1a\nnope"), 0o644); err != nil {
		t.Fatalf("writing the fixture: %v", err)
	}
	a.bundleDrop(bad, root)
	awaitBundle(t, a)
	if !strings.Contains(a.warnLine, "screenshot") {
		t.Errorf("the refusal does not name the file:\n  %s", a.warnLine)
	}
	if !strings.Contains(strings.ToLower(a.warnLine), "zip") {
		t.Errorf("the refusal does not say what was wrong with it:\n  %s", a.warnLine)
	}
	if _, err := os.Stat(root); err == nil {
		if entries, _ := os.ReadDir(root); len(entries) > 0 {
			t.Error("a refused bundle created something in the themes folder")
		}
	}
}

// TestOnlyOneBundleJobRunsAtATime — hard rule 4 applied to the transport: the job
// pointer IS the flag, and a second drop while one runs is refused politely.
func TestOnlyOneBundleJobRunsAtATime(t *testing.T) {
	a, root := dropRig(t)
	pack := fixtureBundle(t, "paper")
	a.bundleDrop(pack, root)
	first := a.themeImp.job
	if first == nil {
		t.Fatal("the drop started nothing")
	}
	a.bundleDrop(fixtureBundle(t, "ink"), root)
	if a.themeImp.job != first {
		t.Fatal("a second drop replaced the job in flight — the first goroutine's result would land " +
			"on a job nobody is holding")
	}
	if !strings.Contains(a.warnLine, "already") {
		t.Errorf("the refusal was silent:\n  %s", a.warnLine)
	}
	awaitBundle(t, a)
}

// TestAnImportNeverRepointsTheThemeRoot is the W2 defect, gated at the LANDING
// rather than at the claim.
//
// dropclaim_test.go already pins that a bundle never reaches the Settings screen's
// theme-folder arm. This is the other door: the import itself must not "helpfully"
// point the custom root at wherever the theme landed, which would make every
// unrelated theme in that folder appear and the user's own choice disappear.
func TestAnImportNeverRepointsTheThemeRoot(t *testing.T) {
	a, root := dropRig(t)
	a.d.Prefs = newTestPrefs(t)
	a.d.Prefs.SetTheme("default", `C:\somewhere\the\user\chose`)
	pack := fixtureBundle(t, "paper")
	a.bundleDrop(pack, root)
	awaitBundle(t, a)
	a.bundleDrop(pack, root)
	awaitBundle(t, a)
	if _, customRoot := a.d.Prefs.Theme(); customRoot != `C:\somewhere\the\user\chose` {
		t.Errorf("the import repointed the theme root to %q — that is the W2 bug arriving by the front door", customRoot)
	}
	drainThemeApply(t, a, a.themeGen.Load())
}

// ---------------------------------------------------------------------------
// The import report
// ---------------------------------------------------------------------------

// TestEveryDegradeReachesASurface is the gate that had to exist before the
// metadata ruling could.
//
// Sidecar.Notes() had collected degrade reports since W1 and a census across the
// tree found its only readers were TESTS — the format promised "degrade with a
// note" and the note reached nobody. W8's metadata truncation would have inherited
// exactly that, so the truncation would have been silent in practice however
// loudly the format described it.
//
// It drives buildThemeReport and noteThemeReport, which is the shipped pair, and
// then asserts on a.warnLine — the surface a human actually reads.
func TestEveryDegradeReachesASurface(t *testing.T) {
	// A sidecar carrying one of each input class: a metadata degrade (W8), and an
	// element the reader skipped (the forward-compat path, W1).
	long := strings.Repeat("é", theme.NameRuneCap+30)
	sc, err := theme.ParseSidecar([]byte("[theme]\nformat = 1\nname = Paper\ncredit = " + long +
		"\n\n[element.mystery]\nkind = something_from_2029\n"))
	if err != nil {
		t.Fatalf("the fixture refused to load: %v", err)
	}
	if len(sc.Notes()) < 2 {
		t.Fatalf("the fixture produced %d notes, want at least the degrade and the skip — "+
			"this gate would be measuring nothing", len(sc.Notes()))
	}

	report := buildThemeReport(sc, []string{"Court Serif"}, "unbound: ic_chatlog_left")
	if len(report) < 4 {
		t.Fatalf("the report holds %d entries, want the notes plus the fonts plus the unbound keys: %v",
			len(report), report)
	}
	if !reportMentions(report, "credit") {
		t.Errorf("the metadata degrade is missing from the report: %v\n"+
			"A ruling that shortens somebody's attribution line and says nothing is a silent "+
			"truncation with extra steps", report)
	}
	if !reportMentions(report, "Court Serif") || !reportMentions(report, "ic_chatlog_left") {
		t.Errorf("an input this report claims to carry is missing: %v", report)
	}

	a := &App{}
	a.noteThemeReport("paper", report)
	if a.warnLine == "" {
		t.Fatal("the report reached no surface — this is the defect the file exists to close")
	}
	if !strings.Contains(a.warnLine, "paper") || !strings.Contains(a.warnLine, "note") {
		t.Errorf("the line does not say which theme or how many notes:\n  %s", a.warnLine)
	}
	if len(a.themeReport) != len(report) {
		t.Errorf("the editor kept %d of %d notes", len(a.themeReport), len(report))
	}

	// AND IT DOES NOT NAG. An apply is not a user action — healTheme re-kicks one
	// on any T1 eviction of a theme texture — so the SAME report arriving again
	// must not re-post the line.
	a.warnLine = ""
	a.noteThemeReport("paper", report)
	if a.warnLine != "" {
		t.Errorf("an eviction heal re-posted the same report: %q", a.warnLine)
	}

	// A CLEAN THEME SAYS NOTHING. A report that fires on every install is one
	// everybody learns to ignore, which is the same as not having one.
	b := &App{}
	b.noteThemeReport("clean", nil)
	if b.warnLine != "" {
		t.Errorf("a theme with nothing to report still posted %q", b.warnLine)
	}
}

// TestTheApplyBuildsTheReport pins the wiring: the report is built on the apply
// GOROUTINE, from the three inputs, and landed by the poll. Without either call
// site the gate above still passes while nothing is ever reported in the client.
func TestTheApplyBuildsTheReport(t *testing.T) {
	apply := funcBodyInPackage(t, "func (a *App) applyThemeAsync(")
	if !strings.Contains(apply, "buildThemeReport(") {
		t.Error("applyThemeAsync no longer builds the import report — Sidecar.Notes() goes back to " +
			"being collected and read by nobody, which is what rule 11 calls a parsed-but-never-applied tier")
	}
	land := funcBodyInPackage(t, "func (a *App) pollThemeApply(")
	if !strings.Contains(land, "noteThemeReport(") {
		t.Error("pollThemeApply no longer lands the import report — it would be built off-thread and dropped")
	}
}

// TestImportReportNamesEveryDeclaredGap is design §Q5's own requirement, stated as
// a gate: an AO2 feature this client INHERITS rather than imports must be named,
// with its reason, on the theme that ships one.
//
// WHY IT IS A CENSUS OVER themeReportGaps AND NOT THREE HAND-WRITTEN CASES. The
// gaps are a table precisely so a third one is a row; a gate that listed the two
// that exist today would go green the day somebody added the third and reported
// nothing. So it walks the table, builds a sidecar that trips exactly that row, and
// demands a line — which is a claim about the mechanism rather than about its
// current contents.
//
// The two gaps are `[import] lobby_design` and `per_misc`. `_sharp` is the one
// declared gap deliberately absent, for the reason themereport.go states: it is not
// on the sidecar and there is no census to ask.
func TestImportReportNamesEveryDeclaredGap(t *testing.T) {
	if len(themeReportGaps) == 0 {
		t.Fatal("the declared-gap table is empty — §Q5 names two inherited gaps that the reader " +
			"records and nothing applies, and this gate would be measuring nothing")
	}
	for i, g := range themeReportGaps {
		sc := theme.NewSidecar()
		if g.on(sc) {
			t.Fatalf("gap %d fires on a theme that declares nothing — every install would carry the "+
				"line, and a report that always fires is one nobody reads", i)
		}
		if got := buildThemeReport(sc, nil, ""); reportMentions(got, g.line) {
			t.Fatalf("gap %d is reported before anything declares it: %v", i, got)
		}
		// Trip exactly this row, through the FORMAT's own parser rather than by
		// assigning the field: the flag has to survive the round trip a real import
		// makes, and a gate that set s.Import.PerMisc directly would stay green with
		// the reader's own [import] parse deleted.
		tripped := tripGapViaTheReader(t, i)
		if !g.on(tripped) {
			t.Fatalf("gap %d did not survive a parse — the flag the report reads is not the flag the "+
				"reader writes", i)
		}
		got := buildThemeReport(tripped, nil, "")
		if !reportMentions(got, g.line) {
			t.Errorf("a theme declaring gap %d gets no line about it: %v\n"+
				"§Q5: the five declared gaps, by name, with the reason — a client that silently drops "+
				"half a theme teaches its author nothing", i, got)
		}
	}

	// AND THE PRESERVED ENTRIES, the other half of "nothing is silent any more":
	// an `[x.*]` vendor section is carried through byte-for-byte, which is correct
	// and — without this line — indistinguishable from having been eaten.
	withVendor, err := theme.ParseSidecar([]byte("[theme]\nformat = 1\n\n[x.someeditor]\nlayer = 3\n"))
	if err != nil {
		t.Fatalf("the vendor-section fixture refused to load: %v", err)
	}
	if len(withVendor.Unknown()) == 0 {
		t.Fatal("the fixture's [x.*] section was not preserved — the gate below would measure nothing")
	}
	if got := buildThemeReport(withVendor, nil, ""); !reportMentions(got, "preserved unchanged") {
		t.Errorf("a carried-through vendor section is not reported: %v", got)
	}
}

// tripGapViaTheReader parses a sidecar whose [import] section turns on exactly the
// i-th declared gap, by finding the one flag that does it. Brute force over a tiny
// fixed set, so a new gap needs no change here — only its key added to the list.
func tripGapViaTheReader(t *testing.T, i int) *theme.Sidecar {
	t.Helper()
	for _, key := range [...]string{"lobby_design", "per_misc"} {
		sc, err := theme.ParseSidecar([]byte("[theme]\nformat = 1\n\n[import]\n" + key + " = 1\n"))
		if err != nil {
			t.Fatalf("[import] %s = 1 will not parse: %v", key, err)
		}
		if themeReportGaps[i].on(sc) {
			return sc
		}
	}
	t.Fatalf("no [import] key in this helper turns on gap %d — add it here, so the gate keeps "+
		"driving the reader rather than the struct field", i)
	return nil
}

func reportMentions(report []string, needle string) bool {
	for _, r := range report {
		if strings.Contains(r, needle) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// The seams
// ---------------------------------------------------------------------------

// TestTheBundlePollIsWiredIntoTheFrame. pollThemeBundle is the ONE place a
// finished stage reaches the UI; without its call site the consent line never
// appears, the import never lands, and every gate above still passes because they
// drain the channel themselves.
func TestTheBundlePollIsWiredIntoTheFrame(t *testing.T) {
	body := funcBodyInPackage(t, "func (a *App) Frame(")
	if !strings.Contains(body, "a.pollThemeBundle()") {
		t.Error("App.Frame no longer calls pollThemeBundle — a dropped theme would be read, or " +
			"extracted, and nothing would ever land it on screen")
	}
	editor := funcBodySource(t, "themeeditor.go", "drawThemeEditor")
	if !containsCall(editor, "pollEditorExport") {
		t.Error("the editor frame no longer polls the export — Share would write a bundle and never say so")
	}
}

// TestTheDropRouterAndTheExtractorAgreeOnWhatABundleIs. The routing constant used
// to be a literal declared beside a comment promising it would become an alias
// when the extractor landed. It did.
func TestTheDropRouterAndTheExtractorAgreeOnWhatABundleIs(t *testing.T) {
	if themePackExt != themepack.PackExt {
		t.Fatalf("the drop router claims %q and the extractor writes %q", themePackExt, themepack.PackExt)
	}
	src := bundleGateSource(t, "dropclaim.go")
	if strings.Contains(src, `themePackExt = ".aotheme"`) {
		t.Error("themePackExt is a literal again — the router and the extractor can now drift, which " +
			"is precisely what the alias exists to prevent")
	}
}

// TestTheAO2ExportNeverProducesAPathForTheAuthorsDesignINI is the structural half
// of "the writer refuses courtroom_design.ini outside the AO2 export".
//
// AST, over the whole package: no file may join a path onto theme.DesignFileName
// and hand it to a writer. The AO2 bake produces BYTES and the bytes go into a
// zip — so the author's own file cannot be overwritten by construction, not by
// anybody remembering.
func TestTheAO2ExportNeverProducesAPathForTheAuthorsDesignINI(t *testing.T) {
	writers := map[string]bool{
		"WriteFile": true, "Create": true, "OpenFile": true, "Rename": true, "Remove": true,
		"writeFileAtomically": true, "copyFileAtomically": true,
	}
	for _, name := range bundleGateGoFiles(t) {
		_, f := parsedFile(t, name)
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			fn := ""
			switch c := call.Fun.(type) {
			case *ast.SelectorExpr:
				fn = c.Sel.Name
			case *ast.Ident:
				fn = c.Name
			}
			if !writers[fn] {
				return true
			}
			for _, arg := range call.Args {
				if mentionsDesignFileName(arg) {
					t.Errorf("%s: %s() is handed a path built from theme.DesignFileName — the author's own "+
						"courtroom_design.ini is the ONE file the editor must never write (design §Q5): a "+
						"re-download would destroy their work and every upstream diff would be noise. The "+
						"AO2-compat export produces BYTES for the bundle instead.", name, fn)
				}
			}
			return true
		})
	}
}

// ---------------------------------------------------------------------------
// Export — the other direction
// ---------------------------------------------------------------------------

// stageExportable opens the editor over a real theme FOLDER, saves once so the
// folder holds the document that is about to be bundled, and returns the folder.
//
// The save is not scaffolding: Share bundles the FILES ON DISK, so a gate that
// exported an unsaved document would be measuring an empty folder.
func stageExportable(t *testing.T, a *App) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), theme.ThemesDirName, a.te.doc.name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("making the theme folder: %v", err)
	}
	editorFixtureCatalog(t, a, dir, themeOriginYours)
	// A hand-written AO2 half, with a COMMENT in it: the AO2-compat mode runs the
	// lossless carrier over this file, and the comment is what proves it went
	// through INIDoc rather than being rebuilt.
	design := "; hand-written by the theme's author\ncourtroom = 0, 0, 256, 192\nviewport = 0, 0, 256, 192\n"
	if err := os.WriteFile(filepath.Join(dir, theme.DesignFileName), []byte(design), 0o644); err != nil {
		t.Fatalf("writing %s: %v", theme.DesignFileName, err)
	}
	a.editorSave()
	if !a.editorAwaitSave(bundleGateWait) {
		t.Fatalf("the fixture theme would not save: %s", a.te.status)
	}
	return dir
}

// TestSharingAThemeWritesABundleThatImportsBack is the export half's end-to-end
// gate, and it is the one that makes editorAwaitExport a shipped path rather than
// a function with no callers.
//
// It asserts on the FILE — the bundle exists, it is outside the theme folder, and
// the tree that comes back out of it is the tree that went in. Round-tripping is
// not a feature that had to be built; it is a consequence of "a theme IS a folder",
// and this is where that claim is measured through the client's own buttons.
func TestSharingAThemeWritesABundleThatImportsBack(t *testing.T) {
	a, cleanup, _ := stageEditorCanvas(t, [2]int{0, 0})
	defer cleanup()
	dir := stageExportable(t, a)

	a.editorExport()
	if a.te.export == nil {
		t.Fatalf("Share started nothing: %s", a.te.status)
	}
	dest := a.te.export.path
	if !a.editorAwaitExport(bundleGateWait) {
		t.Fatalf("the bundle was never written: %s", a.te.status)
	}
	if _, err := os.Stat(dest); err != nil {
		t.Fatalf("no bundle at %s: %v", dest, err)
	}
	// OUTSIDE THE THEME FOLDER. A .aotheme written inside themes/<name>/ would be
	// bundled by the NEXT export of that theme, and every share would carry the
	// previous one inside it.
	if strings.HasPrefix(filepath.Clean(dest), filepath.Clean(dir)+string(filepath.Separator)) {
		t.Errorf("the bundle landed inside the theme folder (%s) — the next export would bundle it", dest)
	}
	// The receipt names the size, which is the only question an author has.
	if !strings.Contains(a.te.status, "KB") && !strings.Contains(a.te.status, "MB") {
		t.Errorf("the receipt is %q — it must name the packed size", a.te.status)
	}

	// AND IT IMPORTS BACK, through the real reader, byte for byte.
	root := filepath.Join(t.TempDir(), "imported")
	m, err := themepack.Extract(dest, root)
	if err != nil {
		t.Fatalf("the bundle this client wrote will not import: %v", err)
	}
	want := treeUnder(t, dir)
	got := treeUnder(t, m.Dir)
	if len(want) == 0 {
		t.Fatal("the theme folder is empty — the gate would pass on nothing")
	}
	for rel, body := range want {
		other, ok := got[rel]
		if !ok {
			t.Errorf("%s did not survive the round trip", rel)
			continue
		}
		if other != body {
			t.Errorf("%s came back different", rel)
		}
	}
	if len(got) != len(want) {
		t.Errorf("%d files went in and %d came out", len(want), len(got))
	}
}

// TestTheAO2CompatBundleCarriesTheBakeAndTheLostReport pins the Shift mode: the
// bundle gets a BAKED courtroom_design.ini and ASYNCAO-LOST.txt, and the author's
// own file on disk is untouched — which is the promise the whole export was
// designed around.
func TestTheAO2CompatBundleCarriesTheBakeAndTheLostReport(t *testing.T) {
	a, cleanup, _ := stageEditorCanvas(t, [2]int{0, 0})
	defer cleanup()
	dir := stageExportable(t, a)
	before, err := os.ReadFile(filepath.Join(dir, theme.DesignFileName))
	if err != nil {
		t.Fatal(err)
	}

	a.ctx.shiftHeld = true
	a.editorExport()
	a.ctx.shiftHeld = false
	if a.te.export == nil {
		t.Fatalf("Shift+Share started nothing: %s", a.te.status)
	}
	dest := a.te.export.path
	if !a.editorAwaitExport(bundleGateWait) {
		t.Fatalf("the AO2-compat bundle was never written: %s", a.te.status)
	}

	root := filepath.Join(t.TempDir(), "imported")
	m, err := themepack.Extract(dest, root)
	if err != nil {
		t.Fatalf("the AO2-compat bundle will not import: %v", err)
	}
	baked, err := os.ReadFile(filepath.Join(m.Dir, theme.DesignFileName))
	if err != nil {
		t.Fatalf("the bundle has no %s: %v", theme.DesignFileName, err)
	}
	// THROUGH THE LOSSLESS CARRIER: the author's comment is still in the baked file.
	if !strings.Contains(string(baked), "hand-written by the theme's author") {
		t.Errorf("the bake dropped the author's comment — it must go through INIDoc:\n%s", baked)
	}
	if _, err := os.Stat(filepath.Join(m.Dir, theme.LostFileName)); err != nil {
		t.Errorf("the AO2-compat bundle carries no %s: %v", theme.LostFileName, err)
	}
	// AND THE FOLDER ON DISK NEVER MOVED. This is the highest-blast-radius promise
	// in the feature (design §Q5 R3) and the only place it is measured after a real
	// export rather than by reading the code.
	after, err := os.ReadFile(filepath.Join(dir, theme.DesignFileName))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Errorf("the AO2-compat export rewrote the author's own %s:\n%s", theme.DesignFileName, after)
	}
	if !strings.Contains(a.te.status, theme.LostFileName) {
		t.Errorf("the receipt is %q — the AO2 mode must say what AO2 drops", a.te.status)
	}
}

// TestAThemeThatWouldNotSaveIsNotShared pins "Validate before pack". The model is
// pushed past a format cap WITHOUT going through the editor's clamping setters —
// which is exactly the state a preset apply or a replayed op could produce — and
// Share must refuse it in the writer's own words rather than handing somebody a
// bundle whose sidecar the reader rejects.
func TestAThemeThatWouldNotSaveIsNotShared(t *testing.T) {
	a, cleanup, _ := stageEditorCanvas(t, [2]int{0, 0})
	defer cleanup()
	stageExportable(t, a)

	a.te.doc.side.Elements[0].ID = "NOT A LEGAL ID"
	a.te.status = ""
	a.editorExport()
	if a.te.export != nil {
		t.Fatal("Share bundled a theme the reader would refuse")
	}
	if !strings.Contains(a.te.status, "cannot be shared") {
		t.Errorf("the refusal reads %q — it must say why, not fail silently", a.te.status)
	}
}

// TestShareRefusesUnsavedEdits keeps the other precondition honest: the zip reads
// the DISK, so bundling a dirty document would ship the theme as it was before
// this session and the author would find out from whoever they sent it to.
func TestShareRefusesUnsavedEdits(t *testing.T) {
	a, cleanup, _ := stageEditorCanvas(t, [2]int{0, 0})
	defer cleanup()
	stageExportable(t, a)

	a.te.doc.dirty = true
	a.te.status = ""
	a.editorExport()
	if a.te.export != nil {
		t.Fatal("Share bundled a document with unsaved edits")
	}
	if !strings.Contains(a.te.status, "save first") {
		t.Errorf("the refusal reads %q — it must offer the fix", a.te.status)
	}
}

// treeUnder reads a folder into rel-path → contents.
func treeUnder(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		out[filepath.ToSlash(rel)] = string(b)
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", dir, err)
	}
	return out
}

// ---------------------------------------------------------------------------
// The Settings row — the browse half
// ---------------------------------------------------------------------------

// TestTheSettingsImportRowIsTheSameFunnelAsTheDrop is the anti-second-path gate.
//
// A "Browse…" button that started its own extraction would be a second importer:
// two places deciding caps, consent and destination, drifting the first time one
// of them was fixed. So the row's action calls handleThemeBundleDrop, and the
// browser's pick calls handleThemeBundleDrop, and this census says so.
func TestTheSettingsImportRowIsTheSameFunnelAsTheDrop(t *testing.T) {
	action := funcBodySource(t, "themeimport.go", "themeImportRowAction")
	if !containsCall(action, "handleThemeBundleDrop") {
		t.Error("the Settings Import row no longer routes through handleThemeBundleDrop — it is a " +
			"second import path, and the two will disagree about consent")
	}
	if !containsCall(action, "openDemoBrowserFor") {
		t.Error("the Settings Import row no longer opens a picker — the button does nothing")
	}
	pick := funcBodySource(t, "demobrowser.go", "pickBrowsedFile")
	if !containsCall(pick, "handleThemeBundleDrop") {
		t.Error("the browser's pick no longer routes a theme bundle through handleThemeBundleDrop")
	}
	rows := funcBodySource(t, "settings.go", "drawThemeCatalogRows")
	if !containsCall(rows, "themeImportRowAction") || !containsCall(rows, "themeImportRow") {
		t.Error("Settings ▸ Theme no longer draws the Import row — the browse half is unreachable")
	}
}

// TestTheImportRowSaysWhatWillHappenNext drives the row's wording through its own
// pure builder: idle, in flight, and armed. The armed line is the one that matters
// — it is the client's consent sentence, and it must say that nothing has been
// written yet, because that is the fact that makes pressing the button safe.
func TestTheImportRowSaysWhatWillHappenNext(t *testing.T) {
	idle := buildThemeImportRow("", false)
	if idle.button != "Browse…" || !strings.Contains(idle.note, "drag") {
		t.Errorf("the resting row is %+v — it must offer both ways in", idle)
	}
	busy := buildThemeImportRow("", true)
	if busy.note == idle.note {
		t.Error("the row says nothing different while a bundle is being read")
	}
	armed := buildThemeImportRow(filepath.Join("C:", "dl", "umineko.aotheme"), false)
	if armed.button != "Import" {
		t.Errorf("the armed button reads %q, want the act it performs", armed.button)
	}
	if !strings.Contains(armed.note, "umineko.aotheme") {
		t.Errorf("the armed row is %q — it must name the bundle it would import", armed.note)
	}
	if !strings.Contains(armed.note, "nothing has been written") {
		t.Errorf("the armed row is %q — the whole consent rests on saying nothing has happened yet", armed.note)
	}
}

// TestAnExpiredConsentTakesTheImportButtonAway. The row is memoized, and the
// consent expires on a CLOCK rather than on an event — so a memo keyed on the
// stored path would leave an Import button offering something the funnel would
// refuse.
func TestAnExpiredConsentTakesTheImportButtonAway(t *testing.T) {
	a := &App{}
	pack := fixtureBundle(t, "paper")
	a.themeImp.armedPath, a.themeImp.armedAt = pack, time.Now()
	if got := a.themeImportRow(); got.button != "Import" {
		t.Fatalf("a live consent shows %q", got.button)
	}
	a.themeImp.armedAt = time.Now().Add(-2 * themeImportConsentWindow)
	if got := a.themeImportRow(); got.button != "Browse…" {
		t.Errorf("an EXPIRED consent still offers %q — the memo is keyed on the stored path "+
			"rather than the live one", got.button)
	}
}

// mentionsDesignFileName reports whether an expression contains theme.DesignFileName.
func mentionsDesignFileName(e ast.Expr) bool {
	found := false
	ast.Inspect(e, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "DesignFileName" {
			return true
		}
		if id, ok := sel.X.(*ast.Ident); ok && id.Name == "theme" {
			found = true
		}
		return true
	})
	return found
}
