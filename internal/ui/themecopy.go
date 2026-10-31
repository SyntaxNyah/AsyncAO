package ui

// Copy for editing: turning a theme this client may not write into one it may, so the editor always has somewhere to
// save.
// The copy becomes the applied theme, because the editor opens over the applied sidecar.
// Off the render thread and one at a time: non-nil job means running, and a buffered channel never blocks its
// goroutine.

import (
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/theme"
	"github.com/SyntaxNyah/AsyncAO/internal/themepack"
)

const (
	// themeCopySuffix is a copy's name before collision suffixing; themepack.SanitizeName keeps the parentheses legal.
	themeCopySuffix = " (edited)"

	// themeCopyConfirmWindow is the chip's lifetime, so the offer does not outlive the sentence that made it.
	themeCopyConfirmWindow = editStatusMs * time.Millisecond
)

// themeCopyIntent is what the copy is for.
type themeCopyIntent uint8

const (
	// themeCopyThenOpen: Settings pressed Copy for editing; the copy applies and the editor opens over it.
	themeCopyThenOpen themeCopyIntent = iota
	// themeCopyThenSave: a refused Save was confirmed; the open session is retargeted at the copy and written.
	themeCopyThenSave
)

// themeCopyJob is one in-flight copy.
type themeCopyJob struct {
	// from is the theme name being copied, and the key its layout overrides come from.
	from string
	// src is the folder the catalog resolved for it.
	src string
	// intent is what to do once it lands.
	intent themeCopyIntent
	done   chan themeCopyResult
}

// themeCopyResult is what the goroutine hands back, with no live handles in it.
type themeCopyResult struct {
	manifest *themepack.Manifest
	// provenance is the [import] stamp read back on the goroutine; the open model has none, and its Save would
	// overwrite it.
	provenance theme.ImportInfo
	err        error
}

// themeEditReason is why the applied theme can or cannot be edited in place, shared by the Settings row and copy
// action.
type themeEditReason uint8

const (
	// themeEditInPlace: it has a native tier and it is ours to write.
	themeEditInPlace themeEditReason = iota
	// themeEditNoNativeTier: a plain AO2 theme; the copy's provenance stamp writes the first asyncao_theme.ini.
	themeEditNoNativeTier
	// themeEditNotYours: bundled or read-only; Save would refuse, so the copy is offered before the session.
	themeEditNotYours
)

// themeEditReasonFor is the whole rule, pure over its facts.
// An unknown theme with a native tier is editable: in the first frames after opening, "copy first" would flip the
// button.
func themeEditReasonFor(hasNativeTier bool, entry themeCatalogEntry, known bool) themeEditReason {
	switch {
	case !hasNativeTier:
		return themeEditNoNativeTier
	case known && entry.Origin != themeOriginYours:
		return themeEditNotYours
	default:
		return themeEditInPlace
	}
}

// appliedThemeEditReason answers the same question about what is on screen.
func (a *App) appliedThemeEditReason() themeEditReason {
	name := ""
	if a.d.Prefs != nil {
		name, _ = a.d.Prefs.Load().Theme()
	}
	entry, known := a.themeCatalogSnapshot().Entry(name)
	return themeEditReasonFor(a.themeSidecar != nil, entry, known)
}

// copyThemeForEditing is the one entry point both surfaces use; every refusal is a visible line naming the fix.
// One catalog snapshot answers where the theme lives and where to write, so the two cannot disagree.
func (a *App) copyThemeForEditing(name string, intent themeCopyIntent) {
	if a.themeCopy != nil {
		a.noteThemeCopy("a theme is already being copied — let it finish first")
		return
	}
	if name == "" {
		a.noteThemeCopy("there is no theme applied to copy")
		return
	}
	cat := a.themeCatalogSnapshot()
	if cat == nil {
		// The catalog is the only thing that knows where a theme lives, so kick the scan.
		a.requestThemeCatalog(themeScanExplicit)
		a.noteThemeCopy("the theme folder has not been scanned yet — try again in a moment")
		return
	}
	entry, ok := cat.Entry(name)
	if !ok {
		a.noteThemeCopy(name + " is not in the theme list — press Refresh in Settings ▸ Theme, then try again")
		return
	}
	if entry.Dir == "" {
		a.noteThemeCopy("could not work out where " + name + " lives on this machine")
		return
	}
	if themeEditReasonFor(a.themeSidecar != nil, entry, true) == themeEditInPlace {
		a.noteThemeCopy(name + " is already yours — it can be edited where it is")
		return
	}
	root := cat.WriteRoot
	if root == "" {
		a.noteThemeCopy("this install has nowhere to write themes. Set a theme folder in Settings ▸ Theme.")
		return
	}
	job := &themeCopyJob{from: name, src: entry.Dir, intent: intent, done: make(chan themeCopyResult, 1)}
	a.themeCopy = job
	// themepack sanitizes and suffixes collisions, so a second press yields "… (edited) (2)" instead of overwriting.
	want := name + themeCopySuffix
	go func() {
		m, err := themepack.CopyForEditing(job.src, root, want)
		res := themeCopyResult{manifest: m, err: err}
		if err == nil && m != nil {
			// Read back here (hard rule 2); a folder with no sidecar yields (nil, nil), leaving the zero value.
			if sc, lerr := theme.LoadSidecar(filepath.Join(m.Dir, theme.SidecarFileName)); lerr == nil && sc != nil {
				res.provenance = sc.Import
			}
		}
		job.done <- res
	}()
	a.noteThemeCopy("copying " + name + "…")
}

// noteThemeCopy posts where the user is looking: the rail chip in the editor, else the shared warn line.
func (a *App) noteThemeCopy(line string) {
	if a.te != nil {
		a.te.note(line)
		return
	}
	a.warnBundle(line)
}

// pollThemeCopy lands a finished copy, once per frame from App.Frame, so the result stays on the render thread.
func (a *App) pollThemeCopy() {
	if a.themeCopy == nil {
		return
	}
	select {
	case res := <-a.themeCopy.done:
		a.landThemeCopy(res)
	default:
	}
}

// landThemeCopy is the one place a finished copy changes app state.
func (a *App) landThemeCopy(res themeCopyResult) {
	job := a.themeCopy
	a.themeCopy = nil
	if res.err != nil {
		// themepack's errors name the cap and the entry; the prefix says which copy failed.
		a.noteThemeCopy("could not copy " + job.from + ": " + res.err.Error())
		return
	}
	m := res.manifest
	if m == nil || m.Root == "" || m.Dir == "" {
		a.noteThemeCopy("the copy of " + job.from + " named no folder — open Settings ▸ Theme and pick it")
		return
	}
	// The rescan comes last: a scan publishes a snapshot built before this copy, and a fast walk would clobber the seed
	// below.
	defer a.requestThemeCatalog(themeScanAfterDrop)
	// 1. The layout tweaks come along.
	a.carryThemeRectOverrides(job.from, m.Root)
	// 2. Seed the catalog, or the next Save resolves a snapshot without this folder and refuses a theme that exists.
	a.noteCopiedTheme(job.from, m)
	// 3. Retarget before the apply: reclaimThemeDoc reinstalls only on a name match, so the apply would discard the
	//    session.
	if job.intent == themeCopyThenSave && a.te != nil && a.te.doc != nil {
		a.retargetEditorDoc(m.Root, res.provenance)
	}
	// 4. The copy becomes the applied theme. The custom root stays: the copy lands under the root themeLoadRoots walks.
	if a.d.Prefs != nil {
		_, customRoot := a.d.Prefs.Load().Theme()
		a.d.Prefs.SetTheme(m.Root, customRoot)
	}
	settings.themeName = m.Root
	// No window snap: a copy has the source's canvas, and ResizeWindow also recenters a window the user positioned.
	gen := a.applyThemeAsync()

	line := "Copied " + job.from + " to \"" + m.Root + "\" — it is yours now, the original is untouched"
	if job.intent != themeCopyThenSave {
		// 5. The editor opens over it once that apply lands.
		a.themeEditArmGen, a.themeEditArmFrom = gen, a.screen
		a.noteThemeCopy(line)
		return
	}
	if a.te == nil || a.te.doc == nil {
		a.noteThemeCopy(line + " — the editor was closed, so nothing was written into it")
		return
	}
	a.noteThemeCopy(line)
	// The save's own chip replaces this line, answering the press that started this.
	a.editorSave()
}

// retargetEditorDoc points the open session at the copy, keeping unsaved work: the copy was made from files on disk.
func (a *App) retargetEditorDoc(name string, prov theme.ImportInfo) {
	a.te.doc.name = name
	if prov.DerivedFrom == "" {
		return // nothing was read back; leave the model's own [import] alone
	}
	// Only the three derived keys: the rest of [import] describes shared files, and the model may hold declared gaps.
	a.te.doc.side.Import.DerivedFrom = prov.DerivedFrom
	a.te.doc.side.Import.DerivedAt = prov.DerivedAt
	a.te.doc.side.Import.DerivedHash = prov.DerivedHash
}

// carryThemeRectOverrides copies the layout editor's per-theme tweaks; SetThemeRectOverride's caps bound the file.
func (a *App) carryThemeRectOverrides(from, to string) {
	if a.d.Prefs == nil || from == "" || to == "" {
		return
	}
	for key, r := range a.d.Prefs.Load().ThemeRectOverrides(from) {
		a.d.Prefs.SetThemeRectOverride(to, key, r)
	}
}

// noteCopiedTheme publishes a snapshot including the folder just written: requestThemeCatalog is rate limited and
// single-flight, and Save resolves its root through the catalog.
// Copy-on-write plus CAS; a losing CAS means a real scan won.
func (a *App) noteCopiedTheme(from string, m *themepack.Manifest) {
	prev := a.themeCat.Load()
	if prev == nil {
		return // nothing to extend; the requested scan is the whole answer
	}
	if _, ok := prev.Entry(m.Root); ok {
		return
	}
	if len(prev.Entries) >= themeCatalogCap {
		return // hard rule 4: the snapshot's own bound wins over convenience
	}
	src, _ := prev.Entry(from)
	next := *prev // the four scalar fields describe the SCAN, which has not moved
	next.Entries = make([]themeCatalogEntry, 0, len(prev.Entries)+1)
	next.Entries = append(next.Entries, prev.Entries...)
	next.Entries = append(next.Entries, themeCatalogEntry{
		Name: m.Root,
		Dir:  m.Dir,
		Kind: themeKindAfterCopy(src.Kind),
		// Yours by construction: the destination was this snapshot's WriteRoot.
		Origin: themeOriginYours,
		// Shared: snapshots are immutable, so two entries may name one backing array.
		Subthemes: src.Subthemes,
	})
	// The scanner's order, so the picker does not jump when the real scan lands.
	sort.Slice(next.Entries, func(i, j int) bool {
		return strings.ToLower(next.Entries[i].Name) < strings.ToLower(next.Entries[j].Name)
	})
	a.themeCat.CompareAndSwap(prev, &next)
}

// themeKindAfterCopy is the copy's badge; stampProvenance always writes a native tier, so only the AO2 half carries
// over.
func themeKindAfterCopy(src themeKind) themeKind {
	if src == themeKindAO2 || src == themeKindBoth {
		return themeKindBoth
	}
	return themeKindNative
}

// maybeOpenEditorAfterCopy opens the editor over a copy once its apply lands, armed by one generation like
// maybeResizeToThemeDesign. Arming is consumed on landing and cleared if a later apply outraced it.
func (a *App) maybeOpenEditorAfterCopy(gen uint64) {
	if a.themeEditArmGen == 0 {
		return
	}
	if gen != a.themeEditArmGen {
		if gen > a.themeEditArmGen {
			a.themeEditArmGen = 0
		}
		return
	}
	a.themeEditArmGen = 0
	if a.te != nil {
		return // an editor is already open over something; never replace it silently
	}
	if a.screen != a.themeEditArmFrom {
		return // the user walked away; an editor that opens itself later is a hijack
	}
	if !a.openThemeEditor(a.themeEditArmFrom) {
		a.warnBundle("The copy applied but the editor would not open over it — pick it in Settings ▸ Theme.")
	}
}

// The row's sentences are constants: the label cache keys on the string, and this row draws every frame.
const (
	themeEditNoteInPlace = "design this theme's own elements, colours and motion"
	// Name the limit and offer the fix.
	themeEditNoteNoTier   = "this theme has no AsyncAO tier yet — copying it into your themes folder starts one"
	themeEditNoteNotYours = "this theme is not yours to write — copying it into your themes folder makes it editable"
	themeEditNoteBusy     = "copying… the editor opens over the copy when it is ready"
)

// themeEditorRowText is the row's two strings plus which act the button performs.
type themeEditorRowText struct {
	note   string
	button string
	// copies is true when the button copies rather than opens, so the label and the act cannot disagree.
	copies bool
}

// buildThemeEditorRow is the row's wording, pure over its inputs; the button is never absent.
func buildThemeEditorRow(reason themeEditReason, busy bool) themeEditorRowText {
	if busy {
		return themeEditorRowText{note: themeEditNoteBusy, button: "Copying…", copies: true}
	}
	switch reason {
	case themeEditNoNativeTier:
		return themeEditorRowText{note: themeEditNoteNoTier, button: "Copy for editing", copies: true}
	case themeEditNotYours:
		return themeEditorRowText{note: themeEditNoteNotYours, button: "Copy for editing", copies: true}
	default:
		return themeEditorRowText{note: themeEditNoteInPlace, button: "Open editor"}
	}
}

// themeEditorRow is the row as the Settings screen finds it.
func (a *App) themeEditorRow() themeEditorRowText {
	return buildThemeEditorRow(a.appliedThemeEditReason(), a.themeCopy != nil)
}

// copyAppliedThemeForEditing copies the applied theme, the only thing the editor can open.
func (a *App) copyAppliedThemeForEditing(intent themeCopyIntent) {
	name := ""
	if a.d.Prefs != nil {
		name, _ = a.d.Prefs.Load().Theme()
	}
	a.copyThemeForEditing(name, intent)
}

// awaitThemeCopy drains the in-flight copy with a bound, through the same landing.
func (a *App) awaitThemeCopy(within time.Duration) bool {
	if a.themeCopy == nil {
		return true
	}
	select {
	case res := <-a.themeCopy.done:
		a.landThemeCopy(res)
		return res.err == nil
	case <-time.After(within):
		return false
	}
}
