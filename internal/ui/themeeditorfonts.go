package ui

// The font panel: the theme's type, as a file to drop and a widget to point it at.
// [fonts] and [fontbind] are the tier that renders; an element's own font and size are carried but not drawn,
// because each would rebuild a fontSet and purge the text cache per frame. The inspector says so.
// Every edit is an op, but the file copy is not: undo restores the row and leaves the file, since an unreferenced
// font is inert and an undo must not destroy data outside the document.

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

const (
	// editFontPanelW is two rails, so a class id and family name sit side by side.
	editFontPanelW = editRailW * 2
	// editFontRowH is one family or binding row.
	editFontRowH = editFieldRowH
	// editFontLabelW is the left column.
	editFontLabelW = 108

	// editFontFileMaxBytes bounds a face file at intake (hard rule 4): internFace refuses it at apply anyway,
	// so a huge file is refused by name before anything is read.
	editFontFileMaxBytes = 16 << 20

	// editFontDirName is the theme's face folder, the rung themeSidecarFontPath resolves first.
	editFontDirName = "fonts"
)

// editFontJob is one in-flight face copy;
// the buffered channel never blocks on a closed editor, and the pointer is the flag.
type editFontJob struct {
	family string
	file   string // theme-relative, forward slashes — what the [fonts] row will say
	done   chan error
}

func editorFontPanelRect(w, h int32) sdl.Rect {
	pw := int32(editFontPanelW)
	if pw > w-editRowPad*2 {
		pw = w - editRowPad*2
	}
	// A heading, the class ladder, a separator, the families and a footer.
	ph := editFontRowH*int32(len(theme.FontElements)+theme.FontCap+4) + editRowPad*2
	if ph > h-editBannerH-editRowPad*2 {
		ph = h - editBannerH - editRowPad*2
	}
	return sdl.Rect{X: (w - pw) / 2, Y: editBannerH + editRowPad, W: pw, H: ph}
}

// drawEditorFontPanel draws the panel; not a modal, so the canvas keeps animating behind it.
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
	// The result is dropped: the footer anchors to the panel's bottom, and both list loops stop two rows short of it.
	a.drawEditorFontFamilies(r, y)
	c.popClip(prev, had)
	// The footer is the only thing on screen that says a dropped .ttf is the intake.
	c.LabelClipped(r.X+editRowPad, r.Y+r.H-editFontRowH+3, r.W-editRowPad*2,
		"Drop a .ttf or .otf on this window to add it — it is copied into "+editFontDirName+"\\", ColTextDim)
}

// drawEditorFontBinds lists every AO2 element and its bound family, in theme.FontElements' order, never sorted.
func (a *App) drawEditorFontBinds(r sdl.Rect, y int32) int32 {
	c := a.ctx
	c.Label(r.X+editRowPad, y+3, "Bind a face to an AO2 element", ColAccent)
	y += editFontRowH
	names := a.te.editorFontFamilyNames()
	for i, class := range theme.FontElements {
		if y > r.Y+r.H-editFontRowH*2 {
			break
		}
		row := sdl.Rect{X: r.X + editRowPad, Y: y, W: r.W - editRowPad*2, H: editFontRowH - 2}
		c.LabelClipped(row.X, row.Y+3, editFontLabelW, class, ColText)
		cur, _ := a.te.doc.side.BoundFamily(class)
		ctl := sdl.Rect{X: row.X + editFontLabelW, Y: row.Y, W: row.W - editFontLabelW, H: row.H}
		if next := a.editorFontCycler(ctl, cur, names); next != cur {
			a.editorApply(a.te.mutateFontBind(i, next))
		}
		y += editFontRowH
	}
	return y
}

// drawEditorFontFamilies lists [fonts] with a remove button; there is no add row because the intake is the drop.
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
		// Remove, never rename: every [fontbind] row points at the name, so renaming would silently unbind elements.
		if c.Button(sdl.Rect{X: row.X + row.W - editStepW, Y: row.Y, W: editStepW, H: row.H}, "x") {
			a.editorApply(a.te.mutateFontFamily(i, "", ""))
			return y
		}
		y += editFontRowH
	}
	return y
}

// editorFontCycler is a `< family >` control, "(none)" first, using pickAnchorNone's spelling.
func (a *App) editorFontCycler(r sdl.Rect, cur string, names []string) string {
	c := a.ctx
	// The list always carries "(none)", so nothing to pick from is one entry.
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

// editorFontFamilyNames is the cycler's list: a binding may only name a family, unlike an element's font.
func (te *themeEditor) editorFontFamilyNames() []string {
	te.fontNames = te.fontNames[:0]
	te.fontNames = append(te.fontNames, pickAnchorNone)
	for i := range te.doc.side.Fonts {
		if f := te.doc.side.Fonts[i].Family; f != "" {
			te.fontNames = append(te.fontNames, f)
		}
	}
	return te.fontNames
}

// mutateFontBind and mutateFontFamily build the type-table ops;
// methods only to intern a string, and they write nothing.
func (te *themeEditor) mutateFontBind(classIdx int, family string) themeEditOp {
	if classIdx < 0 || classIdx >= len(theme.FontElements) {
		return themeEditOp{}
	}
	return themeEditOp{
		kind: opFontBind, target: fontTarget(classIdx), field: editFieldFont,
		after: editValueStr(te.doc.intern(family)),
	}
}

func (te *themeEditor) mutateFontFamily(idx int, family, file string) themeEditOp {
	if idx < 0 || idx > len(te.doc.side.Fonts) {
		return themeEditOp{}
	}
	return themeEditOp{
		kind: opFontFamily, target: fontTarget(idx), field: editFieldFont,
		after: editValueStr(te.doc.intern(fontRowValue(family, file))),
	}
}

// editorTakeFontDrop installs a dropped face when the editor is open, since writing keys needs its document and undo.
// ok=false means not ours, and the caller keeps its own message.
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
	// Checked before reading, so an enormous file is refused by name.
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
	// Off the render thread (hard rule 2); the goroutine touches only its two paths.
	go func() { job.done <- copyFileAtomically(path, dest) }()
	a.te.note("copying " + base + "…")
	return true
}

// pollEditorFont lands a finished copy as an undoable [fonts] row, once per editor frame.
func (a *App) pollEditorFont() {
	if a.te == nil || a.te.font == nil {
		return
	}
	// Preview defers the landing: editorApply refuses during the demo, so draining here would lose the face
	// with a false reason. The channel is buffered, so a finished result waits (hard rule 7).
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
		// Through editorApply, so it is one Ctrl+Z.
		if !a.editorApply(a.te.mutateFontFamily(len(a.te.doc.side.Fonts), job.family, job.file)) {
			a.te.note(job.family + " could not be added — the [fonts] table is full")
			return
		}
		a.te.fontsOpen = true // land on the rail that now has something to bind
		a.te.note(job.family + " added — bind it to an element in Fonts")
	default:
	}
}

// editorThemeDir is the folder this document lives in, or why there is none: read-only and bundled themes are refused,
// as a face must not land beside a sidecar we may not write.
func (a *App) editorThemeDir() (string, error) {
	p, err := a.editorSidecarPath()
	if err != nil {
		return "", err
	}
	return filepath.Dir(p), nil
}

// fontFamilyFromFile derives a family name from the stem with separators folded to spaces,
// avoiding a modal at drop time.
func fontFamilyFromFile(base string) string {
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	stem = strings.NewReplacer("_", " ", "-", " ", ".", " ").Replace(stem)
	stem = strings.Join(strings.Fields(stem), " ")
	if n := []rune(stem); len(n) > theme.NameRuneCap {
		stem = string(n[:theme.NameRuneCap])
	}
	return stem
}

// copyFileAtomically writes via a temp file then renames, so a crash leaves no half face; goroutine-only.
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
