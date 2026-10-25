package ui

// SAVING THE DOCUMENT (v1.90.0 W7a) — docs/wip/THEME-EDITOR-DESIGN.md §3.3.
// OFF THE RENDER THREAD (hard rule 2). Serialising is cheap and pure and happens on
// the render thread so the guard's refusal can be shown immediately; the WRITE is a
// goroutine. Exactly one can be in flight — the job pointer is the flag — and its

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// editSaveTempSuffix is the in-progress name's tail. It sits in the theme's own
// folder because os.Rename is only atomic within a filesystem.
const editSaveTempSuffix = ".asyncao-tmp"

// errThemeNotYours is "this theme is not ours to write". A SENTINEL rather than a
// string, because one caller has to tell it apart from every other reason a save
// has nowhere to go: it is the only one with a one-press fix (copy-for-editing),
// and matching on prose would silently stop offering that fix the day the wording
// improved.
var errThemeNotYours = errors.New("this theme is not yours to write")

// editSaveJob is one in-flight write. The channel is buffered so the goroutine
// finishes and exits whether or not anybody is still listening.
type editSaveJob struct {
	path string
	done chan error
}

// editorSave serialises the document and starts the write.
//
// Every refusal below is a CHIP, never a silent no-op: a Save button that sometimes
// does nothing is worse than one that says why it cannot.
func (a *App) editorSave() {
	if a.te == nil || a.te.doc == nil {
		return
	}
	if a.te.save != nil {
		a.te.note("a save is already running")
		return
	}
	path, err := a.editorSidecarPath()
	if err != nil {
		a.offerCopyOnSaveRefusal(err)
		return
	}
	b, err := a.te.doc.bytes()
	if err != nil {
		// The guard refused. It is the same refusal the READER would make, so the
		// message can name the theme and the limit rather than saying "failed".
		a.te.note(themeSidecarRefusalTip(a.te.doc.name, err))
		return
	}
	job := &editSaveJob{path: path, done: make(chan error, 1)}
	a.te.save = job
	go func() { job.done <- writeFileAtomically(job.path, b) }()
}

// offerCopyOnSaveRefusal turns "you may not write this theme" into something the
// user can press (v1.90.0 W8, design §Q5).
// PRESS-AGAIN RATHER THAN A MODAL: the design allows exactly two modals in the
// whole editor and W7 spends neither, so a third here would be the modal misery
// requirement 10 forbids. It is the same idiom requestEditorExit and the import
func (a *App) offerCopyOnSaveRefusal(err error) {
	if !errors.Is(err, errThemeNotYours) {
		a.te.note(err.Error())
		return
	}
	if a.te.copyAt.IsZero() || time.Since(a.te.copyAt) >= themeCopyConfirmWindow {
		a.te.copyAt = time.Now()
		a.te.note(err.Error() + ". Press Save again to copy it into your themes folder and save there")
		return
	}
	// The offer is spent by the act it authorises, exactly as the import's consent is.
	a.te.copyAt = time.Time{}
	a.copyThemeForEditing(a.te.doc.name, themeCopyThenSave)
}

// pollEditorSave lands a finished write. Called once per editor frame, from the
// draw — the same shape pollThemeApply has, and for the same reason: a result that
// arrives between frames must not touch UI state from another goroutine.
func (a *App) pollEditorSave() {
	if a.te == nil || a.te.save == nil {
		return
	}
	select {
	case err := <-a.te.save.done:
		a.landEditorSave(err)
	default:
	}
}

// landEditorSave is the ONE place a finished write changes editor state, so the
// polled path and the awaited one cannot drift.
func (a *App) landEditorSave(err error) {
	path := a.te.save.path
	a.te.save = nil
	if err != nil {
		a.te.note("could not write " + filepath.Base(path) + ": " + err.Error())
		return
	}
	// CLEAN ONLY NOW. dirty is cleared by the WRITE LANDING, never by starting one:
	// an exit that raced a failed save would otherwise discard the edits it believed
	// were already on disk.
	a.te.doc.dirty = false
	// AND THE BASELINE MOVES WITH IT (v1.90.0 W8). "Discard" means "back to the last
	// save", not "back to when I opened the editor": the file now on disk IS the theme,
	// and a discard that put the model back behind it would leave memory and disk
	// holding two different themes with nothing on screen to say so. The same landing
	// owns both because they are one event — see themeDoc.rebaseline.
	a.te.doc.rebaseline()
	a.te.note("saved to " + path)
	a.applyThemeAfterSaveIfTypeChanged()
}

// applyThemeAfterSaveIfTypeChanged kicks a theme apply when — and ONLY when — the
// saved file's TYPE tables differ from the ones the faces on screen were resolved from.
// AFTER THE WRITE rather than at the edit, and that ordering is also load-bearing: an
// apply re-reads the file, so kicking one on an unsaved binding would resolve the OLD
// file and look like the edit had been thrown away. reclaimThemeDoc re-installs the
func (a *App) applyThemeAfterSaveIfTypeChanged() {
	if a.te == nil || a.te.doc == nil {
		return
	}
	sig := a.te.doc.typeSig()
	if sig == a.te.doc.typeSigApplied {
		return
	}
	a.te.doc.typeSigApplied = sig
	a.applyThemeAsync()
}

// editorSidecarPath resolves where this theme's asyncao_theme.ini belongs, or the
// reason there is nowhere to put it.
//
// THE CATALOG IS THE AUTHORITY, not config.UserThemesDir directly: the catalog is
// what already answers "where did this theme come from and is it ours" for the
// settings list, and asking it here is what makes "the editor never writes outside
// the write root" a property of one function rather than of every caller.
func (a *App) editorSidecarPath() (string, error) {
	name := a.te.doc.name
	if name == "" {
		return "", errors.New("this theme has no folder name — nothing to save into")
	}
	cat := a.themeCatalogSnapshot()
	if cat == nil {
		return "", errors.New("the theme folder has not been scanned yet — try again in a moment")
	}
	entry, ok := cat.Entry(name)
	if !ok {
		return "", errors.New(name + " is not in the theme list — reopen the editor after a refresh")
	}
	if entry.Origin != themeOriginYours {
		// NAME THE OFFENDER, NAME THE LIMIT, OFFER THE FIX (design §3.3) — and the
		// fix is an ACT, not homework: the sentinel is what lets editorSave turn this
		// refusal into the copy that resolves it (offerCopyOnSaveRefusal).
		return "", fmt.Errorf("%s is a %s theme — %w", name, themeOriginLabel(entry.Origin), errThemeNotYours)
	}
	if entry.Dir == "" {
		return "", errors.New("could not work out where " + name + " lives on this machine")
	}
	return filepath.Join(entry.Dir, theme.SidecarFileName), nil
}

// writeFileAtomically replaces path with b: temp file beside it, then rename.
//
// GOROUTINE-ONLY, and it touches nothing but its two arguments — no App, no
// document, no preference — which is what makes the race detector's answer here
// trivial rather than argued.
func writeFileAtomically(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + editSaveTempSuffix
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp) // a failed rename must not leave litter beside the theme
		return err
	}
	return nil
}

// editorAwaitSave drains the current write, with a bound, THROUGH the same landing.
// It exists so a gate can assert on the FILE rather than on a goroutine having been
// started, without sleeping and without reaching into the job's channel itself.
func (a *App) editorAwaitSave(within time.Duration) bool {
	if a.te == nil || a.te.save == nil {
		return true
	}
	select {
	case err := <-a.te.save.done:
		a.landEditorSave(err)
		return err == nil
	case <-time.After(within):
		return false
	}
}
