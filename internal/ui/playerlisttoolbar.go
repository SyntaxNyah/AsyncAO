package ui

// The Players tab's toolbar layout.
//
// WHY this is a planner and not inline arithmetic in drawPlayerList.
//
// The strip used to be two fixed rows of hardcoded offsets, and half of the
// controls were anchored to r.X (growing rightwards) while the other half were
// anchored to r.X+r.W (growing leftwards). Two opposed anchors only stay apart
// while the panel is wide enough for both, and NOTHING checked that:
//
//   - The classic docked panel and a torn-off tab are wide, so it looked fine.
//   - A theme's music_list rect is not. aceattorney2x declares
//     music_list = 0,323,216,277 and insetThemedBody leaves 212 usable px, at which
//     point "Refresh details" (left-anchored, 116 px at r.X+52) and the "Legacy
//     snapshot" tick box (right-anchored) drew straight through each other.
//   - Worse, the Status button was anchored on the FOLLOW checkbox instead of on
//     the Pairs checkbox beside it, so it covered Pairs at EVERY width — even on a
//     1920 px window. That is the stray "s" in the bug report: the tail of "Pairs"
//     sticking out from under "Status: none".
//
// The replacement is a single left-to-right cursor (plStrip). One cursor cannot
// overlap itself by construction: it wraps to a new line when the next control will
// not fit, clamps a control wider than a whole line (Button and CheckboxIn both clip
// their own labels, AO2's truncate_label_text behaviour), and places nothing at all
// once the panel is too small to paint legibly — the same "too small, leave it
// alone" rule insetThemedBody follows.
//
// The plan is a pure value built from injected measurements (the truncateLabelTo
// idiom), so the whole strip is testable without a renderer, and it is a fixed-size
// stack value so the per-frame draw can build one without allocating.

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
	// Pitch plus this gap reproduces the old strip's reservation exactly: two lines
	// cost 2*26+2 = 54 px, which is what the old `r.Y += 26` then `r.Y += 28` took.
	plStripBodyGapPx = int32(2)
	// plStripItemGapPx separates two controls sharing a line.
	plStripItemGapPx = int32(8)
	// plStripBtnPadPx is the horizontal padding a text button adds around its label —
	// the same +16 the Sort button and the music list's Expand/Collapse pair use.
	plStripBtnPadPx = int32(16)
	// plStripLabelOffY drops a plain text label to the optical centre of a
	// plStripRowH line (the +5 the old fixed rows used for "● LIVE" and the status).
	plStripLabelOffY = int32(5)
	// plStripMinItemPx is the narrowest LINE still worth placing a control on: a bare
	// tick box plus its label gap, i.e. a checkbox with no room for any label. Below
	// that the strip places nothing rather than paint slivers of overlapping chrome —
	// a panel that narrow has no usable roster underneath either.
	plStripMinItemPx = checkboxBoxPx + checkboxLabelGapPx
	// plStripMinBodyPx is how much of the panel's HEIGHT the roster keeps no matter
	// how many toolbar lines want to wrap: one area-header row, the smallest thing
	// the list can usefully show. Wrapping is the fix for a narrow panel, but an
	// unbounded wrap in a SHORT panel would eat the list it is a toolbar for.
	plStripMinBodyPx = playerHeaderH
	// plStatusTextMinPx is the narrowest the roster status line ("5 here · live") is
	// still worth drawing at — enough for the head-count and a word of context. It is
	// the only flexible control in the strip: being a clipped label, shrinking it to
	// stay on the current line beats spending a whole extra line on it.
	plStatusTextMinPx = int32(60)
	// plStripCompactPx is the panel width at or above which controls use their FULL
	// labels; below it the ones with a short form use that instead. Anchored on
	// tornTabDefaultW — the width a freshly popped-out tab gets, and the narrowest
	// panel AsyncAO itself ever hands this list. Below that we are inside someone
	// else's rect (a theme's music_list, or a tab the user dragged smaller), where a
	// shorter label is worth far more than the wrapped line it saves: measured on the
	// aceattorney2x rect, compaction takes the strip from five lines to three.
	//
	// It is a PANEL-WIDTH threshold rather than a greedy per-control "does the full
	// label still fit here" test on purpose. Greedy is order-dependent: the first
	// control would take its full width and leave the next one unable to fit even
	// compacted, so the panel would show one long label and then wrap anyway. One
	// threshold makes the whole strip compact or not — predictable, and testable.
	plStripCompactPx = tornTabDefaultW
)

// Fixed control labels. Named here rather than inline so the planner and the draw
// loop measure and paint the same strings (they used to be two separate literals).
const (
	plLiveLabel   = "● LIVE"
	plFetchLabel  = "Fetch:"
	plPairsLabel  = "Pairs"
	plFollowLabel = "Follow"
	// The two controls with a compact form. Both are named by their SHORT label
	// already — "Refresh" and "Legacy" say what they do on their own, and each keeps
	// a tooltip carrying the full sentence — so compacting costs no meaning.
	plRefreshLabel      = "Refresh details"
	plRefreshShortLabel = "Refresh"
	plLegacyLabel       = "Legacy snapshot"
	plLegacyShortLabel  = "Legacy"
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

// plFetchCmds are the legacy-mode roster fetch buttons. An array (not a slice) so
// ranging it copies onto the stack instead of referencing a package-level backing
// store — the draw path builds a plan every frame.
var plFetchCmds = [3]string{"/ga", "/gas", "/getarea"}

// Toolbar control ids. The plan carries ids rather than closures so it stays a plain
// value (no allocation, no captured App) and a test can assert on it with no
// renderer. plItemFetch is shared by the three legacy buttons — their command IS
// their label, so the draw site reads it straight off the item.
const (
	plItemLive = iota + 1
	plItemRefresh
	plItemFetchLabel
	plItemFetch
	plItemLegacy
	plItemSort
	plItemRooms
	plItemStatusText
	plItemStatus
	plItemPairs
	plItemFollow
)

// plToolbarMaxItems is the longest the strip can get, which is the LEGACY branch:
// the "Fetch:" label, three fetch buttons and the Legacy tick box, then Sort, Rooms,
// the status line, Status, Pairs and Follow. The live branch is two shorter. Fixed
// so plToolbarPlan is a stack value.
const plToolbarMaxItems = 11

// plToolbarLabels carries the labels that change from frame to frame. The caller
// builds them (it owns the state they come from) so the planner stays pure and a
// test can drive it with a stub measure func.
type plToolbarLabels struct {
	sort   string // "Sort: UID"
	rooms  string // "Rooms: /gas" — only placed when the roster spans areas
	roster string // "5 here · live" / "3 players  ·  as of 14:02"
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
	pending     bool  // a forced line break is owed before the next placement
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

// newline forces the next control onto a fresh line. Deliberately LAZY: it costs
// nothing if no further control is ever placed, so an empty second half of the
// toolbar does not reserve a blank line.
func (s *plStrip) newline() { s.pending = true }

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
		s.lines, s.pending = 1, false // the first control opens line 1 at the cursor
	case s.pending || s.x+min > s.right:
		if !s.fits(s.lines + 1) {
			s.full = true // the roster would drop below plStripMinBodyPx — stop here
			return sdl.Rect{}, false
		}
		s.x, s.y = s.left, s.y+plStripLinePitch
		s.lines, s.pending = s.lines+1, false
	}
	if avail := s.right - s.x; want > avail {
		want = avail // take what is left of this line (only reachable for a flex control)
	}
	r := sdl.Rect{X: s.x, Y: s.y, W: want, H: plStripRowH}
	s.x += want + plStripItemGapPx
	return r, true
}

// plBtnW / plCheckW are a control's natural width under an injected measure. plCheckW
// mirrors Ctx.CheckboxWidth, which now shares checkboxWidthFor with it so the layout
// pass and the widget can never disagree about how wide a tick box is.
func plBtnW(label string, measure func(string) int32) int32 {
	return measure(label) + plStripBtnPadPx
}

func plCheckW(label string, measure func(string) int32) int32 {
	return checkboxWidthFor(label, measure)
}

// plCompactLabel picks a control's full or short label for a panel of this width.
func plCompactLabel(full, short string, panelW int32) string {
	if panelW < plStripCompactPx {
		return short
	}
	return full
}

// planPlayerToolbar lays out every Players-tab toolbar control for one frame.
//
// Order is the reading order the old fixed rows had — where the roster comes from
// first, then how it is shown, on its own line — with ONE change: the roster status
// line ("12 here · live") is placed last in its half instead of in the middle. It is
// a passive readout and the strip's only flexible control, so putting it last lets it
// soak up whatever a line has left rather than pushing a real control onto a new one.
// Measured on the aceattorney2x rect that is worth a whole wrapped line.
func planPlayerToolbar(r sdl.Rect, lb plToolbarLabels, legacyMode, multiArea bool, measure func(string) int32) plToolbarPlan {
	var p plToolbarPlan
	s := newPlStrip(r)
	if legacyMode {
		if rc, ok := s.place(measure(plFetchLabel)); ok {
			p.add(plItemFetchLabel, plFetchLabel, rc)
		}
		for _, cmd := range plFetchCmds {
			if rc, ok := s.place(plBtnW(cmd, measure)); ok {
				p.add(plItemFetch, cmd, rc)
			}
		}
	} else {
		if rc, ok := s.place(measure(plLiveLabel)); ok {
			p.add(plItemLive, plLiveLabel, rc)
		}
		refresh := plCompactLabel(plRefreshLabel, plRefreshShortLabel, r.W)
		if rc, ok := s.place(plBtnW(refresh, measure)); ok {
			p.add(plItemRefresh, refresh, rc)
		}
	}
	legacyLbl := plCompactLabel(plLegacyLabel, plLegacyShortLabel, r.W)
	if rc, ok := s.place(plCheckW(legacyLbl, measure)); ok {
		p.add(plItemLegacy, legacyLbl, rc)
	}
	// The sort/status half starts on its own line, as it always has: the first half
	// is about WHERE the roster comes from, this one about how it is shown.
	s.newline()
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
	if rc, ok := s.place(plCheckW(plPairsLabel, measure)); ok {
		p.add(plItemPairs, plPairsLabel, rc)
	}
	if rc, ok := s.place(plCheckW(plFollowLabel, measure)); ok {
		p.add(plItemFollow, plFollowLabel, rc)
	}
	// Last, and the strip's only FLEXIBLE control: a clipped label would rather be
	// short than cost a line, so it takes whatever the current line has left.
	if rc, ok := s.placeFlex(measure(lb.roster), plStatusTextMinPx); ok {
		p.add(plItemStatusText, lb.roster, rc)
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
