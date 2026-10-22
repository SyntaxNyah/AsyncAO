package ui

// The Players tab's toolbar layout.

import (
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/veandco/go-sdl2/sdl"
)

// Strip metrics. Chosen so a WIDE panel lays out exactly as the old two-row strip
// did — the classic docked panel and torn-off tabs must not visibly move.
const (
	// plStripRowH is one toolbar line's control height: the 22 px every button and
	// tick box in this strip has always used.
	plStripRowH = int32(22)
	// plStripLineGapPx is the breathing space between two toolbar lines.
	plStripLineGapPx = int32(4)
	// plStripLinePitch is the vertical step from one toolbar line to the next.
	plStripLinePitch = plStripRowH + plStripLineGapPx
	// plStripBodyGapPx is the extra gap between the last toolbar line and the roster.
	// Pitch plus this gap reproduces the old strip's per-line reservation exactly: one
	// line costs 26+2 = 28 px, which is what the old `r.Y += 28` took.
	plStripBodyGapPx = int32(2)
	// plStripItemGapPx separates two controls sharing a line.
	plStripItemGapPx = int32(8)
	// plStripBtnPadPx is the horizontal padding a text button adds around its label —
	// the same +16 the Sort button and the music list's Expand/Collapse pair use.
	plStripBtnPadPx = int32(16)
	// plStripLabelOffY drops a plain text label to the optical centre of a
	// plStripRowH line (the +5 the old fixed rows used). The roster strip no longer
	// draws a bare label, but the Music header's shown/total count still does and
	// shares this strip's metrics.
	plStripLabelOffY = int32(5)
	// plStripMinItemPx is the narrowest LINE still worth placing a control on: a bare
	// tick box plus its label gap, i.e. a checkbox with no room for any label. Below
	// that the strip places nothing rather than paint slivers of overlapping chrome —
	// a panel that narrow has no usable roster underneath either. It stays expressed
	// in checkbox terms even though the strip no longer draws one: it is the kit's
	// smallest labelled control, which is what the floor is really about.
	plStripMinItemPx = checkboxBoxPx + checkboxLabelGapPx
	// plStripMinBodyPx is how much of the panel's HEIGHT the roster keeps no matter
	// how many toolbar lines want to wrap: one area-header row, the smallest thing
	// the list can usefully show. Wrapping is the fix for a narrow panel, but an
	// unbounded wrap in a SHORT panel would eat the list it is a toolbar for.
	plStripMinBodyPx = playerHeaderH
	// plStripMenuPx is the square ⋮ overflow button. Equal to plStripRowH so it
	// shares the row's baseline, exactly as musicIconPx does in the Music header.
	plStripMenuPx = plStripRowH
)

// Fixed control labels. Named here rather than inline so the planner and the draw
// loop measure and paint the same strings (they used to be two separate literals).
const (
	// plStatusPrefix + a statusLabel is the Status button's text; plStatusNoneLabel
	// stands in for the empty label StatusNone returns.
	plStatusPrefix    = "Status: "
	plStatusNoneLabel = "none"
	// plStatusWidestLabel is what the Status button is SIZED to, regardless of the
	// status actually set. Cycling the status must not change the button's width: in a
	// wrapping strip a width change does not merely nudge one button, it can reflow
	// every control after it onto a different line. A test pins that this really is
	// the widest of the five labels statusLabel can return.
	plStatusWidestLabel = plStatusPrefix + "Writing"
)

// Toolbar control ids. The plan carries ids rather than closures so it stays a plain
// value (no allocation, no captured App) and a test can assert on it with no
// renderer.
const (
	plItemSort = iota + 1
	plItemRooms
	plItemStatus
	plItemMenu
)

// plToolbarMaxItems is the longest the strip can get: Sort, Rooms, Status and the ⋮.
// Fixed so plToolbarPlan is a stack value.
const plToolbarMaxItems = 4

// plToolbarLabels carries the labels that change from frame to frame. The caller
// builds them (it owns the state they come from) so the planner stays pure and a
// test can drive it with a stub measure func.
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

// plToolbarPlan is a whole frame's toolbar. h is the vertical room the strip took,
// which the caller subtracts before drawing the roster.
type plToolbarPlan struct {
	items [plToolbarMaxItems]plToolbarItem
	n     int
	h     int32
}

// add records a placed control. The bound check can only fire if the item list above
// grows past plToolbarMaxItems; dropping silently still beats an out-of-range panic
// on the render path.
func (p *plToolbarPlan) add(id int, label string, r sdl.Rect) {
	if p.n >= plToolbarMaxItems {
		return
	}
	p.items[p.n] = plToolbarItem{id: id, label: label, r: r}
	p.n++
}

// plStrip is the single left-to-right cursor the whole toolbar is placed through.
// Two controls can only collide if two independent anchors exist; there is one.
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

// (There is no newline(). This cursor's one planner lays a single row in reading
// order — planPlayerToolbar says so — and the FORCED break, lazy so an empty second
// half never reserves a blank line, is a live feature of the general reflow planner
// that grew out of this one: chromeCtl.brk, in chromerow.go. A second, unreachable
// spelling of it here was a wrap this strip could enter and no caller could ask for.)

// height is the vertical room the placed lines took. Zero when nothing was placed,
// so a panel too small for any control gives its whole height to the roster.
func (s *plStrip) height() int32 {
	if s.lines == 0 {
		return 0
	}
	return s.lines*plStripLinePitch + plStripBodyGapPx
}

// place reserves the next control's rect at its natural width.
func (s *plStrip) place(w int32) (sdl.Rect, bool) { return s.placeFlex(w, w) }

// placeFlex reserves the next control's rect, wrapping to a new line first when it
// will not fit on the current one. min is the narrowest the control may be drawn:
// for a fixed control it equals want, and for a clipped label it is smaller, letting
// the label shrink into the tail of the current line instead of costing a whole one.
//
// ok=false means DRAW NOTHING for this control — the panel cannot hold it. That is
// the degradation contract: a missing control is recoverable (the panel is wider in
// the classic dock and in a torn-off tab), a control drawn over its neighbour is not.
func (s *plStrip) placeFlex(want, min int32) (sdl.Rect, bool) {
	if s.full || min <= 0 {
		return sdl.Rect{}, false
	}
	lineW := s.right - s.left
	if lineW < plStripMinItemPx {
		return sdl.Rect{}, false // narrower than a bare tick box: nothing legible fits
	}
	if min > lineW {
		// Wider than the whole panel. Clamp rather than drop: Button and CheckboxIn
		// both clip their own label to their rect, so the control degrades to a
		// shortened label instead of overflowing into its neighbours.
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

// planPlayerToolbar lays every Players-tab toolbar control out for one frame.
// ONE line, in reading order: how the roster is ORDERED (Sort, then Rooms when there
// are area groups to order), what YOU are broadcasting (Status), and everything else
// A panel too narrow for one line wraps rather than clips (plStrip.placeFlex), and a
// panel too SHORT stops opening lines before the roster falls under plStripMinBodyPx.
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
	// Sized to plStatusWidestLabel, not to what it is about to DRAW: see that
	// constant — a width that changed with the status would reflow the whole strip.
	if rc, ok := s.place(plBtnW(plStatusWidestLabel, measure)); ok {
		p.add(plItemStatus, lb.status, rc)
	}
	if rc, ok := s.place(plStripMenuPx); ok {
		p.add(plItemMenu, "", rc)
	}
	p.h = s.height()
	return p
}

// playerStatusButtonLabel is the Status button's text: "Status: " plus the current
// status, or plStatusNoneLabel when nothing is set (statusLabel returns "" there,
// and a bare "Status: " reads as a broken widget).
func playerStatusButtonLabel(s courtroom.Status) string {
	lbl := statusLabel(s)
	if lbl == "" {
		lbl = plStatusNoneLabel
	}
	return plStatusPrefix + lbl
}
