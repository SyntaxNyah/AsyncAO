package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
)

// TestWarmKeeperSkipsAConclusivelyMissingDesk pins the half of #27's fix that was
// missing: healScenery's desk arm already refuses to re-demand a base the
// TextureStore marked conclusively absent (a 404'd base can never become
// Contains, so it re-entered the demand arm forever), and keepSceneAssetsWarm —
// the same heal, on the drawn AND the minimized path — had no such gate. That is
// half the 2026-08-09 field cadence: one background that ships no <pos>_overlay
// re-demanded on every tick of every message.
//
// The nil-in-test Manager is the tripwire: a re-demand panics. The second arm is
// the load-bearing half of the proof — the SAME scene, un-marked, must still
// reach the Manager, so the gate is scoped to conclusive misses and has not
// quietly become a blanket "never heal the desk".
func TestWarmKeeperSkipsAConclusivelyMissingDesk(t *testing.T) {
	ren, cleanup := newCaptureHarness(t)
	defer cleanup()

	newRoom := func(t *testing.T) (*App, *render.TextureStore) {
		t.Helper()
		store, err := render.NewTextureStore(ren)
		if err != nil {
			t.Skipf("texture store unavailable: %v", err)
		}
		a := testTabApp(t)
		a.d.Store = store
		a.room = &courtroom.Courtroom{}
		a.room.Scene.ShowDesk = true
		a.room.Scene.DeskBase = "warm://botc-townsquare/evening_overlay"
		a.sceneWarmLastDemand = time.Time{} // throttle wide open
		return a, store
	}

	marked, store := newRoom(t)
	store.MarkMissing(marked.room.Scene.DeskBase)
	marked.keepSceneAssetsWarm() // gated: must not touch the nil Manager

	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("an UNMARKED absent desk must still re-demand — the gate must key on IsMissing, not skip every desk")
			}
		}()
		unmarked, _ := newRoom(t)
		unmarked.keepSceneAssetsWarm()
	}()
}

// TestCharINIURLGoesThroughTheCharacterFolderSeam pins that char.ini is spelled
// by the URLBuilder like every other asset of the same character. It used to be
// hand-built from Origin()+"characters/"+strings.ToLower(name), which bypassed
// all three of the seam's jobs: the nested identity (2026-08-09 field report),
// the casing power-user setting (char.ini 404'd outright on a capitalised-folder
// server), and per-segment escaping (a spaced name was keyed unescaped, so the
// char.ini cache key did not match the folder its sprites used).
func TestCharINIURLGoesThroughTheCharacterFolderSeam(t *testing.T) {
	const origin = "https://cdn/base/"
	a := testTabApp(t)

	a.urls = courtroom.NewURLBuilder(origin)
	if got, want := a.charINIURL("SG/Faris NyanNyan"), origin+"characters/sg/faris%20nyannyan/char.ini"; got != want {
		t.Errorf("nested identity: charINIURL = %q, want %q", got, want)
	}
	if got := a.charINIURL("Phoenix Wright"); got != origin+"characters/phoenix%20wright/char.ini" {
		t.Errorf("spaced identity: charINIURL = %q", got)
	}
	// Always under the same folder the character's art is fetched from — the
	// property that makes "one minting seam" mean anything.
	for _, name := range []string{"Phoenix Wright", "SG/Faris NyanNyan", "phoenix"} {
		if folder := a.urls.CharFolder(name); !strings.HasPrefix(a.charINIURL(name), folder) {
			t.Errorf("%q: char.ini %q is not under the character folder %q", name, a.charINIURL(name), folder)
		}
	}

	a.urls = courtroom.NewURLBuilder(origin).WithCharCase(courtroom.CharCaseTitle)
	if got, want := a.charINIURL("phoenix wright"), origin+"characters/Phoenix%20Wright/char.ini"; got != want {
		t.Errorf("title-case server: charINIURL = %q, want %q", got, want)
	}
}
