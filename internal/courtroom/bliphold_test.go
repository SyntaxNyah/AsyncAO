package courtroom

import "testing"

// TestResolveBlipHoldsWhileCharINIInFlight pins #69's fix: when the speaker's
// char.ini is still in flight, the blip is HELD (blipPending, empty ref) rather
// than defaulted to "male", so a character's first message never sounds the
// wrong set. Once the meta lands, resolveBlip re-mints the correct set, and an
// empty-but-known answer falls back to AO's default.
func TestResolveBlipHoldsWhileCharINIInFlight(t *testing.T) {
	room, _, _, _ := newCourtroomRig(t)

	room.blipWire = "" // no wire blip: fall through to char.ini
	room.blipSpeaker = "Phoenix"
	room.BlipNameFor = func(char string) (string, bool) { return "", false } // in-flight

	room.resolveBlip()
	if !room.blipPending || room.blipRef.Base != "" {
		t.Fatalf("in-flight char.ini must hold: pending=%v ref=%q", room.blipPending, room.blipRef.Base)
	}

	// char.ini lands: known=true with the character's own set.
	room.BlipNameFor = func(char string) (string, bool) { return "female", true }
	room.resolveBlip()
	if room.blipPending {
		t.Fatal("a known char.ini must not leave the blip pending")
	}
	if want := room.urls.BlipRef("female").Base; room.blipRef.Base != want {
		t.Fatalf("blip base = %q, want %q", room.blipRef.Base, want)
	}

	// An empty-but-known answer falls back to AO's default, never a stale ref.
	room.BlipNameFor = func(char string) (string, bool) { return "", true }
	room.resolveBlip()
	if want := room.urls.BlipRef(defaultBlipSet).Base; room.blipRef.Base != want {
		t.Fatalf("empty known answer must default: base = %q, want %q", room.blipRef.Base, want)
	}
}
