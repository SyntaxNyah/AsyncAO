package ui

// Gates on the guided base-folder setup (issue #72) and the folder report behind
// it.
//
// The class of bug this whole surface exists to remove is the SILENT one: a
// setup that looks applied and is not, a folder one level off, a base whose
// characters carry no char.ini. So most of what is pinned here is what the user
// gets TOLD, and the rest is that nothing is written until they say so.

import (
	"archive/zip"
	"go/ast"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
)

// writeBaseTree lays out a folder from a rel→content map, creating parents.
// Entries ending in "/" are empty directories, which is how a character folder
// with no char.ini is expressed.
func writeBaseTree(t *testing.T, root string, files map[string]string) string {
	t.Helper()
	for rel, body := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if strings.HasSuffix(rel, "/") {
			if err := os.MkdirAll(p, 0o755); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// TestBaseScanCountsWhatIsActuallyThere is the report's happy path: the numbers
// the wizard shows are counted off the disk, not inferred from the fact that a
// folder was picked.
func TestBaseScanCountsWhatIsActuallyThere(t *testing.T) {
	dir := writeBaseTree(t, t.TempDir(), map[string]string{
		"characters/witch/char.ini":         "[Emotions]\nnumber = 1\n",
		"characters/witch/(a)idle.png":      "x",
		"characters/beato/char.ini":         "[Options]\n",
		"characters/nobody/":                "", // a character folder with no ini
		"background/court/defenseempty.png": "x",
		"background/lobby/":                 "",
		"sounds/music/theme.opus":           "x",
		"evidence/knife.png":                "x",
	})

	s := scanBaseFolder(dir)
	if s.err != "" {
		t.Fatalf("scan failed: %s", s.err)
	}
	if !s.looksLikeBase() {
		t.Error("a folder with characters/ and background/ was not recognised as a base")
	}
	if s.chars != 3 {
		t.Errorf("chars = %d, want 3", s.chars)
	}
	if s.bgs != 2 {
		t.Errorf("bgs = %d, want 2", s.bgs)
	}
	if s.iniOK != 2 || s.iniOf != 3 {
		t.Errorf("char.ini = %d of %d, want 2 of 3", s.iniOK, s.iniOf)
	}
	if !s.hasSounds || !s.hasEvidence {
		t.Errorf("sounds/=%v evidence/=%v, want both found", s.hasSounds, s.hasEvidence)
	}
	if s.hasMisc {
		t.Error("misc/ reported present in a folder that has none")
	}
	if s.suggest != "" {
		t.Errorf("a correct pick was second-guessed with %q", s.suggest)
	}
	// A base that is fine gets no warnings at all: a screen that always warns is a
	// screen nobody reads.
	if warns := baseScanWarnings(s); len(warns) != 0 {
		t.Errorf("a usable base produced warnings: %q", warns)
	}
	// And the neutral report names the char.ini finding, because that is the one
	// thing about a base a user cannot see by looking at the folder.
	joined := strings.Join(baseScanReport(s), " | ")
	if !containsFold(joined, baseCharINI) {
		t.Errorf("the report never mentions %s: %q", baseCharINI, joined)
	}
}

// TestBaseScanWarnsWhenNoCharacterShipsAnINI is issue #72 stated before the fact.
//
// This is the state that reads as "it worked": the sprites load out of the
// folder, so the setup looks correct, while every emote list, showname, blip set
// and chatbox skin still comes from the server's copy of that character. Nothing
// on screen explains the difference, so the report has to say it up front.
func TestBaseScanWarnsWhenNoCharacterShipsAnINI(t *testing.T) {
	dir := writeBaseTree(t, t.TempDir(), map[string]string{
		"characters/witch/(a)idle.png": "x",
		"characters/beato/(a)idle.png": "x",
	})
	s := scanBaseFolder(dir)
	if s.iniOK != 0 || s.iniOf != 2 {
		t.Fatalf("char.ini = %d of %d, want 0 of 2", s.iniOK, s.iniOf)
	}
	warns := strings.Join(baseScanWarnings(s), " | ")
	if !containsFold(warns, baseCharINI) {
		t.Fatalf("no warning mentions %s: %q", baseCharINI, warns)
	}
	// It has to name a CONSEQUENCE, not just the missing file — "no char.ini" means
	// nothing to somebody who has never had to care what one is.
	if !containsFold(warns, "server") {
		t.Errorf("the warning never says where the metadata will come from instead: %q", warns)
	}
	// And it must not claim the folder is unusable: the sprites do load.
	for _, lie := range []string{"will not load", "won't work", "unusable"} {
		if containsFold(warns, lie) {
			t.Errorf("the warning claims %q, but a base with no inis still serves its art: %q", lie, warns)
		}
	}
}

// TestBaseScanCorrectsTheTwoNearMisses pins the folder-picking mistakes that
// actually happen. Both used to end the same way — the folder was accepted, the
// index found nothing, and the client went on streaming with no explanation.
func TestBaseScanCorrectsTheTwoNearMisses(t *testing.T) {
	root := t.TempDir()
	base := filepath.Join(root, "base")
	writeBaseTree(t, root, map[string]string{
		"base/characters/witch/char.ini": "[Options]\n",
		"base/background/court/":         "",
	})

	// One level TOO HIGH: the install root that contains the base.
	up := scanBaseFolder(root)
	if up.looksLikeBase() {
		t.Fatal("the install root was accepted as a base")
	}
	if up.suggest != base {
		t.Errorf("suggest = %q, want %q — the wizard offers this as a one-click fix", up.suggest, base)
	}
	if warns := strings.Join(baseScanWarnings(up), " | "); !strings.Contains(warns, base) {
		t.Errorf("the warning does not name the folder that would work: %q", warns)
	}

	// One level TOO DEEP: characters/ itself.
	deep := scanBaseFolder(filepath.Join(base, baseDirCharacters))
	if deep.looksLikeBase() {
		t.Fatal("the characters/ folder was accepted as a base")
	}
	if deep.suggest != base {
		t.Errorf("suggest = %q, want the parent %q", deep.suggest, base)
	}
}

// TestBaseScanReadsAZipPack: a .zip is mountable, so the setup flow has to be
// able to describe one. Refusing would send the user back to the text field this
// surface replaces.
func TestBaseScanReadsAZipPack(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mypack.zip")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for _, name := range []string{
		"characters/witch/char.ini",
		"characters/witch/(a)idle.webp",
		"characters/beato/(a)idle.webp",
		`characters\lambda\char.ini`, // a pack zipped by a Windows tool
		"background/court/defenseempty.png",
		"sounds/music/theme.opus",
	} {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte("x")); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	s := scanBaseFolder(path)
	if s.err != "" {
		t.Fatalf("scan failed: %s", s.err)
	}
	if !s.isZip {
		t.Error("a .zip was walked as a folder")
	}
	if s.chars != 3 {
		t.Errorf("chars = %d, want 3 — a character contributes one entry per sprite, not one per character", s.chars)
	}
	if s.bgs != 1 {
		t.Errorf("bgs = %d, want 1", s.bgs)
	}
	if s.iniOK != 2 || s.iniOf != 3 {
		t.Errorf("char.ini = %d of %d, want 2 of 3 (the archive lists every character, so the sample is all of them)", s.iniOK, s.iniOf)
	}
	if !s.hasSounds {
		t.Error("sounds/ not found in the pack")
	}
	if !containsFold(strings.Join(baseScanReport(s), " "), "pack") {
		t.Error("the report calls a .zip a folder")
	}
}

// TestBaseScanReportsAnUnreadablePick: a path that is not there must produce an
// error the wizard can show, not a report full of confident zeroes.
func TestBaseScanReportsAnUnreadablePick(t *testing.T) {
	s := scanBaseFolder(filepath.Join(t.TempDir(), "nope"))
	if s.err == "" {
		t.Fatal("a missing folder scanned clean")
	}
	file := filepath.Join(t.TempDir(), "notafolder.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if s := scanBaseFolder(file); s.err == "" {
		t.Error("a plain file was accepted as a base folder")
	}
	if s := scanBaseFolder("   "); s.err == "" {
		t.Error("an empty pick scanned clean")
	}
}

// TestBaseScanFolderNamesMatchTheURLBuilder keeps this file's folder names tied
// to the layout the client actually requests.
//
// courtroom/urlbuilder.go is the authority and its path constants are
// unexported, so the parity is checked through the exported builder: if a
// directory is ever renamed there, the scan would otherwise go on reporting
// every base as empty, and nothing else in the suite would notice.
func TestBaseScanFolderNamesMatchTheURLBuilder(t *testing.T) {
	u := courtroom.NewURLBuilder("http://example.test/base/")
	for _, tc := range []struct {
		name string
		url  string
	}{
		{baseDirCharacters, u.CharFolder("witch")},
		{baseDirBackground, u.BackgroundsRoot()},
		{baseDirSounds, u.SFX("realization")},
		{baseDirEvidence, u.Evidence("knife.png")},
	} {
		if !strings.Contains(tc.url, "/"+tc.name+"/") {
			t.Errorf("the scan looks for %q but the URL builder asks for %q — every base would scan as empty",
				tc.name, tc.url)
		}
	}
}

// TestBaseWizardWritesNothingUntilTheUserApplies is the promise the report rests
// on. Showing a folder's contents before committing is only worth anything if
// browsing, scanning and re-picking genuinely change nothing.
func TestBaseWizardWritesNothingUntilTheUserApplies(t *testing.T) {
	a := testTabApp(t)
	dir := writeBaseTree(t, t.TempDir(), map[string]string{"characters/witch/char.ini": "[Options]\n"})
	restoreBaseWizard(t)

	a.openBaseWizard()
	baseWizard.setBasePath(dir)
	awaitBaseScan(t)
	baseWizard.mode = assetSrcLocal

	if _, mounts := a.d.Prefs.LocalAssets(); len(mounts) != 0 {
		t.Fatalf("picking a folder wrote mounts before Use this folder: %v", mounts)
	}
	if a.assetSourceMode() != assetSrcStream {
		t.Fatal("picking a folder changed the asset source before Use this folder")
	}
	// Cancel is the same no-op the ✕ is.
	baseWizard.open = false
	if _, mounts := a.d.Prefs.LocalAssets(); len(mounts) != 0 {
		t.Errorf("cancelling wrote mounts: %v", mounts)
	}
}

// TestBaseWizardAppliesTheFolderFirstAndTheChosenMode is the commit. The order
// matters: mounts are searched first-hit-wins, so the folder the user just
// pointed at and inspected has to win a tie against one configured months ago.
func TestBaseWizardAppliesTheFolderFirstAndTheChosenMode(t *testing.T) {
	a := testTabApp(t)
	restoreBaseWizard(t)
	old := filepath.FromSlash("C:/AO2/older-pack")
	if !a.d.Prefs.SetLocalAssets(false, []string{old}) {
		t.Fatal("seeding a mount was refused")
	}
	dir := writeBaseTree(t, t.TempDir(), map[string]string{"characters/witch/char.ini": "[Options]\n"})

	a.openBaseWizard()
	baseWizard.setBasePath(dir)
	baseWizard.mode = assetSrcLayered
	a.applyBaseWizard()

	if baseWizard.open {
		t.Error("the wizard stayed open after applying")
	}
	_, mounts := a.d.Prefs.LocalAssets()
	if len(mounts) != 2 || mounts[0] != dir || mounts[1] != old {
		t.Fatalf("mounts = %v, want the new folder FIRST then %q", mounts, old)
	}
	if got := a.assetSourceMode(); got != assetSrcLayered {
		t.Errorf("mode = %d, want assetSrcLayered", got)
	}
	// Applying the SAME folder again moves it to the front rather than duplicating.
	a.openBaseWizard()
	baseWizard.setBasePath(old)
	baseWizard.mode = assetSrcLocal
	a.applyBaseWizard()
	_, mounts = a.d.Prefs.LocalAssets()
	if len(mounts) != 2 || mounts[0] != old || mounts[1] != dir {
		t.Fatalf("re-picking an existing mount gave %v, want it moved to the front", mounts)
	}
	if got := a.assetSourceMode(); got != assetSrcLocal {
		t.Errorf("mode = %d, want assetSrcLocal", got)
	}
	// The line the page shows afterwards has to distinguish the two modes, since
	// one of them is a promise that nothing will stream.
	layered := baseWizardApplied(dir, assetSrcLayered)
	local := baseWizardApplied(dir, assetSrcLocal)
	if layered == local {
		t.Fatal("both modes report the same thing")
	}
	if !containsFold(local, "nothing will stream") {
		t.Errorf("the local-only line does not say nothing streams: %q", local)
	}
}

// TestOpenBaseWizardTakesTheDropBeforeTheThemeRoot pins the drop arm from both
// sides.
//
// The wizard says "or drag the folder onto this window", so an open wizard has to
// consume the drop — otherwise it falls through to the Settings screen's default,
// which points the user's THEME ROOT at the folder they were setting up as an
// asset base. That is the silent-wrong-action class dropclaim.go exists to end,
// reached through a different door. And a CLOSED wizard, or a path some global
// owner already claimed, must not be touched — stealing those would resurrect the
// same bug from the other side.
func TestOpenBaseWizardTakesTheDropBeforeTheThemeRoot(t *testing.T) {
	a := testTabApp(t)
	restoreBaseWizard(t)
	dir := writeBaseTree(t, t.TempDir(), map[string]string{"characters/witch/char.ini": "[Options]\n"})

	if a.baseWizardTakesDrop(dir) {
		t.Fatal("a CLOSED wizard swallowed a drop — the theme-folder import would stop working")
	}
	baseWizard.open = true
	for _, owned := range []string{
		filepath.Join(dir, "umineko"+themePackExt),
		filepath.Join(dir, "clip.aorec"),
		filepath.Join(dir, "CourtSerif.ttf"),
	} {
		if a.baseWizardTakesDrop(owned) {
			t.Errorf("the wizard stole %q from its global owner", filepath.Base(owned))
		}
	}
	if !a.baseWizardTakesDrop(dir) {
		t.Fatal("an OPEN wizard ignored a dropped folder, so it would repoint the theme root instead")
	}
	awaitBaseScan(t)
	if baseWizard.path != dir {
		t.Errorf("dropped folder = %q, want %q", baseWizard.path, dir)
	}
	// A dropped FILE means its folder, matching the rest of the app.
	baseWizard.setBasePath("")
	if !a.baseWizardTakesDrop(filepath.Join(dir, "characters", "witch", baseCharINI)) {
		t.Fatal("a dropped file inside the base was ignored")
	}
	awaitBaseScan(t)
	if baseWizard.path != filepath.Join(dir, "characters", "witch") {
		t.Errorf("a dropped file resolved to %q, want its own folder", baseWizard.path)
	}
	// Except a .zip, which is a mountable pack in its own right: taking its PARENT
	// would mount the user's Downloads folder.
	pack := filepath.Join(dir, "mypack.zip")
	if err := os.WriteFile(pack, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	baseWizard.setBasePath("")
	if !a.baseWizardTakesDrop(pack) {
		t.Fatal("a dropped .zip was ignored")
	}
	awaitBaseScan(t)
	if baseWizard.path != pack {
		t.Errorf("a dropped .zip resolved to %q, want the pack itself", baseWizard.path)
	}
}

// TestBaseWizardScanStaysOffTheRenderThread is the hard-rule-2 gate.
//
// scanBaseFolder opens directories and stats files, so a call on the render path
// is a frame hitch on a local disk and a freeze on a network share. The seam is
// "the only way to run a scan is kickScan's goroutine", and a census is the only
// way to see that from a test: a direct call would work perfectly in every test
// and every fast-disk playtest.
func TestBaseWizardScanStaysOffTheRenderThread(t *testing.T) {
	// The functions allowed to name scanBaseFolder at all: the goroutine that runs
	// it, and nothing else.
	allowed := map[string]bool{"kickScan": true}
	seen := 0
	packageFuncs(t, func(file, fn string, body *ast.BlockStmt) {
		if !containsCall(body, "scanBaseFolder") {
			return
		}
		seen++
		if allowed[fn] {
			return
		}
		t.Errorf("%s: %s calls scanBaseFolder, which walks the disk. Every draw path in this package runs on "+
			"the render thread, where synchronous disk I/O is hard rule 2. Post the pick through "+
			"baseWizardState.setBasePath instead", file, fn)
	})
	if seen == 0 {
		t.Fatal("nothing calls scanBaseFolder — the census is checking an empty package")
	}
	// And the one caller has to actually be a goroutine, or the allow-list above is
	// permitting a blocking call by name.
	body := funcBodySource(t, "basewizard.go", "kickScan")
	if !goCall(body, "scanBaseFolder") {
		t.Error("kickScan calls scanBaseFolder inline — the whole point of the latch is that the walk runs " +
			"off-thread, and a blocking listing here freezes the frame the user clicked Browse on")
	}
}

// TestApplyIsTheOnlyWriterInTheWizard proves the "changes nothing until you say
// so" promise structurally rather than by one example.
//
// TestBaseWizardWritesNothingUntilTheUserApplies drives one path through the
// wizard; this says no OTHER path can exist. The preference writers are easy to
// reach for — setAssetSourceMode is one line and does the obvious thing — so a
// later wave adding "apply as you pick" would break the report's whole reason for
// existing and no example test would notice.
func TestApplyIsTheOnlyWriterInTheWizard(t *testing.T) {
	writers := []string{"setAssetSourceMode", "SetLocalAssets", "SetLayeredAssets", "rebuildAssetOrigin"}
	const owner = "applyBaseWizard"
	found := false
	packageFuncs(t, func(file, fn string, body *ast.BlockStmt) {
		if file != "basewizard.go" && file != "basescan.go" {
			return
		}
		for _, w := range writers {
			if !containsCall(body, w) {
				continue
			}
			if fn == owner {
				found = true
				continue
			}
			t.Errorf("%s: %s calls %s. Only %s may write the asset source — the wizard shows a folder's "+
				"contents BEFORE committing, and that is only worth anything if browsing, scanning and "+
				"re-picking are read-only", file, fn, w, owner)
		}
	})
	if !found {
		t.Errorf("%s writes nothing — pressing Use this folder would do nothing at all", owner)
	}
}

// TestSettingsRunsTheWizardBeforeItsOverlays pins the wiring drawSettings owns,
// which no test can reach by drawing (it is called by zero tests).
//
// Two orderings matter and both are invisible at run time in a passing test.
// The drop arm must precede the theme-folder router, or an open wizard's drop
// repoints the theme root. And the wizard must draw BEFORE the file browser its
// own step 1 opens, because the topmost panel is the one drawn last.
func TestSettingsRunsTheWizardBeforeItsOverlays(t *testing.T) {
	body := funcBodySource(t, "settings.go", "drawSettings")
	order := callOrder(body, "baseWizardTakesDrop", "settingsDropAction", "drawBaseWizard", "drawDemoBrowser")
	for _, want := range []string{"baseWizardTakesDrop", "settingsDropAction", "drawBaseWizard", "drawDemoBrowser"} {
		if !containsName(order, want) {
			t.Fatalf("drawSettings never calls %s — the wizard is unreachable or unwired", want)
		}
	}
	if indexOfName(order, "baseWizardTakesDrop") > indexOfName(order, "settingsDropAction") {
		t.Error("drawSettings routes a drop through settingsDropAction before offering it to the open wizard — " +
			"a folder dropped on the wizard would repoint the user's THEME ROOT instead")
	}
	if indexOfName(order, "drawBaseWizard") > indexOfName(order, "drawDemoBrowser") {
		t.Error("drawSettings draws the wizard AFTER the file browser, so the browser the wizard opens would " +
			"be painted underneath it")
	}
	// The wizard is a blocking modal: the page behind it must be click-dead, or a
	// click on the modal also fires whatever settings row sits under it.
	if !bodyMentionsIdent(body, "baseWizard") {
		t.Error("drawSettings never mentions baseWizard — it is outside the modal fence")
	}
}

// TestBaseFolderBrowserPicksADirectory: the base browser is the first purpose
// whose answer is the folder you are STANDING IN rather than a row in the list,
// so the affordance that commits it has to exist and has to be purpose-gated.
func TestBaseFolderBrowserPicksADirectory(t *testing.T) {
	if !browsePicksDir(purposeBaseFolder) {
		t.Fatal("the base-folder browser has no way to pick the folder it is showing")
	}
	for _, p := range []browsePurpose{purposeVideo, purposeCheck, purposePackage, purposeThemeBundle, purposeThemeImage} {
		if browsePicksDir(p) {
			t.Errorf("purpose %d grew a Use-this-folder button; those purposes pick a FILE", p)
		}
	}
	// It still lists .zip packs: a pack is a legitimate answer to "where are your
	// AO files", and a listing filtered to directories only would make a folder
	// full of them look empty to somebody checking they had the right one.
	keep := browseKeepRule(purposeBaseFolder)
	if !keep("mypack.zip") || !keep("MYPACK.ZIP") {
		t.Error("the base-folder browser hides .zip packs")
	}
	if keep("clip.aorec") || keep("notes.txt") {
		t.Error("the base-folder browser lists files that cannot be mounted")
	}
	// And the button is drawn from the purpose, not from a flag some future caller
	// has to remember to set.
	body := funcBodySource(t, "demobrowser.go", "drawDemoBrowser")
	if !containsCall(body, "browsePicksDir") {
		t.Error("drawDemoBrowser no longer consults browsePicksDir — the Use-this-folder button is gone " +
			"and the base browser has no pick action at all")
	}
}

// TestAssetPanelLeadsWithTheButtonAndSaysWhatIsHappening covers the panel the
// wizard fronts. The summary line is the question the old panel never answered:
// not "what are my options" but "what is it doing right now".
func TestAssetPanelLeadsWithTheButtonAndSaysWhatIsHappening(t *testing.T) {
	const mount = `C:\AO2\base`
	for _, tc := range []struct {
		name   string
		mode   int
		mounts []string
		want   []string
	}{
		{"streaming", assetSrcStream, nil, []string{"streaming everything"}},
		{"layered", assetSrcLayered, []string{mount}, []string{mount, "first"}},
		{"local only", assetSrcLocal, []string{mount}, []string{mount, "nothing is streamed"}},
		// The state the old panel buried at the bottom of the page.
		{"configured but inert", assetSrcStream, []string{mount}, []string{"not being used"}},
	} {
		got := assetSourceSummary(tc.mode, tc.mounts)
		for _, want := range tc.want {
			if !containsFold(got, want) {
				t.Errorf("%s summary %q does not mention %q", tc.name, got, want)
			}
		}
	}
	// Extra folders are counted, not listed: the full list is twenty lines further
	// down the page and repeating it here is the clutter the summary replaces.
	multi := assetSourceSummary(assetSrcLayered, []string{mount, `C:\AO2\pack2`, `C:\AO2\pack3`})
	if !containsFold(multi, "+2 more") {
		t.Errorf("a multi-mount summary %q does not count the rest", multi)
	}
	if containsFold(multi, "pack3") {
		t.Errorf("the summary lists every mount: %q", multi)
	}
	// The button is the first thing in the panel, and the advanced rows still draw
	// (a hidden row is invisible to the settings search, which indexes at draw
	// time — see drawAssetSourceAdvanced).
	body := funcBodySource(t, "settingsassets.go", "drawAssetSourceSettings")
	order := callOrder(body, "drawAssetSourceHeadline", "drawAssetSourceAdvanced")
	if len(order) != 2 || order[0] != "drawAssetSourceHeadline" {
		t.Errorf("the assets panel draws %v — the guided button must come first, and the advanced rows "+
			"must still draw or the settings search cannot find them", order)
	}
	if !containsCall(funcBodySource(t, "settingsassets.go", "drawAssetSourceHeadline"), "openBaseWizard") {
		t.Error("the panel's headline button no longer opens the wizard")
	}
}

// --- helpers ----------------------------------------------------------------

// restoreBaseWizard resets the package-level wizard and puts it back afterwards,
// so these gates neither inherit nor leak state (it is package-level for the same
// reason `settings` is: the Settings screen is single-instance).
// It also waits out an in-flight scan before restoring. Cleanup order is
// last-registered-first, so this runs BEFORE t.TempDir() is deleted: without the
// wait a test can end with the walk still inside a directory being removed, which
// is a flake on Windows rather than a real defect.
func restoreBaseWizard(t *testing.T) {
	t.Helper()
	saved := baseWizard
	fresh := baseWizardState{res: make(chan baseScan, baseScanResCap), mode: assetSrcLayered}
	baseWizard = fresh
	t.Cleanup(func() {
		if baseWizard.scanning {
			select {
			case <-fresh.res:
			case <-time.After(baseScanPolls * time.Millisecond):
			}
		}
		baseWizard = saved
	})
}

// awaitBaseScan drains the scan the way the wizard's draw does, failing rather
// than hanging.
func awaitBaseScan(t *testing.T) {
	t.Helper()
	for i := 0; i < baseScanPolls; i++ {
		baseWizard.pollBaseScan()
		if baseWizard.scanned {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("the folder scan never landed")
}

// baseScanPolls bounds awaitBaseScan at a second of millisecond polls — long
// enough for a temp-folder walk on a loaded machine, short enough to fail rather
// than hang a suite.
const baseScanPolls = 1000

// goCall reports a `go f(...)` statement calling fn — the difference between
// "runs off-thread" and "blocks the frame", which containsCall cannot see.
func goCall(n ast.Node, fn string) bool {
	found := false
	ast.Inspect(n, func(node ast.Node) bool {
		g, ok := node.(*ast.GoStmt)
		if !ok {
			return true
		}
		if containsCall(g, fn) {
			found = true
		}
		return true
	})
	return found
}

// containsName / indexOfName read a callOrder result. callOrder returns the
// names in source order with repeats, so a plain index is what "came first"
// means here.
func containsName(order []string, want string) bool { return indexOfName(order, want) >= 0 }

func indexOfName(order []string, want string) int {
	for i, n := range order {
		if n == want {
			return i
		}
	}
	return -1
}

// bodyMentionsIdent reports an identifier appearing anywhere in a statement —
// used where the thing being pinned is a field read (baseWizard.open in the
// modal fence) rather than a call. srcgate_test.go's mentionsIdent answers the
// same question for an EXPRESSION; a function body is not one.
func bodyMentionsIdent(n ast.Node, name string) bool {
	found := false
	ast.Inspect(n, func(node ast.Node) bool {
		if id, ok := node.(*ast.Ident); ok && id.Name == name {
			found = true
		}
		return true
	})
	return found
}
