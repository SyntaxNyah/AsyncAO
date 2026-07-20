package ui

import (
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/render"
)

// TestResumeArmGatedByDuck pins ARM 1 (the confirmed live bug): applyResumeDuck's
// swap branch must arm the delivery-await ONLY when there is a real duck to hold
// (musicTabDucked). Under MusicAcrossTabs ON the stream is deliberately audible
// (ducked==false), so arming an await there is pure liability: a stalled/404'd/never-
// delivered destination track would trip settleAwaitedMusic's never-arrives timeout,
// whose awaitTimeoutStop heals the audible continuity stream to SILENCE — killing the
// very stream the toggle exists to keep playing.
func TestResumeArmGatedByDuck(t *testing.T) {
	const url = "http://cdn/dest.opus"

	// ON path: nothing ducked (the audible continuity stream). The swap branch must NOT
	// arm an await — and the stream stays audible (no tab-duck manufactured).
	a := testTabApp(t)
	a.musicTabDucked = false
	a.applyResumeDuck(false, url)
	if a.musicAwaitURL != "" {
		t.Errorf("ON (no duck): swap resume must NOT arm an await, got %q", a.musicAwaitURL)
	}
	if !a.musicAwaitSince.IsZero() {
		t.Error("ON (no duck): no await → no timeout stamp")
	}
	if a.musicTabDucked {
		t.Error("ON (no duck): must not manufacture a duck (stream stays audible)")
	}

	// And with a real (disabled) device, advancing the clock past the timeout must be a
	// pure no-op: settleAwaitedMusic short-circuits on the empty await, so it can never
	// StopMusic the audible stream. (This is the live-bug guard: pre-fix an await armed
	// here, and past 10s awaitTimeoutStop silenced the stream.)
	a.d.Audio = &render.Audio{} // disabled: CurrentMusicURL "" is safe
	base := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	a.frameNow = base.Add(2 * musicAwaitTimeout)
	a.settleAwaitedMusic()
	if a.musicAwaitURL != "" || a.musicTabDucked {
		t.Error("ON (no duck): settleAwaitedMusic must no-op — no await to time out, stream never silenced")
	}

	// OFF contrast: a real duck IS held (backgrounded, acoustically isolated). The swap
	// branch must arm the await so the duck lifts only once THIS track is delivered — the
	// wrong-song-during-fetch guard the swap path exists for. This preserves
	// TestMusicResumeDuckDecision's ducked-true case.
	b := testTabApp(t)
	b.musicTabDucked = true
	b.applyResumeDuck(false, url)
	if b.musicAwaitURL != url {
		t.Errorf("OFF (ducked): swap resume must arm the await, got %q want %q", b.musicAwaitURL, url)
	}
	if b.musicAwaitSince.IsZero() {
		t.Error("OFF (ducked): an armed await needs its timeout stamp")
	}
	if !b.musicTabDucked {
		t.Error("OFF (ducked): the duck must hold until delivery")
	}
}

// TestOwnerMusicInputs pins ARM 3's pure owner-volume resolution: the single music
// stream is scaled by the OWNER tab's per-server (master, music) when a DIFFERENT tab
// is active and the owner has a profile — else it stays the active tab's. This is the
// fix for "active tab per-server music=0 silences the backgrounded owner's continuity
// stream" (a second "toggle does nothing" path).
func TestOwnerMusicInputs(t *testing.T) {
	const active = "wss://active:2096"
	const owner = "wss://owner:2096"

	// No owner set: the active tab's values pass through unchanged.
	a := testTabApp(t)
	a.serverKey = active
	a.musicOwnerKey = ""
	if m, mu := a.ownerMusicInputs(100, 0); m != 100 || mu != 0 {
		t.Errorf("unset owner → active pass-through, got (%d,%d) want (100,0)", m, mu)
	}

	// Owner == active tab: still pass-through (the common single-owner case).
	a.musicOwnerKey = active
	if m, mu := a.ownerMusicInputs(80, 55); m != 80 || mu != 55 {
		t.Errorf("owner==active → active pass-through, got (%d,%d) want (80,55)", m, mu)
	}

	// Distinct owner WITH a per-server profile (music=100), active tab music=0: the music
	// channel must follow the OWNER, so the stream is audible despite the active tab's 0.
	a.musicOwnerKey = owner
	a.d.Prefs.SetServerAudioVolumes(owner, 100, 100, 70, 60) // enables the owner profile
	if m, mu := a.ownerMusicInputs(100, 0); mu != 100 || m != 100 {
		t.Errorf("distinct owner (music=100) must govern, got (%d,%d) want master=100 music=100", m, mu)
	}

	// Owner profile with music=0: the stream is deterministically silent (owner chose 0).
	a.d.Prefs.SetServerAudioVolumes(owner, 100, 0, 70, 60)
	if _, mu := a.ownerMusicInputs(100, 100); mu != 0 {
		t.Errorf("owner music=0 must silence the stream, got music=%d want 0", mu)
	}

	// Distinct owner WITHOUT a profile: graceful fallback to the active tab's values.
	a.musicOwnerKey = "wss://unprofiled:2096"
	if m, mu := a.ownerMusicInputs(90, 44); m != 90 || mu != 44 {
		t.Errorf("unprofiled owner → active fallback, got (%d,%d) want (90,44)", m, mu)
	}
}

// TestOwnerClearedOnStop pins that a ~stop clears musicOwnerKey (so a later volume
// compose can't scale a nonexistent stream by a stale owner's level) while a real
// track stamps the active tab as owner. Exercised through the active-tab EventMusic
// handler (handleSessionEvents), the single site that stamps/clears the owner.
func TestOwnerClearedOnStop(t *testing.T) {
	a := testTabApp(t)
	a.serverName, a.serverKey = "Srv", "wss://srv:2096"
	a.sess = courtroom.NewRehearsalSession("", []string{"Phoenix"})

	// A real track: the active tab becomes the audible owner.
	a.sess.MusicTrack = "Trial.opus"
	a.handleSessionEvents([]courtroom.Event{{Kind: courtroom.EventMusic, Text: "Trial.opus"}})
	if a.musicOwnerKey != "wss://srv:2096" {
		t.Errorf("a real track must stamp the active tab as owner, got %q", a.musicOwnerKey)
	}

	// A ~stop clears MusicTrack; the handler must drop the owner stamp with it.
	a.sess.MusicTrack = ""
	a.handleSessionEvents([]courtroom.Event{{Kind: courtroom.EventMusic, Text: "~stop.mp3"}})
	if a.musicOwnerKey != "" {
		t.Errorf("a ~stop must clear the owner stamp, got %q", a.musicOwnerKey)
	}
}

// TestBackgroundMusicFollow pins ARM 2's pure decision: a backgrounded tab's DJ /play
// should switch the single AUDIBLE stream to the new track ONLY when MusicAcrossTabs is
// ON, a device exists, the stream's owner IS this tab, and the tab has a real track.
// Under OFF (silent-isolated) or when another tab owns the stream, it's bookkeeping only
// — no audible change. Uses a disabled render.Audio (the a.d.Audio != nil guard) and
// inspects the pure return, so no live mixer is needed.
func TestBackgroundMusicFollow(t *testing.T) {
	const bgKey = "wss://bg:2096"
	newSession := func(track string) *sessionState {
		s := &sessionState{serverName: "BG", serverKey: bgKey, urls: courtroom.NewURLBuilder("https://cdn.example/base/")}
		s.sess = courtroom.NewRehearsalSession("", []string{"Phoenix"})
		s.sess.MusicTrack = track // HandlePacket already advanced this on the real path
		return s
	}

	// ON + this tab owns the audible stream + a real track: FOLLOW (switch to its URL).
	a := testTabApp(t)
	a.d.Audio = &render.Audio{}
	a.d.Prefs.SetMusicAcrossTabs(true)
	a.musicOwnerKey = bgKey
	s := newSession("Battle.opus")
	url, ok := a.backgroundMusicFollow(s)
	if !ok || url != s.urls.MusicURL("Battle.opus") {
		t.Errorf("ON + owner + track must follow: got (%q,%v)", url, ok)
	}

	// OFF: bookkeeping only — never switch the (silent-isolated) stream.
	b := testTabApp(t)
	b.d.Audio = &render.Audio{}
	b.d.Prefs.SetMusicAcrossTabs(false)
	b.musicOwnerKey = bgKey
	if _, ok := b.backgroundMusicFollow(newSession("Battle.opus")); ok {
		t.Error("OFF must not switch the stream (acoustic isolation)")
	}

	// ON but ANOTHER tab owns the stream: don't hijack it.
	c := testTabApp(t)
	c.d.Audio = &render.Audio{}
	c.d.Prefs.SetMusicAcrossTabs(true)
	c.musicOwnerKey = "wss://other:2096"
	if _, ok := c.backgroundMusicFollow(newSession("Battle.opus")); ok {
		t.Error("ON but not the owner: must not switch a foreign-owned stream")
	}

	// ON + owner but a ~stop left MusicTrack empty: nothing to switch to.
	d := testTabApp(t)
	d.d.Audio = &render.Audio{}
	d.d.Prefs.SetMusicAcrossTabs(true)
	d.musicOwnerKey = bgKey
	if _, ok := d.backgroundMusicFollow(newSession("")); ok {
		t.Error("~stop (empty track) must not switch")
	}
}

// TestRouteBackgroundEventMusicPreservesLatch pins that adding the EventMusic case to
// routeBackgroundEvent leaves the deadReason kick/ban latch (another change's landed
// work in the same func) untouched: a background EventMusic updates only music state,
// and an EventDisconnect still marks the tab dead with its reason for the activation
// dialog.
func TestRouteBackgroundEventMusicPreservesLatch(t *testing.T) {
	a := testTabApp(t)
	a.d.Audio = &render.Audio{}
	a.d.Prefs.SetMusicAcrossTabs(true)
	tab := &courtTab{state: sessionState{serverName: "BG", serverKey: "wss://bg:2096", urls: courtroom.NewURLBuilder("https://cdn.example/base/")}}
	tab.state.sess = courtroom.NewRehearsalSession("", []string{"Phoenix"})
	tab.state.sess.MusicTrack = "Song.opus"
	a.musicOwnerKey = "wss://bg:2096"

	// A background music change must not mark the tab dead or set a reason.
	a.routeBackgroundEvent(tab, courtroom.Event{Kind: courtroom.EventMusic, Text: "Song.opus", Loop: true})
	if tab.dead || tab.deadReason != "" {
		t.Errorf("EventMusic must not touch the dead latch, got dead=%v reason=%q", tab.dead, tab.deadReason)
	}

	// The deadReason latch still fires for a kick/ban in the same func.
	a.routeBackgroundEvent(tab, courtroom.Event{Kind: courtroom.EventDisconnect, Text: "Kicked: spam"})
	if !tab.dead || tab.deadReason != "Kicked: spam" {
		t.Errorf("EventDisconnect must still latch dead+reason, got dead=%v reason=%q", tab.dead, tab.deadReason)
	}
}
