package ui

// Pair-panel offset rows: playtest regression pins (Nightingale). The rows
// accept edits three ways (typing, −/+ buttons, mousewheel); while the text
// field is FOCUSED it displays the edit buffer, so a mouse edit must refresh
// that buffer — wheeling never blurs, and the row froze at its old number
// until a click-off ("stay at 0 or whatever value until I click off").

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
)

func testOffsetApp(t *testing.T) *App {
	t.Helper()
	ren, cleanup := newCaptureHarness(t)
	t.Cleanup(cleanup)
	ctx, err := NewCtx(ren)
	if err != nil {
		t.Skipf("kit unavailable: %v", err)
	}
	prefs, err := config.New(filepath.Join(t.TempDir(), config.PrefsFileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = prefs.Close() })
	a := &App{ctx: ctx}
	a.d.Prefs = prefs
	a.resetSessionState()
	return a
}

// offsetFrame drives one frame of the offset row at (10,10): field ~(96..152),
// "+" button ~(186..210), all rows y=10..34.
func offsetFrame(a *App, val *int, mx, my int32, click bool, wheel int32) {
	a.ctx.BeginFrame(16 * time.Millisecond)
	a.ctx.mouseX, a.ctx.mouseY = mx, my
	a.ctx.clicked = click
	a.ctx.wheelY = wheel
	if next := a.offsetControl("pairoffx", 10, 10, "Offset X %", *val, &a.pairOffXText); next != *val {
		*val = next
	}
}

// TestOffsetControlWheelRefreshesFocusedBuffer pins the fix: wheel over the
// row with the field focused updates the DISPLAYED buffer immediately (the
// wheel never blurs, so the old code froze the display until a click-off).
func TestOffsetControlWheelRefreshesFocusedBuffer(t *testing.T) {
	a := testOffsetApp(t)
	val := 0
	offsetFrame(a, &val, 120, 20, true, 0) // click INSIDE the field -> focus
	if a.ctx.focusID != "pairoffx" {
		t.Fatalf("field click must focus it, focusID=%q", a.ctx.focusID)
	}
	offsetFrame(a, &val, 120, 20, false, 1) // wheel up over the row, still focused
	if val != offsetStep {
		t.Fatalf("wheel must step the value, got %d want %d", val, offsetStep)
	}
	if a.ctx.focusID != "pairoffx" {
		t.Fatalf("wheeling must not blur, focusID=%q", a.ctx.focusID)
	}
	if want := "5"; a.pairOffXText != want {
		t.Errorf("focused buffer must mirror the wheel edit, buf=%q want %q", a.pairOffXText, want)
	}
}

// TestOffsetControlButtonEditShowsNextFrame pins the −/+ path: the click blurs
// the field (click outside it) and the next frame's mirror shows the value.
func TestOffsetControlButtonEditShowsNextFrame(t *testing.T) {
	a := testOffsetApp(t)
	val := 0
	offsetFrame(a, &val, 120, 20, true, 0) // focus the field
	offsetFrame(a, &val, 190, 20, true, 0) // click "+"
	if val != offsetStep {
		t.Fatalf("+ must step the value, got %d want %d", val, offsetStep)
	}
	offsetFrame(a, &val, 300, 200, false, 0) // idle frame: unfocused mirror
	if want := "5"; a.pairOffXText != want {
		t.Errorf("buffer must show the button edit, buf=%q want %q", a.pairOffXText, want)
	}
}

// TestOffsetControlTypingKeepsPartialInput pins that the mouse-edit refresh
// didn't break typing: a partial "-" in the focused field survives the frame
// (no mouse edit happened, so nothing may stomp the buffer).
func TestOffsetControlTypingKeepsPartialInput(t *testing.T) {
	a := testOffsetApp(t)
	val := 0
	offsetFrame(a, &val, 120, 20, true, 0) // focus the field
	a.ctx.BeginFrame(16 * time.Millisecond)
	a.ctx.mouseX, a.ctx.mouseY = 120, 20
	a.ctx.typed = "-" // like SDL text input would deliver
	if next := a.offsetControl("pairoffx", 10, 10, "Offset X %", val, &a.pairOffXText); next != val {
		val = next
	}
	if a.pairOffXText != "0-" && a.pairOffXText != "-0" && a.pairOffXText != "-" {
		t.Errorf("partial typed input must survive, buf=%q", a.pairOffXText)
	}
	if val != 0 {
		t.Errorf("an unparseable partial must not commit, val=%d", val)
	}
}
