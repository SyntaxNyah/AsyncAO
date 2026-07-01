package ui

import (
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
)

// Packet inspector (#333) — the data behind the Debug panel's "Packets" view: a
// bounded, render-thread ring of the ACTIVE connection's recent packets plus
// per-header in/out counts. Recording is ALWAYS on (not gated on the panel being
// open) so it captures what happened BEFORE you opened the panel — it's a
// fixed-size struct copy on the packet-pump path, never the drawn 60 fps loop,
// so it costs effectively nothing and doesn't touch the zero-alloc render gates
// (only the panel's fmt draw is panel-gated). Every write happens on the render
// thread — pumpConnection (inbound) and the active session's send closure
// (outbound, gated conn==a.conn) — so, like the debugLog ring, no locks are
// needed. Scoped to the active connection: a tab switch / reconnect resets it,
// so the counts stay coherent for one server.

const (
	// pktLogCap bounds the recent-packet ring (hard rule #4).
	pktLogCap = 256
	// pktHeaderCap bounds the distinct-header count maps. AO's header set is a
	// small fixed vocabulary, but a hostile server could invent headers, so any
	// past the cap bucket into pktOtherHeader rather than growing the map unbounded.
	pktHeaderCap   = 64
	pktOtherHeader = "(other)"
)

// packetRec is one recorded packet: header, an approximate wire size and field
// count, when it happened, and its direction. Fixed-size (no retained field
// slice), so recording is a cheap value copy off the hot path.
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

// record appends one packet. Always called; only the draw is panel-gated.
// Render-thread only — no locking.
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

// reset clears the recorder for a new connection (the ring backing array is kept
// — stale slots are masked by total/idx on read). Called when the active conn changes.
func (pl *packetLog) reset() {
	pl.idx, pl.total, pl.inTotal, pl.outTotal = 0, 0, 0, 0
	for k := range pl.inCount {
		delete(pl.inCount, k)
	}
	for k := range pl.outCount {
		delete(pl.outCount, k)
	}
}

// recent returns up to n of the most recent records, newest first, into dst
// (reused by the caller to stay alloc-free on the draw path). A ring read: the
// live window is the last min(total, pktLogCap) slots ending just before idx.
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

// notePktConn resets the packet log when the active connection changes, so the
// ring + counts always describe one server. Called on the inbound pump (which
// runs for the active conn) and on connect.
func (a *App) notePktConn() {
	if a.conn != a.pktConn {
		a.pkts.reset()
		a.pktConn = a.conn
	}
}
