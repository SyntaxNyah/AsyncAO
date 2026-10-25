package network

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// dribbleBody flushes response headers immediately, then writes total bytes in
// chunkSize pieces with pause between each — modelling a large track served
// over a slow-but-flowing link. Because headers land instantly, the header
// (TTFB) deadline is satisfied at once; only the BODY takes real wall time.
func dribbleBody(w http.ResponseWriter, total, chunkSize int, pause time.Duration) {
	fl, ok := w.(http.Flusher)
	if !ok {
		return
	}
	// No Content-Length: force the chunked/unknown-length read path (the one a
	// streaming CDN /play link exercises), so both readBody branches are covered
	// across the suite.
	w.WriteHeader(http.StatusOK)
	fl.Flush() // headers out NOW → TTFB is effectively zero
	chunk := make([]byte, chunkSize)
	for i := range chunk {
		chunk[i] = byte('a' + i%26)
	}
	sent := 0
	for sent < total {
		n := chunkSize
		if total-sent < n {
			n = total - sent
		}
		if _, err := w.Write(chunk[:n]); err != nil {
			return
		}
		fl.Flush()
		sent += n
		time.Sleep(pause)
	}
}

// TestSlowLargeBodyOutlivesHeaderDeadline pins §1.1: the TTFB-derived request
// deadline must bound only the wait for HEADERS, not the body transfer. A body
// that dribbles for far longer than that deadline (as a ~10MB music track does
// on a slow link) must still complete, because the body read gets its own
// larger bodyBudget once headers arrive.
//
// Under the OLD behaviour (a single context.WithTimeout(adaptiveTimeout) that
// governed the whole request), this exact handler cut the read mid-stream with
// context.DeadlineExceeded — the "big music silently doesn't play" bug.
func TestSlowLargeBodyOutlivesHeaderDeadline(t *testing.T) {
	const (
		// The header deadline. adaptiveTimeout returns c.timeout when the host
		// has no TTFB samples yet, so this IS the pre-fix whole-request cap.
		headerDeadline = 250 * time.Millisecond
		// A multi-MB body dribbled well past the header deadline: 4 MB in
		// 64 KB chunks, 5 ms apart ≈ 320 ms of transfer — comfortably longer
		// than headerDeadline, so the old whole-request deadline would have
		// killed it, but far under bodyBudget and the test's own budget.
		bodySize  = 4 << 20
		chunkSize = 64 << 10
		pause     = 5 * time.Millisecond
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dribbleBody(w, bodySize, chunkSize, pause)
	}))
	defer srv.Close()

	c := newClient(headerDeadline, NotFoundCacheTTL)

	start := time.Now()
	data, err := c.Fetch(context.Background(), srv.URL+"/sounds/music/big-track.opus")
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("slow large body failed: %v (elapsed %v) — the header deadline must not bound the body read", err, elapsed)
	}
	if len(data) != bodySize {
		t.Fatalf("got %d body bytes, want %d — the read was cut short", len(data), bodySize)
	}
	// Sanity: the transfer genuinely outlived the header deadline, so this test
	// actually exercises the decoupling rather than finishing before it matters.
	if elapsed <= headerDeadline {
		t.Fatalf("transfer took %v, not longer than the %v header deadline — test isn't exercising the fix", elapsed, headerDeadline)
	}
	// Keep the whole thing comfortably fast (rule: under ~10s wall).
	if elapsed > 5*time.Second {
		t.Fatalf("transfer took %v — unexpectedly slow", elapsed)
	}
}

// TestBodyBudgetTimeoutIsNotA404 pins rule 6 across the §1.1 change: a body-read
// timeout (a genuinely stalled/too-slow transfer that blows bodyBudget) is a
// TRANSIENT error, never the definitive ErrAssetNotFound, and must never be
// negative-cached — otherwise a slow link would poison a perfectly-present
// track as "missing" for the whole 404 TTL.
func TestBodyBudgetTimeoutIsNotA404(t *testing.T) {
	const (
		headerDeadline = 2 * time.Second
		// A tiny body budget the dribble deliberately overruns, forcing a
		// body-read timeout after headers have already arrived.
		tinyBodyBudget = 120 * time.Millisecond
		bodySize       = 1 << 20
		chunkSize      = 16 << 10
		pause          = 20 * time.Millisecond // ~1.3s total ≫ tinyBodyBudget
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dribbleBody(w, bodySize, chunkSize, pause)
	}))
	defer srv.Close()

	c := newClient(headerDeadline, NotFoundCacheTTL)
	c.bodyBudget = tinyBodyBudget // inject a body ceiling the slow stream overruns
	url := srv.URL + "/sounds/music/stalls.opus"

	_, err := c.Fetch(context.Background(), url)
	if err == nil {
		t.Fatal("expected the over-slow body to time out")
	}
	if errors.Is(err, ErrAssetNotFound) {
		t.Fatalf("body-read timeout was categorised as ErrAssetNotFound: %v (rule 6)", err)
	}

	// It must NOT have been added to the negative cache: a repeat fetch is a
	// fresh upstream attempt, never a cached-404 short-circuit.
	before := c.Stats().Cached404s
	if _, found := c.notFound.Get(url); found {
		t.Fatal("a body-read timeout was negative-cached as a 404 (rule 6 violation)")
	}
	_, _ = c.Fetch(context.Background(), url)
	if got := c.Stats().Cached404s; got != before {
		t.Fatalf("Cached404s advanced by %d — a body timeout is being served from the 404 cache (rule 6)", got-before)
	}
}
