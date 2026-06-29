package courtroom

import "testing"

func TestAsyncAODetection(t *testing.T) {
	c := &Courtroom{}
	if c.RemoteIsAsyncAO("Phoenix") {
		t.Fatal("nobody recorded yet")
	}
	c.rememberAsyncAO("Phoenix")
	if !c.RemoteIsAsyncAO("Phoenix") {
		t.Error("Phoenix should be detected as AsyncAO")
	}
	if c.RemoteIsAsyncAO("Edgeworth") {
		t.Error("Edgeworth was never seen — false positive")
	}
	c.rememberAsyncAO("") // blank (system / spectator) is ignored
	if c.RemoteIsAsyncAO("") {
		t.Error("blank name must not be stored")
	}
}

// TestAsyncAOMarkerGate: the shared hasMarker gate the receive hook uses fires on a
// real cross-client frame (here a profile marker) and not on plain text — so any
// frame, not just profiles, trips detection.
func TestAsyncAOMarkerGate(t *testing.T) {
	if hasMarker("just a normal line") {
		t.Error("plain text must not look like a frame")
	}
	mk := WireProfile{Pronouns: "they/them"}.EncodeMarker()
	if !hasMarker("hello" + mk) {
		t.Error("a profile-marked message must trip the detection gate")
	}
}
