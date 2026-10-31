package ui

// Per-field undo/redo for the kit's text fields, fed by the field's own edits and by out-of-band rewrites (own-echo
// IC clear, a macro eating the draft), which textField detects even while unfocused, so a sent line is recoverable.
// Histories are wiped on tab switch and fresh session so a draft never resurfaces across sessions.

import (
	"time"
	"unicode/utf8"
)

const (
	// fieldUndoDepth caps one field's undo (and redo) stack.
	fieldUndoDepth = 64
	// fieldHistFieldsCap caps how many fields keep a history at once (LRU-evicted).
	fieldHistFieldsCap = 16
	// fieldUndoCoalesce groups ±1-rune edits into one undo step; bigger jumps always get their own, so a burst can
	// never swallow a recoverable line.
	fieldUndoCoalesce = 600 * time.Millisecond
)

type fieldSnap struct {
	value string
	caret int
}

// fieldHistory is one field's undo state; lastKnown is the value at the previous draw, to detect out-of-band changes.
type fieldHistory struct {
	lastKnown  string
	lastCaret  int
	undo, redo []fieldSnap
	lastPush   time.Time
}

// record pushes the pre-change state and clears redo; a ±1-rune change within fieldUndoCoalesce extends the last step.
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

// step applies one undo or redo; ok is false when that stack is empty (the chord is still consumed).
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
	// A restore is a deliberate step: stop the next edit from coalescing into a pre-undo push.
	h.lastPush = time.Time{}
	return snap, true
}

// fieldTrack fetches the field's history and records any out-of-band change. A first sighting only seeds lastKnown:
// a restored draft is a starting state, not a change to undo out of.
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
		// Evict the least recently created history (the use list is in append order).
		oldest := c.fieldHistUse[0]
		c.fieldHistUse = c.fieldHistUse[1:]
		delete(c.fieldHists, oldest)
	}
	h := &fieldHistory{lastKnown: value}
	c.fieldHists[id] = h
	c.fieldHistUse = append(c.fieldHistUse, id)
	return h
}

// ClearFieldHistories wipes undo state so a draft can't resurface across sessions and one tab's values don't read as
// changes to another's.
func (c *Ctx) ClearFieldHistories() {
	c.fieldHists = nil
	c.fieldHistUse = c.fieldHistUse[:0]
	c.selAnchor = -1
}

// fieldSel returns the focused field's ordered selection [lo,hi) clamped to rc runes, (0,0) when there is none.
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

// selectionFor returns the [lo,hi) rune selection of field id; ok is false when id isn't focused or nothing is
// selected.
func (c *Ctx) selectionFor(id, value string) (lo, hi int, ok bool) {
	if c.focusID != id {
		return 0, 0, false
	}
	lo, hi = c.fieldSel(utf8.RuneCountInString(value))
	return lo, hi, hi > lo
}

// wordBoundsAt returns the [lo,hi) rune range of the word at idx, a rune index or a caret boundary. A boundary just
// after a word steps back onto it, so double-clicking the right half of its last letter selects the word, not the gap.
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
	if idx > 0 && isSpace(runes[idx]) && !isSpace(runes[idx-1]) {
		idx-- // a boundary just past a word belongs to the word
	}
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
