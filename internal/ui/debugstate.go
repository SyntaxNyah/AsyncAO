package ui

import "time"

// debugSink is the bounded failure log behind the debug overlay: a ring of
// stamped rows, folded by a time window. It is a leaf service owned by env
// (window-level, like wins) so it survives a tab park/restore. `pkts`/`pktConn`
// and `chatDiag` are NOT part of it: they are per-connection / per-frame state.
type debugSink struct {
	log []debugEntry
}

// push appends one line to the bounded failure log, folding a repeat of a
// message already seen inside debugFoldWindow into that row's ×N counter so a
// chatty source can't flush real failures out of the ring. Render thread only.
func (d *debugSink) push(line string) { d.pushAt(line, time.Now()) }

// pushAt is push with the clock injected — the fold is a TIME window, so the
// tests drive the window from here rather than by sleeping.
func (d *debugSink) pushAt(line string, now time.Time) {
	// The ring is ordered by latest sighting (a fold moves its row to the tail),
	// so scanning backwards can stop at the first row outside the window: every
	// row before it is older still. That bounds the scan without a second cap.
	for i := len(d.log) - 1; i >= 0; i-- {
		e := d.log[i]
		if now.Sub(e.at) > debugFoldWindow {
			break
		}
		if e.raw != line {
			continue
		}
		e.count++
		e.at = now
		e.text = e.render()
		// Move to the tail: keeps the ordering the early exit above relies on,
		// and keeps a repeating source where the reader is looking (newest end)
		// instead of pinned at the position of its first sighting.
		copy(d.log[i:], d.log[i+1:])
		d.log[len(d.log)-1] = e
		return
	}
	e := debugEntry{raw: line, at: now, count: 1}
	e.text = e.render()
	d.log = append(d.log, e)
	if len(d.log) > debugLogCap {
		d.log = d.log[len(d.log)-debugLogCap:]
	}
}

// lines is the ring for the panels that draw it (newest last).
func (d *debugSink) lines() []debugEntry { return d.log }

// len is the ring's row count.
func (d *debugSink) len() int { return len(d.log) }
