package ui

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"testing"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/render"
)

// The #21 theme-parity arc's two oracles.
//
// Almost every commit in that arc claims "the classic layout is untouched". The
// tree could not prove it: the themed alloc gate (wholescreenalloc_test.go
// TestDrawCourtroomThemedZeroAlloc) proves the THEMED branch ran, and nothing
// proved the CLASSIC branch did not move.
//
// (a) TestClassicDispatchFence is ALWAYS ON. It pins the structural claim the
//     whole arc leans on: with no theme rects staged, drawCourtroom must take
//     the classic branch (screens.go:1700-1706) and must not arm any themed-only
//     latch. Without it a commit that accidentally validated a themeless layout
//     cache would reroute every un-themed player through the themed path and no
//     test would notice.
//
// (b) TestClassicFrameDigest is opt-in (ASYNCAO_GOLDEN=<path>). It hashes the
//     pixels of a settled classic courtroom frame so a "themed-only" commit can
//     be PROVEN not to have moved a classic pixel rather than asserted to. It is
//     deliberately NOT a checked-in golden: the digest folds in the host's
//     freetype build, so the only meaningful comparison is same-machine,
//     before-versus-after.

// classicDigestW / classicDigestH size the digest frame. 1280x720 is the same
// geometry every whole-screen alloc gate measures, so both oracles describe the
// same screen; another size would wrap text differently and produce an unrelated
// digest.
const (
	classicDigestW = 1280
	classicDigestH = 720
)

// classicDigestFrames is how many frames run before the pixels are read. The
// staged app negative-caches its asset misses and grows the label atlas over the
// first few passes; this is far past that and still cheap enough to run per
// commit.
const classicDigestFrames = 40

// TestClassicDispatchFence pins the classic/themed fork at screens.go:1700.
func TestClassicDispatchFence(t *testing.T) {
	a, cleanup := stageSettledCourtroom(t)
	defer cleanup()

	if a.themeRects != nil {
		t.Fatal("fixture regression: stageSettledCourtroom must stage NO theme rects")
	}
	a.drawCourtroom(classicDigestW, classicDigestH)

	// themeWindowLayout returns &a.themeLay, so this IS the value the dispatch
	// branched on one call earlier.
	if a.themeLay.valid {
		t.Error("a themeless frame validated the themed layout cache — drawCourtroom would dispatch to drawCourtroomThemed")
	}
	if a.toolboxThemeRectOn {
		t.Error("toolboxThemeRectOn armed on a classic frame — only the themed path may set it")
	}
}

// captureClassicDigest renders classicDigestFrames settled classic frames into an
// offscreen target and returns the hex SHA-256 of the last one's pixels.
func captureClassicDigest(t *testing.T, a *App) string {
	t.Helper()
	ct, err := render.NewCaptureTarget(a.ctx.Ren, classicDigestW, classicDigestH)
	if err != nil {
		t.Skipf("render targets unavailable: %v", err)
	}
	defer ct.Close()

	var last string
	for i := 0; i < classicDigestFrames; i++ {
		frame, cerr := ct.Capture(a.ctx.Ren, func(sdl.Rect) {
			a.drawCourtroom(classicDigestW, classicDigestH)
		})
		if cerr != nil {
			t.Skipf("capture: %v", cerr)
		}
		if i == classicDigestFrames-1 {
			h := sha256.Sum256(frame.Pix)
			last = hex.EncodeToString(h[:])
		}
	}
	return last
}

// TestClassicFrameDigest records or verifies the settled classic frame.
//
// Usage, from the repo root, BEFORE and AFTER a commit that claims classic
// parity:
//
//	$env:ASYNCAO_GOLDEN = "$env:TEMP\asyncao-classic.golden"
//	go test -run TestClassicFrameDigest -count=1 ./internal/ui   # records
//	<make the change>
//	go test -run TestClassicFrameDigest -count=1 ./internal/ui   # verifies
//
// Unset, it skips — CI never runs it, because the digest folds in the host's
// font rasteriser.
func TestClassicFrameDigest(t *testing.T) {
	path := os.Getenv("ASYNCAO_GOLDEN")
	if path == "" {
		t.Skip("set ASYNCAO_GOLDEN=<file> to record/verify the classic-frame digest")
	}
	a, cleanup := stageSettledCourtroom(t)
	defer cleanup()

	got := captureClassicDigest(t, a)
	// Self-check FIRST: a second run must agree with the first, or the frame is
	// not settled and any golden mismatch below would be noise rather than a
	// regression.
	if again := captureClassicDigest(t, a); again != got {
		t.Fatalf("the classic frame is not deterministic (%s then %s) — a time-varying draw shipped; fix that before trusting any golden", got, again)
	}

	want, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		if werr := os.WriteFile(path, []byte(got), 0o644); werr != nil {
			t.Fatal(werr)
		}
		t.Logf("recorded classic-frame digest %s → %s", got, path)
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	if string(want) != got {
		t.Fatalf("classic frame changed: golden %s, now %s — a commit that claimed themed-only parity moved a classic pixel", string(want), got)
	}
}
