package ui

// SAVING THE DOCUMENT (v1.90.0 W7a) — docs/wip/THEME-EDITOR-DESIGN.md §3.3.
//
// ONE concern: getting the edited document onto disk without ever being able to
// break the theme it came from.
//
// SAFE BY CONSTRUCTION, IN THREE INDEPENDENT WAYS, and none of them is discipline:
//
//  1. THE BYTES GO THROUGH THE GUARD. themeDoc.bytes is Sidecar.Bytes, which
//     VALIDATES the model and refuses before it touches the lossless carrier — so a
//     document the reader would not accept never becomes a file. The refusal
//     surfaces as a chip naming the offender, which is the whole of design §3.3's
//     error-copy rule: name the offender, name the limit, offer the fix.
//  2. THE WRITE ROOT IS NOT A PARAMETER. It is the catalog's own answer for this
//     theme (themeCatalogEntry.Dir + Origin), and only themeOriginYours is written.
//     A bundled or read-only theme is REFUSED with the reason, because editing
//     somebody else's folder in place is the highest-blast-radius risk the design
//     names (§6 R3) and W8's copy-for-editing is its answer.
//  3. THE FILE IS REPLACED ATOMICALLY. Temp file in the SAME directory, then
//     os.Rename — the shape config's preference saver and the disk cache's writer
//     both use. A crash mid-write leaves the previous theme, never half of one.
//
// OFF THE RENDER THREAD (hard rule 2). Serialising is cheap and pure and happens on
// the render thread so the guard's refusal can be shown immediately; the WRITE is a
// goroutine. Exactly one can be in flight — the job pointer is the flag — and its
// channel is buffered, so the goroutine can never block on an editor that closed
// while it ran.

import (
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// editSaveTempSuffix is the in-progress name's tail. It sits in the theme's own
// folder because os.Rename is only atomic within a filesystem.
const editSaveTempSuffix = ".asyncao-tmp"

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
		a.te.note(err.Error())
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
	a.te.note("saved to " + path)
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
		// NAME THE OFFENDER, NAME THE LIMIT, OFFER THE FIX (design §3.3).
		return "", errors.New(name + " is a " + themeOriginLabel(entry.Origin) +
			" theme — copy it into your themes folder before editing it")
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
