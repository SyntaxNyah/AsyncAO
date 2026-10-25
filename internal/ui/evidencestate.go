package ui

import (
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
)

// evidenceState owns evid state.
type evidenceState struct {
	showEvid    bool // evidence panel
	evidIdx     int  // selected evidence (-1 = none)
	evidPresent bool // armed: next IC message carries the selection
	evidEditing bool // editor open (add when evidIdx == -1)
	evidName    string
	evidDesc    string
	evidImage   string
	evidScroll  int32
	// evidDescScroll (Issue #15) scrolls the inspector's description text
	// instead of forcing the whole panel to be resized to read it.
	evidDescScroll    int32
	evidDescScrollIdx int
	evidAsk           []time.Time // thumbnail demand pacing, parallel to Evidence
	// evShow is the incoming presented-evidence pop-up.
	evShowImg string
	evShowAt  time.Time
	// evidPresentImage is the #59 present-image override: when armed and
	// non-empty, presenting shows THIS image instead of the selected evidence's
	// own image (decouples the presented picture from the evidence id).
	evidPresentImage string
	// evidCtxMenu opens a small right-click context menu over the evidence list
	// (Edit / Delete / Present / Pin), positioned at evidCtxX/evidCtxY.
	evidCtxMenu bool
	evidCtxX    int32
	evidCtxY    int32
	// evidInspectorDetached pops the inspector (the right column, which now
	// hosts editing as a view/edit toggle) out into its own floating window;
	// evidInspectorWin is that window's geometry. One at a time for now — the
	// inspector edits a single selection, so "multiple edited evidences" would
	// mean multiple of these windows later.
	evidInspectorDetached bool
	evidInspectorWin      floatWin
	// evidGridView toggles the evidence browser between the DRO-style icon
	// list and the AO2 thumbnail grid. In-memory (not persisted): the earlier
	// evidenceMode preference was dropped and this is a quick layout flip, not
	// a stored setting.
	evidGridView bool
	// evidIconSize scales the evidence list icons and grid cells (32–96 px;
	// default 40). In-memory like evidGridView — the icon-size slider in the
	// evidence window drives it and both layouts rescale together.
	evidIconSize int32
	// evidStream toggles the evidence panel's image source: OFF (default) reads
	// evidence images from the user's local mounts (local://); ON streams them
	// from the server. Session-scoped. evidLocalOrigin caches the local:// origin
	// (rebuilt on mount change); "" when there are no local mounts, so the toggle
	// falls back to streaming.
	evidStream      bool
	evidLocalOrigin string
	// Server evidence image picker (#119 stream mode): the evidence editor's
	// "Browse…" button, when streaming is on, opens this modal grid of the server's
	// evidence/ autoindex instead of the local folder browser.
	evidServerPickerOpen bool
	evidServerNames      []string
	evidServerLower      []string
	evidServerRes        chan []string
	evidServerBusy       bool
	evidServerSearch     string
	evidServerScroll     int32
	// evidDiscardConfirm is the "Stop editing?" modal: swapping the selection
	// (or Add new) while the editor is open first asks. evidDiscardTarget is
	// the index the swap would land on (-1 = Add new).
	evidDiscardConfirm bool
	evidDiscardTarget  int
	// evidConflict is the incoming-edit compare view: an EE for the item being
	// edited arrived while the editor was open. evidIncoming snapshots the
	// server's new {name,desc,image} so the user can Keep mine / Accept theirs
	// instead of silently losing the unsaved work.
	evidConflict bool
	evidIncoming courtroom.EvidenceItem
}
