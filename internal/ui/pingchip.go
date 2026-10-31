package ui

import (
	"context"
	"fmt"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/protocol"
	"github.com/SyntaxNyah/AsyncAO/internal/sdl"
)

// Optional signal-bar chip showing WebSocket round-trip time, exact ms on hover.
// A background goroutine pings the active connection and stores the RTT atomically; none runs while the chip is off.

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

// updatePingLoop keeps the ping loop on the active connection while the chip is on; render thread, once per frame.
func (a *App) updatePingLoop() {
	want := a.conn
	if !a.d.Prefs.Load().PingChip {
		want = nil
	}
	if want == a.pingConn {
		return // already pinging the right conn (or both nil)
	}
	a.stopPingLoop()
	if want != nil {
		a.startPingLoop(want)
	}
}

func (a *App) startPingLoop(conn *protocol.Conn) {
	ctx, cancel := context.WithCancel(context.Background())
	a.pingCancel = cancel
	a.pingConn = conn
	a.pingRTT.Store(0)
	go a.pingLoop(ctx, conn)
}

// stopPingLoop cancels the live loop (idempotent) and clears the RTT so the chip shows no stale ping.
func (a *App) stopPingLoop() {
	if a.pingCancel != nil {
		a.pingCancel()
		a.pingCancel = nil
	}
	a.pingConn = nil
	a.pingRTT.Store(0)
}

// pingLoop pings conn every pingInterval until ctx is cancelled, storing the RTT (0 = unknown) in the atomic.
func (a *App) pingLoop(ctx context.Context, conn *protocol.Conn) {
	t := time.NewTicker(pingChipInterval)
	defer t.Stop()
	a.pingOnce(ctx, conn) // one immediate ping so the chip fills in fast
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			a.pingOnce(ctx, conn)
		}
	}
}

func (a *App) pingOnce(ctx context.Context, conn *protocol.Conn) {
	pctx, cancel := context.WithTimeout(ctx, pingChipTimeout)
	rtt, err := conn.Ping(pctx)
	cancel()
	if err != nil {
		a.pingRTT.Store(0) // unknown
		return
	}
	a.pingRTT.Store(int64(rtt))
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

// drawPingChip draws four rising bars, the first N filled by quality, with the exact ms on hover.
func (a *App) drawPingChip(x, y int32) {
	if !a.d.Prefs.Load().PingChip || a.conn == nil {
		return
	}
	c := a.ctx
	rtt := a.pingRTT.Load()
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

	if a.pingLabelMs != ms || a.pingLabel == "" { // rebuild the tooltip only when the ms moves
		if unknown {
			a.pingLabel = "Ping: — (measuring…)"
		} else {
			a.pingLabel = fmt.Sprintf("Ping: %d ms", ms)
		}
		a.pingLabelMs = ms
	}
	c.Tooltip(sdl.Rect{X: x, Y: y, W: chipW, H: chipH}, a.pingLabel)
}
