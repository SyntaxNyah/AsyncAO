package ui

import (
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
)

// Packet inspector: the data behind the Debug panel's Packets view, a bounded ring of the active connection's recent
// packets plus per-header in/out counts. Recording is always on so it captures what happened before the panel opened.
// All writes are on the render thread (pumpConnection inbound, the session send closure outbound), so no locks.
// A tab switch or reconnect resets it.

const (
	// pktLogCap bounds the recent-packet ring (hard rule 4).
	pktLogCap = 256
	// pktHeaderCap bounds the distinct-header count maps; a hostile server could invent headers,
	// so extras bucket into pktOtherHeader.
	pktHeaderCap   = 64
	pktOtherHeader = "(other)"
)

// packetRec is one recorded packet: header, approximate wire size and field count, time and direction. Fixed-size.
type packetRec struct {
	at     time.Time
	hdr    string
	size   int
	fields int
	out    bool // false = inbound (server → us), true = outbound (us → server)
}

// packetLog is the render-thread packet recorder behind the Debug panel.
type packetLog struct {
	ring     []packetRec // cap pktLogCap; a wrapping ring
	idx      int         // next write slot
	total    int         // total ever recorded on this connection (drives ring fill)
	inCount  map[string]int
	outCount map[string]int
	inTotal  int
	outTotal int
}

// record appends one packet; always called, render thread only.
func (pl *packetLog) record(hdr string, fields, size int, out bool) {
	if pl.ring == nil {
		pl.ring = make([]packetRec, pktLogCap)
		pl.inCount = make(map[string]int, pktHeaderCap)
		pl.outCount = make(map[string]int, pktHeaderCap)
	}
	pl.ring[pl.idx] = packetRec{at: time.Now(), hdr: hdr, size: size, fields: fields, out: out}
	pl.idx = (pl.idx + 1) % pktLogCap
	pl.total++
	m := pl.inCount
	if out {
		m, pl.outTotal = pl.outCount, pl.outTotal+1
	} else {
		pl.inTotal++
	}
	if _, ok := m[hdr]; !ok && len(m) >= pktHeaderCap {
		hdr = pktOtherHeader // bound the distinct-header set (rule #4)
	}
	m[hdr]++
}

// reset clears the recorder for a new connection; the ring is kept and stale slots are masked by total/idx.
func (pl *packetLog) reset() {
	pl.idx, pl.total, pl.inTotal, pl.outTotal = 0, 0, 0, 0
	for k := range pl.inCount {
		delete(pl.inCount, k)
	}
	for k := range pl.outCount {
		delete(pl.outCount, k)
	}
}

// recent returns up to n records, newest first, into dst (reused to stay allocation-free).
func (pl *packetLog) recent(dst []packetRec, n int) []packetRec {
	dst = dst[:0]
	have := pl.total
	if have > pktLogCap {
		have = pktLogCap
	}
	if n > have {
		n = have
	}
	for i := 0; i < n; i++ {
		// idx points one past the newest; walk backwards, wrapping.
		j := (pl.idx - 1 - i + pktLogCap*2) % pktLogCap
		dst = append(dst, pl.ring[j])
	}
	return dst
}

// recordPacket is the App-level tap: records p in the given direction, computing
// an approximate wire size (header + fields + delimiters) without allocating.
func (a *App) recordPacket(p protocol.Packet, out bool) {
	size := len(p.Header)
	for i := range p.Fields {
		size += len(p.Fields[i]) + 1 // +1 ≈ the '#' delimiter
	}
	a.pkts.record(p.Header, len(p.Fields), size, out)
}

// notePktConn resets the packet log when the active connection changes, so it describes one server.
func (a *App) notePktConn() {
	if a.conn != a.pktConn {
		a.pkts.reset()
		a.pktConn = a.conn
	}
}
