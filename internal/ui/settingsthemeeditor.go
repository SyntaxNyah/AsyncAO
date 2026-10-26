package ui

// SETTINGS ▸ THEME EDITOR (v1.90.0 W10) — the authoring tab.
//
// ONE concern: the rows that make, open and receive a theme. Selection — the
// catalog, the badge, the subtheme variant, the write root, the art budget — stays
// on the Theme tab, which is what someone is looking at when they want to PICK one.
//
// WHY THERE IS A TAB AT ALL. The editor shipped as one row two thirds of the way
// down the Theme tab, between "Get themes" and "Your themes folder", under the
// label "Theme editor:" — findable by search (W7a's census made sure of that) and
// invisible to anyone scrolling. A tab in the sidebar is the one surface a user
// reads top-to-bottom before they give up, and the flagship of the release has to
// be on it.
//
// THE ROW ORDER IS THE STORY: make one → open the one you have → take one somebody
// sent you. Creation first because it is the act with no prerequisite; import last
// because it is the one that needs a file you already have.
//
// Every draw helper here shadows `pad := a.formX` (the settings-origin rule — a
// missing shadow renders the row silently under the sidebar) and registers its rows
// with c.onRow so the gather-search finds them by label.

import (
	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// ---------------------------------------------------------------------------
// Named metrics (hard rule 9)
// ---------------------------------------------------------------------------

const (
	// themeMakeNameFieldW is the "call it" field. Wide enough for a name at
	// themepack.NameRuneCap without the caret scrolling on a normal name.
	themeMakeNameFieldW = 260
	// themeMakeTemplateW is the template cycler beside it.
	themeMakeTemplateW = 190
	// themeMakeAxisW is one preset-axis cycler (layout, style). Two of them fit the
	// card at settMinCardW with the label gutter intact.
	themeMakeAxisW = 190
	// themeMakeGap separates the controls on a multi-control row.
	themeMakeGap = 10
	// themeMakeBtnW is the Create button — wider than the catalog rows' buttons
	// because it is the headline act of the tab and its label carries a verb.
	themeMakeBtnW = 150
	// themeMakeRowH / themeMakeNoteRowH are a control row and the hint under it.
	// Same measurements the catalog rows use, so the two tabs line up.
	themeMakeRowH     = 30
	themeMakeNoteRowH = 22
)

// The tab's fixed strings. Package constants because every one of them draws on
// every frame the tab is open and the label cache keys on the string — a formatted
// one would rasterise a fresh texture sixty times a second (the settings-text
// doctrine; see themeRowText).
const (
	themeMakeIntro = "a theme of your own: elements, colours, motion and images, edited live over the real courtroom. It lands in your themes folder and nothing else is touched."
	themeMakeHint  = "the name becomes the folder; an existing name gets a \"(2)\" rather than overwriting anything"
	themeMakeBusy  = "Creating…"
	themeMakeVerb  = "Create & edit"

	themeEditorOpenIntro = "the theme on screen is what the editor opens over — pick a different one in Settings ▸ Theme first if that is not the one you want"

	themeImportIntro = "someone sent you an .aotheme? Drag it onto the window, or browse for one you have already downloaded. Nothing is installed until you confirm what is in it."

	// themeAuthoringHintText is the cross-tab pointer left on the Theme tab where
	// the two rows used to be. Worded as the hint machinery words its own line, so
	// the two read the same wherever a user meets them.
	themeAuthoringHintText = "→ the Theme editor tab covers this — open it"
	themeAuthoringHintRow  = "Make or edit a theme"
)

// ---------------------------------------------------------------------------
// The tab
// ---------------------------------------------------------------------------

// drawSettingsThemeEditor draws the authoring tab.
//
// THE WIDTH IS THE CARD'S, NOT THE WINDOW'S — the house form every other tab body
// follows (drawSettingsGeneral, drawSettingsTheme, drawSettingsAssets, …), and it is
// load-bearing here rather than cosmetic: drawSettingsTabBody hands each body the
// WINDOW width, while the card is clipped to {formX, formW}. The two right-aligned
// buttons below place themselves at `w - themeGetBtnW - scrollBarW`, so with the
// window width they land outside the clip — clipped at the edge on a narrow window
// and, on a wide one, drawn and hit-tested entirely past it (Ctx.hovering reads
// false outside c.clipRect), i.e. an invisible, unpressable button. Rebasing here
// also gives the note labels their true widths, which the card's row-label limit was
// silently trimming for them. These rows read correct at the old call site only
// because drawSettingsTheme had already rebased w before calling them.
func (a *App) drawSettingsThemeEditor(y, _ int32) int32 {
	w := a.formW2()
	y = a.settingsSection(y, w, "New theme")
	y = a.drawThemeMakeRows(y, w)

	y = a.settingsSection(y, w, "The editor")
	y = a.drawThemeEditorOpenRow(y, w)

	y = a.settingsSection(y, w, "Bring a theme in")
	y = a.drawThemeImportRow(y, w)
	return y
}

// drawThemeMakeRows is the CREATOR: a name, a starting point, and the button that
// makes the folder and opens the editor over it.
//
// THE TEMPLATES ARE ALL THINGS THAT ALREADY SHIPPED (themecreate.go's header): a
// blank sidecar, W9's preset compose funnel, or W8's copy-for-editing. This row
// picks between them and performs none of them.
//
// A CYCLER, NOT THE GALLERY'S TWO-AXIS GRID. The gallery is 21 × 14 miniatures and
// wants a panel; a settings row is one line tall, and the axes here are the same
// two lists in the same order, so a user who picks a pair here and then opens the
// gallery finds themselves where they left off (openThemeGallery seeds from the
// theme's declared preset ids). The grid stays where the space for it is.
func (a *App) drawThemeMakeRows(y, w int32) int32 {
	c := a.ctx
	pad := a.formX // the settings-origin shadow rule
	if c.onRow != nil {
		c.onRow("Make a new theme", y)
	}
	c.LabelClipped(pad, y+4, w-pad-scrollBarW, themeMakeIntro, ColTextDim)
	y += themeMakeNoteRowH

	// --- the name -----------------------------------------------------------
	if c.onRow != nil {
		c.onRow("Name the new theme", y)
	}
	c.Label(pad, y+4, "Call it:", ColText)
	name, _ := c.TextField("newthemename",
		sdl.Rect{X: pad + themeRowLabelW, Y: y, W: themeMakeNameFieldW, H: fieldH},
		settings.newThemeName, themeCreateFallbackName)
	// CLAMPED AT THE SANITIZER'S OWN BOUND, here rather than at the create: themepack
	// truncates past themepack.NameRuneCap, so a field that accepted more would show
	// the user a name the folder is never going to have and then quietly shorten it.
	settings.newThemeName = truncRunes(name, themeCreateNameRuneCap)
	c.LabelClipped(pad+themeRowLabelW+themeMakeNameFieldW+themeMakeGap, y+4,
		w-pad-themeRowLabelW-themeMakeNameFieldW-themeMakeGap-scrollBarW, themeMakeHint, ColTextDim)
	y += themeMakeRowH

	// --- the starting point --------------------------------------------------
	if c.onRow != nil {
		c.onRow("Start from a template", y)
	}
	c.Label(pad, y+4, "Start from:", ColText)
	tpl := settings.newThemeTemplate
	if next, changed := c.Dropdown("newthemetpl",
		sdl.Rect{X: pad + themeRowLabelW, Y: y, W: themeMakeTemplateW, H: btnH},
		themeCreateTemplateNames[:], int(tpl)); changed && next >= 0 && next < int(themeCreateTemplateCount) {
		settings.newThemeTemplate = themeCreateTemplate(next)
		tpl = settings.newThemeTemplate
	}
	c.LabelClipped(pad+themeRowLabelW+themeMakeTemplateW+themeMakeGap, y+4,
		w-pad-themeRowLabelW-themeMakeTemplateW-themeMakeGap-scrollBarW,
		themeCreateTemplateNotes[tpl], ColTextDim)
	y += themeMakeRowH

	// --- the two preset axes, only for the template that uses them -----------
	if tpl == templatePreset {
		y = a.drawThemePresetAxes(y, w)
	}

	// --- the act --------------------------------------------------------------
	if c.onRow != nil {
		c.onRow("Create the theme", y)
	}
	busy := a.themeCreate != nil || a.themeCopy != nil
	label := themeMakeVerb
	if busy {
		label = themeMakeBusy
	}
	if c.Button(sdl.Rect{X: pad, Y: y, W: themeMakeBtnW, H: btnH}, label) && !busy {
		// ONE CALL, and the branch inside it is the template's, not this row's
		// (startThemeCreateFromTemplate): a draw site that chose between "mint" and
		// "copy" itself would be a second place that has to know which templates are
		// which.
		if a.startThemeCreateFromTemplate(settings.newThemeTemplate, settings.newThemeName, settings.presetPair()) {
			settings.newThemeName = "" // spent: the next create starts from the placeholder again
		}
	}
	y += themeMakeRowH
	return y
}

// drawThemePresetAxes is the two-cycler pick for templatePreset.
//
// THE LIBRARY LOAD IS ONE sync.Once OVER EMBEDDED BYTES (theme.ShippedPresets), not
// a disk read — the fragments ship inside the executable — so it is safe on a
// frame. A failure draws a line and no cyclers rather than refusing the tab: an
// empty axis that says why beats a control that does nothing.
func (a *App) drawThemePresetAxes(y, w int32) int32 {
	c := a.ctx
	pad := a.formX // the settings-origin shadow rule
	lib, err := theme.ShippedPresets()
	if err != nil || lib == nil {
		c.LabelClipped(pad, y+4, w-pad-scrollBarW,
			"the built-in preset library could not be read — pick Blank instead", ColDanger)
		return y + themeMakeNoteRowH
	}
	if c.onRow != nil {
		c.onRow("Preset layout and style", y)
	}
	c.Label(pad, y+4, "Preset:", ColText)
	names := &settings.presetAxisNames
	names.refresh(lib)
	x := pad + themeRowLabelW
	if next, changed := c.Dropdown("newthemelayout",
		sdl.Rect{X: x, Y: y, W: themeMakeAxisW, H: btnH}, names.layouts, settings.presetLayout); changed {
		settings.presetLayout = next
	}
	x += themeMakeAxisW + themeMakeGap
	if next, changed := c.Dropdown("newthemestyle",
		sdl.Rect{X: x, Y: y, W: themeMakeAxisW, H: btnH}, names.styles, settings.presetStyle); changed {
		settings.presetStyle = next
	}
	x += themeMakeAxisW + themeMakeGap
	c.LabelClipped(x, y+4, w-x-scrollBarW,
		"layout × style — the same pairs the editor's Gallery panel shows", ColTextDim)
	return y + themeMakeRowH
}

// drawThemeEditorOpenRow is the row that MOVED here from the Theme tab, unchanged in
// behaviour: open what can be opened, copy what cannot.
//
// The two strings and the branch are themecopy.go's (themeEditorRow /
// copyAppliedThemeForEditing) and are untouched by the move — the row is in a
// different place, not a different row.
func (a *App) drawThemeEditorOpenRow(y, w int32) int32 {
	c := a.ctx
	pad := a.formX // the settings-origin shadow rule
	if c.onRow != nil {
		c.onRow("Theme editor", y)
	}
	c.LabelClipped(pad, y+4, w-pad-scrollBarW, themeEditorOpenIntro, ColTextDim)
	y += themeMakeNoteRowH

	if c.onRow != nil {
		c.onRow("Open the theme editor", y)
	}
	c.Label(pad, y+4, "Theme editor:", ColText)
	// THE BUTTON IS NEVER ABSENT (v1.90.0 W8). This row used to draw "copy it for
	// editing first" with NO button beside it, so an AO2 theme — the state every AO2
	// user is in on first run — got advice and no way to act on it.
	edit := a.themeEditorRow()
	c.LabelClipped(pad+themeRowLabelW, y+4, w-pad-themeRowLabelW-themeGetBtnsW-scrollBarW, edit.note, ColTextDim)
	if c.Button(sdl.Rect{X: w - themeGetBtnW - scrollBarW, Y: y, W: themeGetBtnW, H: btnH}, edit.button) {
		if edit.copies {
			a.copyAppliedThemeForEditing(themeCopyThenOpen)
		} else {
			a.openThemeEditor(ScreenSettings)
		}
	}
	return y + themeGetRowH
}

// drawThemeImportRow is the .aotheme intake, moved here from the Theme tab.
//
// IT IS AUTHORING, not selection, and that is why it moved: it is the receiving
// half of the editor's own Share button, the two shipped as one funnel in W8, and
// the Theme tab is about choosing among the themes you already have. "Get themes"
// stayed behind, because that row answers "I have none" — a selection dead end, not
// a creator flow.
//
// IT IS THE SAME FUNNEL as a drag (themeimport.go): the button opens the in-app
// browser bound to purposeThemeBundle, and picking calls handleThemeBundleDrop.
func (a *App) drawThemeImportRow(y, w int32) int32 {
	c := a.ctx
	pad := a.formX // the settings-origin shadow rule
	if c.onRow != nil {
		c.onRow("Import a theme bundle", y)
	}
	c.LabelClipped(pad, y+4, w-pad-scrollBarW, themeImportIntro, ColTextDim)
	y += themeMakeNoteRowH

	if c.onRow != nil {
		c.onRow("Import:", y)
	}
	c.Label(pad, y+4, "Import:", ColText)
	imp := a.themeImportRow()
	c.LabelClipped(pad+themeRowLabelW, y+4, w-pad-themeRowLabelW-themeGetBtnsW-scrollBarW, imp.note, ColTextDim)
	if c.Button(sdl.Rect{X: w - themeGetBtnW - scrollBarW, Y: y, W: themeGetBtnW, H: btnH}, imp.button) {
		a.themeImportRowAction()
	}
	return y + themeGetRowH
}

// ---------------------------------------------------------------------------
// The cross-tab hint left behind on the Theme tab
// ---------------------------------------------------------------------------

// drawThemeAuthoringHint is the pointer the Theme tab keeps where the editor and
// import rows used to be.
//
// A MOVED ROW LEAVES A HOLE where the user's memory is, and the hint machinery is
// the tool for it — the keyword fallback already draws exactly this sentence for a
// search that resolves to another tab, so this is the same line in the same words,
// on the surface rather than in the search results. It registers with c.onRow under
// the label people would search for, so the gather-search finds the SIGNPOST too and
// nobody lands on a tab that no longer answers.
func (a *App) drawThemeAuthoringHint(y, w int32) int32 {
	c := a.ctx
	pad := a.formX // the settings-origin shadow rule
	if c.onRow != nil {
		c.onRow(themeAuthoringHintRow, y)
	}
	c.Label(pad, y+4, "Make or edit:", ColText)
	r := sdl.Rect{X: pad + themeRowLabelW, Y: y, W: w - pad - themeRowLabelW - scrollBarW, H: btnH}
	if c.hovering(r) {
		c.Fill(r, ColPanelHi)
	}
	c.LabelClipped(r.X, y+4, r.W, themeAuthoringHintText, ColAccent)
	if c.clicked && c.hovering(r) {
		settings.tab = tabThemeEditor
		// The index is rebuilt on the next search: the row offsets it stores are
		// per-tab, and a jump that left it stale would scroll a later search to a row
		// that has moved.
		settings.indexBuilt = false
		c.clicked = false
	}
	return y + themeGetRowH
}

// ---------------------------------------------------------------------------
// The preset axes, as the settings tab sees them
// ---------------------------------------------------------------------------

// presetAxisNameCache memoizes the two dropdowns' label slices.
//
// KEYED ON THE LIBRARY POINTER, which is a valid identity precisely because
// theme.ShippedPresets is immutable after load and hands back the same pointer
// every time (presetlib.go's "IMMUTABLE AFTER LOAD" note). Without the memo the tab
// would rebuild two 20-odd-entry string slices on every frame it is open — the
// per-frame-allocation-in-a-loop shape the settings-text doctrine exists to stop.
type presetAxisNameCache struct {
	lib             *theme.PresetLibrary
	layouts, styles []string
}

// refresh rebuilds the labels only when the library it was built from changed.
func (p *presetAxisNameCache) refresh(lib *theme.PresetLibrary) {
	if lib == nil || p.lib == lib {
		return
	}
	p.lib = lib
	p.layouts = presetAxisLabels(lib.Layouts())
	p.styles = presetAxisLabels(lib.Styles())
}

// presetAxisLabels is one axis's display names, in the library's order.
func presetAxisLabels(list []*theme.Preset) []string {
	out := make([]string, 0, len(list))
	for _, p := range list {
		if p == nil {
			continue
		}
		out = append(out, p.Name)
	}
	return out
}

// presetPair is the pair the two dropdowns currently name.
//
// It resolves through theme.ShippedPresets rather than through the cached label
// slices, because a NAME is not an identity — MergePresets needs the *Preset, and
// re-deriving it from a label would be a second lookup table to keep in step with
// the first. An unreadable library yields the empty pair, which the funnel refuses
// with its own sentence (errThemeCreateNoPair).
func (s *settingsState) presetPair() theme.PresetPair {
	lib, err := theme.ShippedPresets()
	if err != nil || lib == nil {
		return theme.PresetPair{}
	}
	return theme.PresetPair{
		Layout: presetAt(lib.Layouts(), s.presetLayout),
		Style:  presetAt(lib.Styles(), s.presetStyle),
	}
}
