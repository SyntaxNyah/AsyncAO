package ui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

// TestFontLoadingWakesAnIdleScreen pins the fix for labels that stayed boxes on a
// screen nobody touched. The render loop draws only when something happens, and a
// label asks for the fallback faces while it draws — after this frame's font polls
// have run. Both halves must wake the loop: the request, so the next frame starts
// the load, and the finished load, so the next frame installs the faces.
func TestFontLoadingWakesAnIdleScreen(t *testing.T) {
	t.Setenv("SDL_VIDEODRIVER", "dummy")
	if err := sdl.Init(sdl.INIT_VIDEO | sdl.INIT_EVENTS); err != nil {
		t.Skipf("SDL unavailable: %v", err)
	}
	defer sdl.Quit()
	EnsureWakeEvent()
	drainWake := func() bool {
		woke := false
		for ev := sdl.PollEvent(); ev != nil; ev = sdl.PollEvent() {
			if IsWakeEvent(ev) {
				woke = true
			}
		}
		return woke
	}
	drainWake() // start from an empty queue

	c := &Ctx{}
	c.noteScript("★ Phone Book")
	if !drainWake() {
		t.Error("a label asking for the fallback faces did not wake the loop")
	}
	c.noteScript("「Edgeworth」") // the broad request is already raised; this one is the CJK tier
	if !drainWake() {
		t.Error("a label asking for the CJK faces did not wake the loop")
	}
	c.noteUncovered('ⵜ')
	if !drainWake() {
		t.Error("a rune queued for the census did not wake the loop")
	}

	const fileMode = 0o600
	font := filepath.Join(t.TempDir(), "face.ttf")
	if err := os.WriteFile(font, []byte("font bytes"), fileMode); err != nil {
		t.Fatal(err)
	}
	ch := make(chan [][]byte, 1)
	loadFontFiles([]string{font, filepath.Join(t.TempDir(), "missing.ttf")}, ch)
	if got := <-ch; len(got) != 1 {
		t.Errorf("loaded %d faces, want the 1 that exists", len(got))
	}
	if !drainWake() {
		t.Error("finished font load did not wake the loop")
	}
}

// TestSkipFrameRendersPendingFontWork pins the other half: a wake event is not a
// reason to render, so a static screen that skips frames must still render one
// while font work is pending — a request a label raised, or a finished load to
// install — and go back to skipping once it is handled. The request is raised once
// per session: raised again on every draw, a machine without the faces would keep
// the loop rendering forever.
func TestSkipFrameRendersPendingFontWork(t *testing.T) {
	a := expApp(t)
	if !a.SkipFrame(true, false) {
		t.Fatal("precondition: a static lobby must skip")
	}

	a.ctx.noteScript("★ Phone Book")
	if a.SkipFrame(true, false) {
		t.Error("a label's font request did not get a frame to start the load")
	}
	if !a.ctx.TakeWantsFallback() {
		t.Fatal("the label did not raise a font request")
	}
	if !a.SkipFrame(true, false) {
		t.Error("with the request taken, the static lobby must skip again")
	}
	a.ctx.noteScript("★ Phone Book")
	if !a.SkipFrame(true, false) {
		t.Error("the request was raised a second time — on a machine without the faces the loop would never rest")
	}

	a.fallbackFontRes = make(chan [][]byte, 1)
	a.fallbackFontRes <- [][]byte{[]byte("font bytes")}
	if a.SkipFrame(true, false) {
		t.Error("a finished font load did not get a frame to install it")
	}
}
