package ui

import (
	"sync/atomic"

	"github.com/veandco/go-sdl2/sdl"
)

// The render-loop wake event (experimental event-driven loop): background
// producers — the websocket read loop, the decode pool's delivery — push one
// SDL user event so a main loop parked in WaitEventTimeout processes their
// work immediately instead of waiting out its idle tick. SDL_PushEvent is
// documented thread-safe, which is the whole point: this is the ONE sanctioned
// cross-thread doorbell into the SDL event queue (no SDL state is touched).
//
// wakePending collapses bursts: a hundred packets landing while one loop pass
// runs push exactly one event (the pass drains everything anyway). The flag
// clears when the loop consumes the event, so a producer firing after that
// queues a fresh one — at most one stale extra pass, never a missed wake.
var (
	wakeEventType atomic.Uint32
	wakePending   atomic.Bool
)

// EnsureWakeEvent registers the user event type once. Render thread, after
// sdl.Init; safe to call again (no-op). Registration failure (event range
// exhausted — practically impossible) leaves wakes disabled: PushWake becomes
// a no-op and the loop's timeout heartbeat still bounds staleness.
func EnsureWakeEvent() {
	if wakeEventType.Load() != 0 {
		return
	}
	// RegisterEvents returns (uint32)(-1) on exhaustion; treat it as "off".
	if id := sdl.RegisterEvents(1); id != ^uint32(0) {
		wakeEventType.Store(id)
	}
}

// PushWake queues one wake event (deduplicated) — safe from any goroutine.
func PushWake() {
	t := wakeEventType.Load()
	if t == 0 || !wakePending.CompareAndSwap(false, true) {
		return
	}
	if _, err := sdl.PushEvent(&sdl.UserEvent{Type: t}); err != nil {
		// Queue full / event system gone: clear the flag so a later push can
		// retry; the loop's heartbeat covers the gap regardless.
		wakePending.Store(false)
	}
}

// IsWakeEvent reports whether ev is the wake doorbell, consuming the pending
// flag (the loop is awake now — later producers must be able to knock again).
func IsWakeEvent(ev sdl.Event) bool {
	t := wakeEventType.Load()
	if t == 0 {
		return false
	}
	if ue, ok := ev.(*sdl.UserEvent); ok && ue.Type == t {
		wakePending.Store(false)
		return true
	}
	return false
}

// IsRealInput reports whether ev is REAL user input — the only kind that arms
// the full-rate input grace. Window/driver housekeeping (EXPOSED repaints,
// RENDER_TARGETS_RESET after heavy texture traffic, focus/move events) still
// renders a frame like any event, but must never hold max fps for the grace
// window: with a big animated sprite on stage its texture traffic fired such
// events every few seconds and the client burst to full rate for exactly the
// grace second "as if it detected an input" (playtest, v1.55.0-test2).
func IsRealInput(ev sdl.Event) bool {
	switch ev.(type) {
	case *sdl.MouseMotionEvent, *sdl.MouseButtonEvent, *sdl.MouseWheelEvent,
		*sdl.KeyboardEvent, *sdl.TextInputEvent, *sdl.TextEditingEvent,
		*sdl.DropEvent, *sdl.TouchFingerEvent:
		return true
	}
	return false
}
