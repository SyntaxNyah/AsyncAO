package ui

import (
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
)

// TestCharMetaStaleMarkerRefetches pins #69: a char.ini result dropped on channel
// overflow leaves an in-flight (done=false) marker. Once that marker goes stale,
// charMetaFor must re-arm the fetch instead of returning the dead marker — a
// character whose result was dropped would otherwise hold their blips/skin/effects
// for the whole session.
func TestCharMetaStaleMarkerRefetches(t *testing.T) {
	a, cleanup := roomFactoryApp(t)
	defer cleanup()
	a.urls = courtroom.NewURLBuilder(gateOrigin)
	a.charMetaRes = make(chan charMetaFetch, charMetaResCap)

	url := a.charINIURL("Phoenix")

	// First call arms a fetch and records a fresh in-flight marker.
	_ = a.charMetaFor("Phoenix")
	if a.charMetaCache[url].done || a.charMetaCache[url].stamp.IsZero() {
		t.Fatalf("first miss must arm an in-flight marker: %+v", a.charMetaCache[url])
	}

	// Simulate a result dropped on channel overflow: the marker is left done=false
	// with a stale stamp.
	a.charMetaCache[url] = charMeta{}

	// The next call must re-arm (fresh stamp), not hand back the dead marker.
	_ = a.charMetaFor("Phoenix")
	if a.charMetaCache[url].stamp.IsZero() {
		t.Fatal("a stale in-flight marker was returned without re-arming the fetch")
	}
	if a.charMetaCache[url].done {
		t.Fatal("a re-armed fetch must still be an in-flight marker, not settled")
	}

	// Drain the two in-flight fetch results so the goroutines read a.d.Manager
	// BEFORE teardown (a fetch that runs after cleanup reads a torn-down Manager
	// and panics). Local-mode fetch of a non-http origin settles immediately.
	deadline := time.After(5 * time.Second)
	for i := 0; i < 2; i++ {
		select {
		case <-a.charMetaRes:
		case <-deadline:
			t.Fatal("char.ini fetch goroutine did not settle")
		}
	}
}
