package ui

// The Players tab's toolbar: one left-to-right cursor, so its controls cannot overlap as opposed anchors could.
// It wraps when a control does not fit and places nothing on a panel too small to paint legibly.
// Refresh, Legacy, Pairs and Follow live in the panel's overflow menu (musicheader.go).

import (
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

const (
	// plStripRowH is one toolbar line's control height.
	plStripRowH = int32(22)
	// plStripLineGapPx is the breathing space between two toolbar lines.
	plStripLineGapPx = int32(4)
	// plStripLinePitch is the vertical step from one toolbar line to the next.
	plStripLinePitch = plStripRowH + plStripLineGapPx
	// plStripBodyGapPx is the extra gap between the last toolbar line and the roster.
	plStripBodyGapPx = int32(2)
	// plStripItemGapPx separates two controls sharing a line.
	plStripItemGapPx = int32(8)
	// plStripBtnPadPx is the horizontal padding a text button adds around its label.
	plStripBtnPadPx = int32(16)
	// plStripLabelOffY drops a plain text label to the optical centre of a line; the Music header uses it.
	plStripLabelOffY = int32(5)
	// plStripMinItemPx is the narrowest line worth placing a control on: a bare tick box plus its label gap.
	plStripMinItemPx = checkboxBoxPx + checkboxLabelGapPx
	// plStripMinBodyPx is the roster height kept however many toolbar lines wrap: one area-header row.
	plStripMinBodyPx = playerHeaderH
	// plStripMenuPx is the square overflow button, equal to plStripRowH so it shares the row's baseline.
	plStripMenuPx = plStripRowH
)

// Fixed control labels, so the planner and the draw loop measure and paint the same strings.
const (
	// plStatusPrefix + a statusLabel is the Status button's text; plStatusNoneLabel replaces StatusNone's empty label.
	plStatusPrefix    = "Status: "
	plStatusNoneLabel = "none"
	// plStatusWidestLabel sizes the Status button whatever the status, so cycling it cannot reflow the strip.
	// It must be the widest label statusLabel can return.
	plStatusWidestLabel = plStatusPrefix + "Writing"
)

// Toolbar control ids; the plan carries ids rather than closures so it stays a plain value with no allocation.
const (
	plItemSort = iota + 1
	plItemRooms
	plItemStatus
	plItemMenu
)

// plToolbarMaxItems is the longest the strip can get: Sort, Rooms, Status and the overflow button.
const plToolbarMaxItems = 4

// plToolbarLabels carries the labels that change per frame; the caller builds them so the planner stays pure.
type plToolbarLabels struct {
	sort   string // "Sort: UID"
	rooms  string // "Rooms: /gas" — only placed when the roster spans areas
	status string // "Status: none" — DRAWN text; the button is sized to plStatusWidestLabel
}

// plToolbarItem is one placed control: what it is, what it says, and where it goes.
type plToolbarItem struct {
	id    int
	label string
	r     sdl.Rect
}

// plToolbarPlan is a whole frame's toolbar; h is the vertical room taken, which the caller subtracts for the roster.
type plToolbarPlan struct {
	items [plToolbarMaxItems]plToolbarItem
	n     int
	h     int32
}

// add records a placed control, dropping past plToolbarMaxItems rather than panicking on the render path.
func (p *plToolbarPlan) add(id int, label string, r sdl.Rect) {
	if p.n >= plToolbarMaxItems {
		return
	}
	p.items[p.n] = plToolbarItem{id: id, label: label, r: r}
	p.n++
}

// plStrip is the single left-to-right cursor the whole toolbar is placed through.
type plStrip struct {
	left, right int32 // the panel's wrap columns: controls live in [left, right)
	top         int32 // the strip's first line's Y
	maxH        int32 // the most vertical room the strip may take (see plStripMinBodyPx)
	x, y        int32 // cursor — where the next control's top-left goes
	lines       int32 // lines opened so far; 0 before the first placement
	full        bool  // no further line fits vertically — every later place() fails
}

func newPlStrip(r sdl.Rect) plStrip {
	return plStrip{
		left:  r.X,
		right: r.X + r.W,
		top:   r.Y,
		maxH:  r.H - plStripMinBodyPx,
		x:     r.X,
		y:     r.Y,
	}
}

// fits reports whether n toolbar lines still leave the roster its minimum height.
func (s *plStrip) fits(n int32) bool {
	return n*plStripLinePitch+plStripBodyGapPx <= s.maxH
}

// height is the vertical room the placed lines took; zero when nothing was placed.
func (s *plStrip) height() int32 {
	if s.lines == 0 {
		return 0
	}
	return s.lines*plStripLinePitch + plStripBodyGapPx
}

// place reserves the next control's rect at its natural width.
func (s *plStrip) place(w int32) (sdl.Rect, bool) { return s.placeFlex(w, w) }

// placeFlex reserves the next control's rect, wrapping first if it will not fit; min is the narrowest it may be drawn.
// ok=false means draw nothing: a missing control is recoverable, one drawn over its neighbour is not.
func (s *plStrip) placeFlex(want, min int32) (sdl.Rect, bool) {
	if s.full || min <= 0 {
		return sdl.Rect{}, false
	}
	lineW := s.right - s.left
	if lineW < plStripMinItemPx {
		return sdl.Rect{}, false // narrower than a bare tick box: nothing legible fits
	}
	if min > lineW {
		// Wider than the whole panel: clamp rather than drop, because Button and CheckboxIn clip their own labels.
		min = lineW
	}
	if want < min {
		want = min
	}
	switch {
	case s.lines == 0:
		if !s.fits(1) {
			s.full = true
			return sdl.Rect{}, false
		}
		s.lines = 1 // the first control opens line 1 at the cursor
	case s.x+min > s.right:
		if !s.fits(s.lines + 1) {
			s.full = true // the roster would drop below plStripMinBodyPx — stop here
			return sdl.Rect{}, false
		}
		s.x, s.y = s.left, s.y+plStripLinePitch
		s.lines++
	}
	if avail := s.right - s.x; want > avail {
		want = avail // take what is left of this line (only reachable for a flex control)
	}
	r := sdl.Rect{X: s.x, Y: s.y, W: want, H: plStripRowH}
	s.x += want + plStripItemGapPx
	return r, true
}

// plBtnW is a text button's natural width under an injected measure.
func plBtnW(label string, measure func(string) int32) int32 {
	return measure(label) + plStripBtnPadPx
}

// planPlayerToolbar lays out one frame's toolbar, with the overflow button last so it lands at the right end.
func planPlayerToolbar(r sdl.Rect, lb plToolbarLabels, multiArea bool, measure func(string) int32) plToolbarPlan {
	var p plToolbarPlan
	s := newPlStrip(r)
	if rc, ok := s.place(plBtnW(lb.sort, measure)); ok {
		p.add(plItemSort, lb.sort, rc)
	}
	if multiArea { // nothing to order in a single-area roster
		if rc, ok := s.place(plBtnW(lb.rooms, measure)); ok {
			p.add(plItemRooms, lb.rooms, rc)
		}
	}
	// Sized to plStatusWidestLabel, not the drawn label: a width that changed with the status would reflow the strip.
	if rc, ok := s.place(plBtnW(plStatusWidestLabel, measure)); ok {
		p.add(plItemStatus, lb.status, rc)
	}
	if rc, ok := s.place(plStripMenuPx); ok {
		p.add(plItemMenu, "", rc)
	}
	p.h = s.height()
	return p
}

// playerStatusButtonLabel is the Status button's text; plStatusNoneLabel avoids a bare "Status: " that reads as broken.
func playerStatusButtonLabel(s courtroom.Status) string {
	lbl := statusLabel(s)
	if lbl == "" {
		lbl = plStatusNoneLabel
	}
	return plStatusPrefix + lbl
}
