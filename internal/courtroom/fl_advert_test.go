package courtroom

// AsyncAO fork addition: the capability advert must stay POSITIONAL on the wire.
// A typed aolib.FL is encoded as JSON once the session negotiates JSON mode, and
// a JSON FL arrives at the server's FL shim with a nil body, clearing the
// feature set instead of setting it. These tests pin the property that makes the
// advert survive: the packet handed to Conn.Send has no typed form, so it takes
// the p.String() branch and stays FL#...#% in both wire modes.

import (
	"testing"

	aolib "github.com/AO-Underground/aolib/go/v2"

	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
)

func captureSession() (*Session, *[]protocol.Packet) {
	sent := make([]protocol.Packet, 0, 4)
	return NewSession(func(p protocol.Packet) error {
		sent = append(sent, p)
		return nil
	}, "hdid-advert"), &sent
}

// TestAdvertiseFeaturesEmitsPositionalPacket covers the positional dispatch path
// (HandlePacket), which is what tests and replay use.
func TestAdvertiseFeaturesEmitsPositionalPacket(t *testing.T) {
	s, sent := captureSession()

	feed(t, s, "FL#grouppair#timeleap#%")

	if len(*sent) != 1 {
		t.Fatalf("advert replies = %d, want 1: %+v", len(*sent), *sent)
	}
	p := (*sent)[0]
	if p.Typed() != nil {
		t.Fatal("advert carries a typed form: it would encode as JSON in JSON mode and clear the server's feature set")
	}
	if got, want := p.String(), "FL#grouppair#timeleap#%"; got != want {
		t.Fatalf("advert wire = %q, want %q", got, want)
	}
}

// TestAdvertiseFeaturesTypedPathIsAlsoPositional covers the live network path
// (handleTyped), where the server's FL arrives as a canonical aolib packet.
func TestAdvertiseFeaturesTypedPathIsAlsoPositional(t *testing.T) {
	s, sent := captureSession()

	s.HandlePacket(protocol.NewTypedPacket(&aolib.FL{Features: []string{
		protocol.FeatureGroupPair, protocol.FeatureTimeLeap,
	}}))

	if len(*sent) != 1 {
		t.Fatalf("advert replies = %d, want 1: %+v", len(*sent), *sent)
	}
	if (*sent)[0].Typed() != nil {
		t.Fatal("typed-path advert carries a typed form; it must be positional")
	}
	if got, want := (*sent)[0].String(), "FL#grouppair#timeleap#%"; got != want {
		t.Fatalf("advert wire = %q, want %q", got, want)
	}
}

// TestAdvertiseFeaturesSkipsUnsupported extends the existing rule that we never
// advertise a capability the server did not offer.
func TestAdvertiseFeaturesSkipsUnsupported(t *testing.T) {
	s, sent := captureSession()

	feed(t, s, "FL#yellowtext#%")

	if len(*sent) != 0 {
		t.Fatalf("advert = %+v, want none (server offered no TL/GP feature)", *sent)
	}
}
