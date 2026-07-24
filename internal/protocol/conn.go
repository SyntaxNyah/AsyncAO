package protocol

import (
	"context"
	"crypto/tls"
	"errors"
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
	// keepaliveInterval is how often the connection's keepalive goroutine sends the
	// CH ping. Matches AO2-Client's keepalive_timer (courtroom.cpp,
	// keepalive_timer->start(45000)). It runs on its OWN goroutine (keepaliveLoop),
	// NOT the render loop — Windows stalls a minimized app's frame loop when it's
	// occluded by a fullscreen foreground app, which used to freeze the old
	// loop-driven ping and let the server idle-drop the "silent" client.
	keepaliveInterval = 45 * time.Second
	// incomingQueueCap bounds the deliver-loop → client channel; the game
	// loop drains it every frame.
	incomingQueueCap = 256
	// readBacklogCap bounds the read-loop → deliver-loop parking channel that sits
	// in FRONT of Incoming. It exists so readLoop NEVER blocks on a full Incoming:
	// coder/websocket answers the server's WS PING control frames only while some
	// goroutine is inside ws.Read (read.go handleControl), so a reader parked on a
	// channel send stops ponging — and once Incoming filled (hours at idle-room
	// packet rates, ~256 × 60 s) the server's ping-timeout dropped a perfectly
	// healthy minimized client. Windows provably stalls the render loop that drains
	// Incoming while the app is occluded (the v1.81.5 finding), so the socket must
	// stay alive without it — for the WHOLE occlusion, which the reporting users
	// measure in workdays. Sized for a BUSY room's full occluded stretch, not just
	// an idle one (~0.5 packets/s of active courtroom chatter → ~18 h; idle rooms →
	// days), because an occluded-but-healthy client is indistinguishable from a
	// wedged one down here and must not be torn down inside any plausible stall.
	// Costs ~1.6 MiB of preallocated channel buffer per connection (48 B/slot) —
	// noise against the 256 MiB budget, and the courtroom's own bounded IC queue
	// (messageQueueCap drop-oldest + packed-room catch-up) collapses the replay on
	// restore, so depth here never turns into a replay storm. Still a hard, named
	// bound: exhaustion means the drain has been dead for many busy hours with
	// traffic still arriving, and THEN we disconnect (errReadBacklogOverflow).
	readBacklogCap = 32768
	// readBacklogMaxBytes bounds the WIRE bytes parked in the backlog channel, so a
	// hostile/buggy server spraying maximum-size frames (maxIncomingBytes each)
	// can't turn the packet-count bound into gigabytes of buffered strings. Idle
	// traffic is tens of bytes per packet, so real stalls never approach this.
	readBacklogMaxBytes = 32 << 20
	// maxIncomingBytes bounds one server packet defensively (character
	// lists on 200-char servers fit comfortably).
	maxIncomingBytes = 8 << 20

	// userAgent mirrors AO2-Client's request header shape.
	userAgent = "AsyncAO/" + Version + " (Desktop)"
	// Version is the client version advertised in the ID handshake.
	Version = "2.11.0-asyncao"
	// ClientName is the software name sent in the ID packet.
	ClientName = "AsyncAO"
)

// errReadBacklogOverflow is the terminal error when the app has not drained
// Incoming for an entire readBacklogCap of arriving packets (or the backlog's
// byte bound): the render thread is genuinely wedged, not merely napping, so
// the connection is abandoned deliberately rather than buffered without bound.
var errReadBacklogOverflow = errors.New("protocol: incoming packet backlog overflowed (client stopped draining)")

// sizedPacket carries a parsed packet plus its wire size through the backlog
// channel, so the byte bound can be released when the packet is handed on.
type sizedPacket struct {
	p Packet
	n int
}

// Conn is a WebSocket AO2 connection: one text frame in = one packet out the
// Incoming channel; Send serializes and writes one packet. Legacy raw TCP is
// not supported anywhere in AsyncAO.
type Conn struct {
	ws       *websocket.Conn
	incoming chan Packet
	readErr  atomic.Pointer[error]
	closed   chan struct{}
	once     sync.Once

	// backlog decouples readLoop from Incoming (see readBacklogCap): readLoop is
	// its only sender/closer and never blocks on it; deliverLoop moves packets on
	// to Incoming with a blocking send, which is safe because deliverLoop is NOT
	// the goroutine that must keep re-entering ws.Read to answer server pings.
	backlog chan sizedPacket
	// backlogBytes tracks wire bytes currently parked in backlog (single adder:
	// readLoop; single subtractor: deliverLoop) against readBacklogMaxBytes.
	backlogBytes atomic.Int64

	// writeMu serializes every ws.Write: application Sends (render thread) and the
	// keepalive goroutine share the one underlying socket, and coder/websocket
	// permits only a single writer at a time (concurrent reads are fine, which is
	// why readLoop needs no lock). Held only for the duration of a frame write.
	writeMu sync.Mutex
	// keepalive is the CH ping payload the keepalive goroutine sends each interval,
	// swapped atomically by the client (SetKeepalive) so it always carries the
	// current char id. nil means "nothing to send yet" (pre-join / handshake).
	keepalive atomic.Pointer[string]
	// keepaliveEvery is this connection's ping interval (0 → keepaliveInterval
	// default); set from DialOptions so tests can shorten it.
	keepaliveEvery time.Duration

	// notify, when set, is called from the read loop after each packet is
	// queued on Incoming (and once when Incoming closes) — the experimental
	// event-driven render loop's wake hook, so a packet landing mid-idle is
	// processed immediately instead of on the next poll tick. The callback
	// must be cheap and non-blocking; protocol stays SDL-free (the UI injects
	// an SDL wake-event push).
	notify atomic.Pointer[func()]

	sent     atomic.Int64
	received atomic.Int64
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
	// KeepaliveInterval overrides how often the keepalive goroutine pings (0 = the
	// keepaliveInterval package default). Tests set a short value to exercise the
	// goroutine without waiting 45 s; production leaves it zero.
	KeepaliveInterval time.Duration
	// ReadBacklogCap overrides the backlog channel's packet capacity (0 = the
	// readBacklogCap package default). Tests set a tiny value to exercise the
	// wedged-client overflow disconnect without buffering thousands of packets;
	// production leaves it zero.
	ReadBacklogCap int
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

	backlogCap := readBacklogCap
	if len(opts) > 0 && opts[0].ReadBacklogCap > 0 {
		backlogCap = opts[0].ReadBacklogCap
	}
	c := &Conn{
		ws:       ws,
		incoming: make(chan Packet, incomingQueueCap),
		backlog:  make(chan sizedPacket, backlogCap),
		closed:   make(chan struct{}),
	}
	if len(opts) > 0 && opts[0].KeepaliveInterval > 0 {
		c.keepaliveEvery = opts[0].KeepaliveInterval
	}
	go c.readLoop()
	go c.deliverLoop()   // backlog → Incoming hand-off; exits when backlog closes or on Close (§17.4)
	go c.keepaliveLoop() // fires the CH ping off the render loop; exits on Close (no per-conn leak, §17.4)
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
	c.writeMu.Lock()
	err := c.ws.Write(ctx, websocket.MessageText, []byte(p.String()))
	c.writeMu.Unlock()
	if err != nil {
		return fmt.Errorf("protocol: sending %s: %w", p.Header, err)
	}
	c.sent.Add(1)
	return nil
}

// SetKeepalive installs (or clears, with an empty string) the CH ping payload the
// keepalive goroutine sends each keepaliveInterval. The client refreshes it whenever
// the char id can change (join / char pick); an empty string parks the ping (nothing
// to send yet). Safe to call from any goroutine — the render loop sets it, the
// keepalive goroutine reads it, via an atomic swap.
func (c *Conn) SetKeepalive(payload string) {
	if payload == "" {
		c.keepalive.Store(nil)
		return
	}
	c.keepalive.Store(&payload)
}

// keepaliveLoop sends the CH keepalive on a fixed interval from its OWN goroutine,
// independent of the render loop — the whole point of the fix. AsyncAO used to fire
// the ping from the frame/Background pump, but Windows stalls that loop when the app
// is minimized behind a fullscreen foreground app (a video, say), so the ping
// stopped and the server idle-dropped the "silent" client. This goroutine keeps it
// flowing regardless of window state, exactly like readLoop keeps reading. A write
// failure here is a genuinely dead socket (this is real traffic we must send, not a
// probe), so it surfaces the drop through the same readErr/Close path as any read
// error — no false positives like the removed staleness watchdog. Exits when the
// conn closes, so a reconnect cycle can't leak it (rule §17.4).
func (c *Conn) keepaliveLoop() {
	every := c.keepaliveEvery
	if every <= 0 {
		every = keepaliveInterval
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-c.closed:
			return
		case <-t.C:
		}
		p := c.keepalive.Load()
		if p == nil {
			continue // not joined yet — nothing to ping with
		}
		ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
		c.writeMu.Lock()
		err := c.ws.Write(ctx, websocket.MessageText, []byte(*p))
		c.writeMu.Unlock()
		cancel()
		if err != nil {
			// A real write failed → the socket is dead. Record it (first error wins;
			// after Close, readLoop won't clobber it) and Close, so Incoming closes and
			// the drop reaches the UI (→ lobby) without waiting for the read side.
			e := fmt.Errorf("protocol: keepalive write failed: %w", err)
			c.readErr.CompareAndSwap(nil, &e)
			c.Close()
			return
		}
		c.sent.Add(1)
	}
}

// Close tears the connection down. Safe to call multiple times.
func (c *Conn) Close() {
	c.once.Do(func() {
		close(c.closed)
		_ = c.ws.Close(websocket.StatusGoingAway, "client closing")
	})
}

// readLoop's ONE invariant is that it always returns to ws.Read promptly: the
// library answers server WS PING control frames only from inside an in-flight
// Read, and an unanswered ping is how servers drop a healthy idle client (the
// hours-minimized disconnect — the render loop that drains Incoming is stalled
// by the OS while occluded, so this goroutine is the only thing keeping the
// socket alive). It therefore NEVER blocks on a channel send: packets go into
// backlog non-blockingly, and deliverLoop does the blocking hand-off to
// Incoming. Backlog exhaustion (count or bytes) is the deliberate exception:
// the drain has been dead for the whole bound with traffic still arriving, so
// the client is wedged and disconnecting is correct.
func (c *Conn) readLoop() {
	// readLoop is backlog's only sender, so it closes it on exit; deliverLoop
	// then finishes delivering whatever is parked and closes Incoming — a
	// dropped connection still surfaces (the "connection closed" toast + tab
	// teardown) right after the buffered packets. Deliberately NO Close() on
	// the transport-error path below: closing c.closed would make deliverLoop
	// abandon parked packets, and surfacing the drop "immediately" buys
	// nothing while the drain is stalled (nobody is watching a stalled
	// window; on resume pumpConnection drains the parked packets AND hits the
	// channel close in the same call, so the drop surfaces the same frame).
	// Until then deliverLoop just stays parked — bounded, one per conn, and
	// released by the resume, by keepaliveLoop's write-failure Close, or by
	// the app's own teardown Close.
	defer close(c.backlog)
	for {
		msgType, data, err := c.ws.Read(context.Background())
		if err != nil {
			select {
			case <-c.closed: // deliberate Close: not an error
			default:
				e := err
				c.readErr.Store(&e)
			}
			return
		}
		if msgType != websocket.MessageText {
			continue // AO is a text protocol; ignore stray binary frames
		}
		packet, err := ParsePacket(string(data))
		if err != nil {
			continue // tolerate malformed frames like AO2-Client does
		}
		c.received.Add(1)
		if c.backlogBytes.Load()+int64(len(data)) > readBacklogMaxBytes {
			c.readErr.CompareAndSwap(nil, &errReadBacklogOverflow)
			c.Close()
			return
		}
		select {
		case c.backlog <- sizedPacket{p: packet, n: len(data)}:
			c.backlogBytes.Add(int64(len(data)))
		case <-c.closed:
			return
		default:
			// Backlog full: the app hasn't drained Incoming for the entire
			// bound while packets kept arriving — wedged, not napping.
			c.readErr.CompareAndSwap(nil, &errReadBacklogOverflow)
			c.Close()
			return
		}
	}
}

// deliverLoop moves packets backlog → Incoming. Its send may block for hours —
// that's the point: blocking HERE (instead of in readLoop) is what keeps the
// reader inside ws.Read answering server pings while the render loop is
// stalled. When the drain resumes, delivery resumes instantly (no waiting for
// the next inbound frame). It is Incoming's only closer. On a transport drop
// (readLoop exits WITHOUT Close, so c.closed stays open) the select's closed
// arm is dead and every parked packet is delivered before the range ends —
// nothing is dropped ahead of the close notification. Once c.closed IS closed
// (deliberate Close, overflow, or keepaliveLoop's write-failure Close on an
// already-dead socket) still-parked packets may be abandoned — acceptable, the
// connection is being torn down and a reconnect resyncs full server state.
func (c *Conn) deliverLoop() {
	defer func() {
		close(c.incoming)
		c.wake() // the close wake: a drop must surface as fast as a packet would
	}()
	for item := range c.backlog {
		select {
		case c.incoming <- item.p:
			c.backlogBytes.Add(-int64(item.n))
			c.wake()
		case <-c.closed:
			return // deliberate Close / overflow teardown: stop delivering
		}
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
