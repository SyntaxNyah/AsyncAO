package ui

// THE CREATOR, THE TEMPLATES, THE IMAGE INTAKE AND THE LIVE TOGGLE (v1.90.0 W10).
//
// Every gate here DRIVES the seam rather than mirroring it (hard rule 11): the
// templates go through newThemeSidecarFrom, the folder through the real
// themepack.CreateFromSidecar, the undo group through applyImageIntakeOps and
// editorUndo, and the toggle through setEditorLive and editorApply. Deleting any of
// them fails the suite; re-implementing one in a test would not have caught the
// defects each was written for.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
	"github.com/SyntaxNyah/AsyncAO/internal/themepack"
)

// decodedFixtureFor is a one-frame Decoded of the given size — enough for
// newImageElement, which reads only Width and Height. No pixels are allocated: the
// element's placement is pure arithmetic over the two numbers.
func decodedFixtureFor(w, h int) *assets.Decoded {
	return &assets.Decoded{Width: w, Height: h}
}

// ---------------------------------------------------------------------------
// The creation funnel is ONE
// ---------------------------------------------------------------------------

// TestOneThemeCreationFunnel is the deletion-catcher rule 11 asks for.
//
// Before this wave internal/ui minted theme folders in TWO places: the preset
// gallery's own job/poll/land, and nothing else — which was fine right up to the
// moment a second surface wanted one, because the second copy is where the catalog
// seed, the collision suffix or the write-root refusal gets forgotten. The mint is
// now startThemeCreate and this census says there is exactly one of it.
func TestOneThemeCreationFunnel(t *testing.T) {
	var callers []string
	for _, name := range bundleGateGoFiles(t) {
		if strings.Contains(bundleGateSource(t, name), "themepack.CreateFromSidecar(") {
			callers = append(callers, name)
		}
	}
	if len(callers) != 1 || callers[0] != "themecreate.go" {
		t.Fatalf("themepack.CreateFromSidecar is called from %v — it must be called from themecreate.go "+
			"and nowhere else, or the catalog seed, the collision suffix and the write-root refusal have "+
			"a second place to be forgotten", callers)
	}
	// AND BOTH SURFACES REACH IT. The gallery is the in-editor pick; the settings tab
	// is the one with no prerequisite.
	gallery := funcBodySource(t, "themegallery.go", "createThemeFromGalleryPick")
	if !containsCall(gallery, "startThemeCreate") {
		t.Error("the preset gallery no longer routes its create through the shared funnel — it is a " +
			"second mint, and the two will disagree about the write root")
	}
	settingsTab := funcBodySource(t, "settingsthemeeditor.go", "drawThemeMakeRows")
	if !containsCall(settingsTab, "startThemeCreateFromTemplate") {
		t.Error("the Theme editor tab no longer offers a create — the templates row is the only path from " +
			"'I have no theme of my own' to the editor")
	}
	// AND THE LANDING OPENS THE EDITOR THROUGH THE COPY PATH'S OWN ARMING, rather
	// than a second one-shot with its own generation rails.
	land := funcBodySource(t, "themecreate.go", "landThemeCreate")
	if !containsCall(land, "noteCopiedTheme") || !containsCall(land, "applyThemeAsync") {
		t.Error("a created theme no longer seeds the catalog and applies — the very next Save resolves " +
			"through a snapshot that predates the folder and refuses a theme sitting on disk")
	}
	if !strings.Contains(bundleGateSource(t, "themecreate.go"), "a.themeEditArmGen, a.themeEditArmFrom = gen, a.screen") {
		t.Error("the create no longer arms the SHARED open-the-editor one-shot (maybeOpenEditorAfterCopy) — " +
			"a second opening path is a second set of generation rails to get wrong")
	}
}

// ---------------------------------------------------------------------------
// Templates
// ---------------------------------------------------------------------------

// TestEveryTemplateEitherBuildsAModelOrRefusesByName drives newThemeSidecarFrom
// over the whole enum.
//
// TOTAL OVER THE ENUM, deliberately: a template added without a model builder would
// otherwise fall through to the "does not build a theme on its own" arm and silently
// create nothing. templateCopyCurrent is the one that legitimately answers that way,
// because a copy has a SOURCE FOLDER and comes through themecopy.go instead.
func TestEveryTemplateEitherBuildsAModelOrRefusesByName(t *testing.T) {
	lib, err := theme.ShippedPresets()
	if err != nil {
		t.Fatalf("the shipped preset library did not load: %v", err)
	}
	pair := theme.PresetPair{Layout: presetAt(lib.Layouts(), 0), Style: presetAt(lib.Styles(), 0)}
	for tpl := themeCreateTemplate(0); tpl < themeCreateTemplateCount; tpl++ {
		sc, err := newThemeSidecarFrom(tpl, "Gate theme", pair)
		if tpl == templateCopyCurrent {
			if err != errThemeCreateNoModel {
				t.Errorf("%v built a model (%v) — a copy has a source FOLDER, so routing it through the "+
					"in-memory mint would silently drop its files, its layout overrides and its provenance",
					tpl, err)
			}
			continue
		}
		if err != nil || sc == nil {
			t.Fatalf("%v built no model: %v", tpl, err)
		}
		if sc.Meta.Name != "Gate theme" {
			t.Errorf("%v named the theme %q, want the name it was asked for", tpl, sc.Meta.Name)
		}
		// EVERY TEMPLATE MUST SURVIVE THE WRITE GUARD. Sidecar.Bytes runs Validate, so
		// a model that would not read back cannot reach the disk — and finding that out
		// here rather than in a chip is the point of the gate.
		if _, err := sc.Bytes(); err != nil {
			t.Errorf("%v produced a model the writer refuses: %v", tpl, err)
		}
	}
	// AND THE PRESET TEMPLATE REFUSES AN EMPTY PAIR BY NAME rather than building an
	// unnamed blank: the settings row can be on templatePreset with an unreadable
	// library, and a silent blank theme would be the worst possible answer.
	if _, err := newThemeSidecarFrom(templatePreset, "x", theme.PresetPair{}); err != errThemeCreateNoPair {
		t.Errorf("templatePreset with no pair returned %v, want errThemeCreateNoPair", err)
	}
}

// TestEveryTemplateHasALabelAndANote pins the two fixed tables against the enum.
// A template with no label draws an empty dropdown row; one with no note draws a
// control whose consequence is unstated.
func TestEveryTemplateHasALabelAndANote(t *testing.T) {
	for tpl := themeCreateTemplate(0); tpl < themeCreateTemplateCount; tpl++ {
		if themeCreateTemplateNames[tpl] == "" {
			t.Errorf("template %d has no label", tpl)
		}
		if themeCreateTemplateNotes[tpl] == "" {
			t.Errorf("template %q has no note — the row would say what it does and not what you get", tpl)
		}
	}
}

// ---------------------------------------------------------------------------
// A fresh theme is valid from birth
// ---------------------------------------------------------------------------

// TestABlankThemeIsValidFromBirth is the promise the creator makes: what it writes
// parses back with ZERO notes, validates, and loads as a theme.
//
// IT GOES THROUGH THE REAL MACHINERY end to end — newThemeSidecarFrom →
// themepack.CreateFromSidecar (which is Sidecar.Bytes, i.e. the write guard) →
// os.ReadFile → theme.ParseSidecar → Validate → theme.Load. No hand-rolled INI text
// anywhere, which is the whole reason the funnel exists.
func TestABlankThemeIsValidFromBirth(t *testing.T) {
	root := t.TempDir()
	sc, err := newThemeSidecarFrom(templateBlank, "My Theme", theme.PresetPair{})
	if err != nil {
		t.Fatalf("building the blank model: %v", err)
	}
	m, err := themepack.CreateFromSidecar(root, "My Theme", sc)
	if err != nil {
		t.Fatalf("creating the theme: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(m.Dir, theme.SidecarFileName))
	if err != nil {
		t.Fatalf("the created theme has no sidecar: %v", err)
	}
	back, err := theme.ParseSidecar(body)
	if err != nil {
		t.Fatalf("a theme this client just wrote does not read back: %v", err)
	}
	if notes := back.Notes(); len(notes) != 0 {
		t.Errorf("a freshly created theme reads back with %d degrade notes (%v) — the import report would "+
			"greet its author with a list of complaints about a file they never touched", len(notes), notes)
	}
	if err := back.Validate(); err != nil {
		t.Errorf("a freshly created theme does not validate: %v", err)
	}
	if back.Meta.Name != "My Theme" {
		t.Errorf("the display name did not survive the round trip: %q", back.Meta.Name)
	}
	// AND IT APPLIES: theme.Load over the root that holds it resolves the folder and
	// finds the sidecar this client just wrote. A theme that cannot be loaded is not a
	// theme, however well it parses.
	//
	// The flat tier is what makes this root work with no themes/ level (theme.Load's
	// own note on bare-folder imports, #21).
	tm, err := theme.Load(m.Root, "", []string{root})
	if err != nil {
		t.Fatalf("the created theme does not load: %v", err)
	}
	if tm.Sidecar() == nil {
		t.Fatal("theme.Load found no AsyncAO tier in a theme whose only file is one")
	}
	if err := tm.SidecarErr(); err != nil {
		t.Errorf("the loader refused the sidecar it just read: %v", err)
	}

	// COLLISION-SAFE: the second one is suffixed, never an overwrite (the themecopy
	// " (2)" precedent, which is themepack's own freeName).
	second, err := themepack.CreateFromSidecar(root, "My Theme", sc)
	if err != nil {
		t.Fatalf("creating the same name twice: %v", err)
	}
	if second.Root == m.Root {
		t.Fatalf("both creates landed in %q — the second overwrote the first", m.Root)
	}
	if _, err := os.Stat(filepath.Join(m.Dir, theme.SidecarFileName)); err != nil {
		t.Errorf("the first theme is gone after the second was created: %v", err)
	}
}

// ---------------------------------------------------------------------------
// The image intake
// ---------------------------------------------------------------------------

// TestOneThemeImageIntakeFunnel is the intake's deletion-catcher.
//
// TWO DOORS, ONE FUNNEL: a drag lands in editorTakeImage and so does the browser's
// pick. What this forbids is a THIRD — a surface that copies a picture into a theme
// or appends a [media] row without going through the caps, the safepath join and the
// budget verdict that live in one place.
func TestOneThemeImageIntakeFunnel(t *testing.T) {
	const owner = "thememediaintake.go"
	for _, needle := range []string{"opMediaRow", "editImageDirName"} {
		var users []string
		for _, name := range bundleGateGoFiles(t) {
			if name == owner || name == "themeeditorop.go" || name == "themeeditordoc.go" {
				continue // the op kind's declaration and the document's writer are its own
			}
			if strings.Contains(bundleGateSource(t, name), needle) {
				users = append(users, name)
			}
		}
		if len(users) != 0 {
			t.Errorf("%s is used outside the intake funnel, in %v — a second place that declares theme "+
				"media is a second place to forget the cap, the safepath join or the budget", needle, users)
		}
	}
	drop := funcBodySource(t, "demofile.go", "handleThemeImageDrop")
	if !containsCall(drop, "editorTakeImage") {
		t.Error("a dropped picture no longer reaches the intake funnel")
	}
	pick := funcBodySource(t, "demobrowser.go", "pickBrowsedFile")
	if !containsCall(pick, "editorTakeImage") {
		t.Error("the browser's pick no longer reaches the intake funnel — browsing and dropping would " +
			"then be two importers with two answers about the caps")
	}
	rail := funcBodySource(t, "themeeditor.go", "drawEditorRailActions")
	if !containsCall(rail, "openDemoBrowserFor") {
		t.Error("the element rail no longer offers the browse half — a drop is the only way in, and a " +
			"drop is undiscoverable")
	}
}

// TestDroppedPicturesAreClaimedBeforeTheThemeRootArm is the W2 bug's own shape,
// asked about pictures.
//
// A dropped FILE must never reach the Settings screen's theme-folder arm: that arm
// turns a file into "its parent folder" and stores that as the user's theme root —
// typically Downloads. Fonts closed this in W4 and bundles in W2; a picture is the
// third file type the editor takes and it needed the same claim.
func TestDroppedPicturesAreClaimedBeforeTheThemeRootArm(t *testing.T) {
	for _, ext := range editIntakeImageExts {
		path := filepath.Join("C:\\", "Users", "someone", "Downloads", "art"+ext)
		claim := claimDroppedFile(path, false)
		if claim != dropClaimThemeImage {
			t.Errorf("a dropped %s is claimed as %v, not dropClaimThemeImage", ext, claim)
		}
		if act := settingsDropAction(claim); act != settingsDropIgnore {
			t.Errorf("the Settings screen acts on a dropped %s (%v) — it would repoint the user's theme "+
				"root at their Downloads folder", ext, act)
		}
		if !isThemeImageName("ART" + strings.ToUpper(ext)) {
			t.Errorf("the browser's filter misses %s in upper case", ext)
		}
	}
	// A capital-letter path is the same file. The claim lowercases; the browse filter
	// must too, or the same picture is takeable one way and invisible the other.
	if claimDroppedFile(`C:\x\ART.PNG`, false) != dropClaimThemeImage {
		t.Error("an upper-case .PNG is not claimed — extensions arrive however the filesystem spells them")
	}
	// And a format the decode pipeline cannot sniff is NOT claimed: a wrong action is
	// worse than no action (themeFontExts' own ruling about .ttc and .woff).
	if got := claimDroppedFile(`C:\x\photo.jpeg`, false); got == dropClaimThemeImage {
		t.Error(".jpeg is claimed as theme art — it is not in ElemImage's list, so the intake would copy " +
			"a file the theme cannot draw")
	}
}

// TestAnImageIntakeIsOneUndoStep drives the undoable half of the intake for real:
// the [media] declaration and the element go in together and come out together.
//
// THE DEFECT IT FORBIDS: an undo that took back only the element would leave the
// declaration behind, and a declared image nothing points at still costs the
// theme's art budget — the budget bar would go on counting a picture nobody can see
// and nothing in the UI could remove it.
func TestAnImageIntakeIsOneUndoStep(t *testing.T) {
	a, prefs := newEffectsRig(t)
	prefs.SetTheme("fixture", "")
	a.themeSidecar = theme.NewSidecar()
	if !a.openThemeEditorForTest(ScreenSettings) {
		t.Fatal("the editor refused to open over a sidecar")
	}
	row := theme.MediaEntry{ID: "art", File: "images/art.png", Bytes: 4096, Hash: "abc123"}
	el := newImageElement("art", decodedFixtureFor(64, 32), 0)
	if why := a.applyImageIntakeOps(row, el); why != "" {
		t.Fatalf("the intake ops were refused: %s", why)
	}
	sc := a.te.doc.side
	if len(sc.Media) != 1 || sc.Media[0] != row {
		t.Fatalf("the [media] row did not land: %+v", sc.Media)
	}
	if len(sc.Elements) != 1 || sc.Elements[0].Media != "art" || sc.Elements[0].Kind != theme.ElemImage {
		t.Fatalf("the element did not land: %+v", sc.Elements)
	}
	// A fresh element must be able to PAINT. A zero Element deliberately cannot
	// (theme.Element's note on Opacity), so an intake that appended one would add a
	// rail row and nothing to the screen — the "it did not work" shape.
	if !sc.Elements[0].Rect.Valid() || sc.Elements[0].EffectiveOpacity() == 0 {
		t.Errorf("the new element cannot paint: rect %+v, opacity %d", sc.Elements[0].Rect, sc.Elements[0].Opacity)
	}
	if !a.editorUndo() {
		t.Fatal("the intake left nothing to undo")
	}
	if len(sc.Elements) != 0 {
		t.Errorf("undo left %d elements", len(sc.Elements))
	}
	if len(sc.Media) != 0 {
		t.Fatalf("undo took back the element and left the [media] declaration (%+v) — the theme still "+
			"pays for a picture nothing draws, and no surface can remove it", sc.Media)
	}
	// …and redo puts BOTH back, or the group is only half a timeline.
	if !a.editorRedo() {
		t.Fatal("the intake could not be redone")
	}
	if len(sc.Media) != 1 || len(sc.Elements) != 1 {
		t.Errorf("redo restored %d media rows and %d elements, want 1 and 1", len(sc.Media), len(sc.Elements))
	}
}

// TestTheMediaRowEncodingRoundTrips pins the pair mediaRowValue / mediaRowOf.
//
// A PAIR, for the reason editValueSlotRect / slotRectOf are one: four fields packed
// in one order and unpacked in another compiles and is silently wrong — and the
// symptom would be an undo that restored a row with the wrong file or the wrong
// byte cost, i.e. a budget that drifts from the picture it describes.
func TestTheMediaRowEncodingRoundTrips(t *testing.T) {
	for _, m := range []theme.MediaEntry{
		{ID: "art", File: "images/art.png", Bytes: 123456, Hash: "deadbeefcafe"},
		{ID: "b", File: "images/a b.webp", Bytes: 0, Hash: ""},
	} {
		if got := mediaRowOf(mediaRowValue(m)); got != m {
			t.Errorf("round trip changed the row: %+v -> %+v", m, got)
		}
	}
	if got := mediaRowOf(mediaRowValue(theme.MediaEntry{})); got != (theme.MediaEntry{}) {
		t.Errorf("the empty row is the table's own 'no row' value; got %+v", got)
	}
	if v := mediaRowValue(theme.MediaEntry{File: "x"}); v != "" {
		t.Errorf("a row with no id encoded to %q — an id-less row is a removal, not a declaration", v)
	}
}

// TestAFreshImageIDIsLegalAndUnique drives uniqueMediaID.
func TestAFreshImageIDIsLegalAndUnique(t *testing.T) {
	sc := theme.NewSidecar()
	first, ok := uniqueMediaID(sc, "My Wallpaper.PNG")
	if !ok || first != "my_wallpaper" {
		t.Fatalf("uniqueMediaID(%q) = %q, %v — the id must be folded through the format's own grammar",
			"My Wallpaper.PNG", first, ok)
	}
	sc.Media = append(sc.Media, theme.MediaEntry{ID: first, File: "images/a.png"})
	second, ok := uniqueMediaID(sc, "my wallpaper.png")
	if !ok || second == first {
		t.Fatalf("a colliding name reused the id %q — the second import would silently replace the first "+
			"picture everywhere it is drawn", second)
	}
	if len(second) > themeMediaIDMaxLen {
		t.Errorf("the suffixed id %q is past the format's %d-rune bound", second, themeMediaIDMaxLen)
	}
	if _, ok := uniqueMediaID(sc, "。。。.png"); ok {
		t.Error("a name with nothing in the id grammar produced an id — it must refuse by name instead")
	}
}

// TestAPlacedImageFitsTheCanvas drives placedImageSize over the three shapes that
// matter: oversized, tiny and square.
func TestAPlacedImageFitsTheCanvas(t *testing.T) {
	for _, tc := range []struct{ w, h int }{{3840, 2160}, {2, 3}, {512, 512}, {0, 0}} {
		w, h := placedImageSize(tc.w, tc.h)
		if w > editImageMaxPlaceEdge || h > editImageMaxPlaceEdge {
			t.Errorf("a %dx%d source placed at %dx%d — past the %d cap, so it lands bigger than the "+
				"canvas and cannot be grabbed", tc.w, tc.h, w, h, editImageMaxPlaceEdge)
		}
		if w < editImageMinPlaceEdge || h < editImageMinPlaceEdge {
			t.Errorf("a %dx%d source placed at %dx%d — under the %d floor, so it lands as an unclickable "+
				"speck", tc.w, tc.h, w, h, editImageMinPlaceEdge)
		}
	}
}

// ---------------------------------------------------------------------------
// Live preview
// ---------------------------------------------------------------------------

// TestFrozenLivePreviewStillEditsTheDocument is the toggle's whole contract.
//
// FROZEN MUST NOT MEAN READ-ONLY. The one thing a "pause the preview" toggle must
// never do is stop the editor working — the document keeps taking edits, they keep
// being undoable, and Save keeps writing them; only the picture stops re-baking.
// And the frozen state has to be the CHEAP one, or the toggle is a label.
func TestFrozenLivePreviewStillEditsTheDocument(t *testing.T) {
	a, prefs := newEffectsRig(t)
	prefs.SetTheme("fixture", "")
	sc := theme.NewSidecar()
	sc.Elements = append(sc.Elements, editProbeElement())
	a.themeSidecar = sc
	if !a.openThemeEditorForTest(ScreenSettings) {
		t.Fatal("the editor refused to open over a sidecar")
	}
	editUnlockAll(a.te.doc)
	if !a.editorLiveOn() {
		t.Fatal("a fresh editor starts frozen — the zero value must be the behaviour every session " +
			"before the toggle existed had")
	}

	a.setEditorLive(false)
	a.themeLay.valid = true
	before := sc.Elements[0].Hidden
	if !a.editorApply(mutateElemField(elementTarget(0), editFieldHidden, editValueBool(!before))) {
		t.Fatal("a frozen editor refused an edit — the toggle pauses the PICTURE, never the document")
	}
	if sc.Elements[0].Hidden == before {
		t.Error("the edit did not land in the model while frozen")
	}
	if !a.te.doc.dirty {
		t.Error("a frozen edit did not mark the document dirty — Save would silently write nothing")
	}
	if !a.te.ring.canUndo() {
		t.Error("a frozen edit is not undoable")
	}
	if !a.themeLay.valid {
		t.Error("a frozen editor re-baked the canvas anyway — the off state must be the cheap one, or " +
			"the toggle does nothing but change a label")
	}

	// Turning it back on pays the one reconcile the frozen frames skipped.
	a.setEditorLive(true)
	if a.themeLay.valid {
		t.Error("resuming live preview did not invalidate the canvas — the picture would stay frozen " +
			"until something else happened to invalidate it")
	}

	// AND THE EXIT ALWAYS RECONCILES. A paused exit that skipped the art sync would
	// leave the courtroom drawing a plan the document has moved past — the class the
	// second editorSyncArt call site exists to close.
	a.setEditorLive(false)
	a.closeThemeEditor()
	if a.te != nil {
		t.Fatal("the editor did not close")
	}
	closer := funcBodySource(t, "themeeditor.go", "closeThemeEditor")
	if !containsCall(closer, "editorSyncArt") {
		t.Fatal("closeThemeEditor no longer reconciles the art on the way out")
	}
	if !strings.Contains(bundleGateSource(t, "themeeditor.go"), "a.te.livePaused = false\n\ta.editorSyncArt()") {
		t.Error("closeThemeEditor no longer lifts the live-preview pause before its reconcile — a paused " +
			"exit would leave the plan stale with no frame left to fix it")
	}
}

// TestTheLiveToggleIsOnTheRailAndSaysWhichStateItIsIn pins the affordance itself.
// A toggle nobody can find is the defect this whole wave is about.
func TestTheLiveToggleIsOnTheRailAndSaysWhichStateItIsIn(t *testing.T) {
	rail := funcBodySource(t, "themeeditor.go", "drawEditorRailActions")
	if !containsCall(rail, "setEditorLive") || !containsCall(rail, "editorLiveLabel") {
		t.Fatal("the element rail no longer draws the live-preview toggle — the only way to stop the " +
			"canvas re-baking on every keystroke would be to close the editor")
	}
	if editorLiveLabel(true) == editorLiveLabel(false) {
		t.Error("the Live button reads the same in both states")
	}
	// The gate on the one gated spelling: every per-edit invalidate goes through
	// editorInvalidateLive, so a future edit path cannot re-bake past a frozen canvas.
	for _, fn := range []string{"editorApply", "editorUndo", "editorRedo"} {
		body := funcBodySource(t, "themeeditor.go", fn)
		if containsCall(body, "invalidateThemeCanvases") {
			t.Errorf("%s calls invalidateThemeCanvases directly — it must go through editorInvalidateLive, "+
				"or a frozen preview re-bakes on that path alone", fn)
		}
		if !containsCall(body, "editorInvalidateLive") {
			t.Errorf("%s no longer invalidates at all — the edit lands and the screen keeps drawing last "+
				"frame's geometry", fn)
		}
	}
}
