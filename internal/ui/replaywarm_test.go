package ui

import (
	"strconv"
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
)

// replayWarmRec builds a small FLAT recording over origin with a message (sprites +
// bg) and a music event (an Exact ref) so the pre-warm has both to enumerate.
func replayWarmRec(origin string) *sceneRecording {
	return &sceneRecording{
		Version: recordingVersion,
		Origin:  origin,
		StartBg: "courtroom",
		Events: []recEvent{
			{Kind: int(courtroom.EventMusic), Text: "Objection.opus"},
			{Kind: int(courtroom.EventMessage), Message: &protocol.ChatMessage{CharName: "Phoenix", Emote: "normal", Side: "def", Message: "Objection!"}},
		},
	}
}

// TestStartReplayWarmSkipsEmptyOrigin pins that a recording with no streamable
// origin (empty or local://) arms NO pre-warm phase — it plays exactly as before,
// with the render thread never entering the warm gate (H5's "empty origin skips").
func TestStartReplayWarmSkipsEmptyOrigin(t *testing.T) {
	a := testTabApp(t)

	a.startReplayWarm(&sceneRecording{Origin: ""})
	if a.replayWarming() {
		t.Error("an empty origin must not arm the replay pre-warm")
	}
	a.startReplayWarm(&sceneRecording{Origin: "local://mount/"})
	if a.replayWarming() {
		t.Error("a non-http (local) origin must not arm the replay pre-warm")
	}
	a.startReplayWarm(nil)
	if a.replayWarming() {
		t.Error("a nil recording must not arm the replay pre-warm")
	}
}

// TestStartReplayWarmEnumeratesAndSplits pins that an http-origin recording arms the
// phase and enumerates the scene once, splitting texture refs (submitted
// incrementally) from music refs (Exact, prefetched once). Format auto-detect is
// turned OFF so no seed goroutine interposes — the split is what's under test.
func TestStartReplayWarmEnumeratesAndSplits(t *testing.T) {
	a, _ := newWarmHarness(t)
	a.d.Prefs.SetFormatAutoDetect(false) // no seed sub-phase; isolate the enumeration

	a.startReplayWarm(replayWarmRec("http://example.test/"))
	w := a.replayWarm
	if w == nil {
		t.Fatal("an http-origin recording must arm the replay pre-warm")
	}
	if w.seeding {
		t.Error("auto-detect off: the seed sub-phase must not run")
	}
	if len(w.warmRefs) == 0 {
		t.Error("no texture refs enumerated — the message's bg/desk/sprite refs are missing")
	}
	if len(w.musicRefs) == 0 {
		t.Error("no music refs enumerated — the MC event's Exact track is missing")
	}
	// Music must be an Exact URL (carries its own extension), never a probe base.
	for _, m := range w.musicRefs {
		if m.Type != assets.AssetTypeMusic || !m.Exact {
			t.Errorf("music ref %+v is not an Exact AssetTypeMusic ref", m)
		}
	}
	// pendingWarm starts equal to the full texture set (nothing submitted yet).
	if len(w.pendingWarm) != len(w.warmRefs) {
		t.Errorf("pendingWarm=%d warmRefs=%d — the whole texture set must start pending", len(w.pendingWarm), len(w.warmRefs))
	}
}

// TestTickReplayWarmConclusiveMissEndsPromptly is the H5 companion to the H3 armor:
// a FLAT replay whose every asset conclusively 404s (dead/empty CDN) must END the
// pre-warm phase promptly via the shared conclusive-miss release — NOT freeze for
// the full hard cap — so playback of a never-resolving recording still begins. Uses
// the same fresh-created + MarkMissing drive as the export armor: only the settled-
// includes-missing path can end this within the tick budget.
func TestTickReplayWarmConclusiveMissEndsPromptly(t *testing.T) {
	a, store := newWarmHarness(t)
	a.d.Prefs.SetFormatAutoDetect(false) // isolate the warm from the seed sub-phase

	// A big scene: more sprite refs than the in-flight window, so the never-settled
	// stall shape applies (the first window fills; without the miss release nothing
	// prunes and the phase would sit until the hard cap).
	rec := &sceneRecording{Version: recordingVersion, Origin: "http://example.test/", StartBg: "courtroom"}
	for i := 0; i < warmInFlightWindow+40; i++ {
		rec.Events = append(rec.Events, recEvent{
			Kind:    int(courtroom.EventMessage),
			Message: &protocol.ChatMessage{CharName: charName(i), Emote: "normal", Side: "def", Message: "x"},
		})
	}
	a.startReplayWarm(rec)
	if a.replayWarm == nil {
		t.Fatal("warm not armed")
	}
	// Fresh creation: the hard cap is 20 s away, so only the miss release can end this.
	a.replayWarm.created = time.Now()

	const tickBudget = 80
	ticks := 0
	for ; ticks < tickBudget && a.replayWarming(); ticks++ {
		a.tickReplayWarm()
		// tickReplayWarm may END the warm this very tick (the release firing is
		// the point of the test) — endReplayWarm nils a.replayWarm, so re-check
		// before touching the state or the success path panics on nil.
		w := a.replayWarm
		if w == nil {
			ticks++
			break
		}
		for base := range w.warmInFlight {
			store.MarkMissing(base) // the conclusive 404 signal (as drainWarnings relays live)
		}
	}
	if a.replayWarming() {
		t.Fatalf("replay pre-warm never ended for an all-404 scene in %d ticks — playback would stall for the full hard cap", tickBudget)
	}
	if ticks >= tickBudget {
		t.Errorf("took the full tick budget (%d) — the miss release should end an all-404 warm far sooner", tickBudget)
	}
}

// TestTickReplayWarmHardCapBackstop pins the last-resort ceiling: a scene whose refs
// neither resolve NOR report a conclusive miss must still end the phase at
// gifWarmHardCap so a replay can never wedge on the warm. Stamp creation past the cap
// and one tick must clear the phase.
func TestTickReplayWarmHardCapBackstop(t *testing.T) {
	a, _ := newWarmHarness(t) // never mark anything resident/missing: the never-settled shape
	a.d.Prefs.SetFormatAutoDetect(false)

	rec := &sceneRecording{Version: recordingVersion, Origin: "http://example.test/", StartBg: "courtroom"}
	for i := 0; i < warmInFlightWindow+10; i++ {
		rec.Events = append(rec.Events, recEvent{
			Kind:    int(courtroom.EventMessage),
			Message: &protocol.ChatMessage{CharName: charName(i), Emote: "normal", Side: "def", Message: "x"},
		})
	}
	a.startReplayWarm(rec)
	if a.replayWarm == nil {
		t.Fatal("warm not armed")
	}
	// Stamp creation just past the hard cap so the first tick's backstop fires.
	a.replayWarm.created = time.Now().Add(-gifWarmHardCap - time.Second)

	a.tickReplayWarm()
	if a.replayWarming() {
		t.Fatal("hard cap did not end a never-settling replay pre-warm — it would hang the replay")
	}
}

// TestTickReplayWarmPrefetchesMusicOnce pins that the pre-warm submits the scene's
// music refs exactly once (Exact prefetch), so sound is warm before playback without
// re-queuing a fetch every tick. The nil-safe LocalFetcher Manager makes the
// PrefetchExact a real (missing) submit; we assert the one-shot latch flips.
func TestTickReplayWarmPrefetchesMusicOnce(t *testing.T) {
	a, _ := newWarmHarness(t)
	a.d.Prefs.SetFormatAutoDetect(false)

	a.startReplayWarm(replayWarmRec("http://example.test/"))
	if a.replayWarm == nil || len(a.replayWarm.musicRefs) == 0 {
		t.Fatal("warm not armed with music refs")
	}
	if a.replayWarm.musicDone {
		t.Fatal("musicDone set before any tick")
	}
	a.tickReplayWarm()
	if !a.replayWarm.musicDone {
		t.Error("music prefetch one-shot never fired on the first tick")
	}
	// A second tick must not un-latch it (would re-queue the fetch each frame).
	a.tickReplayWarm()
	if a.replayWarm != nil && !a.replayWarm.musicDone {
		t.Error("music one-shot un-latched on a later tick")
	}
}

// charName gives a distinct speaker per index so SceneAssets emits distinct sprite
// bases (the same intent as mkWarmRefs, but through the real enumeration path).
func charName(i int) string {
	return "Char" + strconv.Itoa(i)
}
