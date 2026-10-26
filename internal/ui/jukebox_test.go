package ui

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/SyntaxNyah/AsyncAO/internal/config"
	"github.com/veandco/go-sdl2/sdl"
)

// TestJukeBindCaptureConsumesKey pins the capture/fire ordering: pollJukeBind
// binds the armed target, clears the arm, AND consumes the keypress — so the
// same key press that completes a bind can't ALSO fire a /play through
// handleJukeboxKeys that frame. A later press of the bound key does fire.
func TestJukeBindCaptureConsumesKey(t *testing.T) {
	want := strings.ToLower(sdl.GetKeyName(sdl.K_a))
	if want == "" {
		t.Skip("SDL key names unavailable in this environment")
	}
	j := config.OpenJukebox(filepath.Join(t.TempDir(), "j.json"))
	defer j.Close()
	j.AddPlaylist("Set")
	j.AddEntry(0, "song", "https://x")

	// macroBind:-1 mirrors NewApp so the macro guard doesn't mask the keypress
	// guard we're actually testing.
	a := &App{ctx: &Ctx{}, juke: j, macroBind: -1}
	a.jukeBindFor = "e:0:0"
	a.ctx.keyPressed = sdl.K_a
	a.pollJukeBind()

	if a.ctx.keyPressed != 0 {
		t.Error("pollJukeBind must consume the keypress so it can't also fire this frame")
	}
	if a.jukeBindFor != "" {
		t.Error("capture should clear after binding")
	}
	if got := j.Playlists()[0].Entries[0].Key; got != want {
		t.Errorf("song key = %q, want %q", got, want)
	}
	if a.handleJukeboxKeys() { // keyPressed was consumed above
		t.Error("handleJukeboxKeys must not fire on a consumed keypress")
	}

	// The BIND still captures the plain key ("a"), but firing it is an ALT chord
	// now (bareBindKey, qol.go): the Discord focus model gave every bare letter to
	// the IC input, so a bare press must be inert here.
	a.ctx.keyPressed = sdl.K_a
	if a.handleJukeboxKeys() {
		t.Error("a BARE bound key must no longer fire — plain letters belong to the IC input")
	}
	// Alt + the bound key resolves and fires.
	a.ctx.keyPressed, a.ctx.altHeld = sdl.K_a, true
	if !a.handleJukeboxKeys() {
		t.Error("Alt + a bound key should fire handleJukeboxKeys")
	}
}
