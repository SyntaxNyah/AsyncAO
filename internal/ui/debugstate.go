package ui

import (
	"github.com/SyntaxNyah/AsyncAO/internal/debuglog"
)

// debugSink is ui's thin adapter over internal/debuglog. The ring, the fold
// window and the rendered row live in that package now — a compile-enforced
// boundary (ui cannot see the []Entry; it can only Push/PushAt/Lines/Len), which
// is the shape the rest of the decomposition is aiming at.
type debugSink struct {
	debuglog.Sink
}

// push appends one line to the bounded failure log (render thread only).
func (d *debugSink) push(line string) { d.Push(line) }

// lines is the ring for the panels that draw it (newest last).
func (d *debugSink) lines() []debuglog.Entry { return d.Lines() }

// len is the ring's row count.
func (d *debugSink) len() int { return d.Len() }
