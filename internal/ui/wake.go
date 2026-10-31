package ui

import (
	"sync/atomic"

	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

// The render-loop wake event: background producers (websocket read loop, decode pool) push one SDL user event so a
// main loop parked in WaitEventTimeout runs at once instead of waiting out its idle tick. SDL_PushEvent is thread-safe
// and is the one sanctioned cross-thread call into SDL. wakePending collapses bursts into one event and clears when
// the loop consumes it, so a wake is never missed.
var (
	wakeEventType atomic.Uint32
	wakePending   atomic.Bool
)

// EnsureWakeEvent registers the user event type once, on the render thread after sdl.Init. If registration fails
// PushWake is a no-op and the loop's timeout still bounds staleness.
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
		// Queue full or event system gone: clear the flag so a later push can retry.
		wakePending.Store(false)
	}
}

// IsWakeEvent reports whether ev is the wake event, clearing wakePending so later producers can push again.
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
