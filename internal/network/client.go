// Package network fetches assets over HTTP with aggressive deduplication and
// negative caching (spec §7), runs the prioritized fetch worker pool,
// and talks to the AO master server. Every request must earn its RTT:
// duplicate in-flight fetches collapse via singleflight, cached 404s never
// touch the wire inside their TTL, and failing hosts back off exponentially.
//
// The game connection itself is WebSocket-only (see internal/protocol);
// legacy raw-TCP servers are intentionally unsupported.
package network

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hashicorp/golang-lru/v2/expirable"
	"golang.org/x/sync/singleflight"
)

const (
	// Transport tuning (spec §7). Most AO asset hosts speak plain
	// http://, where ForceAttemptHTTP2 is inert and tuned HTTP/1.1
	// keep-alive does the work; HTTP/2 kicks in automatically on https.
	// Idle-per-host matches the worker pool: at 8 every 16-wide fetch
	// burst closed half its connections and the NEXT burst re-dialed
	// (plus TLS handshake on https) — cold-load bursts are back-to-back,
	// so all lanes stay warm now. The total covers maxTabs concurrent
	// servers' asset hosts at full width plus master-list/manifest slack.
	defaultMaxConnsPerHost     = 16
	defaultMaxIdleConnsPerHost = 16
	defaultMaxIdleConnsTotal   = 96
	defaultIdleConnTimeout     = 90 * time.Second
	defaultTLSHandshakeTimeout = 2 * time.Second
	// defaultResponseHeaderTimeout fails a stalled server faster than the
	// overall request deadline, freeing the connection slot for the next
	// probe.
	defaultResponseHeaderTimeout = 3 * time.Second
	tlsSessionCacheSize          = 64

	// DefaultRequestTimeout caps every asset request end-to-end.
	DefaultRequestTimeout = 5 * time.Second

	// NotFoundCacheSize / NotFoundCacheTTL bound the negative cache: a 404
	// is never re-probed inside the TTL (spec §17.6).
	NotFoundCacheSize = 1024
	NotFoundCacheTTL  = 5 * time.Minute

	// Host failure backoff: doubles per consecutive failure, capped.
	backoffBase = 500 * time.Millisecond
	backoffMax  = 30 * time.Second

	// Adaptive deadlines (the bounded "self-tuning within the cap" the
	// perf roadmap calls for): each host's observed time-to-first-byte
	// EWMA sets that host's request deadline at adaptiveLatencyMultiple ×
	// EWMA, clamped to [adaptiveTimeoutFloor, the client timeout]. A
	// degrading mirror stops pinning fetch workers for the full global
	// timeout — the lane keeps flowing for healthy hosts — while fast
	// hosts never notice (the floor dwarfs their real responses).
	adaptiveLatencyMultiple = 8
	adaptiveTimeoutFloor    = 2 * time.Second
	// EWMA weight 1/4: new samples track congestion without letting one
	// blip swing the average.
	ewmaWeightDen = 4

	// unknownLengthPrealloc seeds the scratch buffer for responses without
	// a Content-Length header.
	unknownLengthPrealloc = 64 << 10
)

// ErrAssetNotFound reports a 404, possibly served from the negative cache
// without a network round-trip.
var ErrAssetNotFound = errors.New("network: asset not found")

// ErrHostBackingOff reports that the host failed recently and the client is
// inside its exponential backoff window.
var ErrHostBackingOff = errors.New("network: host backing off after failures")

// Client is the deduplicating, negatively-caching asset fetcher. All methods
// are safe for concurrent use.
type Client struct {
	httpClient *http.Client
	group      singleflight.Group
	notFound   *expirable.LRU[string, struct{}]
	dns        *dnsCache
	backoff    sync.Map // host string → *hostBackoff
	hostLat    sync.Map // host string → *hostLatency (TTFB EWMA)
	timeout    time.Duration
	bufPool    sync.Pool // *bytes.Buffer for unknown-length reads

	requests   atomic.Int64
	hits       atomic.Int64
	notFounds  atomic.Int64
	cached404s atomic.Int64
	failures   atomic.Int64
	bytesRead  atomic.Int64

	// assetOrigin, when set, is sent as the Origin (and matching Referer) header on
	// every asset fetch — a power-user override for servers that gate their asset
	// base by Origin / CORS. nil = unset (default). Read on the fetch goroutines,
	// set from the Security setting; atomic so it's race-free.
	assetOrigin atomic.Pointer[string]

	// latMultiple overrides adaptiveLatencyMultiple when non-zero (the power-user
	// per-host deadline knob): each host's request deadline = multiple × its TTFB
	// EWMA, clamped. Atomic — read per request, set from Settings.
	latMultiple atomic.Int32

	// globalTTFBNs is the all-hosts TTFB EWMA (cold-load profiling: the debug
	// overlay's per-stage line wants ONE fetch number, not a per-host map walk).
	globalTTFBNs atomic.Int64
}

// AvgTTFB reports the all-hosts time-to-first-byte EWMA (zero until the first
// sample) — the fetch stage of the cold-load profiling line.
func (c *Client) AvgTTFB() time.Duration { return time.Duration(c.globalTTFBNs.Load()) }

// Hard bounds the deadline-multiple setter enforces regardless of what the
// caller sends (a runaway pref can't produce a 0× or 1000× deadline).
const (
	latMultipleFloor = 1
	latMultipleCeil  = 64
)

// SetAdaptiveLatencyMultiple overrides the per-host adaptive-deadline multiple
// (0 = the built-in default, adaptiveLatencyMultiple). Live-safe: read per
// request. Lower = give up on a degrading mirror sooner (snappier shedding,
// more spurious timeouts on jittery links); higher = more patient.
func (c *Client) SetAdaptiveLatencyMultiple(n int) {
	if n != 0 {
		if n < latMultipleFloor {
			n = latMultipleFloor
		}
		if n > latMultipleCeil {
			n = latMultipleCeil
		}
	}
	c.latMultiple.Store(int32(n))
}

// SetAssetOrigin sets the Origin (and Referer) header sent on every asset fetch.
// Empty clears it (the default — no header). Safe to call from any goroutine. The
// power-user escape hatch for servers that only serve their base to a specific web
// origin (a "join only via https://webao.example" setup).
func (c *Client) SetAssetOrigin(origin string) {
	if origin == "" {
		c.assetOrigin.Store(nil)
		return
	}
	c.assetOrigin.Store(&origin)
}

// NewClient builds a Client with the §7 transport tuning.
func NewClient() *Client {
	return newClient(DefaultRequestTimeout, NotFoundCacheTTL)
}

// NewClientNotFoundTTL is NewClient with a power-user negative-cache TTL (how
// long a 404 stays "missing" before a re-probe is allowed; 0 = the default
// NotFoundCacheTTL). BOOT-applied by design: the expirable LRU takes its TTL at
// construction, and rebuilding it live would flush every cached 404 — the
// Settings row says "applies on restart".
func NewClientNotFoundTTL(ttl time.Duration) *Client {
	if ttl <= 0 {
		ttl = NotFoundCacheTTL
	}
	return newClient(DefaultRequestTimeout, ttl)
}

// newClient lets tests shrink timeouts and TTLs.
func newClient(timeout, notFoundTTL time.Duration) *Client {
	dns := newDNSCache()
	transport := &http.Transport{
		MaxConnsPerHost:       defaultMaxConnsPerHost,
		MaxIdleConnsPerHost:   defaultMaxIdleConnsPerHost,
		MaxIdleConns:          defaultMaxIdleConnsTotal,
		IdleConnTimeout:       defaultIdleConnTimeout,
		ResponseHeaderTimeout: defaultResponseHeaderTimeout,
		// Assets are pre-compressed media; transparent gzip would burn CPU
		// for nothing.
		DisableCompression:  true,
		ForceAttemptHTTP2:   true,
		TLSHandshakeTimeout: defaultTLSHandshakeTimeout,
		TLSClientConfig: &tls.Config{
			ClientSessionCache: tls.NewLRUClientSessionCache(tlsSessionCacheSize),
		},
		DialContext: dns.DialContext,
	}
	return &Client{
		httpClient: &http.Client{Transport: transport},
		notFound:   expirable.NewLRU[string, struct{}](NotFoundCacheSize, nil, notFoundTTL),
		dns:        dns,
		timeout:    timeout,
		bufPool: sync.Pool{
			New: func() any {
				buf := &bytes.Buffer{}
				buf.Grow(unknownLengthPrealloc)
				return buf
			},
		},
	}
}

// PreResolve warms the DNS cache for host so the first asset probe never
// blocks on a lookup. Call it at server connect with the asset host.
func (c *Client) PreResolve(ctx context.Context, host string) {
	c.dns.preResolve(ctx, host)
}

// Fetch returns the asset bytes at url. Concurrent fetches of the same URL
// collapse into one upstream request whose result every caller shares; ctx
// cancels only this caller's wait, not the shared fetch. The returned slice
// is owned by the asset pipeline and must be treated as immutable.
func (c *Client) Fetch(ctx context.Context, url string) ([]byte, error) {
	if _, found := c.notFound.Get(url); found {
		c.cached404s.Add(1)
		return nil, fmt.Errorf("%w: %s (cached)", ErrAssetNotFound, url)
	}
	if host := hostOf(url); host != "" {
		if until, backing := c.backingOff(host); backing {
			return nil, fmt.Errorf("%w: %s until %s", ErrHostBackingOff, host, until.Format(time.RFC3339))
		}
	}

	ch := c.group.DoChan(url, func() (any, error) {
		return c.fetchOnce(url)
	})
	select {
	case res := <-ch:
		if res.Err != nil {
			return nil, res.Err
		}
		return res.Val.([]byte), nil
	case <-ctx.Done():
		// The shared fetch keeps running for other waiters; only this
		// caller gives up.
		return nil, ctx.Err()
	}
}

// hostLatency is one host's TTFB EWMA in nanoseconds. Plain atomic
// load/store: a lost concurrent update only delays the average by one
// sample — fine for statistics, free of CAS loops on the fetch path.
type hostLatency struct{ ewmaNs atomic.Int64 }

// observeLatency folds one time-to-first-byte sample into host's EWMA (and the
// all-hosts one the profiling line reads).
func (c *Client) observeLatency(host string, sample time.Duration) {
	if host == "" || sample <= 0 {
		return
	}
	if old := c.globalTTFBNs.Load(); old == 0 {
		c.globalTTFBNs.Store(int64(sample))
	} else {
		c.globalTTFBNs.Store(old + (int64(sample)-old)/ewmaWeightDen)
	}
	v, _ := c.hostLat.LoadOrStore(host, &hostLatency{})
	lat := v.(*hostLatency)
	old := lat.ewmaNs.Load()
	if old == 0 {
		lat.ewmaNs.Store(int64(sample))
		return
	}
	lat.ewmaNs.Store(old + (int64(sample)-old)/ewmaWeightDen)
}

// adaptiveTimeout returns the per-request deadline for host: the global
// timeout until samples exist, then multiple×EWMA clamped to
// [adaptiveTimeoutFloor, global timeout].
func (c *Client) adaptiveTimeout(host string) time.Duration {
	v, ok := c.hostLat.Load(host)
	if !ok {
		return c.timeout
	}
	ewma := time.Duration(v.(*hostLatency).ewmaNs.Load())
	if ewma <= 0 {
		return c.timeout
	}
	multiple := time.Duration(c.latMultiple.Load())
	if multiple == 0 {
		multiple = adaptiveLatencyMultiple // the built-in default (power-user knob unset)
	}
	d := ewma * multiple
	if d < adaptiveTimeoutFloor {
		return adaptiveTimeoutFloor
	}
	if d > c.timeout {
		return c.timeout
	}
	return d
}

// fetchOnce performs the single upstream request behind singleflight. It
// runs detached from any one caller's context, bounded by the host's
// adaptive deadline, so one impatient caller cannot kill the fetch for
// everyone and one slow host cannot pin workers for the global timeout.
func (c *Client) fetchOnce(url string) ([]byte, error) {
	c.requests.Add(1)
	host := hostOf(url)
	ctx, cancel := context.WithTimeout(context.Background(), c.adaptiveTimeout(host))
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("network: building request for %s: %w", url, err)
	}
	if o := c.assetOrigin.Load(); o != nil { // power-user Origin/CORS override
		req.Header.Set("Origin", *o)
		req.Header.Set("Referer", *o+"/") // some bases gate by Referer (hotlink protection) rather than Origin
	}

	start := time.Now()
	resp, err := c.httpClient.Do(req)
	if err != nil {
		c.recordFailure(host)
		c.failures.Add(1)
		return nil, fmt.Errorf("network: fetching %s: %w", url, err)
	}
	// Do returns once headers arrive: this IS the time-to-first-byte.
	c.observeLatency(host, time.Since(start))
	defer func() {
		// Drain so the keep-alive connection is reusable.
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}()

	switch {
	case resp.StatusCode == http.StatusOK:
		data, err := c.readBody(resp)
		if err != nil {
			c.failures.Add(1)
			return nil, fmt.Errorf("network: reading %s: %w", url, err)
		}
		c.recordSuccess(host)
		c.hits.Add(1)
		c.bytesRead.Add(int64(len(data)))
		return data, nil

	case resp.StatusCode == http.StatusNotFound:
		c.notFound.Add(url, struct{}{})
		c.notFounds.Add(1)
		c.recordSuccess(host) // the host is healthy; the asset just isn't there
		return nil, fmt.Errorf("%w: %s", ErrAssetNotFound, url)

	default:
		c.failures.Add(1)
		return nil, fmt.Errorf("network: fetching %s: unexpected status %s", url, resp.Status)
	}
}

// readBody reads the response payload. With a known Content-Length the
// destination is allocated exactly once at final size and filled with
// io.ReadFull — no growth, no copies. Unknown lengths accumulate in a pooled
// scratch buffer and are copied out once.
//
// Deliberate deviation from spec §7's "pooled []byte" for the known-
// length path: the returned payload is retained indefinitely by the T2/T3
// caches, so a pooled buffer could never be returned to the pool — pooling
// would add a copy and zero reuse. See docs/ARCHITECTURE.md.
func (c *Client) readBody(resp *http.Response) ([]byte, error) {
	if n := resp.ContentLength; n > 0 {
		data := make([]byte, n)
		if _, err := io.ReadFull(resp.Body, data); err != nil {
			return nil, err
		}
		return data, nil
	}

	buf := c.bufPool.Get().(*bytes.Buffer)
	defer func() {
		buf.Reset()
		c.bufPool.Put(buf)
	}()
	if _, err := buf.ReadFrom(resp.Body); err != nil {
		return nil, err
	}
	data := make([]byte, buf.Len())
	copy(data, buf.Bytes())
	return data, nil
}

// ForgetNotFound drops url from the negative cache (e.g. the user toggled
// fallbacks and wants an immediate retry).
func (c *Client) ForgetNotFound(url string) {
	c.notFound.Remove(url)
}

// --- Host backoff ------------------------------------------------------------

type hostBackoff struct {
	mu       sync.Mutex
	failures int
	until    time.Time
}

func (c *Client) backoffFor(host string) *hostBackoff {
	if v, ok := c.backoff.Load(host); ok {
		return v.(*hostBackoff)
	}
	v, _ := c.backoff.LoadOrStore(host, &hostBackoff{})
	return v.(*hostBackoff)
}

func (c *Client) backingOff(host string) (time.Time, bool) {
	v, ok := c.backoff.Load(host)
	if !ok {
		return time.Time{}, false
	}
	b := v.(*hostBackoff)
	b.mu.Lock()
	defer b.mu.Unlock()
	if time.Now().Before(b.until) {
		return b.until, true
	}
	return time.Time{}, false
}

func (c *Client) recordFailure(host string) {
	if host == "" {
		return
	}
	b := c.backoffFor(host)
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failures++
	delay := backoffBase << (b.failures - 1)
	if delay > backoffMax || delay <= 0 {
		delay = backoffMax
	}
	b.until = time.Now().Add(delay)
}

func (c *Client) recordSuccess(host string) {
	if host == "" {
		return
	}
	if v, ok := c.backoff.Load(host); ok {
		b := v.(*hostBackoff)
		b.mu.Lock()
		b.failures = 0
		b.until = time.Time{}
		b.mu.Unlock()
	}
}

// hostOf extracts the host:port of an absolute http(s) URL without a full
// url.Parse on the hot path.
func hostOf(rawURL string) string {
	rest := rawURL
	if i := indexAfterScheme(rest); i >= 0 {
		rest = rest[i:]
	} else {
		return ""
	}
	for i := 0; i < len(rest); i++ {
		if rest[i] == '/' || rest[i] == '?' || rest[i] == '#' {
			return rest[:i]
		}
	}
	return rest
}

func indexAfterScheme(rawURL string) int {
	const sep = "://"
	for i := 0; i+len(sep) <= len(rawURL); i++ {
		if rawURL[i:i+len(sep)] == sep {
			return i + len(sep)
		}
	}
	return -1
}

// --- DNS pre-resolve ---------------------------------------------------------

// dnsCache resolves hosts ahead of time so the first probe after server
// connect doesn't pay a DNS round-trip. Entries refresh lazily after
// dnsRefreshInterval: an expired entry is still dialed (last known IP) while
// a background re-resolve replaces it.
type dnsCache struct {
	mu       sync.RWMutex
	entries  map[string]dnsEntry
	resolver *net.Resolver
	dialer   *net.Dialer
}

const (
	dnsRefreshInterval = 5 * time.Minute
	dnsResolveTimeout  = 3 * time.Second
)

type dnsEntry struct {
	addrs   []string
	expires time.Time
}

func newDNSCache() *dnsCache {
	return &dnsCache{
		entries:  map[string]dnsEntry{},
		resolver: net.DefaultResolver,
		dialer:   &net.Dialer{},
	}
}

func (d *dnsCache) preResolve(ctx context.Context, host string) {
	ctx, cancel := context.WithTimeout(ctx, dnsResolveTimeout)
	defer cancel()
	addrs, err := d.resolver.LookupHost(ctx, host)
	if err != nil || len(addrs) == 0 {
		return
	}
	d.mu.Lock()
	d.entries[host] = dnsEntry{addrs: addrs, expires: time.Now().Add(dnsRefreshInterval)}
	d.mu.Unlock()
}

// lookup returns cached addresses and whether a refresh is due.
func (d *dnsCache) lookup(host string) (addrs []string, stale bool, found bool) {
	d.mu.RLock()
	entry, ok := d.entries[host]
	d.mu.RUnlock()
	if !ok || len(entry.addrs) == 0 {
		return nil, false, false
	}
	return entry.addrs, time.Now().After(entry.expires), true
}

// DialContext dials addr, substituting a cached IP for the host when one is
// known so the connection never blocks on DNS.
func (d *dnsCache) DialContext(ctx context.Context, netw, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return d.dialer.DialContext(ctx, netw, addr)
	}
	addrs, stale, found := d.lookup(host)
	if !found {
		return d.dialer.DialContext(ctx, netw, addr)
	}
	if stale {
		go d.preResolve(context.Background(), host)
	}
	conn, err := d.dialer.DialContext(ctx, netw, net.JoinHostPort(addrs[0], port))
	if err != nil {
		// Cached IP went bad: fall back to a normal resolving dial.
		return d.dialer.DialContext(ctx, netw, addr)
	}
	return conn, nil
}

// --- Stats -------------------------------------------------------------------

// ClientStats is a point-in-time counter snapshot for the metrics sampler.
type ClientStats struct {
	Requests   int64 // upstream requests actually issued
	Hits       int64 // 200 responses
	NotFounds  int64 // upstream 404s
	Cached404s int64 // 404s answered from the negative cache
	Failures   int64 // transport errors and unexpected statuses
	BytesRead  int64
}

// Stats snapshots the client's counters.
func (c *Client) Stats() ClientStats {
	return ClientStats{
		Requests:   c.requests.Load(),
		Hits:       c.hits.Load(),
		NotFounds:  c.notFounds.Load(),
		Cached404s: c.cached404s.Load(),
		Failures:   c.failures.Load(),
		BytesRead:  c.bytesRead.Load(),
	}
}
