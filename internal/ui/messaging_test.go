package ui

import (
	"path/filepath"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/SyntaxNyah/AsyncAO/internal/courtroom"
)

// TestRouteIncomingPM: a received PM via the server-attributed path (Nyathena /
// Athena "[PM] [UID n] name") lands in the sender's DM thread as a not-from-me
// line, with any hidden AsyncAO control frame stripped from the shown text.
func TestRouteIncomingPM(t *testing.T) {
	prefs, err := config.New(filepath.Join(t.TempDir(), config.PrefsFileName))
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}
	t.Cleanup(func() { _ = prefs.Close() })
	a := &App{d: Deps{Prefs: prefs}}
	a.serverKey = "s"

	body := "hey there" + courtroom.WireMessage{Kind: courtroom.MsgDM}.EncodeMarker()
	a.routeIncomingPM(0, "Phoenix", body)
	got := a.pmThreads["phoenix"]
	if len(got) != 1 || got[0].fromMe || got[0].text != "hey there" {
		t.Errorf("routeIncomingPM recorded wrong: %+v", got)
	}
}
