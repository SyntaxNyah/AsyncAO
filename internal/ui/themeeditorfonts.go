package ui

// THE FONT RAIL (v1.90.0 W7b) — docs/wip/THEME-EDITOR-DESIGN.md §3.1's panel list.
//
// ONE concern: the theme's TYPE, as a thing you can drop a file onto and point at a
// widget.
//
// WHY IT IS THE TIER THAT SHIPS. There are two font tiers in this format and only one
// of them renders. `[fonts]` + `[fontbind]` bind an AO2 element CLASS to a face file
// the theme carries; applySidecarFonts resolves them on every theme apply and the
// courtroom really does change face (W4, themefontbind_test.go). An ELEMENT's own
// `font`/`size` are carried and not drawn in format 1, and that is a MEASURED ruling
// rather than an omission — paintElementText spells out the arithmetic: every other
// face in this client lives in a fontSet holding exactly one point size, so an element
// asking a panel's set for its own size rebuilds that set, and purges the text cache
// with it, on every frame the panel and the element are both on screen. Reversing it
// needs a per-element face pool with its own cap and its own lifetime, which is a wave
// of its own; W7b therefore ships the picker, the rail and the intake for the tier
// that renders, and the inspector SAYS SO on the tier that does not
// (drawInspectorTail) instead of letting `size = 27` do nothing in silence.
//
// EVERY EDIT IS AN OP. opFontFamily and opFontBind go through editorApply exactly as a
// rect drag does, so Ctrl+Z takes back a binding, "Back, Back" throws the whole
// session's type away with the rest of it (restoreBaseline), and the censuses that
// forbid a second mutation path cover this rail without knowing it exists.
//
// THE FILE COPY IS NOT AN OP, and that asymmetry is deliberate. Undo restores the
// [fonts] ROW; the .ttf stays in the theme's fonts\ folder. A font file nobody
// references is inert — it is not read, not opened and not shipped by anything but an
// export — whereas an undo that deleted a file the user dropped would be an undo that
// destroys data outside the document it owns. Same judgement the media graveyard
// makes: bodies survive, rows are the undoable thing.

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// ---------------------------------------------------------------------------
// Named caps and metrics (hard rule 9)
// ---------------------------------------------------------------------------

const (
	// editFontPanelW is the panel's width: two rails' worth, so it can show a class id
	// and a family name side by side without either clipping to nothing.
	editFontPanelW = editRailW * 2
	// editFontRowH is one family or binding row.
	editFontRowH = editFieldRowH
	// editFontLabelW is the left column (a class id, or "Family").
	editFontLabelW = 108

	// editFontFileMaxBytes bounds a face file the editor will copy into a theme
	// (hard rule 4). internFace already refuses an oversized face at APPLY time; this
	// is the same limit applied at INTAKE, so a 400 MB file dropped on the editor is
	// refused by name before anything is read rather than copied and then ignored.
	// 16 MiB is far past any real TTF/OTF — the largest CJK faces this client ships
	// fallbacks for are single-digit megabytes.
	editFontFileMaxBytes = 16 << 20

	// editFontDirName is the folder inside a theme that carries its faces. It is the
	// rung themeSidecarFontPath resolves first, so a file copied here is found by the
	// resolver without the author writing a path.
	editFontDirName = "fonts"
)

// editFontJob is one in-flight face copy. Same shape as editSaveJob and for the same
// reasons: the channel is buffered so the goroutine never blocks on an editor that
// closed, and exactly one runs at a time because the pointer IS the flag.
type editFontJob struct {
	family string
	file   string // theme-relative, forward slashes — what the [fonts] row will say
	done   chan error
}

// ---------------------------------------------------------------------------
// The panel
// ---------------------------------------------------------------------------

// editorFontPanelRect is the panel's rect, centred over the canvas.
func editorFontPanelRect(w, h int32) sdl.Rect {
	pw := int32(editFontPanelW)
	if pw > w-editRowPad*2 {
		pw = w - editRowPad*2
	}
	// Height: a heading, the class ladder, a separator, the families, and a footer.
	ph := editFontRowH*int32(len(theme.FontElements)+theme.FontCap+4) + editRowPad*2
	if ph > h-editBannerH-editRowPad*2 {
		ph = h - editBannerH - editRowPad*2
	}
	return sdl.Rect{X: (w - pw) / 2, Y: editBannerH + editRowPad, W: pw, H: ph}
}

// drawEditorFontPanel is the rail. A PANEL, not a modal (design §3.1 allows exactly
// two modals in the editor and spends both elsewhere), so the canvas behind it keeps
// drawing and the theme keeps animating while you pick a face.
func (a *App) drawEditorFontPanel(w, h int32) {
	if a.te == nil || !a.te.fontsOpen {
		return
	}
	c := a.ctx
	r := editorFontPanelRect(w, h)
	c.Fill(r, ColBackground)
	c.Border(r, ColAccent)
	c.Label(r.X+editRowPad, r.Y+4, "Fonts — the theme's own faces", ColText)
	if c.Button(sdl.Rect{X: r.X + r.W - 60 - editRowPad, Y: r.Y + 2, W: 60, H: editFontRowH - 2}, "Close") {
		a.te.fontsOpen = false
		return
	}
	prev, had := c.pushClip(r)
	y := r.Y + editFontRowH + 2
	y = a.drawEditorFontBinds(r, y)
	y += editRowPad
	c.Fill(sdl.Rect{X: r.X + editRowPad, Y: y, W: r.W - editRowPad*2, H: 1}, ColPanelHi)
	y += editRowPad
	// The families list is the LAST thing on the running cursor, and its answer is
	// deliberately dropped: the footer below is anchored to the panel's own bottom
	// edge, not to where the list ended, and both list loops stop two rows short of it
	// (`y > r.Y+r.H-editFontRowH*2`) precisely so the two can never meet. Keeping the
	// assignment would read as a cursor somebody forgot to use.
	a.drawEditorFontFamilies(r, y)
	c.popClip(prev, had)
	// The footer says where a face comes FROM, because "drop a .ttf here" is the whole
	// intake and nothing else on screen says it.
	c.LabelClipped(r.X+editRowPad, r.Y+r.H-editFontRowH+3, r.W-editRowPad*2,
		"Drop a .ttf or .otf on this window to add it — it is copied into "+editFontDirName+"\\", ColTextDim)
}

// drawEditorFontBinds is the CLASS LADDER: every AO2 element this client dresses, and
// the family (if any) the theme binds it to.
//
// theme.FontElements' own order, never sorted: it is append-only and meaningful
// (TestFontElementsIsAppendOnly), and it is the order the classes appear in AO2's
// courtroom_fonts.ini.
func (a *App) drawEditorFontBinds(r sdl.Rect, y int32) int32 {
	c := a.ctx
	c.Label(r.X+editRowPad, y+3, "Bind a face to an AO2 element", ColAccent)
	y += editFontRowH
	names := a.editorFontFamilyNames()
	for i, class := range theme.FontElements {
		if y > r.Y+r.H-editFontRowH*2 {
			break
		}
		row := sdl.Rect{X: r.X + editRowPad, Y: y, W: r.W - editRowPad*2, H: editFontRowH - 2}
		c.LabelClipped(row.X, row.Y+3, editFontLabelW, class, ColText)
		cur, _ := a.te.doc.side.BoundFamily(class)
		ctl := sdl.Rect{X: row.X + editFontLabelW, Y: row.Y, W: row.W - editFontLabelW, H: row.H}
		if next := a.editorFontCycler(ctl, cur, names); next != cur {
			a.editorApply(a.mutateFontBind(i, next))
		}
		y += editFontRowH
	}
	return y
}

// drawEditorFontFamilies lists [fonts]: a family name, the file it resolves to, and a
// remove button. There is no ADD row, because the intake is the DROP — deriving the
// family from the file name is what lets a face be installed without a modal, and the
// design allows two modals in the whole editor and spends both elsewhere.
func (a *App) drawEditorFontFamilies(r sdl.Rect, y int32) int32 {
	c := a.ctx
	c.Label(r.X+editRowPad, y+3, "Faces this theme carries ("+
		strconv.Itoa(len(a.te.doc.side.Fonts))+" / "+strconv.Itoa(theme.FontCap)+")", ColAccent)
	y += editFontRowH
	for i := range a.te.doc.side.Fonts {
		if y > r.Y+r.H-editFontRowH*2 {
			break
		}
		f := a.te.doc.side.Fonts[i]
		row := sdl.Rect{X: r.X + editRowPad, Y: y, W: r.W - editRowPad*2, H: editFontRowH - 2}
		c.LabelClipped(row.X, row.Y+3, editFontLabelW, f.Family, ColText)
		c.LabelClipped(row.X+editFontLabelW, row.Y+3, row.W-editFontLabelW-editStepW*2, f.File, ColTextDim)
		// REMOVE, never RENAME, from this rail: a family's name is what every
		// [fontbind] row points at, so renaming it in place would silently unbind every
		// element that used it. Removing is the honest destructive action, it is one
		// undo step, and re-adding under a new name is two.
		if c.Button(sdl.Rect{X: row.X + row.W - editStepW, Y: row.Y, W: editStepW, H: row.H}, "x") {
			a.editorApply(a.mutateFontFamily(i, "", ""))
			return y
		}
		y += editFontRowH
	}
	return y
}

// editorFontCycler is a `< family >` control over the families this theme declares,
// with "(none)" first. Returns the (possibly changed) family.
//
// It shares pickAnchorNone's spelling of "nothing" with the inspector's own pickers so
// the two surfaces do not invent two words for one state.
func (a *App) editorFontCycler(r sdl.Rect, cur string, names []string) string {
	c := a.ctx
	// The list ALWAYS carries "(none)", so "nothing to pick from" is one entry, not
	// zero — a bare len==0 test here would be a branch that can never run and a hint
	// the user never sees.
	if len(names) <= 1 {
		c.LabelClipped(r.X+2, r.Y+3, r.W-4, "no faces yet — drop a .ttf on this window", ColTextDim)
		return cur
	}
	i := pickIndexOf(names, cur)
	step := 0
	if c.Button(sdl.Rect{X: r.X, Y: r.Y, W: editStepW, H: r.H}, "<") {
		step = -1
	}
	if c.Button(sdl.Rect{X: r.X + r.W - editStepW, Y: r.Y, W: editStepW, H: r.H}, ">") {
		step = 1
	}
	label := cur
	if label == "" {
		label = pickAnchorNone
	}
	c.LabelClipped(r.X+editStepW+2, r.Y+3, r.W-editStepW*2-4, label, ColText)
	if step == 0 {
		return cur
	}
	if i < 0 {
		i = 0 // a binding pointing at a family [fonts] no longer declares: start at "(none)"
	} else {
		i = ((i+step)%len(names) + len(names)) % len(names)
	}
	return pickValueAt(names, i)
}

// editorFontFamilyNames is the cycler's list: "(none)" plus every declared family. It
// reuses the inspector's own cached pickFace list minus its class-id half, because the
// two lists answer different questions — a BINDING may only name a family, while an
// element's `font` may also name a class to inherit from.
func (a *App) editorFontFamilyNames() []string {
	a.te.fontNames = a.te.fontNames[:0]
	a.te.fontNames = append(a.te.fontNames, pickAnchorNone)
	for i := range a.te.doc.side.Fonts {
		if f := a.te.doc.side.Fonts[i].Family; f != "" {
			a.te.fontNames = append(a.te.fontNames, f)
		}
	}
	return a.te.fontNames
}

// ---------------------------------------------------------------------------
// The mutators — top-level, returning ops, exactly like the inspector's
// ---------------------------------------------------------------------------

// mutateFontBind / mutateFontFamily build the two type-table ops. They are METHODS
// only because they need the document to intern a string; they still write nothing,
// which is the property the funnel's censuses are about.
func (a *App) mutateFontBind(classIdx int, family string) themeEditOp {
	if classIdx < 0 || classIdx >= len(theme.FontElements) {
		return themeEditOp{}
	}
	return themeEditOp{
		kind: opFontBind, target: fontTarget(classIdx), field: editFieldFont,
		after: editValueStr(a.te.doc.intern(family)),
	}
}

func (a *App) mutateFontFamily(idx int, family, file string) themeEditOp {
	if idx < 0 || idx > len(a.te.doc.side.Fonts) {
		return themeEditOp{}
	}
	return themeEditOp{
		kind: opFontFamily, target: fontTarget(idx), field: editFieldFont,
		after: editValueStr(a.te.doc.intern(fontRowValue(family, file))),
	}
}

// ---------------------------------------------------------------------------
// Intake — a dropped .ttf becomes a [fonts] row
// ---------------------------------------------------------------------------

// editorTakeFontDrop is W4's deferred half, landed.
//
// W4's handleThemeFontDrop wrote down exactly why it could not install a face: "landing
// a font means writing two keys into somebody's asyncao_theme.ini, and the sidecar
// WRITER is wired to the editor's document and its undo stack (W7)." With the editor
// open both of those exist, so the drop installs; with it shut the W4 message still
// stands and still tells the user what to do by hand.
//
// ok=false means "not ours" — the caller keeps its own message.
func (a *App) editorTakeFontDrop(path string) bool {
	if a.te == nil || a.te.doc == nil {
		return false
	}
	if a.te.font != nil {
		a.te.note("one face at a time — the last one is still being copied")
		return true
	}
	dir, err := a.editorThemeDir()
	if err != nil {
		a.te.note(err.Error())
		return true
	}
	base := filepath.Base(path)
	family := fontFamilyFromFile(base)
	if family == "" {
		a.te.note(base + " has no usable family name — rename it and drop it again")
		return true
	}
	if len(a.te.doc.side.Fonts) >= theme.FontCap {
		a.te.note("this theme already carries " + strconv.Itoa(theme.FontCap) +
			" faces, which is the limit — remove one before adding another")
		return true
	}
	// The SIZE is checked before anything is read, so an enormous file is refused by
	// name rather than copied and then ignored at apply time (design §3.3's shape:
	// name the offender, name the limit, offer the fix).
	info, err := os.Stat(path)
	if err != nil {
		a.te.note("could not read " + base + ": " + err.Error())
		return true
	}
	if info.Size() > editFontFileMaxBytes {
		a.te.note(base + " is " + strconv.FormatInt(info.Size()>>20, 10) +
			" MB — the per-face limit is " + strconv.Itoa(editFontFileMaxBytes>>20) + " MB")
		return true
	}
	job := &editFontJob{
		family: family,
		file:   editFontDirName + "/" + base,
		done:   make(chan error, 1),
	}
	dest := filepath.Join(dir, editFontDirName, base)
	a.te.font = job
	// OFF THE RENDER THREAD (hard rule 2), the same shape editorSave uses: the
	// goroutine touches nothing but its two paths.
	go func() { job.done <- copyFileAtomically(path, dest) }()
	a.te.note("copying " + base + "…")
	return true
}

// pollEditorFont lands a finished face copy and turns it into an undoable [fonts] row.
// Called once per editor frame beside pollEditorSave, and for the same reason: a
// result that arrives between frames must not touch UI state from another goroutine.
func (a *App) pollEditorFont() {
	if a.te == nil || a.te.font == nil {
		return
	}
	// PREVIEW DEFERS THE LANDING, it does not consume it (field batch 9). The row
	// lands through editorApply, which refuses while the demo is up — so draining the
	// channel here would throw away a face the user dropped seconds earlier, silently
	// (the chip is not drawn over the demo) and with a false reason ("the [fonts]
	// table is full", which it is not). Leaving the job in place costs nothing: the
	// channel is buffered, the goroutine has already handed its result over, and the
	// first editing frame after the demo lands it exactly as if Preview had never been
	// pressed. Hard rule 7's shape — a finished result is deferred, never dropped.
	if a.editorPreviewOn() {
		return
	}
	select {
	case err := <-a.te.font.done:
		job := a.te.font
		a.te.font = nil
		if err != nil {
			a.te.note("could not copy " + filepath.Base(job.file) + ": " + err.Error())
			return
		}
		// THE ROW LANDS THROUGH THE FUNNEL, so it is one Ctrl+Z like everything else.
		if !a.editorApply(a.mutateFontFamily(len(a.te.doc.side.Fonts), job.family, job.file)) {
			a.te.note(job.family + " could not be added — the [fonts] table is full")
			return
		}
		a.te.fontsOpen = true // land on the rail that now has something to bind
		a.te.note(job.family + " added — bind it to an element in Fonts")
	default:
	}
}

// editorThemeDir is the folder this document lives in, or the reason there is none.
// It is editorSidecarPath's directory, deliberately: the ONE authority on "where this
// theme lives and is it ours to write" already refuses a bundled or read-only theme
// with a reason, and a face copied beside a sidecar we may not write would be a file
// dropped into somebody else's folder.
func (a *App) editorThemeDir() (string, error) {
	p, err := a.editorSidecarPath()
	if err != nil {
		return "", err
	}
	return filepath.Dir(p), nil
}

// fontFamilyFromFile derives a family name from a file name: the stem, with separators
// folded to spaces and the common weight suffix left alone.
//
// A DERIVED name rather than a typed one, because the alternative at drop time is a
// modal asking for a string, and the design allows two modals in the whole editor and
// spends both elsewhere. The name is editable afterwards only by removing and
// re-adding (see drawEditorFontFamilies' `x`), which is the honest cost of never
// silently unbinding an element.
func fontFamilyFromFile(base string) string {
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	stem = strings.NewReplacer("_", " ", "-", " ", ".", " ").Replace(stem)
	stem = strings.Join(strings.Fields(stem), " ")
	if n := []rune(stem); len(n) > theme.NameRuneCap {
		stem = string(n[:theme.NameRuneCap])
	}
	return stem
}

// copyFileAtomically writes src's bytes to dest via a temp file beside it, then
// renames — the same shape writeFileAtomically uses, so a crash mid-copy leaves the
// theme without the face rather than with half of one.
//
// GOROUTINE-ONLY, and it touches nothing but its two arguments.
func copyFileAtomically(src, dest string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if int64(len(b)) > editFontFileMaxBytes {
		return os.ErrInvalid // the file grew between the stat and the read
	}
	return writeFileAtomically(dest, b)
}
