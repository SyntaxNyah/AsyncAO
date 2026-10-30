package main

import "github.com/SyntaxNyah/AsyncAO/internal/sdl"

// mainWindowShouldQuit is the run loop's whole quit decision; closing another window must not end the loop.
func mainWindowShouldQuit(ev sdl.Event, mainWindowID uint32) bool {
	switch e := ev.(type) {
	case *sdl.QuitEvent:
		return true
	case *sdl.WindowEvent:
		return e.Event == sdl.WINDOWEVENT_CLOSE && e.WindowID == mainWindowID
	}
	return false
}
