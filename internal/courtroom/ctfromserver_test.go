package courtroom

import (
	"testing"

	aolib "github.com/AO-Underground/aolib/go/v2"

	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
)

// Issue #68: CT's third field says whether the server itself sent the line; only "1" means it did.
func TestOOCLineCarriesWhetherTheServerSentIt(t *testing.T) {
	s := NewSession((&sentRecorder{}).send, "h")
	for raw, want := range map[string]bool{
		"CT#Server#welcome#1#%": true,
		"CT#Bob#hi#0#%":         false,
		"CT#Bob#hi#%":           false,
		"CT#Bob#hi#7#%":         false,
	} {
		if ev := feed(t, s, raw); len(ev) != 1 || ev[0].Kind != EventOOC || ev[0].FromServer != want {
			t.Errorf("%s → %+v, want FromServer=%v", raw, ev, want)
		}
	}
	typed := s.HandlePacket(protocol.NewTypedPacket(&aolib.CTToClient{Name: "Server", Message: "hi", IsFromServer: true}))
	if len(typed) != 1 || !typed[0].FromServer {
		t.Errorf("typed CT from the server → %+v", typed)
	}
}
