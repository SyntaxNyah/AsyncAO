package render

import (
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
)

const (
	sceneryTestBudget  = 64 << 20
	sceneryTestFrames  = 3
	sceneryTestSide    = 256
	sceneryTestStep    = 16 * time.Millisecond
	sceneryTestSources = 2
	sceneryTestBg      = "live://background/court/defenseempty"
	sceneryTestDesk    = "live://background/court/defensedesk"
	sceneryTestFirst   = "live://characters/a/(a)normal"
	sceneryTestSecond  = "live://characters/b/(a)normal"
	sceneryTestOwnFile = "live://background/court/defenseempty.webp"
	sceneryTestOther   = "live://background/court/witnessempty.webp"
)

func TestTheSceneryOnScreenKeepsAnimatingWhenTheSpeakerChanges(t *testing.T) {
	ren, cleanup := newHeadlessRenderer(t)
	defer cleanup()
	store, err := NewTextureStoreBudget(ren, sceneryTestBudget)
	if err != nil {
		t.Skipf("texture store unavailable: %v", err)
	}
	defer store.Purge()
	upload := func(bases ...string) {
		t.Helper()
		for _, base := range bases {
			if err := store.Upload(base, sizedFixture(sceneryTestSide, sceneryTestFrames)); err != nil {
				t.Fatal(err)
			}
		}
	}
	upload(sceneryTestBg, sceneryTestDesk, sceneryTestFirst)
	v := NewViewport(store)
	scene := &courtroom.Scene{BackgroundBase: sceneryTestBg, DeskBase: sceneryTestDesk}
	scene.Speaker.IdleBase = sceneryTestFirst
	v.Update(scene, sceneryTestStep)
	upload(sceneryTestSecond)
	scene.Speaker.IdleBase = sceneryTestSecond
	v.Update(scene, sceneryTestStep)

	for _, base := range []string{sceneryTestBg, sceneryTestDesk, sceneryTestSecond} {
		if page, ok := store.Get(base); !ok || page.Stilled || len(page.Frames) != sceneryTestFrames {
			t.Errorf("%s on screen was cut down: ok=%v stilled=%v", base, ok, ok && page.Stilled)
		}
	}
	if page, ok := store.Get(sceneryTestFirst); !ok || !page.Stilled {
		t.Errorf("the previous speaker off screen must be cut down: ok=%v", ok)
	}
}

func TestACutDownPageNeverGrowsFramesFromAnotherFile(t *testing.T) {
	ren, cleanup := newHeadlessRenderer(t)
	defer cleanup()
	store, err := NewTextureStoreBudget(ren, sceneryTestBudget)
	if err != nil {
		t.Skipf("texture store unavailable: %v", err)
	}
	defer store.Purge()
	stream := func(file string, offset int) {
		t.Helper()
		if err := store.AppendStream(sceneryTestBg, file, streamChunk(offset, true, sceneryTestSources)); err != nil {
			t.Fatal(err)
		}
	}
	stream(sceneryTestOwnFile, 0)
	stream(sceneryTestOwnFile, 1)
	store.ReduceAnimatedExcept(nil)

	stream(sceneryTestOther, 0)
	stream(sceneryTestOwnFile, 1)
	page, ok := store.Get(sceneryTestBg)
	if !ok || page.File != sceneryTestOther || len(page.Frames) != 1 || page.Stilled {
		t.Fatalf("ok=%v file=%q frames=%d: another file must replace the page, and a late chunk of the first must not join it",
			ok, page.File, len(page.Frames))
	}
	stream(sceneryTestOther, 1)
	if page, _ := store.Get(sceneryTestBg); len(page.Frames) != sceneryTestSources {
		t.Fatalf("frames %d: the new file's own frames must still grow the page", len(page.Frames))
	}
}
