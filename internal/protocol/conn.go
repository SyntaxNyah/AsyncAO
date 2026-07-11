package protocol

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
)

const (
	// dialTimeout caps the WebSocket handshake.
	dialTimeout = 10 * time.Second
	// writeTimeout caps one outgoing packet write.
	writeTimeout = 10 * time.Second
	// incomingQueueCap bounds the read-loop → client channel; the game
	// loop drains it every frame.
	incomingQueueCap = 256
	// maxIncomingBytes bounds one server packet defensively (character
	// lists on 200-char servers fit comfortably).
	maxIncomingBytes = 8 << 20

	// staleReadTimeout is how long the read stream may go silent before the
	// staleness watchdog probes the link. It MUST be ≥ 2× the client keepalive
	// (the UI's keepalivePeriod = 45 s): a healthy server answers every CH ping,
	// so at ~2× the ping period a silent read stream means the link is dead
	// (NAT timeout / no FIN), not merely quiet. Named so both the UI keepalive
	// and this stay in step. Overridable per-Dial (DialOptions) for tests.
	staleReadTimeout = 100 * time.Second
	// staleWatchdogInterval is how often the watchdog checks read-staleness. It
	// must be well below staleReadTimeout so the silence is caught promptly but
	// far above one frame, so the goroutine barely wakes.
	staleWatchdogInterval = 5 * time.Second
	// stalePingTimeout bounds the ping round-trip the watchdog fires on silence:
	// no pong within this window and the connection is declared dead.
	stalePingTimeout = 10 * time.Second

	// userAgent mirrors AO2-Client's request header shape.
	userAgent = "AsyncAO/" + Version + " (Desktop)"
	// Version is the client version advertised in the ID handshake.
	Version = "2.11.0-asyncao"
	// ClientName is the software name sent in the ID packet.
	ClientName = "AsyncAO"
)

// Conn is a WebSocket AO2 connection: one text frame in = one packet out the
// Incoming channel; Send serializes and writes one packet. Legacy raw TCP is
// not supported anywhere in AsyncAO.
type Conn struct {
	ws       *websocket.Conn
	incoming chan Packet
	readErr  atomic.Pointer[error]
	closed   chan struct{}
	once     sync.Once

	// notify, when set, is called from the read loop after each packet is
	// queued on Incoming (and once when Incoming closes) — the experimental
	// event-driven render loop's wake hook, so a packet landing mid-idle is
	// processed immediately instead of on the next poll tick. The callback
	// must be cheap and non-blocking; protocol stays SDL-free (the UI injects
	// an SDL wake-event push).
	notify atomic.Pointer[func()]

	sent     atomic.Int64
	received atomic.Int64

	// lastReadAt is the unix-nano time of the most recent successful read (set
	// at Dial and after every framed packet). The staleness watchdog reads it to
	// tell a silent-but-alive link from a dead one. Atomic: written by readLoop,
	// read by the watchdog goroutine.
	lastReadAt atomic.Int64
	// staleReadTimeout is this connection's read-silence threshold (0 → the
	// package default); set from DialOptions so tests can shorten it.
	staleTimeout time.Duration
}

// SetNotify installs (or clears, with nil) the packet-arrival wake callback.
// Safe to call at any time from any goroutine.
func (c *Conn) SetNotify(f func()) {
	if f == nil {
		c.notify.Store(nil)
		return
	}
	c.notify.Store(&f)
}

// wake invokes the notify callback if one is installed.
func (c *Conn) wake() {
	if f := c.notify.Load(); f != nil {
		(*f)()
	}
}

// DialOptions tunes a Dial. The zero value is the secure default: wss:// TLS
// certificates are verified by the transport. SkipTLSVerify mirrors the
// power-user "Validate server certificates" setting being OFF (the app default,
// so self-signed community AO servers stay reachable). ws:// is never affected.
//
// Origin, when non-empty, is sent as the HTTP Origin header on the WebSocket
// handshake — the power-user escape hatch for servers that allowlist only their
// own web client's origin on the SOCKET (e.g. sof.beauty accepts only
// "webao.sof.beauty"; the check is trivially spoofable, but without the header
// the server refuses the upgrade). Empty = no Origin header (today's behaviour;
// a native client normally sends none). The asset-fetch counterpart is the
// separate Asset Origin override in internal/network.
type DialOptions struct {
	SkipTLSVerify bool
	Origin        string
	// StaleReadTimeout overrides the read-silence threshold the staleness
	// watchdog uses (0 = the staleReadTimeout package default). Tests set a
	// short value to exercise the watchdog without waiting minutes; production
	// leaves it zero.
	StaleReadTimeout time.Duration
}

// Dial connects to a ws:// or wss:// AO server URL and starts the read loop.
// An optional DialOptions controls TLS verification; the zero value verifies.
func Dial(ctx context.Context, wsURL string, opts ...DialOptions) (*Conn, error) {
	ctx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()

	dialOpts := &websocket.DialOptions{
		HTTPHeader: http.Header{"User-Agent": []string{userAgent}},
	}
	// Power-user WS Origin override (see DialOptions.Origin). Set only when
	// configured, so the default handshake is byte-identical to before.
	if len(opts) > 0 && opts[0].Origin != "" {
		dialOpts.HTTPHeader.Set("Origin", opts[0].Origin)
	}
	// Only wss:// is affected — ws:// upgrades over plain TCP and ignores the TLS
	// config. We install a custom client ONLY when skipping verification, so the
	// verifying default keeps coder/websocket's shared default client untouched.
	if len(opts) > 0 && opts[0].SkipTLSVerify {
		dialOpts.HTTPClient = &http.Client{
			Transport: &http.Transport{
				// Opt-in, gated behind the power-user Security toggle (default OFF =
				// accept self-signed, which most community AO servers use).
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			},
		}
	}

	ws, _, err := websocket.Dial(ctx, wsURL, dialOpts)
	if err != nil {
		return nil, fmt.Errorf("protocol: dialing %s: %w", wsURL, err)
	}
	ws.SetReadLimit(maxIncomingBytes)

	c := &Conn{
		ws:       ws,
		incoming: make(chan Packet, incomingQueueCap),
		closed:   make(chan struct{}),
	}
	if len(opts) > 0 && opts[0].StaleReadTimeout > 0 {
		c.staleTimeout = opts[0].StaleReadTimeout
	}
	c.lastReadAt.Store(time.Now().UnixNano()) // the dial itself counts as fresh traffic
	go c.readLoop()
	go c.watchStale() // read-staleness watchdog; exits when the conn closes (no per-conn leak)
	return c, nil
}

// Incoming returns the channel of parsed server packets. It closes when the
// connection dies; Err then reports why.
func (c *Conn) Incoming() <-chan Packet {
	return c.incoming
}

// Err reports the terminal read error after Incoming closes (nil on clean
// shutdown via Close).
func (c *Conn) Err() error {
	if p := c.readErr.Load(); p != nil {
		return *p
	}
	return nil
}

// Send serializes and writes one packet.
func (c *Conn) Send(ctx context.Context, p Packet) error {
	ctx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()
	if err := c.ws.Write(ctx, websocket.MessageText, []byte(p.String())); err != nil {
		return fmt.Errorf("protocol: sending %s: %w", p.Header, err)
	}
	c.sent.Add(1)
	return nil
}

// Close tears the connection down. Safe to call multiple times.
func (c *Conn) Close() {
	c.once.Do(func() {
		close(c.closed)
		_ = c.ws.Close(websocket.StatusGoingAway, "client closing")
	})
}

func (c *Conn) readLoop() {
	// The close wake matters too: a dropped connection must surface (the
	// "connection closed" toast + tab teardown) as fast as a packet would.
	defer func() {
		close(c.incoming)
		c.wake()
	}()
	for {
		msgType, data, err := c.ws.Read(context.Background())
		if err != nil {
			select {
			case <-c.closed: // deliberate Close (or a watchdog-declared stale death): not a fresh error
			default:
				c.storeReadErr(err)
			}
			return
		}
		// A framed read of ANY kind (even an ignored binary/pong-shaped frame)
		// means the link is alive — reset the staleness clock before filtering.
		c.lastReadAt.Store(time.Now().UnixNano())
		if msgType != websocket.MessageText {
			continue // AO is a text protocol; ignore stray binary frames
		}
		packet, err := ParsePacket(string(data))
		if err != nil {
			continue // tolerate malformed frames like AO2-Client does
		}
		c.received.Add(1)
		select {
		case c.incoming <- packet:
			c.wake()
		case <-c.closed:
			return
		}
	}
}

// storeReadErr records the terminal read error, but only the FIRST one wins:
// the staleness watchdog stores its "connection stale" error and then calls
// Close(), which unblocks readLoop's Read with a socket error — this guard keeps
// that later, less-informative error from clobbering the watchdog's diagnosis.
func (c *Conn) storeReadErr(err error) {
	e := err
	c.readErr.CompareAndSwap(nil, &e)
}

// watchStale is the read-staleness watchdog: one goroutine per connection, tied
// to the connection's lifetime (it exits the moment closed fires, so a
// reconnect cycle can't leak goroutines — rule §17.4). On a link that goes
// silent past staleTimeout (NAT/idle timeout with no FIN, which leaves ws.Read
// blocked forever), it fires a bounded Ping; a missing pong declares the
// connection dead, which flows through the same closed-Incoming path as any
// other drop (so auto-reconnect handles it uniformly). Ping is concurrency-safe
// with the read loop and Send (coder/websocket), and this stays SDL-free.
func (c *Conn) watchStale() {
	timeout := c.staleTimeout
	if timeout <= 0 {
		timeout = staleReadTimeout
	}
	// Check more often than one full timeout so silence is caught promptly,
	// but never below the interval floor (a tiny test timeout mustn't spin).
	interval := staleWatchdogInterval
	if interval > timeout {
		interval = timeout
	}
	// Cap the probe's ping timeout at the staleness window: probing longer than
	// the window itself is pointless, and it keeps a short (test) threshold fast.
	// In production timeout ≫ stalePingTimeout, so this is the 10 s ping budget.
	pingTimeout := stalePingTimeout
	if pingTimeout > timeout {
		pingTimeout = timeout
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-c.closed:
			return
		case <-ticker.C:
		}
		last := time.Unix(0, c.lastReadAt.Load())
		if time.Since(last) < timeout {
			continue // recent traffic: the link is alive
		}
		// Silent past the threshold — probe the peer. A pong resets lastReadAt
		// via the read loop; no pong within pingTimeout means it's dead.
		ctx, cancel := context.WithTimeout(context.Background(), pingTimeout)
		_, err := c.Ping(ctx)
		cancel()
		if err != nil {
			c.storeReadErr(fmt.Errorf("protocol: connection stale (no data for %s, ping failed: %w)", timeout, err))
			c.Close() // unblocks readLoop → Incoming closes → the drop surfaces
			return
		}
		// Pong arrived: the link is alive. coder/websocket consumes the pong
		// control frame internally (it never surfaces as a data read), so reset
		// the staleness clock here — otherwise a genuinely idle link would be
		// re-probed every interval.
		c.lastReadAt.Store(time.Now().UnixNano())
	}
}

// Ping sends a WebSocket ping control frame and returns the round-trip time once the peer's
// pong arrives (or an error if ctx fires first / the conn died). coder/websocket permits Ping
// concurrently with the read loop and Send, so a background ping-loop can measure latency
// without disturbing traffic. Used by the optional connection-quality chip (#128).
func (c *Conn) Ping(ctx context.Context) (time.Duration, error) {
	start := time.Now()
	if err := c.ws.Ping(ctx); err != nil {
		return 0, err
	}
	return time.Since(start), nil
}

// ConnStats is a point-in-time counter snapshot.
type ConnStats struct {
	Sent     int64
	Received int64
}

// Stats snapshots the connection counters.
func (c *Conn) Stats() ConnStats {
	return ConnStats{Sent: c.sent.Load(), Received: c.received.Load()}
}
