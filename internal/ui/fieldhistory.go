package ui

// Per-field undo/redo for the kit's text fields (playtest: "it generally
// needs to behave more like a native text field… it doesn't have ctrl+z").
// Every field gets a bounded history keyed by its id. Two things feed it:
//
//   1. the field's own edits (typing, backspace, paste, selection replace),
//      coalesced so one Ctrl+Z undoes a burst, not a keystroke;
//   2. OUT-OF-BAND rewrites — the own-echo IC clear, a chat command clearing
//      the box, a palette command template, a macro eating the draft — which
//      textField detects as "the value changed since my last draw". That
//      detection runs even while the field is unfocused (the next draw
//      records whatever changed in between), which is what makes the eaten
//      IC line recoverable: press Ctrl+Z and the send comes back.
//
// This replaces the old one-slot stashICUndo/stashOOCUndo swap: same
// recovery, native semantics (a real history + redo), every field covered.
//
// Bounds (hard rule 4): fieldHistFieldsCap histories × fieldUndoDepth
// snapshots each, LRU-evicted; histories are wiped on tab switch / fresh
// session so a draft can never resurface across sessions (the multi-tab
// isolation rule).

import (
	"time"
	"unicode/utf8"
)

const (
	// fieldUndoDepth caps one field's undo (and redo) stack.
	fieldUndoDepth = 64
	// fieldHistFieldsCap caps how many distinct fields keep a history at once
	// (LRU-evicted). A session touches far fewer editable fields than this.
	fieldHistFieldsCap = 16
	// fieldUndoCoalesce groups consecutive ±1-rune edits (a typing or
	// backspacing burst) into ONE undo step, native-editor style. Bigger
	// jumps — a paste, a selection replace, the echo clear — always get
	// their own step, so a burst can never swallow a recoverable line.
	fieldUndoCoalesce = 600 * time.Millisecond
)

// fieldSnap is one undo/redo snapshot: the value and where the caret was.
type fieldSnap struct {
	value string
	caret int
}

// fieldHistory is one field's undo state. lastKnown is the value as of the
// field's previous draw — the out-of-band change detector.
type fieldHistory struct {
	lastKnown  string
	lastCaret  int
	undo, redo []fieldSnap
	lastPush   time.Time
}

// record pushes the pre-change state onto the undo stack (and forks redo),
// coalescing typing bursts: a ±1-rune change within fieldUndoCoalesce of the
// previous push extends the current step instead of adding one.
func (h *fieldHistory) record(prevVal string, prevCaret int, nextVal string, now time.Time) {
	if prevVal == nextVal {
		return
	}
	h.redo = h.redo[:0] // a fresh edit forks history — redo dies
	d := utf8.RuneCountInString(nextVal) - utf8.RuneCountInString(prevVal)
	if d >= -1 && d <= 1 && len(h.undo) > 0 && now.Sub(h.lastPush) < fieldUndoCoalesce {
		h.lastPush = now // burst continues — the existing step already holds its start
		return
	}
	h.undo = append(h.undo, fieldSnap{value: prevVal, caret: prevCaret})
	if len(h.undo) > fieldUndoDepth {
		copy(h.undo, h.undo[1:])
		h.undo = h.undo[:fieldUndoDepth]
	}
	h.lastPush = now
}

// step applies one undo (redo=false) or redo (redo=true) against the field's
// CURRENT state, returning the snapshot to restore. ok=false when that stack
// is empty (the chord is still consumed — it was aimed at this field).
func (h *fieldHistory) step(cur string, curCaret int, redo bool) (fieldSnap, bool) {
	from, to := &h.undo, &h.redo
	if redo {
		from, to = &h.redo, &h.undo
	}
	n := len(*from)
	if n == 0 {
		return fieldSnap{}, false
	}
	snap := (*from)[n-1]
	*from = (*from)[:n-1]
	*to = append(*to, fieldSnap{value: cur, caret: curCaret})
	if len(*to) > fieldUndoDepth {
		copy(*to, (*to)[1:])
		*to = (*to)[:fieldUndoDepth]
	}
	// A restore is a deliberate step, never part of a typing burst: stop the
	// next edit from coalescing into a pre-undo push.
	h.lastPush = time.Time{}
	return snap, true
}

// fieldTrack is textField's per-draw entry point: it fetches (creating if
// needed, LRU-evicting past the cap) the field's history and records any
// OUT-OF-BAND change — value != the value this field drew last. A field's
// FIRST sighting only seeds the detector: a restored draft is a starting
// state, not a change to undo out of. Cost on the steady no-change path:
// one map hit + one string compare.
func (c *Ctx) fieldTrack(id, value string) *fieldHistory {
	if c.fieldHists == nil {
		c.fieldHists = make(map[string]*fieldHistory, fieldHistFieldsCap)
	}
	if h, ok := c.fieldHists[id]; ok {
		if h.lastKnown != value {
			h.record(h.lastKnown, h.lastCaret, value, time.Now())
		}
		return h
	}
	if len(c.fieldHists) >= fieldHistFieldsCap && len(c.fieldHistUse) > 0 {
		// Evict the least-recently-CREATED history (the use list is append-
		// order). Fields cycle far below the cap in practice; a history that
		// does get evicted simply starts fresh on its next draw.
		oldest := c.fieldHistUse[0]
		c.fieldHistUse = c.fieldHistUse[1:]
		delete(c.fieldHists, oldest)
	}
	h := &fieldHistory{lastKnown: value}
	c.fieldHists[id] = h
	c.fieldHistUse = append(c.fieldHistUse, id)
	return h
}

// ClearFieldHistories wipes every field's undo state — called on tab switch
// and fresh sessions so a draft can't resurface across sessions (multi-tab
// isolation), and the out-of-band detector can't see one tab's field values
// as "changes" to another's.
func (c *Ctx) ClearFieldHistories() {
	c.fieldHists = nil
	c.fieldHistUse = c.fieldHistUse[:0]
	c.selAnchor = -1
}

// fieldSel returns the focused field's ordered selection [lo,hi) clamped to
// rc runes — (0,0) when there is none. Selection state lives on the Ctx and
// is only meaningful for the focused field (like the caret).
func (c *Ctx) fieldSel(rc int) (int, int) {
	if c.selAnchor < 0 {
		return 0, 0
	}
	lo, hi := c.selAnchor, c.caret
	if lo > hi {
		lo, hi = hi, lo
	}
	if lo < 0 {
		lo = 0
	}
	if hi > rc {
		hi = rc
	}
	if lo >= hi {
		return 0, 0
	}
	return lo, hi
}

// wordBoundsAt returns the [lo,hi) rune range of the "word" containing idx —
// a maximal run of non-space runes (or of spaces, when idx sits on one),
// which is the native double-click rule. idx clamps into the text.
func wordBoundsAt(runes []rune, idx int) (int, int) {
	n := len(runes)
	if n == 0 {
		return 0, 0
	}
	if idx >= n {
		idx = n - 1
	}
	if idx < 0 {
		idx = 0
	}
	isSpace := func(r rune) bool { return r == ' ' || r == '\t' }
	class := isSpace(runes[idx])
	lo, hi := idx, idx+1
	for lo > 0 && isSpace(runes[lo-1]) == class {
		lo--
	}
	for hi < n && isSpace(runes[hi]) == class {
		hi++
	}
	return lo, hi
}
