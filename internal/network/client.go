// Package network fetches assets with deduplication and negative caching, and talks to the AO master server.
// In-flight fetches collapse, cached 404s stay off the wire inside their TTL, and failing hosts back off.
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

	"github.com/SyntaxNyah/AsyncAO/internal/helper/value"
	"github.com/SyntaxNyah/AsyncAO/internal/netproxy"
	"github.com/SyntaxNyah/AsyncAO/internal/update"
)

const (
	// Idle-per-host matches the worker pool, so back-to-back fetch bursts reuse connections instead of reconnecting.
	defaultMaxConnsPerHost     = 16
	defaultMaxIdleConnsPerHost = 16
	defaultMaxIdleConnsTotal   = 96
	defaultIdleConnTimeout     = 90 * time.Second
	// defaultTLSHandshakeTimeout: the handshake runs before the request is written, so the header timeout never
	// covers it. Slow hosts need seconds; fast hosts are still cut by the adaptive deadline (2s floor).
	defaultTLSHandshakeTimeout = 15 * time.Second
	// defaultResponseHeaderTimeout: slow file hosts, where music lives, take seconds to send the first byte.
	defaultResponseHeaderTimeout = 15 * time.Second
	tlsSessionCacheSize          = 64

	// DefaultRequestTimeout caps the wait for headers, not the body, and is the adaptive deadline's ceiling.
	DefaultRequestTimeout = 15 * time.Second

	// bodyTransferBudget is separate from the header deadline: the TTFB EWMA is dominated by tiny sprite probes,
	// and sharing it would cut large music files mid-stream.
	bodyTransferBudget = 180 * time.Second

	// NotFoundCacheSize and NotFoundCacheTTL bound the negative cache (hard rule 6).
	NotFoundCacheSize = 1024
	NotFoundCacheTTL  = 5 * time.Minute

	// Host failure backoff: doubles per consecutive failure, capped.
	backoffBase = 500 * time.Millisecond
	backoffMax  = 30 * time.Second

	// A host's deadline is adaptiveLatencyMultiple x its TTFB EWMA, clamped to [adaptiveTimeoutFloor, client
	// timeout], so a degrading mirror doesn't pin fetch workers.
	adaptiveLatencyMultiple = 8
	adaptiveTimeoutFloor    = 2 * time.Second
	// EWMA weight 1/4: tracks congestion without letting one blip swing the average.
	ewmaWeightDen = 4

	// unknownLengthPrealloc seeds the scratch buffer for responses without a Content-Length.
	unknownLengthPrealloc = 64 << 10
)

// ErrAssetNotFound reports a 404, possibly served from the negative cache.
var ErrAssetNotFound = errors.New("network: asset not found")

// ErrHostBackingOff reports that the host is inside its exponential backoff window.
var ErrHostBackingOff = errors.New("network: host backing off after failures")

// Client is the deduplicating, negatively-caching asset fetcher, safe for concurrent use.
type Client struct {
	sockets    *netproxy.Factory
	httpClient *http.Client //nolint:forbidigo // its transport comes from the factory
	group      singleflight.Group
	notFound   *expirable.LRU[string, struct{}]
	dns        *dnsCache
	backoff    sync.Map // host string → *hostBackoff
	hostLat    sync.Map // host string → *hostLatency (TTFB EWMA)
	timeout    time.Duration
	// bodyBudget is a field rather than the const so tests can shrink it.
	bodyBudget time.Duration
	bufPool    sync.Pool // *bytes.Buffer for unknown-length reads

	requests   atomic.Int64
	hits       atomic.Int64
	notFounds  atomic.Int64
	cached404s atomic.Int64
	failures   atomic.Int64
	bytesRead  atomic.Int64

	// assetOrigin, if set, is the Origin and Referer on every asset fetch, for servers restricting by origin.
	assetOrigin atomic.Pointer[string]

	// latMultiple overrides adaptiveLatencyMultiple when non-zero.
	latMultiple atomic.Int32

	// globalTTFBNs is the all-hosts TTFB EWMA, for the debug overlay's single fetch number.
	globalTTFBNs atomic.Int64
}

// AvgTTFB reports the all-hosts time-to-first-byte EWMA; zero until the first sample.
func (c *Client) AvgTTFB() time.Duration { return time.Duration(c.globalTTFBNs.Load()) }

// Bounds enforced by the setter, so a bad preference can't produce a 0x or 1000x deadline.
const (
	latMultipleFloor = 1
	latMultipleCeil  = 64
)

// SetAdaptiveLatencyMultiple overrides the deadline multiple; 0 means default, lower gives up on a mirror sooner.
func (c *Client) SetAdaptiveLatencyMultiple(n int) {
	if n != 0 {
		n = value.Clamp(n, latMultipleFloor, latMultipleCeil)
	}
	c.latMultiple.Store(int32(n))
}

// SetAssetOrigin sets the Origin and Referer on every asset fetch, or clears them when empty; safe from any goroutine.
func (c *Client) SetAssetOrigin(origin string) {
	if origin == "" {
		c.assetOrigin.Store(nil)
		return
	}
	c.assetOrigin.Store(&origin)
}

// NewClientNotFoundTTL sets the 404 TTL (0 means NotFoundCacheTTL) at construction: the expirable LRU takes it
// there, and rebuilding it live would flush every cached 404.
func NewClientNotFoundTTL(ttl time.Duration, sockets *netproxy.Factory) *Client {
	if ttl <= 0 {
		ttl = NotFoundCacheTTL
	}
	return newClient(DefaultRequestTimeout, ttl, sockets)
}

func newClient(timeout, notFoundTTL time.Duration, sockets *netproxy.Factory) *Client {
	dns := newDNSCache()
	transport := sockets.NewTransport()
	transport.MaxConnsPerHost = defaultMaxConnsPerHost
	transport.MaxIdleConnsPerHost = defaultMaxIdleConnsPerHost
	transport.MaxIdleConns = defaultMaxIdleConnsTotal
	transport.IdleConnTimeout = defaultIdleConnTimeout
	transport.ResponseHeaderTimeout = defaultResponseHeaderTimeout
	// Assets are pre-compressed media, so transparent gzip would waste CPU.
	transport.DisableCompression = true
	transport.ForceAttemptHTTP2 = true
	transport.TLSHandshakeTimeout = defaultTLSHandshakeTimeout
	transport.TLSClientConfig = &tls.Config{
		ClientSessionCache: tls.NewLRUClientSessionCache(tlsSessionCacheSize),
	}
	transport.DialContext = sockets.Dialer(dns.DialContext)
	sockets.Track(transport)
	return &Client{
		sockets:    sockets,
		httpClient: &http.Client{Transport: transport}, //nolint:forbidigo // its transport comes from the factory
		notFound:   expirable.NewLRU[string, struct{}](NotFoundCacheSize, nil, notFoundTTL),
		dns:        dns,
		timeout:    timeout,
		bodyBudget: bodyTransferBudget,
		bufPool: sync.Pool{
			New: func() any {
				buf := &bytes.Buffer{}
				buf.Grow(unknownLengthPrealloc)
				return buf
			},
		},
	}
}

// PreResolve warms the DNS cache for host so the first asset probe doesn't block on a lookup.
// A proxied name is never looked up here, as that would publish it to the machine's resolver.
func (c *Client) PreResolve(ctx context.Context, host string) {
	if host == "" || c.sockets.Proxied(host) {
		return
	}
	c.dns.preResolve(ctx, host)
}

// Fetch shares one upstream request between callers; ctx cancels only this caller's wait, and the returned
// slice must be treated as immutable.
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
		// The shared fetch keeps running for other waiters.
		return nil, ctx.Err()
	}
}

// hostLatency uses plain load and store, as a lost update only delays the average by one sample.
type hostLatency struct{ ewmaNs atomic.Int64 }

// observeLatency folds one TTFB sample into the host's EWMA and the all-hosts one.
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

// adaptiveTimeout is the global timeout until samples exist, then multiple x EWMA, clamped.
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

// clientUserAgent replaces Go's default, which sits on WAF and bot-filter lists in front of asset mirrors.
var clientUserAgent = "AsyncAO/" + update.Version

const (
	// transientFetchAttempts bounds tries after a transport error or 5xx, as CDN-fronted origins usually succeed
	// on immediate retry; a 404 is definitive.
	transientFetchAttempts = 2
	// transientRetryDelay lets the CDN pick a healthy origin connection, and still resolves within one message.
	transientRetryDelay = 300 * time.Millisecond
)

// fetchOnce is detached from any caller's context, so one impatient caller can't kill the shared fetch;
// the host's adaptive deadline bounds each attempt.
func (c *Client) fetchOnce(url string) ([]byte, error) {
	var lastErr error
	for attempt := 0; attempt < transientFetchAttempts; attempt++ {
		if attempt > 0 {
			time.Sleep(transientRetryDelay)
		}
		data, err := c.fetchAttempt(url)
		if err == nil || errors.Is(err, ErrAssetNotFound) {
			return data, err // success, or a definitive miss — no retry
		}
		lastErr = err
	}
	return nil, lastErr
}

// fetchAttempt is one request: 200 gives bytes, 404 the negative-cached miss, anything else a transient error.
func (c *Client) fetchAttempt(url string) ([]byte, error) {
	c.requests.Add(1)
	host := hostOf(url)
	// One timer drives two deadlines: adaptive TTFB for headers, then the body budget, so a small-asset EWMA
	// can't cut a large body mid-stream.
	ctx, cancel := context.WithCancel(context.Background())
	deadline := time.AfterFunc(c.adaptiveTimeout(host), cancel)
	defer cancel()
	defer deadline.Stop()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("network: building request for %s: %w", url, err)
	}
	req.Header.Set("User-Agent", clientUserAgent)
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
	// Headers arrived: swap to the body budget. If the timer already fired, ctx is canceled and the read fails fast.
	deadline.Reset(c.bodyBudget)
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
		// 5xx means the host is misbehaving, so it counts toward backoff; fetchOnce's retry absorbs one-off hiccups.
		c.recordFailure(host)
		c.failures.Add(1)
		return nil, fmt.Errorf("network: fetching %s: unexpected status %s", url, resp.Status)
	}
}

// readBody allocates once for a known length; that path can't pool because T2 and T3 retain the payload
// indefinitely (docs/ARCHITECTURE.md). Unknown lengths use a pooled scratch buffer.
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

// ForgetNotFound drops url from the negative cache.
func (c *Client) ForgetNotFound(url string) {
	c.notFound.Remove(url)
}

// ClearNotFound forgets every 404; Settings pairs it with the manager's conclusive-miss memory.
func (c *Client) ClearNotFound() {
	c.notFound.Purge()
}

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
	now := time.Now()
	// Count at most one failure per backoff window: the 16 workers time out together on one hiccup, and counting
	// each would saturate the delay at backoffMax. Inside a window, extend it without bumping the counter.
	// A down host still climbs across windows:
	if now.Before(b.until) {
		delay := backoffBase << (b.failures - 1)
		if delay > backoffMax || delay <= 0 {
			delay = backoffMax
		}
		b.until = now.Add(delay)
		return
	}
	b.failures++
	delay := backoffBase << (b.failures - 1)
	if delay > backoffMax || delay <= 0 {
		delay = backoffMax
	}
	b.until = now.Add(delay)
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

// hostOf extracts host:port without a full url.Parse on the hot path.
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

// dnsCache dials an expired entry's last known IP while a background re-resolve replaces it.
type dnsCache struct {
	mu       sync.RWMutex
	entries  map[string]dnsEntry
	resolver *net.Resolver
	dialer   *net.Dialer //nolint:forbidigo // only reached through the socket factory, for direct routes
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
		dialer:   &net.Dialer{}, //nolint:forbidigo // only reached through the socket factory, for direct routes
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

// DialContext substitutes a cached IP for the host when known, so the connection doesn't block on DNS.
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

// ClientStats is a counter snapshot for the metrics sampler.
type ClientStats struct {
	Requests   int64 // upstream requests actually issued
	Hits       int64 // 200 responses
	NotFounds  int64 // upstream 404s
	Cached404s int64 // 404s answered from the negative cache
	Failures   int64 // transport errors and unexpected statuses
	BytesRead  int64
}

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
