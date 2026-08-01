package ui

import (
	"testing"

	"github.com/veandco/go-sdl2/sdl"
)

// The chip strips are the one piece of themed geometry AsyncAO invents rather
// than reads, so nothing in the theme data can stop them overflowing: the widths
// were constants and the panel is whatever the theme declared. That is how
// "Player List" came to hang out over the right edge of a 216 px music_list.
//
// These tests pin the total contract instead of the one width that was reported.

// themedStripCases is every strip the courtroom draws, by the (want, views) pair
// its call site passes.
var themedStripCases = []struct {
	name  string
	want  []themedChipSpec
	views []themedChipSpec
}{
	{"music panel, theme has no switch_area_music", themedListViews[:], themedListViews[:]},
	{"music panel, theme placed AO2's A/M button", themedRosterStrip, themedListViews[:]},
	{"merged chatlog panel", themedLogViews[:], themedLogViews[:]},
}

// TestThemedChipStripNeverLeavesItsPanel is the defect, generalised: for EVERY
// panel width — including absurd ones — the strip stays inside the body it was
// given, its chips stay readable, and they never overlap each other.
func TestThemedChipStripNeverLeavesItsPanel(t *testing.T) {
	// Heights around the strip's own floor: too short for any strip, exactly the
	// floor, and a real panel.
	heights := []int32{0, 1, themedStripH, themedStripH + minThemedElementPx - 1, themedStripH + minThemedElementPx, 277}
	for _, tc := range themedStripCases {
		for _, view := range []int{logTabMusic, logTabAreas, logTabPlayers, logTabLog, logTabOOC, logTabNotes, 4242} {
			for w := int32(0); w <= 400; w++ {
				for _, h := range heights {
					// A non-zero origin catches a fitter that reasons in widths and
					// forgets the panel's X (the strip is drawn at body.X, not 0).
					body := sdl.Rect{X: 17, Y: 23, W: w, H: h}
					chips, n := fitThemedChips(body, tc.want, tc.views, view)
					if n < 0 || n > themedChipsMax {
						t.Fatalf("%s w=%d h=%d view=%d: n=%d out of range", tc.name, w, h, view, n)
					}
					fits := w >= themedChipMinW && h >= themedStripH+minThemedElementPx
					if fits != (n > 0) {
						t.Fatalf("%s w=%d h=%d view=%d: n=%d, want a strip=%v", tc.name, w, h, view, n, fits)
					}
					prev := int32(-1 << 30)
					for i := int32(0); i < n; i++ {
						r := chips[i].r
						if r.X < body.X || r.X+r.W > body.X+body.W {
							t.Fatalf("%s w=%d h=%d view=%d: chip %d %+v runs outside the panel %+v",
								tc.name, w, h, view, i, r, body)
						}
						if r.Y != body.Y || r.H != themedChipH || r.Y+r.H > body.Y+body.H {
							t.Fatalf("%s w=%d h=%d view=%d: chip %d %+v is not a strip row of the panel %+v",
								tc.name, w, h, view, i, r, body)
						}
						if r.W < themedChipMinW {
							t.Fatalf("%s w=%d h=%d view=%d: chip %d is %d px wide — below themedChipMinW, so it carries no label",
								tc.name, w, h, view, i, r.W)
						}
						if r.X < prev {
							t.Fatalf("%s w=%d h=%d view=%d: chip %d starts at %d, inside its neighbour (ends %d)",
								tc.name, w, h, view, i, r.X, prev)
						}
						prev = r.X + r.W + themedChipGap
						if chips[i].label == "" {
							t.Fatalf("%s w=%d h=%d view=%d: chip %d has no label", tc.name, w, h, view, i)
						}
					}
				}
			}
		}
	}
}

// TestThemedListStripFitsTheRealPanels is the reported bug in its two concrete
// shapes: aceattorney2x's own music_list (216 px) and AO2's stock default one
// (224 px). BOTH are narrower than three chips at their natural widths, which is
// the premise of the fitter — if a width constant changes so that the naive strip
// starts fitting, this fails and the fitter's reason has to be re-derived rather
// than the numbers quietly edited.
func TestThemedListStripFitsTheRealPanels(t *testing.T) {
	th := loadAceAttorney2x(t)
	fixture, ok := th.ElementRect("music_list")
	if !ok {
		t.Fatal("fixture lost its music_list rect")
	}
	stock := ao2DefaultDesignRects["music_list"]

	natural := themedListViews[0].w + themedListViews[1].w + themedListViews[2].w + 2*themedChipGap
	for _, tc := range []struct {
		name string
		w, h int
	}{
		{"aceattorney2x music_list", fixture.W, fixture.H},
		{"AO2 stock default music_list", stock.W, stock.H},
	} {
		// ThemeFitNative (the default) draws a design rect 1:1, so the design width
		// IS the panel width on screen.
		body := insetThemedBody(sdl.Rect{X: 0, Y: 0, W: int32(tc.w), H: int32(tc.h)})
		if natural <= body.W {
			t.Fatalf("%s: the naive three-chip strip (%d px) now fits a %d px body — the premise of fitThemedChips moved, re-derive it",
				tc.name, natural, body.W)
		}
		for _, sc := range themedStripCases {
			chips, n := fitThemedChips(body, sc.want, sc.views, logTabMusic)
			if n == 0 {
				t.Fatalf("%s / %s: no strip drew in a real panel", tc.name, sc.name)
			}
			right := chips[n-1].r.X + chips[n-1].r.W
			if right > body.X+body.W {
				t.Errorf("%s / %s: strip ends at %d, panel ends at %d — the chip hangs over the theme's art",
					tc.name, sc.name, right, body.X+body.W)
			}
		}
		// The shipping case for both panels: the theme placed switch_area_music (it
		// is in AO2's stock defaults, so every themed layout resolves one), so the
		// strip is the roster chip alone and keeps its full label.
		chips, n := fitThemedChips(body, themedRosterStrip, themedListViews[:], logTabMusic)
		if n != 1 || chips[0].label != "Player List" || chips[0].r.W != themedPlayersTabW {
			t.Errorf("%s: roster strip = %d chip(s) %+v, want one full-width %q", tc.name, n, chips[0], "Player List")
		}
	}
}

// TestThemedListViewsAlwaysReachable walks the panel the way a user does: from
// every view, click every control the frame offers, and check all three views are
// still reachable. This is what makes dropping the Music/Areas chips safe, and it
// is the question the A/M button alone answers WRONG — themedSwitchAreaMusic sends
// the roster to Areas and never back, so without a roster chip the roster becomes
// a one-way door.
func TestThemedListViewsAlwaysReachable(t *testing.T) {
	all := []int{logTabMusic, logTabAreas, logTabPlayers}
	for _, am := range []bool{false, true} {
		want := themedListViews[:]
		if am {
			want = themedRosterStrip
		}
		for w := themedChipMinW; w <= 400; w++ {
			body := sdl.Rect{X: 5, Y: 9, W: w, H: 277}
			for _, start := range all {
				seen := map[int]bool{start: true}
				queue := []int{start}
				for len(queue) > 0 {
					v := queue[0]
					queue = queue[1:]
					next := make([]int, 0, themedChipsMax+1)
					chips, n := fitThemedChips(body, want, themedListViews[:], v)
					for i := int32(0); i < n; i++ {
						next = append(next, chips[i].tab)
					}
					if am {
						next = append(next, themedSwitchAreaMusic(v))
					}
					for _, nv := range next {
						if !seen[nv] {
							seen[nv] = true
							queue = append(queue, nv)
						}
					}
				}
				for _, v := range all {
					if !seen[v] {
						t.Fatalf("A/M=%v w=%d: starting on view %d, view %d is unreachable (reached %v)", am, w, start, v, seen)
					}
				}
			}
		}
	}
}

// TestFitThemedChipsZeroAlloc keeps the strip off the heap. It is drawn on every
// themed frame, under the whole-screen 0-alloc gate (wholescreenalloc_test.go) —
// returning the array by value rather than filling a caller's pointer is the only
// reason that gate still passes, and it is the kind of signature "cleanup" that
// gets reverted by someone who does not know why it is written that way.
func TestFitThemedChipsZeroAlloc(t *testing.T) {
	body := sdl.Rect{X: 0, Y: 0, W: 212, H: 277}
	var sink int32
	if n := testing.AllocsPerRun(200, func() {
		_, k := fitThemedChips(body, themedRosterStrip, themedListViews[:], logTabMusic)
		sink += k
	}); n != 0 {
		t.Fatalf("fitThemedChips allocates %.1f/op, want 0 (sink=%d)", n, sink)
	}
}
