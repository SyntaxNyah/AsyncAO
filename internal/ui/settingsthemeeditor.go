package ui

// Settings > Theme editor: the authoring tab. One concern: the rows that make, open and receive a theme. Selection
// (catalog, badge, subtheme variant, write root, art budget) stays on the Theme tab.
//
// Row order: make one, open the one you have, take one somebody sent you. Creation comes first because it has no
// prerequisite; import last because it needs a file you already have.
//
// Every draw helper shadows `pad := a.formX` (the settings-origin rule: a missing shadow renders the row under the
// sidebar) and registers its rows with c.onRow so the gather-search finds them by label.

import (
	"github.com/SyntaxNyah/AsyncAO/internal/helper/text"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

const (
	// themeMakeNameFieldW is the "call it" field, wide enough for a name at themepack.NameRuneCap.
	themeMakeNameFieldW = 260
	// themeMakeTemplateW is the template cycler beside it.
	themeMakeTemplateW = 190
	// themeMakeAxisW is one preset-axis cycler; two of them fit the card at settMinCardW.
	themeMakeAxisW = 190
	// themeMakeGap separates the controls on a multi-control row.
	themeMakeGap = 10
	// themeMakeBtnW is the Create button, wider than the catalog buttons because its label carries a verb.
	themeMakeBtnW = 150
	// themeMakeRowH / themeMakeNoteRowH are a control row and the hint under it, matching the catalog rows.
	themeMakeRowH     = 30
	themeMakeNoteRowH = 22
)

// The tab's fixed strings are constants because they draw every frame and the label cache keys on the string; a
// formatted one would rasterise a fresh texture every frame (see themeRowText).
const (
	themeMakeIntro = "a theme of your own: elements, colours, motion and images, edited live over the real courtroom. It lands in your themes folder and nothing else is touched."
	themeMakeHint  = "the name becomes the folder; an existing name gets a \"(2)\" rather than overwriting anything"
	themeMakeBusy  = "Creating…"
	themeMakeVerb  = "Create & edit"

	themeEditorOpenIntro = "the theme on screen is what the editor opens over — pick a different one in Settings ▸ Theme first if that is not the one you want"

	themeImportIntro = "someone sent you an .aotheme? Drag it onto the window, or browse for one you have already downloaded. Nothing is installed until you confirm what is in it."

	// themeAuthoringHintText is the pointer left on the Theme tab where the two rows used to be, worded as the hint
	// machinery words its own line.
	themeAuthoringHintText = "→ the Theme editor tab covers this — open it"
	themeAuthoringHintRow  = "Make or edit a theme"
)

// drawSettingsThemeEditor draws the authoring tab. The width is the card's, not the window's, like every other tab
// body: drawSettingsTabBody hands each body the window width while the card is clipped to {formX, formW}, so the
// right-aligned buttons at `w - themeGetBtnW - scrollBarW` would land outside the clip, drawn and hit-tested past it
// (Ctx.hovering reads false outside c.clipRect).
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

// drawThemeMakeRows is the creator: a name, a starting point, and the button that makes the folder and opens the
// editor.
//
// The templates all already exist (themecreate.go): a blank sidecar, the preset compose funnel, or copy-for-editing;
// this row picks one and performs none of them.
//
// A cycler, not the gallery's two-axis grid: a settings row is one line tall, and the axes are the same two lists in
// the same order, so a pair picked here matches the gallery (openThemeGallery seeds from the theme's declared preset
// ids).
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
	// Clamped at the sanitizer's bound here, not at create: themepack truncates past themepack.NameRuneCap, so a longer
	// field would show a name the folder never gets.
	settings.newThemeName = text.Truncate(name, themeCreateNameRuneCap)
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
		// One call: the branch is the template's, not this row's (startThemeCreateFromTemplate).
		if a.startThemeCreateFromTemplate(settings.newThemeTemplate, settings.newThemeName, settings.presetPair()) {
			settings.newThemeName = "" // spent: the next create starts from the placeholder again
		}
	}
	y += themeMakeRowH
	return y
}

// drawThemePresetAxes is the two-cycler pick for templatePreset. theme.ShippedPresets is one sync.Once over embedded
// bytes, not a disk read, so it is safe on a frame. A failure draws a line and no cyclers.
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

// drawThemeEditorOpenRow is the row moved from the Theme tab: open what can be opened, copy what cannot. The strings
// and the branch are themecopy.go's (themeEditorRow / copyAppliedThemeForEditing).
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
	// The button is never absent: an AO2 theme (every first-run user) must get a way to copy it for editing, not just
	// advice.
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

// drawThemeImportRow is the .aotheme intake. It is authoring, not selection: the receiving half of the editor's Share
// button. "Get themes" stayed on the Theme tab because it answers "I have none".
//
// Same funnel as a drag (themeimport.go): the button opens the in-app browser bound to purposeThemeBundle, and picking
// calls handleThemeBundleDrop.
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

// drawThemeAuthoringHint is the pointer the Theme tab keeps where the editor and import rows used to be: the same
// sentence the keyword fallback draws for a search resolving to another tab. It registers with c.onRow under the label
// people would search for, so the search finds the signpost too.
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
		// The index is rebuilt on the next search: stored row offsets are per-tab and a stale index would scroll to a
		// moved row.
		settings.indexBuilt = false
		c.clicked = false
	}
	return y + themeGetRowH
}

// presetAxisNameCache memoizes the two dropdowns' label slices, keyed on the library pointer: theme.ShippedPresets is
// immutable after load and returns the same pointer every time (presetlib.go). Without it the tab would rebuild two
// string slices every frame.
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

// presetPair is the pair the two dropdowns currently name. It resolves through theme.ShippedPresets, not the cached
// label slices, because a name is not an identity: MergePresets needs the *Preset. An unreadable library yields the
// empty pair, which the funnel refuses with errThemeCreateNoPair.
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
