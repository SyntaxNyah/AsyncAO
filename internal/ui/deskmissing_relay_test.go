package ui

import (
	"testing"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/assets"
	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
	"github.com/SyntaxNyah/AsyncAO/internal/network"
	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
)

// TestDrainWarningsRelaysDeskMissing pins the #44 bridge end to end over the REAL
// asset pipeline: a background whose position ships no desk image exhausts its whole
// candidate chain, the Manager reports it missing on the existing (droppable,
// bounded) Warning lane, and drainWarnings relays that one signal to BOTH halves of
// the fix —
//
//   - Store.MarkMissing, so the viewport's syncAnimDesk releases the desk layer
//     instead of holding the PREVIOUS room's desk forever (a 404'd base never
//     becomes resident, so the old sticky gate never rebound it);
//   - Courtroom.NotifyDeskMissing, so the live message's ShowDesk drops — AO2's
//     set_scene forces show_desk=false when the desk file does not exist
//     (../AO2-Client/src/courtroom.cpp:4628-4634).
//
// No extra network probe: the Warning rides the desk prefetch the courtroom already
// issues for every message.
func TestDrainWarningsRelaysDeskMissing(t *testing.T) {
	a := testTabApp(t)
	dir := t.TempDir() // empty pack: background/gs4/defensedesk exists in no format
	local, cleanup := wireLocalManager(t, a, dir)
	defer cleanup()
	// The LocalMode resolver walks the configured formats; point the desk type at
	// .png so the probe list is a real (and short) chain over the on-disk root.
	a.d.Prefs.SetFormatOrder(assets.AssetTypeDeskOverlay.Name(), []string{config.ExtPNG})

	// A real room with a live DeskShow message, so the relay has something to
	// re-derive (NotifyDeskMissing only touches the scene of the room whose CURRENT
	// desk base matches).
	sess := courtroom.NewSession(func(protocol.Packet) error { return nil }, "test-hdid")
	sess.Background = "gs4"
	room := courtroom.NewCourtroom(courtroom.NewURLBuilder(local.BaseURL()), a.d.Manager, sess, nil)
	a.room = room
	room.HandleEvent(courtroom.Event{Kind: courtroom.EventMessage, Message: &protocol.ChatMessage{
		CharName: "Phoenix", Emote: "normal", Side: "def", DeskMod: protocol.DeskShow,
	}})
	room.Update(0) // dequeue: begin() derives Scene.DeskBase and prefetches it
	deskBase := room.Scene.DeskBase
	if deskBase == "" || !room.Scene.ShowDesk {
		t.Fatalf("test setup: want a live visible desk, got base=%q ShowDesk=%v", deskBase, room.Scene.ShowDesk)
	}

	// The LocalFetcher pipeline is synchronous but pool-scheduled; poll until the
	// conclusive-404 Warning has been drained and relayed.
	deadline := time.Now().Add(5 * time.Second)
	for room.Scene.ShowDesk || !a.d.Store.IsMissing(deskBase) {
		if time.Now().After(deadline) {
			t.Fatalf("desk miss never relayed: ShowDesk=%v IsMissing=%v", room.Scene.ShowDesk, a.d.Store.IsMissing(deskBase))
		}
		a.drainWarnings()
		select {
		case d := <-a.d.Manager.Decoded():
			if d.Asset != nil {
				d.Asset.Release()
			}
		default:
			time.Sleep(2 * time.Millisecond)
		}
	}

	// Positive control on the type gate: a BACKGROUND miss must not be relayed as a
	// desk miss (the arm is keyed on AssetTypeDeskOverlay, not "anything scenery").
	bgBase := room.Scene.BackgroundBase
	if bgBase == deskBase {
		t.Fatal("test setup: the bg and desk bases must differ for the type-gate control")
	}
	a.d.Manager.Prefetch(bgBase, assets.AssetTypeBackground, network.PriorityHigh) // AssetType: Background
	ctrl := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(ctrl) {
		a.drainWarnings()
		select {
		case d := <-a.d.Manager.Decoded():
			if d.Asset != nil {
				d.Asset.Release()
			}
		default:
			time.Sleep(2 * time.Millisecond)
		}
	}
	if a.d.Store.IsMissing(bgBase) {
		t.Error("a missing BACKGROUND must not be marked missing — the background arm of set_scene has a fallback, the desk arm does not")
	}
}
