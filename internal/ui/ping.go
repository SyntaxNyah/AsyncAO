package ui

// Lobby connect-time ("ping") sort (M7, opt-in): the "Ping" button probes every
// joinable server's TCP-connect RTT on a bounded worker pool and sorts the list
// by it. Off by default — no probing happens until you press the button, so the
// default lobby is byte-identical. Probing is pure network work on goroutines;
// the render thread only drains a bounded result channel.

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/SyntaxNyah/AsyncAO/internal/network"
)

const (
	// pingConcurrency bounds simultaneous probe dials (rule §17.4); pingTimeout
	// caps each, so a dead host can't stall the sweep.
	pingConcurrency = 8
	pingTimeout     = 2 * time.Second
	// pingResBuf bounds the result channel; the lobby drains it every frame.
	pingResBuf = 64
)

// pingResult carries one probe's outcome to the render thread. A blank url is
// the "sweep done" sentinel; gen lets pollPing drop a superseded sweep.
type pingResult struct {
	url string
	rtt time.Duration
	ok  bool
	gen int
}

// startPinging probes every joinable server's connect RTT on a bounded worker
// pool and switches the lobby to connect-time sort. Targets are snapshotted
// here (render thread) so the goroutines touch no shared App state except the
// bounded result channel. A re-probe bumps pingGen so a prior sweep's late
// results are ignored.
func (a *App) startPinging() {
	if a.pinging {
		return
	}
	type target struct{ url, addr string }
	var targets []target
	for i := range a.servers {
		e := &a.servers[i]
		if !e.Joinable() {
			continue
		}
		if addr := e.DialTarget(); addr != "" {
			targets = append(targets, target{e.WebSocketURL(), addr})
		}
	}
	if len(targets) == 0 {
		return
	}
	a.pinging = true
	a.pingMode = true
	a.pingGen++
	gen, res := a.pingGen, a.pingRes
	go func() {
		jobs := make(chan target)
		var wg sync.WaitGroup
		for w := 0; w < pingConcurrency; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for t := range jobs {
					ctx, cancel := context.WithTimeout(context.Background(), pingTimeout)
					rtt, err := network.ProbeLatency(ctx, t.addr)
					cancel()
					select {
					case res <- pingResult{url: t.url, rtt: rtt, ok: err == nil, gen: gen}:
					default: // result chan full: the lobby will re-ping if it cares
					}
				}
			}()
		}
		for _, t := range targets {
			jobs <- t
		}
		close(jobs)
		wg.Wait()
		select {
		case res <- pingResult{gen: gen}: // blank url = done
		default:
		}
	}()
}

// pollPing drains probe results into a.pings on the render thread — a
// non-blocking drain called from the lobby each frame, so it costs nothing idle.
func (a *App) pollPing() {
	for {
		select {
		case r := <-a.pingRes:
			if r.gen != a.pingGen {
				continue // a superseded sweep
			}
			if r.url == "" { // done
				a.pinging = false
				a.applyServerSort()
				continue
			}
			if a.pings == nil {
				a.pings = map[string]time.Duration{}
			}
			if r.ok {
				a.pings[r.url] = r.rtt
			} else {
				a.pings[r.url] = -1 // unreachable
			}
		default:
			return
		}
	}
}

// applyServerSort rebuilds the lobby list (ping-aware when pingMode) and resets
// the index-based selection — mirroring the refresh / favourite re-sorts.
func (a *App) applyServerSort() {
	a.servers = a.mergedFavorites()
	// Clear all three selection caches together (descLinks included — a stale
	// link slice is a latent trap for any draw path that consults it alone).
	a.selServer, a.descLines, a.descLinks = -1, nil, nil // row indices changed with the sort
}

// sortByPing orders joinable servers by connect RTT — favorites pinned, probed
// ascending, then unprobed/unreachable; legacy entries last. Stable.
func sortByPing(entries []network.ServerEntry, pings map[string]time.Duration) {
	rank := func(e network.ServerEntry) (time.Duration, bool) {
		d, ok := pings[e.WebSocketURL()]
		if !ok || d < 0 {
			return 0, false // unprobed or unreachable
		}
		return d, true
	}
	sort.SliceStable(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		aLegacy, bLegacy := !a.Joinable(), !b.Joinable()
		if aLegacy != bLegacy {
			return bLegacy
		}
		if !aLegacy && a.Favorite != b.Favorite {
			return a.Favorite
		}
		ra, oka := rank(a)
		rb, okb := rank(b)
		if oka != okb {
			return oka // known latency before unknown
		}
		if oka && ra != rb {
			return ra < rb
		}
		return a.Name < b.Name
	})
}
