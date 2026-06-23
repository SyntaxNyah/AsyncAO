package ui

import (
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
)

// TestRemoteProfileThroughRoom pins #101 slice 2 end-to-end: a profile-marked IC message
// makes the room remember that character's profile, and profileFor surfaces it for the
// player-list row — while a player we've received nothing for shows no card.
func TestRemoteProfileThroughRoom(t *testing.T) {
	a := &App{}
	a.room = newRoomForTest(t)
	prof := courtroom.WireProfile{Pronouns: "they/them", Tag: "objection enjoyer"}

	a.room.HandleEvent(courtroom.Event{Kind: courtroom.EventMessage, Message: msgFor(3, "Phoenix", "hold it"+prof.EncodeMarker())})
	a.room.SkipToIdle()

	got, ok := a.profileFor(&areaPlayer{name: "Phoenix"}, false)
	if !ok {
		t.Fatal("profileFor found no remote profile after a profile-marked message")
	}
	if got.Pronouns != prof.Pronouns || got.Tag != prof.Tag {
		t.Errorf("remote profile = {%q,%q}, want {%q,%q}", got.Pronouns, got.Tag, prof.Pronouns, prof.Tag)
	}
	if _, ok := a.profileFor(&areaPlayer{name: "Edgeworth"}, false); ok {
		t.Error("profileFor returned a profile for a player we've received none for")
	}
}

// TestProfileMarkerKeepsStyle pins the coexistence guard: a profile-only message from a
// styled speaker must NOT clear their sprite style (both ride the same zero-width
// channel, and a profile-frame misread as a style clear would wipe the style).
func TestProfileMarkerKeepsStyle(t *testing.T) {
	a := &App{}
	a.room = newRoomForTest(t)
	style := courtroom.SpriteStyle{Tint: true, R: 9, G: 9, B: 200}

	a.room.HandleEvent(courtroom.Event{Kind: courtroom.EventMessage, Message: msgFor(5, "Phoenix", "styled"+style.EncodeMarker())})
	a.room.SkipToIdle()
	if !a.room.RecalledStyle(5).Active() {
		t.Fatal("style was not remembered")
	}

	prof := courtroom.WireProfile{Pronouns: "he/him"}
	a.room.HandleEvent(courtroom.Event{Kind: courtroom.EventMessage, Message: msgFor(5, "Phoenix", "just a profile"+prof.EncodeMarker())})
	a.room.SkipToIdle()
	if !a.room.RecalledStyle(5).Active() {
		t.Error("a profile-only message wiped the speaker's sprite style")
	}
	if got, ok := a.room.RemoteProfile("Phoenix"); !ok || got.Pronouns != "he/him" {
		t.Errorf("profile not remembered alongside the style: (%+v,%v)", got, ok)
	}
}
