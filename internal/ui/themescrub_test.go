package ui

// THE PAUSED-CLOCK CENSUS GUARD (v1.90.0 W7a).
//
// W5 shipped themeClock.paused with NO WRITER and left the whole failure written
// down at drawElement's census line as a TODO. W7's scrub is the first writer, so
// the guard lands with it — and these gates land BEFORE the scrub UI does, which is
// the point: the defect is invisible on screen (the client simply never falls to
// the idle cadence, with a still picture in front of you), so it has to be caught by
// a test or it is not caught at all.

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/theme"
)

// TestPausedClockNeverPinsTheFrameRate is the idle-CPU-burn gate for a scrubbed
// clock, over BOTH sites that can note the census on the element path.
//
// The three ways a paused clock reports motion forever, each correct in isolation:
//
//   - the five PERIODIC effect kinds return static=false unconditionally (design
//     §Q3: "they note forever, which is correct and is what the author asked for");
//   - a CONDITIONAL one-shot acquires its pool slot at the frozen elapsed, so
//     startedAt == el permanently and its envelope sits at full amplitude;
//   - a LOOPING page's elementFrameIndex returns live=true unconditionally, because
//     a loop never ends.
//
// Every one of them is a still picture once the clock stops.
func TestPausedClockNeverPinsTheFrameRate(t *testing.T) {
	a, lay, _, restore := fxDrawRig(t)
	defer restore()
	a.themeAt = time.Now()
	a.beginThemeFXPass()

	// Clock 1 is HELD at a point a quarter into the period — the position
	// TestLiveElementAlwaysNotesTheCensus proves every kind is unambiguously live at.
	const held = time.Duration(fxTestPeriodMs) * time.Millisecond / 4
	a.themeClocks[1] = themeClock{paused: true, frozen: held}

	for k := theme.EffectKind(0); k < theme.EffectKindCount; k++ {
		if k == theme.FXNone {
			continue
		}
		el := fxTestElement(k)
		el.clock = 1
		a.frameAnimChrome = false
		a.drawElement(lay, &el, int16(k))
		if a.frameAnimChrome {
			t.Errorf("%s on a PAUSED clock noted the animation census — the client would sit at the "+
				"animation cadence on a frame the user deliberately froze, and there is nothing on "+
				"screen to explain why the fans are on", k)
		}
	}

	// The second site: an animated PAGE, which notes with no effect attached at all.
	page := fxTestElement(theme.FXNone)
	page.kind, page.page, page.loop = theme.ElemImage, fxTestPage(t, fxTestStore(t), 4, 50*time.Millisecond), true
	page.clock = 1
	a.frameAnimChrome = false
	if got := a.elementFrame(&page); got == nil {
		t.Fatal("elementFrame returned nil for a four-frame page")
	}
	if a.frameAnimChrome {
		t.Error("a LOOPING page on a paused clock noted the census — elementFrameIndex reports live=true " +
			"for a loop unconditionally, so this is the second site the scrub pins the pacer from")
	}

	// And the guard is not a blanket mute: the SAME element on the shared (running)
	// clock still notes, or the gate above would pass with the census deleted.
	live := fxTestElement(theme.FXPulse)
	live.clock = 0
	a.themeAt = time.Now().Add(-held)
	a.frameAnimChrome = false
	a.drawElement(lay, &live, 0)
	if !a.frameAnimChrome {
		t.Fatal("the same effect on a RUNNING clock did not note the census — this gate would pass with " +
			"NoteAnimating deleted outright")
	}
}

// TestPausedClockStillAppliesItsTerms is the other half, and it is the half a naive
// fix breaks. Holding the frame is the entire point of a scrub, so the resolver must
// stay untouched: clamping elapsed, or making a paused clock resolve neutral, blanks
// the very frame the scrub exists to inspect.
func TestPausedClockStillAppliesItsTerms(t *testing.T) {
	a, lay, seen, restore := fxDrawRig(t)
	defer restore()
	a.themeAt = time.Now()
	a.beginThemeFXPass()

	// A drift is pure geometry, so its held terms are visible as a moved rect.
	const held = time.Duration(fxTestPeriodMs) * time.Millisecond / 4
	a.themeClocks[1] = themeClock{paused: true, frozen: held}

	el := fxTestElement(theme.FXDrift)
	el.clock = 1
	base := el.r
	a.drawElement(lay, &el, 3)
	if len(*seen) != 1 {
		t.Fatalf("the recorder saw %d painter calls, want 1 — a paused element must still DRAW", len(*seen))
	}
	if el.r != base {
		t.Errorf("the baked row was left mutated (%+v -> %+v) — the terms are applied and RESTORED, "+
			"paused or not", base, el.r)
	}
	// The same clock, unpaused, is at exactly the same position: a scrub holds a real
	// frame of the animation rather than a neutral one.
	a.themeClocks[1] = themeClock{}
	a.themeAt = time.Now().Add(-held)
	free := fxTestElement(theme.FXDrift)
	free.clock = 1
	a.drawElement(lay, &free, 4)
	if len(*seen) != 2 {
		t.Fatalf("the recorder saw %d painter calls, want 2", len(*seen))
	}
}

// TestPausedClockGuardIsFunnelledNotScattered is the encapsulation half: the census
// call on the element path exists in exactly ONE place, so a future draw site cannot
// reintroduce the defect by calling NoteAnimating directly.
func TestPausedClockGuardIsFunnelledNotScattered(t *testing.T) {
	body := funcBodyInPackage(t, "func (a *App) noteElementAnimating(")
	if !strings.Contains(body, "a.clockPaused(") || !strings.Contains(body, "a.NoteAnimating()") {
		t.Fatal("noteElementAnimating no longer both checks clockPaused and notes the census — it is the " +
			"whole guard, and every gate above drives it")
	}
	for _, file := range []string{"themeelements.go", "themeclock.go", "themeelementbake.go"} {
		src := readPackageFile(t, file)
		// The ONE legitimate call lives inside noteElementAnimating (themeclock.go).
		want := 0
		if file == "themeclock.go" {
			want = 1
		}
		if got := strings.Count(src, "a.NoteAnimating()"); got != want {
			t.Errorf("%s calls a.NoteAnimating() %d times, want %d — every census call on the element "+
				"path must go through noteElementAnimating, or a paused clock pins the frame rate from "+
				"whichever site forgot", file, got, want)
		}
	}
}

// readPackageFile reads one production file of this package for a source census.
func readPackageFile(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("reading %s: %v", name, err)
	}
	return string(b)
}
