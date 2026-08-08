package ui

// COPY FOR EDITING (v1.90.0 W8) — docs/wip/THEME-EDITOR-DESIGN.md §Q5.
//
// ONE concern: turning a theme you are not allowed to write into one you are, so
// that the editor always has somewhere to save.
//
// WHY IT IS A WHOLE PATH AND NOT A SENTENCE. Two surfaces used to end in advice:
// Settings ▸ Theme said "this theme has no AsyncAO tier yet — copy it for editing
// first" and drew no button beside it, and the editor's Save refused a bundled or
// read-only theme with "copy it into your themes folder before editing it" — after
// a whole session's work. Both told the user to go and use Explorer. This is the
// act those sentences describe, and themepack.CopyForEditing is the primitive that
// performs it; a copy nothing calls is the parsed-but-never-applied shape hard rule
// 11 forbids, and advice nothing performs is its user-facing twin.
//
// THE COPY IS ALWAYS THE APPLIED THEME. You can only edit what is on screen — the
// editor opens over the applied sidecar (armThemeEditor) — so the copy becomes the
// applied theme as part of landing. That is the same "live, not next launch" rule
// the import lands under, for the same reason.
//
// FIVE THINGS THE LANDING OWNS, and they are one event rather than five:
//
//	1. the layout-editor tweaks come with it (design §Q5 step 5) — a copy that
//	   lost them would look wrong the moment it applied;
//	2. the catalog learns about the folder this client just wrote, so the very
//	   next Save can resolve it without waiting for a rescan;
//	3. the copy becomes the applied theme;
//	4. the OPEN SESSION is retargeted (the Save path), so a session's unsaved work
//	   follows the user into the copy instead of being the price of it;
//	5. the editor opens over it (the Settings path), because "Copy for editing" is
//	   one act and finishing in Explorer is not.
//
// OFF THE RENDER THREAD (hard rule 2) and single-flight (hard rule 4): the job
// pointer IS the flag, the channel is buffered so the goroutine never blocks on an
// app that moved on, and the shape is editExportJob's exactly.

import (
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/theme"
	"github.com/SyntaxNyah/AsyncAO/internal/themepack"
)

// ---------------------------------------------------------------------------
// Named constants (hard rule 9)
// ---------------------------------------------------------------------------

const (
	// themeCopySuffix is what a copy is called before collision suffixing. It is
	// the design's own pre-fill (§Q5 step 2), and themepack.SanitizeName keeps its
	// parentheses legal precisely so this name survives the sanitizer.
	themeCopySuffix = " (edited)"

	// themeCopyConfirmWindow is how long a refused Save stays armed for the second
	// press that authorises the copy. It is the CHIP's own lifetime: the offer must
	// not outlive the sentence that made it, or a Save pressed a minute later would
	// perform an act the user can no longer see was offered.
	themeCopyConfirmWindow = editStatusMs * time.Millisecond
)

// ---------------------------------------------------------------------------
// The job
// ---------------------------------------------------------------------------

// themeCopyIntent is what the copy is FOR. It exists so the landing does one
// thing per caller without a second flag that could disagree with the caller that
// started it — the same reason themeImportStage exists.
type themeCopyIntent uint8

const (
	// themeCopyThenOpen: Settings pressed "Copy for editing". The copy applies and
	// the editor opens over it.
	themeCopyThenOpen themeCopyIntent = iota
	// themeCopyThenSave: the editor's Save was refused and the user confirmed. The
	// open session is retargeted at the copy and written into it.
	themeCopyThenSave
)

// themeCopyJob is one in-flight copy.
type themeCopyJob struct {
	// from is the theme NAME being copied — the receipt, and the key its layout
	// overrides are carried from.
	from string
	// src is the folder the catalog resolved for it.
	src string
	// intent is what to do once it lands.
	intent themeCopyIntent
	done   chan themeCopyResult
}

// themeCopyResult is what the goroutine hands back. A VALUE with no live handles
// in it, exactly as themeBundleResult is.
type themeCopyResult struct {
	manifest *themepack.Manifest
	// provenance is the [import] stamp CopyForEditing wrote, read back ON THE
	// GOROUTINE. The open session's model came from the ORIGINAL theme and has no
	// provenance in it, so a retargeted Save would write its own sidecar straight
	// over the four lines that record where the theme came from — credit that
	// survives re-share, deleted by the first save after the copy that earned it.
	provenance theme.ImportInfo
	err        error
}

// ---------------------------------------------------------------------------
// Is a copy needed at all?
// ---------------------------------------------------------------------------

// themeEditReason is why the applied theme can or cannot be edited where it is.
// ONE enum, read by the Settings row (to choose the button) and by the copy
// action (to refuse a pointless copy), so the offer and the act cannot disagree.
type themeEditReason uint8

const (
	// themeEditInPlace: it has a native tier and it is ours to write.
	themeEditInPlace themeEditReason = iota
	// themeEditNoNativeTier: a plain AO2 theme. There is nothing for the editor to
	// open — copying it is what creates the tier (the copy's provenance stamp
	// writes the first asyncao_theme.ini).
	themeEditNoNativeTier
	// themeEditNotYours: bundled or read-only. The editor could open it, but Save
	// would refuse — so the copy is offered BEFORE the session, not after it.
	themeEditNotYours
)

// themeEditReasonFor is the whole rule, PURE over its three facts so the row's
// wording and the action's refusal are testable without a window or a theme.
//
// AN UNKNOWN THEME (no catalog entry yet) with a native tier is treated as
// editable, which is deliberate: that is the state during the first frames after
// the editor opens, and downgrading it to "copy first" would flip the button under
// the user's cursor when the scan lands. Save resolves the origin for real.
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
		name, _ = a.d.Prefs.Theme()
	}
	entry, known := a.themeCatalogSnapshot().Entry(name)
	return themeEditReasonFor(a.themeSidecar != nil, entry, known)
}

// ---------------------------------------------------------------------------
// Entry points
// ---------------------------------------------------------------------------

// copyThemeForEditing is every decision a copy makes, and the ONE entry point
// both surfaces use.
//
// THE CATALOG IS THE AUTHORITY for both halves of the question — where this theme
// LIVES (entry.Dir), and where this client may WRITE (themeCatalog.WriteRoot, which
// is config.UserThemesDir() as the scan resolved it). That is editorSidecarPath's
// own doctrine, and asking one snapshot for both is what stops a copy reading a
// folder one answer named and writing beside a root a different answer resolved.
//
// Every refusal is a visible line naming the offender and the fix (design §3.3) —
// a button that sometimes does nothing is worse than one that says why it cannot.
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
		// The catalog is the only thing that knows where a theme LIVES. Kick the
		// scan the answer needs rather than refusing twice.
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
	// The name is the design's pre-fill; themepack sanitizes it and suffixes any
	// collision, so pressing the button twice yields "… (edited) (2)" rather than
	// overwriting the first copy.
	want := name + themeCopySuffix
	go func() {
		m, err := themepack.CopyForEditing(job.src, root, want)
		res := themeCopyResult{manifest: m, err: err}
		if err == nil && m != nil {
			// Read back ON THIS GOROUTINE (hard rule 2). LoadSidecar is bounded by
			// theme.IniDocByteCap and a folder with no sidecar yields (nil, nil),
			// which leaves the zero value — "nothing to carry".
			if sc, lerr := theme.LoadSidecar(filepath.Join(m.Dir, theme.SidecarFileName)); lerr == nil && sc != nil {
				res.provenance = sc.Import
			}
		}
		job.done <- res
	}()
	a.noteThemeCopy("copying " + name + "…")
}

// noteThemeCopy posts one line where the user is actually looking: the editor's
// rail chip when the editor is open, the client's shared warn line otherwise.
//
// ONE function rather than a parameter at every call site, because the action has
// two entry points and a refusal delivered to the surface the user is NOT on is a
// silent refusal.
func (a *App) noteThemeCopy(line string) {
	if a.te != nil {
		a.te.note(line)
		return
	}
	a.warnBundle(line)
}

// ---------------------------------------------------------------------------
// Landing
// ---------------------------------------------------------------------------

// pollThemeCopy lands a finished copy. Called once per frame from App.Frame,
// beside pollThemeBundle and for the same reason: a result that arrives between
// frames must not touch UI state from another goroutine.
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

// landThemeCopy is the ONE place a finished copy changes app state, so the polled
// path and any awaited one cannot drift.
func (a *App) landThemeCopy(res themeCopyResult) {
	job := a.themeCopy
	a.themeCopy = nil
	if res.err != nil {
		// themepack's errors already name the cap, the number and the offending
		// entry; the theme's name is prefixed so the line says which copy failed.
		a.noteThemeCopy("could not copy " + job.from + ": " + res.err.Error())
		return
	}
	m := res.manifest
	if m == nil || m.Root == "" || m.Dir == "" {
		a.noteThemeCopy("the copy of " + job.from + " named no folder — open Settings ▸ Theme and pick it")
		return
	}
	// THE RESCAN IS REQUESTED LAST, and that is the reason it is deferred rather
	// than written where it reads naturally: a scan publishes a snapshot built
	// before this copy existed, and everything below reads the SEEDED one — the
	// retarget, and the Save that follows it. Kicking the scan first leaves a window
	// in which a fast walk clobbers the seed and the Save refuses a theme that is
	// sitting on disk.
	defer a.requestThemeCatalog(themeScanAfterDrop)
	// 1. THE LAYOUT TWEAKS COME ALONG (design §Q5 step 5).
	a.carryThemeRectOverrides(job.from, m.Root)
	// 2. THE CATALOG LEARNS ABOUT IT before anything asks. Without this the very
	// next Save resolves through a snapshot that predates the folder we just wrote
	// and refuses a theme that exists.
	a.noteCopiedTheme(job.from, m)
	// 3. THE SESSION IS RETARGETED BEFORE THE APPLY IS KICKED. reclaimThemeDoc
	// re-installs the document only when its name matches the applied theme, so a
	// retarget that landed after the apply would let the apply discard the session.
	if job.intent == themeCopyThenSave && a.te != nil && a.te.doc != nil {
		a.retargetEditorDoc(m.Root, res.provenance)
	}
	// 4. THE COPY BECOMES THE APPLIED THEME. You can only edit what is applied.
	// The CUSTOM root is left exactly as it was — a copy lands under the write root
	// themeLoadRoots already walks, so repointing anything here would be the W2 bug
	// arriving by the front door.
	if a.d.Prefs != nil {
		_, customRoot := a.d.Prefs.Theme()
		a.d.Prefs.SetTheme(m.Root, customRoot)
	}
	settings.themeName = m.Root
	// NO #35 WINDOW SNAP is armed, and that is a decision rather than an omission:
	// a copy has the source's canvas, so the target size cannot have changed, and
	// ResizeWindow also RECENTERS — it would yank a window the user had positioned
	// for a geometry that did not move. Same rule the Refresh button follows.
	gen := a.applyThemeAsync()

	line := "Copied " + job.from + " to \"" + m.Root + "\" — it is yours now, the original is untouched"
	if job.intent != themeCopyThenSave {
		// 5. THE EDITOR OPENS OVER IT once that apply lands: the button says "Copy
		// for editing", and a button that copies and then stops has done half of
		// what it offered.
		a.themeEditArmGen, a.themeEditArmFrom = gen, a.screen
		a.noteThemeCopy(line)
		return
	}
	if a.te == nil || a.te.doc == nil {
		a.noteThemeCopy(line + " — the editor was closed, so nothing was written into it")
		return
	}
	a.noteThemeCopy(line)
	// The save's own chip replaces this line, which is the right order: "saved to
	// …" is the answer to the press that started all of this.
	a.editorSave()
}

// retargetEditorDoc points the open session at the copy.
//
// THE UNSAVED WORK IS THE POINT. The copy is made from the FILES ON DISK, so a
// path that closed the document and reopened it over the copy would charge a
// session's work for the privilege of being allowed to save — which is exactly the
// failure the refusal chip existed to describe.
func (a *App) retargetEditorDoc(name string, prov theme.ImportInfo) {
	a.te.doc.name = name
	if prov.DerivedFrom == "" {
		return // nothing was read back; leave the model's own [import] alone
	}
	// ONLY THE THREE DERIVED KEYS. The rest of [import] (subthemes, per_misc,
	// lobby_design) describes the FILES, which both folders share and the open
	// model already records — assigning the whole struct would overwrite the
	// session's declared gaps with whatever the read happened to return.
	a.te.doc.side.Import.DerivedFrom = prov.DerivedFrom
	a.te.doc.side.Import.DerivedAt = prov.DerivedAt
	a.te.doc.side.Import.DerivedHash = prov.DerivedHash
}

// carryThemeRectOverrides copies the layout editor's per-theme tweaks onto the
// copy's name (design §Q5 step 5).
//
// Bounded by preferences' own caps: SetThemeRectOverride refuses past
// themeOvThemesCap / themeOvRectsCap, so this cannot grow the file without limit
// however many copies a user makes.
func (a *App) carryThemeRectOverrides(from, to string) {
	if a.d.Prefs == nil || from == "" || to == "" {
		return
	}
	for key, r := range a.d.Prefs.ThemeRectOverrides(from) {
		a.d.Prefs.SetThemeRectOverride(to, key, r)
	}
}

// noteCopiedTheme publishes a catalog snapshot that includes the folder this
// client has just written.
//
// WHY THE SCAN IS NOT ENOUGH. requestThemeCatalog is single-flight and floored: a
// scan already in flight makes the request a no-op, so the freshly copied theme
// can be invisible for seconds — and the editor's Save resolves its write root
// THROUGH the catalog, so an invisible copy is a Save that refuses a theme sitting
// on disk. The scan is still requested; this only removes the window.
//
// COPY-ON-WRITE + CAS, never a mutation: a snapshot is immutable and the draw path
// reads it with no lock at all. A losing CAS means a real scan published first,
// and its answer is the better one by definition.
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
		// YOURS BY CONSTRUCTION: the destination was this snapshot's own WriteRoot.
		Origin: themeOriginYours,
		// Shared, not copied: snapshots are immutable, so two entries may name one
		// backing array without either being able to change it.
		Subthemes: src.Subthemes,
	})
	// The same order the scanner publishes, so the picker does not jump when the
	// real scan lands.
	sort.Slice(next.Entries, func(i, j int) bool {
		return strings.ToLower(next.Entries[i].Name) < strings.ToLower(next.Entries[j].Name)
	})
	a.themeCat.CompareAndSwap(prev, &next)
}

// themeKindAfterCopy is the copy's badge, derived from the source's.
//
// A COPY ALWAYS HAS A NATIVE TIER, because stampProvenance writes one even when
// the source had none — that is what makes copying a plain AO2 theme the act that
// creates its AsyncAO tier. So the AO2 half is all that carries over.
func themeKindAfterCopy(src themeKind) themeKind {
	if src == themeKindAO2 || src == themeKindBoth {
		return themeKindBoth
	}
	return themeKindNative
}

// maybeOpenEditorAfterCopy opens the editor over a copy once ITS apply lands.
//
// Called from pollThemeApply beside maybeResizeToThemeDesign, and armed exactly as
// that one is — by a single generation, so it cannot leak onto healTheme's
// eviction re-kick, the texture-filter apply or a boot apply. The guard rails are
// the same too: consume the arming unconditionally once its own apply lands, and
// disarm when a later apply has outraced it (generations only increase, so an
// arming left set could never match again).
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

// ---------------------------------------------------------------------------
// The Settings row
// ---------------------------------------------------------------------------

// The row's four sentences, as constants: this row draws every frame the Theme
// tab is open, and the label cache keys on the string, so a formatted one would
// rasterise a fresh texture sixty times a second (the settings-text doctrine —
// see themeRowText).
const (
	themeEditNoteInPlace = "design this theme's own elements, colours and motion"
	// NAME THE OFFENDER, NAME THE LIMIT, OFFER THE FIX (design §3.3). The old copy
	// named the limit and then told the user to go and do it themselves.
	themeEditNoteNoTier   = "this theme has no AsyncAO tier yet — copying it into your themes folder starts one"
	themeEditNoteNotYours = "this theme is not yours to write — copying it into your themes folder makes it editable"
	themeEditNoteBusy     = "copying… the editor opens over the copy when it is ready"
)

// themeEditorRowText is the row's two strings plus which act the button performs.
type themeEditorRowText struct {
	note   string
	button string
	// copies is true when the button copies rather than opens. The DRAW asks this
	// rather than re-deriving the reason, so the label and the act cannot disagree.
	copies bool
}

// buildThemeEditorRow is the row's wording. PURE over its two inputs.
//
// THE BUTTON IS NEVER ABSENT, which is the defect this closes: the row used to
// draw the "copy it for editing first" sentence with no button beside it, so an
// AO2 theme — the state every AO2 user is in on first run — offered advice and no
// way to act on it.
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

// copyAppliedThemeForEditing is the row's other act: copy what cannot be opened.
//
// It resolves the NAME (the applied theme is the only thing the editor can open,
// so it is the only thing worth copying) and hands off. The draw site keeps the
// branch itself — Settings is the design's headline entry point and
// TestEveryThemeEditorSettingIsFindable pins that the row opens the editor from
// there in so many words.
func (a *App) copyAppliedThemeForEditing(intent themeCopyIntent) {
	name := ""
	if a.d.Prefs != nil {
		name, _ = a.d.Prefs.Theme()
	}
	a.copyThemeForEditing(name, intent)
}

// awaitThemeCopy drains the in-flight copy, with a bound, THROUGH the same
// landing — the twin of editorAwaitExport, and it exists for the same reason: a
// gate asserts on the FILES rather than on a goroutine having been started.
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
