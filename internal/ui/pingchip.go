package ui

import (
	"context"
	"fmt"
	"time"

	"github.com/veandco/go-sdl2/sdl"

	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
)

// Connection-quality chip (#128): an OPTIONAL, off-by-default signal-bar icon showing the
// WebSocket round-trip time — green/amber/red bars, with the exact ms on hover. A single
// background goroutine pings the ACTIVE connection and stores the RTT atomically; the frame
// loop starts/stops/retargets it. When the chip is off there's no goroutine at all, so it's
// truly zero-cost by default (the per-frame check is a pointer compare + one bool read).

const (
	pingChipInterval = 4 * time.Second // how often to ping the server
	pingChipTimeout  = 6 * time.Second // per-ping deadline (a stalled pong reads as unknown)

	// Quality thresholds in milliseconds (round-trip).
	pingGoodMs = 80
	pingOkMs   = 150
	pingFairMs = 250
)

var (
	pingColGood = sdl.Color{R: 70, G: 200, B: 90, A: 255}  // green
	pingColWarn = sdl.Color{R: 235, G: 180, B: 40, A: 255} // amber
	pingColBad  = sdl.Color{R: 225, G: 70, B: 70, A: 255}  // red
)

// updatePingLoop (render thread, once per frame) keeps the background ping loop targeting the
// ACTIVE conn while the chip is on, and runs no goroutine when it's off. Cheap: a pointer
// compare + one pref read in steady state.
func (a *App) updatePingLoop() {
	want := a.link.conn
	if !a.d.Prefs.Load().PingChip {
		want = nil
	}
	if want == a.lobby.pingConn {
		return // already pinging the right conn (or both nil)
	}
	a.lobby.stopPingLoop()
	if want != nil {
		a.startPingLoop(want)
	}
}

func (a *App) startPingLoop(conn *protocol.Conn) {
	ctx, cancel := context.WithCancel(context.Background())
	a.lobby.pingCancel = cancel
	a.lobby.pingConn = conn
	a.lobby.pingRTT.Store(0)
	go a.pingLoop(ctx, conn)
}

// stopPingLoop cancels the live loop (idempotent). Also clears the RTT so the chip doesn't show
// a stale ping for a connection that's gone.
func (lobby1 *lobbyState) stopPingLoop() {
	if lobby1.pingCancel != nil {
		lobby1.pingCancel()
		lobby1.pingCancel = nil
	}
	lobby1.pingConn = nil
	lobby1.pingRTT.Store(0)
}

// pingLoop pings conn every pingInterval until ctx is cancelled, storing the round-trip time
// (or 0 = unknown on a failed/timed-out ping). Background goroutine — touches ONLY the atomic.
func (a *App) pingLoop(ctx context.Context, conn *protocol.Conn) {
	t := time.NewTicker(pingChipInterval)
	defer t.Stop()
	a.lobby.pingOnce(ctx, conn) // one immediate ping so the chip fills in fast
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			a.lobby.pingOnce(ctx, conn)
		}
	}
}

func (lobby1 *lobbyState) pingOnce(ctx context.Context, conn *protocol.Conn) {
	pctx, cancel := context.WithTimeout(ctx, pingChipTimeout)
	rtt, err := conn.Ping(pctx)
	cancel()
	if err != nil {
		lobby1.pingRTT.Store(0) // unknown
		return
	}
	lobby1.pingRTT.Store(int64(rtt))
}

// pingQuality maps a round-trip ms to a filled-bar count + colour (0 bars / dim when unknown).
func pingQuality(ms int, unknown bool) (bars int32, col sdl.Color) {
	switch {
	case unknown:
		return 0, ColTextDim
	case ms < pingGoodMs:
		return 4, pingColGood
	case ms < pingOkMs:
		return 3, pingColGood
	case ms < pingFairMs:
		return 2, pingColWarn
	default:
		return 1, pingColBad
	}
}

// drawPingChip draws the signal-bar chip at (x,y) when the chip is on and connected — a row of
// four rising bars, the first N filled by quality, with the exact ms on hover. Read-only of the
// atomic RTT; the bars are Fill rects and the tooltip text is cached (rebuilt only when the ms
// changes), so steady-state draw is 0-alloc.
func (a *App) drawPingChip(x, y int32) {
	if !a.d.Prefs.Load().PingChip || a.link.conn == nil {
		return
	}
	c := a.ctx
	rtt := a.lobby.pingRTT.Load()
	unknown := rtt == 0
	ms := int(rtt / int64(time.Millisecond))
	bars, col := pingQuality(ms, unknown)

	const barW, gap, baseH, step, nBars = int32(3), int32(2), int32(4), int32(3), int32(4)
	chipW := nBars*(barW+gap) - gap
	chipH := baseH + (nBars-1)*step
	for i := int32(0); i < nBars; i++ {
		bh := baseH + i*step
		bc := ColTextDim
		if i < bars {
			bc = col
		}
		c.Fill(sdl.Rect{X: x + i*(barW+gap), Y: y + (chipH - bh), W: barW, H: bh}, bc)
	}

	if a.lobby.pingLabelMs != ms || a.lobby.pingLabel == "" { // rebuild the tooltip only when the ms moves
		if unknown {
			a.lobby.pingLabel = "Ping: — (measuring…)"
		} else {
			a.lobby.pingLabel = fmt.Sprintf("Ping: %d ms", ms)
		}
		a.lobby.pingLabelMs = ms
	}
	c.Tooltip(sdl.Rect{X: x, Y: y, W: chipW, H: chipH}, a.lobby.pingLabel)
}
