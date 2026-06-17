package ui

import (
	"image"
	"strings"
	"testing"
	"time"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// TestCycleField pins Tab/Shift+Tab focus order: draw order forward,
// reversed with shift, wrapping both ways, first field when none focused.
func TestCycleField(t *testing.T) {
	seq := []string{"ic", "ooc", "search"}
	cases := []struct {
		cur  string
		back bool
		want string
	}{
		{"ic", false, "ooc"},
		{"ooc", false, "search"},
		{"search", false, "ic"}, // wrap forward
		{"ooc", true, "ic"},
		{"ic", true, "search"}, // wrap backward
		{"", false, "ic"},      // nothing focused → first
		{"gone", false, "ic"},  // stale focus → first
	}
	for _, tc := range cases {
		if got := cycleField(seq, tc.cur, tc.back); got != tc.want {
			t.Errorf("cycleField(%q, back=%v) = %q, want %q", tc.cur, tc.back, got, tc.want)
		}
	}
}

// TestInkReadabilityGuard pins the theme ink-vs-skin contrast math: dark
// ink on a dark skin falls below the floor (dropped), white ink clears it.
func TestInkReadabilityGuard(t *testing.T) {
	skin := image.NewRGBA(image.Rect(0, 0, 64, 32))
	for i := 0; i < len(skin.Pix); i += 4 {
		skin.Pix[i+0] = 24 // a dark chatbox
		skin.Pix[i+1] = 24
		skin.Pix[i+2] = 30
		skin.Pix[i+3] = 255
	}
	lum := avgSkinLuma(skin, lumaSampleStep)
	if lum < 18 || lum > 32 {
		t.Fatalf("avgSkinLuma = %d, want ≈24 for the flat dark skin", lum)
	}
	black := colLuma(sdl.Color{R: 16, G: 16, B: 16, A: 255})
	white := colLuma(sdl.Color{R: 235, G: 235, B: 235, A: 255})
	if absInt(black-lum) >= minInkSkinContrast {
		t.Error("near-black ink on a dark skin must fall below the contrast floor")
	}
	if absInt(white-lum) < minInkSkinContrast {
		t.Error("white ink on a dark skin must clear the contrast floor")
	}

	// Fully transparent skins read as the dark backdrop, never as black=0.
	clear := image.NewRGBA(image.Rect(0, 0, 8, 8))
	if got := avgSkinLuma(clear, 1); got != transparentSkinLuma {
		t.Errorf("transparent skin luma = %d, want %d", got, transparentSkinLuma)
	}
}

// TestApplyThemePaletteReadabilityFloor pins the kit-wide ink guard: a
// stylesheet whose text has no contrast against its own panels (playtest:
// GrayGarden = black on black settings) gets readable ink re-derived from
// the panel's lightness; switching back restores the stock palette.
func TestApplyThemePaletteReadabilityFloor(t *testing.T) {
	defer applyThemePalette(theme.Palette{}) // restore stock for other tests
	rgb := func(r, g, b uint8) *theme.RGB { return &theme.RGB{R: r, G: g, B: b} }

	// Black-on-black sheet → light ink wins.
	applyThemePalette(theme.Palette{Text: rgb(10, 10, 10), Panel: rgb(18, 18, 20)})
	if absInt(colLuma(ColText)-colLuma(ColPanel)) < minInkSkinContrast {
		t.Errorf("dark-on-dark sheet kept unreadable ink: text %v panel %v", ColText, ColPanel)
	}

	// White-on-white sheet → dark ink wins.
	applyThemePalette(theme.Palette{Text: rgb(245, 245, 245), Panel: rgb(230, 230, 235)})
	if absInt(colLuma(ColText)-colLuma(ColPanel)) < minInkSkinContrast {
		t.Errorf("light-on-light sheet kept unreadable ink: text %v panel %v", ColText, ColPanel)
	}
	if colLuma(ColText) >= paletteLightPanelLuma {
		t.Error("light panels must take dark ink, not light")
	}

	// A readable sheet passes through untouched.
	applyThemePalette(theme.Palette{Text: rgb(220, 230, 240), Panel: rgb(27, 39, 53)})
	if (ColText != sdl.Color{R: 220, G: 230, B: 240, A: 255}) {
		t.Errorf("readable sheet text must apply verbatim, got %v", ColText)
	}

	// Empty palette restores stock exactly.
	applyThemePalette(theme.Palette{})
	if ColText != defaultKitColors[4] || ColPanel != defaultKitColors[1] {
		t.Error("empty palette must restore the stock kit colors")
	}
}

// TestICWrapped pins the IC log wrap cache: rows split to width, inherit
// their entry index (color source), and the cache returns the same backing
// until log/width/query move.
func TestICWrapped(t *testing.T) {
	a := &App{ctx: &Ctx{}}
	a.logPct = DefaultScalePct // share the (nil) chrome font: 8 px/char path
	a.icLog = []icEntry{
		{text: strings.Repeat("word ", 40), color: 2},
		{text: "short", color: 0},
	}
	a.icLogSeq = 1

	rows := a.icWrapped(160, false) // 20 chars per row at the 8 px fallback
	if len(rows) < 3 {
		t.Fatalf("long entry must wrap to multiple rows, got %d", len(rows))
	}
	last := rows[len(rows)-1]
	if last.text != "short" || last.entry != 1 {
		t.Fatalf("tail row = %+v, want the second entry verbatim", last)
	}
	for _, r := range rows[:len(rows)-1] {
		if r.entry != 0 {
			t.Fatalf("wrapped row %+v must keep its source entry index", r)
		}
	}

	again := a.icWrapped(160, false)
	if &again[0] != &rows[0] || len(again) != len(rows) {
		t.Error("unchanged log/width/query must hit the cache (same backing)")
	}
	if narrower := a.icWrapped(80, false); len(narrower) <= len(rows) {
		t.Error("narrower width must produce more wrapped rows")
	}
}

// TestICWrappedTimestamps pins the local-time prefix: with showStamps on, the
// first wrapped row of an entry carries its stamp; toggling rewraps (the stamp
// is part of the memo key). The stamp is formatted once on append, never here.
func TestICWrappedTimestamps(t *testing.T) {
	a := &App{ctx: &Ctx{}}
	a.logPct = DefaultScalePct
	a.icLog = []icEntry{{text: "Phoenix: hold it", color: 2, stamp: "14:32"}}
	a.icLogSeq = 1

	off := a.icWrapped(800, false)
	if len(off) != 1 || off[0].text != "Phoenix: hold it" {
		t.Fatalf("stamps off: want the bare line, got %+v", off)
	}
	on := a.icWrapped(800, true)
	if len(on) != 1 || !strings.HasPrefix(on[0].text, "14:32") {
		t.Fatalf("stamps on: first row must start with the time, got %+v", on)
	}
}

// TestNextRandomEmote pins auto-random emote selection: with >1 emote it always
// returns a DIFFERENT, in-range index than the current one (so the sprite
// visibly changes every send); with 0 or 1 it can't vary and returns cur.
func TestNextRandomEmote(t *testing.T) {
	// Degenerate counts can't vary — return cur, never panic.
	for _, n := range []int{0, 1} {
		if got := nextRandomEmote(n, 0); got != 0 {
			t.Errorf("nextRandomEmote(%d, 0) = %d, want 0 (no variation possible)", n, got)
		}
	}
	// With many emotes, every roll differs from cur and stays in [0, n).
	const n = 10
	for cur := 0; cur < n; cur++ {
		for trial := 0; trial < 200; trial++ {
			got := nextRandomEmote(n, cur)
			if got == cur {
				t.Fatalf("nextRandomEmote(%d, %d) returned the same index", n, cur)
			}
			if got < 0 || got >= n {
				t.Fatalf("nextRandomEmote(%d, %d) = %d, out of range", n, cur, got)
			}
		}
	}
	// An out-of-range current selection (-1 = nothing picked) still yields a
	// valid index without panicking.
	for trial := 0; trial < 200; trial++ {
		if got := nextRandomEmote(n, -1); got < 0 || got >= n {
			t.Fatalf("nextRandomEmote(%d, -1) = %d, out of range", n, got)
		}
	}
}

// TestEmotePageCounterMemoized pins the per-frame emote-grid counter: correct
// text, ZERO allocations while the page is stable (the gate), and a rebuild the
// moment any input changes (the invalidation correctness check).
func TestEmotePageCounterMemoized(t *testing.T) {
	a := &App{}
	got := a.emotePageCounter(1, 3, 24)
	if got != "page 1/3 · 24 emotes" {
		t.Fatalf("label = %q, want %q", got, "page 1/3 · 24 emotes")
	}
	// The always-on draw path re-asks with the same inputs every frame: no alloc.
	if n := testing.AllocsPerRun(100, func() { a.emotePageCounter(1, 3, 24) }); n != 0 {
		t.Errorf("steady-state allocs = %v, want 0", n)
	}
	// Paging changes an input → the label must rebuild (else we'd show a stale
	// counter).
	if g := a.emotePageCounter(2, 3, 24); g != "page 2/3 · 24 emotes" {
		t.Errorf("changed page label = %q, want rebuilt", g)
	}
	if g := a.emotePageCounter(2, 3, 25); g != "page 2/3 · 25 emotes" {
		t.Errorf("changed total label = %q, want rebuilt", g)
	}
}

// TestHPBarStemNoAlloc pins the per-frame HP-bar key lookup: correct stems and
// ZERO allocations (the precomputed table replaced a string concat that ran up
// to 4×/frame in the default courtroom view).
func TestHPBarStemNoAlloc(t *testing.T) {
	a := &App{}
	if s := a.hpBarStem(true, 5); s != "defensebar5" {
		t.Fatalf("defense stem = %q, want defensebar5", s)
	}
	if s := a.hpBarStem(false, 0); s != "prosecutionbar0" {
		t.Fatalf("prosecution stem = %q, want prosecutionbar0", s)
	}
	if s := a.hpBarStem(true, courtroom.HPBarMax); s != "defensebar10" {
		t.Fatalf("max stem = %q, want defensebar10", s)
	}
	for _, def := range []bool{true, false} {
		d := def
		if n := testing.AllocsPerRun(100, func() { _ = a.hpBarStem(d, 7) }); n != 0 {
			t.Errorf("hpBarStem(def=%v) allocs = %v, want 0", d, n)
		}
	}
}

// TestTabChipLabelMemoized pins the always-on tab-strip label: correct text per
// state, ZERO allocations while a tab is stable (the strip asks ~3×/tab/frame),
// and a rebuild when unread changes.
func TestTabChipLabelMemoized(t *testing.T) {
	a := &App{
		sessionState: sessionState{serverName: "ActiveServer"},
		activeTab:    0,
		tabs: []*courtTab{
			{}, // 0: active
			{state: sessionState{serverName: "Other"}, unread: 3}, // 1: backgrounded, unread
			{state: sessionState{serverName: "Gone"}, dead: true}, // 2: backgrounded, dead
		},
	}
	if got := a.tabChipLabel(0); got != "ActiveServer" {
		t.Fatalf("active label = %q, want ActiveServer", got)
	}
	if got := a.tabChipLabel(1); got != "Other (3)" {
		t.Fatalf("unread label = %q, want Other (3)", got)
	}
	if got := a.tabChipLabel(2); got != "Gone ✕" {
		t.Fatalf("dead label = %q, want Gone ✕", got)
	}
	// The per-frame asks (sizing + draw) must allocate nothing once stable.
	if n := testing.AllocsPerRun(100, func() { _ = a.tabChipLabel(1) }); n != 0 {
		t.Errorf("steady-state allocs = %v, want 0", n)
	}
	// A new background message bumps unread → the label must rebuild.
	a.tabs[1].unread = 4
	if got := a.tabChipLabel(1); got != "Other (4)" {
		t.Errorf("rebuilt label = %q, want Other (4)", got)
	}
}

// TestDebugDiagLine pins the debug overlay's diagnostics readout: it reports the
// live structural counts (tabs, area, queue, log sizes, goroutines) so a leak or
// stuck queue is visible at a glance.
func TestDebugDiagLine(t *testing.T) {
	a := &App{}
	a.tabs = []*courtTab{{}, {}}
	a.icLog = make([]icEntry, 3)
	a.oocLog = make([]string, 1)
	got := a.debugDiagLine()
	for _, want := range []string{"tabs 2", "area —", "queue 0", "ic 3", "ooc 1", "goroutines "} {
		if !strings.Contains(got, want) {
			t.Errorf("diag line %q missing %q", got, want)
		}
	}
	a.curArea = "Courtroom 1"
	if got := a.debugDiagLine(); !strings.Contains(got, "area Courtroom 1") {
		t.Errorf("area not shown in %q", got)
	}
}

// TestTimerChipLabelsMemoized pins the server-clock overlay: correct "Tn mm:ss"
// labels and ZERO allocations for a stable (paused) clock — the always-on draw
// asks every frame, so only a ticking second should rebuild.
func TestTimerChipLabelsMemoized(t *testing.T) {
	a := &App{sessionState: sessionState{sess: &courtroom.Session{}}}
	a.sess.Timers[0] = courtroom.TimerState{Visible: true, Left: 90 * time.Second} // paused 01:30
	a.sess.Timers[2] = courtroom.TimerState{Visible: true, Left: 5 * time.Second}  // paused 00:05

	now := time.Now()
	chips := a.timerChipLabels(now)
	if len(chips) != 2 || chips[0] != "T1 01:30" || chips[1] != "T3 00:05" {
		t.Fatalf("labels = %v, want [T1 01:30 T3 00:05]", chips)
	}
	// Paused clocks are stable → the per-frame ask allocates nothing.
	if n := testing.AllocsPerRun(100, func() { _ = a.timerChipLabels(now) }); n != 0 {
		t.Errorf("steady-state allocs = %v, want 0", n)
	}
	// A changed second rebuilds that timer's label.
	a.sess.Timers[0] = courtroom.TimerState{Visible: true, Left: 91 * time.Second}
	if got := a.timerChipLabels(now); got[0] != "T1 01:31" {
		t.Errorf("rebuilt label = %q, want T1 01:31", got[0])
	}
}
