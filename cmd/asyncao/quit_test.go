package main

import (
	"testing"

	"github.com/veandco/go-sdl2/sdl"
)

// These tests drive mainWindowShouldQuit directly — the exact function
// the run loop's event dispatch calls — with synthetic events built by
// hand. sdl.WindowEvent and sdl.QuitEvent are plain exported-field structs
// (see go-sdl2 sdl/events.go), so no sdl.Init, window, or renderer is
// needed to construct one; this is what makes the run loop's raw SDL
// dispatch testable at all (it had zero coverage before this phase).
const testMainWindowID = uint32(1)

// TestMainWindowCloseEndsTheRunLoop pins the fix itself: a
// WINDOWEVENT_CLOSE naming the MAIN window must quit, exactly like
// SDL_QUIT does today. Without the WindowID check this is unreachable
// (single window today), so this test is what stops a future refactor
// from silently deleting the check as "dead code".
func TestMainWindowCloseEndsTheRunLoop(t *testing.T) {
	ev := &sdl.WindowEvent{
		Type:     sdl.WINDOWEVENT,
		WindowID: testMainWindowID,
		Event:    sdl.WINDOWEVENT_CLOSE,
	}
	if !mainWindowShouldQuit(ev, testMainWindowID) {
		t.Fatal("WINDOWEVENT_CLOSE on the main window id must end the run loop")
	}
}

// TestOtherWindowCloseDoesNotEndTheRunLoop is measured fact 1 turned into
// a regression: closing a window that is NOT the main window (e.g. a
// future detached preview) must never quit the app. Today nothing else
// exists to send such an event, but that is exactly why this needs a
// test now — the seam has no other way to prove it holds once a second
// window ships.
func TestOtherWindowCloseDoesNotEndTheRunLoop(t *testing.T) {
	const otherWindowID = testMainWindowID + 1
	ev := &sdl.WindowEvent{
		Type:     sdl.WINDOWEVENT,
		WindowID: otherWindowID,
		Event:    sdl.WINDOWEVENT_CLOSE,
	}
	if mainWindowShouldQuit(ev, testMainWindowID) {
		t.Fatal("WINDOWEVENT_CLOSE on a non-main window id must not end the run loop")
	}
}

// TestQuitEventStillEndsTheRunLoop pins requirement 4 from the phase
// brief: adding the WindowEvent branch must not disturb the existing
// SDL_QUIT path.
func TestQuitEventStillEndsTheRunLoop(t *testing.T) {
	if !mainWindowShouldQuit(&sdl.QuitEvent{Type: sdl.QUIT}, testMainWindowID) {
		t.Fatal("SDL_QUIT must end the run loop")
	}
}

// TestOtherWindowEventOnMainWindowDoesNotEndTheRunLoop guards against a
// too-broad rewrite (e.g. "any WindowEvent for the main window quits")
// collapsing the Event-code check: only WINDOWEVENT_CLOSE is a quit
// signal, not every window event the main window can receive.
func TestOtherWindowEventOnMainWindowDoesNotEndTheRunLoop(t *testing.T) {
	ev := &sdl.WindowEvent{
		Type:     sdl.WINDOWEVENT,
		WindowID: testMainWindowID,
		Event:    sdl.WINDOWEVENT_FOCUS_GAINED,
	}
	if mainWindowShouldQuit(ev, testMainWindowID) {
		t.Fatal("a non-CLOSE WindowEvent on the main window must not end the run loop")
	}
}
