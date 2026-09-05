package main

import "github.com/veandco/go-sdl2/sdl"

// mainWindowShouldQuit reports whether ev ends the run loop, given the id
// of the application's single top-level (main) window. It is the run
// loop's ENTIRE quit decision — main's event dispatch must call this and
// nothing else to decide whether to stop, so the decision stays testable
// in isolation from real SDL windows, renderers and event loops.
//
// MEASURED on this dev box with a two-window go-sdl2 v0.4.40 probe that
// drove the real title-bar-X path (PostMessage(hwnd, WM_CLOSE), never
// SDL_DestroyWindow, which skips the window-manager path and proves
// nothing):
//   - Closing a NON-last window posts only WINDOWEVENT_CLOSE carrying
//     that window's id. SDL does not post SDL_QUIT and does not destroy
//     the window for you.
//   - Closing the MAIN window while a second window is also open ALSO
//     posts no SDL_QUIT — only WINDOWEVENT_CLOSE(mainWindowID).
//
// So the instant a second (e.g. detached preview) window can exist,
// "quit only on SDL_QUIT" stops being equivalent to "quit when the main
// window closes": the main window's own X button would leave the client
// running invisibly behind an orphaned second window, needing Task
// Manager to kill it. Comparing the CLOSE event's WindowID against the
// recorded main window id is what keeps that from happening, while
// leaving a non-main window's CLOSE to be handled elsewhere (that window
// closes itself; the app does not).
//
// Today there is exactly one window, so mainWindowID is the only id any
// WindowEvent ever carries, and this is exactly equivalent to yesterday's
// SDL_QUIT-only behavior. The id comparison is the seam a future second
// window plugs into without ever reintroducing the invisible-hang case
// above.
func mainWindowShouldQuit(ev sdl.Event, mainWindowID uint32) bool {
	switch e := ev.(type) {
	case *sdl.QuitEvent:
		return true
	case *sdl.WindowEvent:
		return e.Event == sdl.WINDOWEVENT_CLOSE && e.WindowID == mainWindowID
	}
	return false
}
