package main

import "github.com/veandco/go-sdl2/sdl"

// mainWindowShouldQuit reports whether ev ends the run loop, given the id
// of the application's single top-level (main) window. It is the run
// loop's ENTIRE quit decision — main's event dispatch must call this and
// nothing else to decide whether to stop, so the decision stays testable
// in isolation from real SDL windows, renderers and event loops.
func mainWindowShouldQuit(ev sdl.Event, mainWindowID uint32) bool {
	switch e := ev.(type) {
	case *sdl.QuitEvent:
		return true
	case *sdl.WindowEvent:
		return e.Event == sdl.WINDOWEVENT_CLOSE && e.WindowID == mainWindowID
	}
	return false
}
