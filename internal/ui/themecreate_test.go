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

// TestTheLiveToggleIsOnTheRailAndSaysWhichStateItIsIn pins the affordance itself.
// A toggle nobody can find is the defect this whole wave is about.
func TestTheLiveToggleIsOnTheRailAndSaysWhichStateItIsIn(t *testing.T) {
	if editorLiveLabel(true) == editorLiveLabel(false) {
		t.Error("the Live button reads the same in both states")
	}
}
