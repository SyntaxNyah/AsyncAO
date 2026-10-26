package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/veandco/go-sdl2/sdl"
)

func TestUnionRect(t *testing.T) {
	a := sdl.Rect{X: 10, Y: 20, W: 30, H: 40}   // x[10,40) y[20,60)
	b := sdl.Rect{X: 100, Y: 200, W: 50, H: 60} // x[100,150) y[200,260)
	if got, want := unionRect(a, b), (sdl.Rect{X: 10, Y: 20, W: 140, H: 240}); got != want {
		t.Fatalf("unionRect = %+v, want %+v", got, want)
	}
	// A zero-area rect contributes nothing (so a preview with no captured trigger
	// still gets a sane corridor = the box alone).
	if got := unionRect(sdl.Rect{}, b); got != b {
		t.Fatalf("unionRect(zero,b) = %+v, want %+v", got, b)
	}
	if got := unionRect(a, sdl.Rect{}); got != a {
		t.Fatalf("unionRect(a,zero) = %+v, want %+v", got, a)
	}
}

// TestSpritePreviewTravelCorridor pins the hover-preview close logic the beta surfaced:
// the box must survive the cursor travelling from the trigger cell to the bottom-right
// box, vanish if the cursor strays off that path, and close promptly once the cursor has
// reached the box and then left it.
func TestSpritePreviewTravelCorridor(t *testing.T) {
	a := testTabApp(t)
	c := a.ctx
	trigger := sdl.Rect{X: 100, Y: 100, W: 80, H: 80}
	box := sdl.Rect{X: 600, Y: 500, W: 200, H: 200}
	// corridor = unionRect(trigger, box) = x[100,800) y[100,700)
	open := func() {
		a.previewBase, a.previewEntered = "x", false
		a.previewFrameRect, a.previewTriggerRect = box, trigger
		c.clicked, c.hoverID = false, ""
	}

	// On the trigger → stays up.
	open()
	c.hoverID, c.hoverRect = "char:x", trigger
	c.mouseX, c.mouseY = trigger.X+5, trigger.Y+5
	a.closeSpritePreviewOnLeave()
	if a.previewBase == "" {
		t.Fatal("cursor on the trigger: preview must stay open")
	}

	// In the gap between trigger and box (over neither) → stays up to travel.
	c.hoverID = ""
	c.mouseX, c.mouseY = 350, 300
	a.closeSpritePreviewOnLeave()
	if a.previewBase == "" {
		t.Fatal("cursor in the travel corridor: preview must stay open")
	}

	// Off the corridor (moved away) → closes.
	c.mouseX, c.mouseY = 900, 40
	a.closeSpritePreviewOnLeave()
	if a.previewBase != "" {
		t.Fatal("cursor strayed off the travel path: preview must close")
	}

	// Reached the box → entered latches; leaving the box then closes even though the
	// cursor is back inside the corridor.
	open()
	c.hoverID = ""
	c.mouseX, c.mouseY = box.X+10, box.Y+10
	a.closeSpritePreviewOnLeave()
	if a.previewBase == "" || !a.previewEntered {
		t.Fatal("cursor in the box: must stay open and mark entered")
	}
	c.mouseX, c.mouseY = 350, 300 // in the corridor, but the box was already entered
	a.closeSpritePreviewOnLeave()
	if a.previewBase != "" {
		t.Fatal("after entering the box, leaving it must close (not held by the corridor)")
	}

	// A click always dismisses (a selection commits).
	open()
	c.hoverID, c.hoverRect = "char:x", trigger
	c.mouseX, c.mouseY = trigger.X+5, trigger.Y+5
	c.clicked = true
	a.closeSpritePreviewOnLeave()
	if a.previewBase != "" {
		t.Fatal("a click must dismiss the preview")
	}
}

// TestSpritePreviewStaleTriggerCloses pins the orphaned-trigger half of the
// frame-pacer cap-latch report: hoverID is cleared only by its own trigger's
// HoverPreview call, so once that trigger stops being drawn (drawer closed,
// emote page flipped, screen switched) the id lingers. Trusting the bare id
// pinned "over trigger" true forever and the box could never leave-close —
// close-on-leave must demand the pointer actually be ON the remembered rect.
func TestSpritePreviewStaleTriggerCloses(t *testing.T) {
	a := testTabApp(t)
	c := a.ctx
	trigger := sdl.Rect{X: 100, Y: 100, W: 80, H: 80}
	box := sdl.Rect{X: 600, Y: 500, W: 200, H: 200}
	a.previewBase, a.previewEntered = "x", false
	a.previewFrameRect, a.previewTriggerRect = box, trigger
	c.hoverID, c.hoverRect = "char:x", trigger // stale: the trigger's screen is gone
	c.mouseX, c.mouseY = 900, 40               // off the trigger, the box, and the corridor
	a.closeSpritePreviewOnLeave()
	if a.previewBase != "" {
		t.Fatal("a stale trigger id must not pin the preview open (the cap-latch bug)")
	}
}

// TestCloseSpritePreviewDisarmsDwell pins the click-commit half: a close while
// the pointer still rests on the trigger must clear the trigger id, or the
// already-elapsed dwell re-opens the box on the very next frame — the silent
// re-arm that carried a "closed" preview across a char pick into the courtroom.
func TestCloseSpritePreviewDisarmsDwell(t *testing.T) {
	a := testTabApp(t)
	a.previewBase = "x"
	a.ctx.hoverID = "char:x"
	a.closeSpritePreview()
	if a.previewBase != "" || a.ctx.hoverID != "" {
		t.Fatal("closeSpritePreview must clear the trigger id (no instant dwell re-open)")
	}
}

// TestScreenSwitchDropsOrphanPreview pins noteScreenTransition: a screen switch
// with a preview still up (pinned or not) must drop the preview, its trigger id,
// and both cached rects — with every close path living in per-screen draw tails,
// a switched-away preview has no owner: it held the event-driven loop at the
// ACTIVE cap and its ghost rect kept claiming wheel/press on the new screen.
func TestScreenSwitchDropsOrphanPreview(t *testing.T) {
	a := testTabApp(t)
	a.screen = ScreenCharSelect
	a.noteScreenTransition() // absorb the initial lobby→charselect flip
	a.previewBase, a.previewPinned = "x", true
	a.ctx.hoverID, a.ctx.hoverRect = "char:x", sdl.Rect{X: 1, Y: 1, W: 10, H: 10}
	a.previewFrameRect = sdl.Rect{X: 5, Y: 5, W: 50, H: 50}
	a.previewTriggerRect = sdl.Rect{X: 1, Y: 1, W: 10, H: 10}

	a.screen = ScreenCourtroom
	a.noteScreenTransition()
	if a.previewBase != "" || a.previewPinned || a.ctx.hoverID != "" {
		t.Fatal("a screen switch must drop the orphaned preview + pin + trigger id")
	}
	if a.previewFrameRect.W != 0 || a.previewTriggerRect.W != 0 {
		t.Fatal("a screen switch must zero the cached box/trigger rects (ghost input claim)")
	}

	// Same screen: a live preview stays untouched.
	a.previewBase = "y"
	a.noteScreenTransition()
	if a.previewBase == "" {
		t.Fatal("no switch → the live preview must stay")
	}
}

// --- char-select hover portrait (B2: "normal" is a convention, not a guarantee) ---

// previewParseWait bounds the wait for the off-thread char.ini parse in the
// portrait tests. The fetch is a local-mount file read, so it lands in
// milliseconds; the budget only has to survive a loaded CI box.
const previewParseWait = 10 * time.Second

// portraitTestApp wires a headless App (real Manager, no SDL) over a local mount
// that holds ONE character's char.ini with the given emote names, and points the
// URL builder at that mount. Returns the app and the character name.
func portraitTestApp(t *testing.T, emotes ...string) (*App, string) {
	t.Helper()
	const char = "rinnosuke morichika_fv" // the reported pack: all-webp, no "normal"
	dir := t.TempDir()
	var b strings.Builder
	b.WriteString("[Options]\nname = " + char + "\n[Emotions]\nnumber = " + fmt.Sprint(len(emotes)) + "\n")
	for i, e := range emotes {
		// Comment#Preanim#Anim#Mod — the four fields ParseCharINI requires.
		fmt.Fprintf(&b, "%d = %s#-#%s#1\n", i+1, e, e)
	}
	iniPath := filepath.Join(dir, "characters", strings.ToLower(char), "char.ini")
	if err := os.MkdirAll(filepath.Dir(iniPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(iniPath, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	local := assets.NewLocalFetcher([]string{dir})
	a := headlessProbeApp(t, local, true)
	a.urls = courtroom.NewURLBuilder(local.BaseURL())
	a.serverKey = "ws://portrait.test" // pollPreviewEmotes drops results for another tab's key
	// The real App constructor makes this one-slot result channel; the test App is
	// built bare, and a nil channel would silently swallow every parse.
	a.previewEmoteRes = make(chan previewEmoteFetch, 1)
	return a, char
}

// waitPortraitEmotes drains pollPreviewEmotes until the char.ini parse lands —
// exactly what the char-select screen does once per frame.
func waitPortraitEmotes(t *testing.T, a *App) {
	t.Helper()
	deadline := time.Now().Add(previewParseWait)
	for time.Now().Before(deadline) {
		a.pollPreviewEmotes()
		if len(a.previewAnims) > 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("the char.ini emote list never landed (pollPreviewEmotes drained nothing)")
}

// TestCharSelectPortraitFallsBackToFirstEmote is the B2 regression: the
// char-select grid hard-coded the emote name "normal", and a pack whose char.ini
// names none (SNormal/SCry/HSmug…) had NO recovery — the box stayed empty
// forever while the cell icon (char_icon.png) loaded fine, which is exactly how
// it was reported. v1.53.0 fixed the identical trap in the pair menu; this pins
// the same recovery for the grid: the optimistic "normal" probe still opens the
// box instantly, then the char.ini repoints it at the pack's FIRST real emote.
func TestCharSelectPortraitFallsBackToFirstEmote(t *testing.T) {
	a, char := portraitTestApp(t, "SNormal", "SCry")

	// Frame 1 — the hover: parse the char.ini, show the optimistic convention.
	a.ensurePreviewEmotes(char, previewEmotePortrait)
	a.setCharPortraitPreview(char)
	if want := a.urls.Emote(char, "normal", courtroom.EmoteIdle); a.previewBase != want {
		t.Fatalf("first frame must probe the conventional idle pose\n got %q\nwant %q", a.previewBase, want)
	}

	// …the parse lands (the cursor may already have left the cell, so the drain,
	// not the next hover frame, has to be what corrects the box).
	waitPortraitEmotes(t, a)
	if want := a.urls.Emote(char, "SNormal", courtroom.EmoteIdle); a.previewBase != want {
		t.Fatalf("a pack with no \"normal\" emote must fall back to its first real emote\n got %q\nwant %q", a.previewBase, want)
	}
	if got := a.previewPortraitAnim(char); got != "SNormal" {
		t.Fatalf("previewPortraitAnim = %q, want the first real emote", got)
	}
	// Re-hovering the same cell must be stable, never bounce back to "normal".
	a.setCharPortraitPreview(char)
	if want := a.urls.Emote(char, "SNormal", courtroom.EmoteIdle); a.previewBase != want {
		t.Fatalf("a later hover frame reverted the correction: got %q, want %q", a.previewBase, want)
	}
}

// TestCharSelectPortraitKeepsNormalWhenPackHasIt is the other half of the
// contract: the vast majority of packs DO ship a "normal" emote, and for those
// the char.ini must change nothing — the optimistic probe already resolved, so
// spending a second probe on a different sprite would be pure waste (and would
// swap the portrait for whatever emote happens to be listed first).
func TestCharSelectPortraitKeepsNormalWhenPackHasIt(t *testing.T) {
	// "Normal" capitalised: emote anims are lowercased into the URL, so the
	// pack's entry IS the base already probed and must be recognised as such.
	a, char := portraitTestApp(t, "cry", "Normal")

	a.ensurePreviewEmotes(char, previewEmotePortrait)
	a.setCharPortraitPreview(char)
	normal := a.urls.Emote(char, "normal", courtroom.EmoteIdle)
	waitPortraitEmotes(t, a)
	if a.previewBase != normal {
		t.Fatalf("a pack that has \"normal\" must keep the optimistic base\n got %q\nwant %q", a.previewBase, normal)
	}
	if got := a.previewPortraitAnim(char); got != "normal" {
		t.Fatalf("previewPortraitAnim = %q, want the conventional %q", got, "normal")
	}
}

// TestPortraitPreviewIgnoresOtherTabsParse pins the multi-tab guard the
// char-select grid now leans on: a parse that finishes after a tab switch
// carries the OLD server key and must be dropped whole — no emote list, and
// certainly no repointing of the new tab's preview box.
func TestPortraitPreviewIgnoresOtherTabsParse(t *testing.T) {
	a, char := portraitTestApp(t, "SNormal")
	a.ensurePreviewEmotes(char, previewEmotePortrait)
	a.setCharPortraitPreview(char)
	normal := a.previewBase
	a.serverKey = "ws://other.tab" // the user switched tabs mid-parse

	// A local-mount parse lands in single-digit milliseconds; this window is the
	// drain budget that gives it every chance to (wrongly) apply.
	const previewDropWindow = 250 * time.Millisecond
	deadline := time.Now().Add(previewDropWindow)
	for time.Now().Before(deadline) {
		a.pollPreviewEmotes()
		time.Sleep(time.Millisecond)
		if len(a.previewAnims) > 0 {
			t.Fatal("another tab's char.ini parse must be dropped, not applied")
		}
	}
	if a.previewBase != normal {
		t.Fatalf("a dropped parse must leave the preview base alone: got %q, want %q", a.previewBase, normal)
	}
}

// TestHoverPreviewToggleGatesOnlyDwell pins the playtest regression: turning
// hover-previews OFF must disable ONLY the dwell pop-up — an explicit
// right-click on a trigger still opens the preview.
func TestHoverPreviewToggleGatesOnlyDwell(t *testing.T) {
	trigger := sdl.Rect{X: 10, Y: 10, W: 50, H: 50}
	c := &Ctx{mouseX: 20, mouseY: 20}
	c.SetHoverPreview(false, 0) // previews toggle OFF

	if c.HoverPreview("emote:a", trigger) {
		t.Fatal("hover with the toggle off must never open a preview")
	}
	c.rightClicked = true
	if !c.HoverPreview("emote:a", trigger) {
		t.Fatal("right-click must open the preview even with the toggle off")
	}
	if c.hoverID != "emote:a" {
		t.Fatal("the right-click open must register the trigger (close-on-leave contract)")
	}
	// Subsequent frames (no right-click) keep the trigger alive while hovered…
	c.rightClicked = false
	if c.HoverPreview("emote:a", trigger) {
		t.Fatal("no dwell may start while the toggle is off")
	}
	if c.hoverID != "emote:a" {
		t.Fatal("an open right-click preview's trigger must stay registered while hovered")
	}
	// …and clear the moment the cursor leaves the trigger.
	c.mouseX, c.mouseY = 500, 500
	if c.HoverPreview("emote:a", trigger) {
		t.Fatal("off-trigger must not preview")
	}
	if c.hoverID != "" {
		t.Fatal("leaving the trigger must clear its id")
	}

	// Toggle ON: the dwell path arms (first frame registers, no instant open).
	c.SetHoverPreview(true, 0)
	c.mouseX, c.mouseY = 20, 20
	if c.HoverPreview("emote:a", trigger) {
		t.Fatal("the first hovered frame only arms the dwell")
	}
	if !c.HoverPreview("emote:a", trigger) {
		t.Fatal("with a zero dwell, the second frame must open")
	}
}
