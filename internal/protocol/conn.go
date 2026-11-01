package protocol

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	aolib "github.com/AO-Underground/aolib/go/v2"

	"github.com/SyntaxNyah/AsyncAO/internal/netproxy"
	"github.com/SyntaxNyah/AsyncAO/internal/packetutil"
)

const (
	dialTimeout = 10 * time.Second
	// writeTimeout caps one outgoing packet write.
	writeTimeout = 10 * time.Second
	// writeQueueCap bounds packets waiting for the socket; a connection this far behind is treated as dead.
	writeQueueCap = 64
	// closeDrainTimeout caps sending what was queued before a deliberate Close.
	closeDrainTimeout = 2 * time.Second
	// keepaliveInterval matches AO2-Client's keepalive_timer (courtroom.cpp).
	keepaliveInterval = 45 * time.Second
	// incomingQueueCap bounds the deliver-loop to client channel; the game loop drains it every frame.
	incomingQueueCap = 256
	// readBacklogCap keeps readLoop from blocking on a full Incoming: coder/websocket answers pings only inside
	// ws.Read, and Windows can stall the draining render loop for a whole occlusion.
	// Sized for about 18 h of a busy room: a stalled client looks like a wedged one and must not be torn down.
	readBacklogCap = 32768
	// readBacklogMaxBytes stops maximum-size frames turning the packet bound into gigabytes of strings.
	readBacklogMaxBytes = 32 << 20
	// maxIncomingBytes bounds one server packet.
	maxIncomingBytes = 8 << 20

	// userAgent mirrors AO2-Client's request header shape.
	userAgent = "AsyncAO/" + Version + " (Desktop)"
	// Version is the client version advertised in the ID handshake.
	Version = "2.11.0-asyncao"
	// ClientName is the software name sent in the ID packet.
	ClientName = "AsyncAO"
)

// errReadBacklogOverflow ends the connection when Incoming went undrained for the whole backlog bound.
var errReadBacklogOverflow = errors.New("protocol: incoming packet backlog overflowed (client stopped draining)")

// errWriteQueueFull ends the connection when the socket stopped taking packets for the whole queue.
var errWriteQueueFull = errors.New("protocol: outgoing packet queue full (connection stalled)")

// sizedPacket carries a packet's wire size so the backlog's byte bound can be released on hand-off.
type sizedPacket struct {
	p Packet
	n int
}

// Conn is a WebSocket AO2 connection: one text frame in, one packet out on Incoming.
type Conn struct {
	ws       *websocket.Conn
	incoming chan Packet
	readErr  atomic.Pointer[error]
	closed   chan struct{}
	once     sync.Once

	// sess decodes inbound frames; Receive is called only by readLoop, and the handlers run on that goroutine.
	sess *aolib.ServerSession
	// frameBytes is the current frame's wire size, for the byte bound; only the readLoop goroutine touches it.
	frameBytes int
	// jsonMode is the outbound format (true is JSON); it flips on the first JSON frame or the decryptor flag.
	jsonMode atomic.Bool

	// backlog decouples readLoop (only sender and closer, never blocks) from Incoming; deliverLoop may block
	// because it doesn't have to answer server pings.
	backlog chan sizedPacket
	// backlogBytes counts wire bytes parked in backlog; readLoop only adds, deliverLoop only subtracts.
	backlogBytes atomic.Int64

	// writes holds encoded frames for writeLoop, the only code that writes to or closes the socket.
	writes chan []byte
	// writeErr is set when a write fails; the socket is unusable after that, so later sends fail at once.
	writeErr atomic.Pointer[error]
	// keepalive is the CH ping payload carrying the current char id; nil means nothing to send yet.
	keepalive atomic.Pointer[string]
	// keepaliveEvery is the ping interval; 0 means keepaliveInterval.
	keepaliveEvery time.Duration

	// notify, if set, wakes the event-driven render loop after each packet and when Incoming closes.
	// It must be cheap and non-blocking, and keeps protocol SDL-free.
	notify atomic.Pointer[func()]

	sent     atomic.Int64
	received atomic.Int64
}

// SetNotify installs or, with nil, clears the packet-arrival callback; safe from any goroutine.
func (c *Conn) SetNotify(f func()) {
	if f == nil {
		c.notify.Store(nil)
		return
	}
	c.notify.Store(&f)
}

func (c *Conn) wake() {
	if f := c.notify.Load(); f != nil {
		(*f)()
	}
}

// DialOptions tunes a Dial; the zero value verifies wss:// TLS certificates.
type DialOptions struct {
	SkipTLSVerify bool
	Origin        string
	// KeepaliveInterval overrides the ping interval; 0 means keepaliveInterval.
	KeepaliveInterval time.Duration
	// ReadBacklogCap overrides the backlog's packet capacity; 0 means readBacklogCap.
	ReadBacklogCap int
}

// registerCodecsOnce: the packet registries are plain maps, so writing them on a second Dial while a live
// session reads them is a fatal concurrent map access.
var registerCodecsOnce sync.Once

func registerCodecs() {
	packetutil.RegisterGroupPairCodec()
}

// Dial connects to a ws:// or wss:// AO server URL and starts the read loop.
func Dial(ctx context.Context, sockets *netproxy.Factory, wsURL string, opts ...DialOptions) (*Conn, error) {
	ctx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()

	dialOpts := &websocket.DialOptions{
		HTTPHeader: http.Header{"User-Agent": []string{userAgent}},
	}
	if len(opts) > 0 && opts[0].Origin != "" {
		dialOpts.HTTPHeader.Set("Origin", opts[0].Origin)
	}
	kind := netproxy.Game
	if len(opts) > 0 && opts[0].SkipTLSVerify {
		kind = netproxy.GameSkipVerify
	}
	dialOpts.HTTPClient = sockets.Current().Client(kind, 0)

	ws, resp, err := websocket.Dial(ctx, wsURL, dialOpts)
	if err != nil {
		// A non-nil resp means the server refused, usually with instructions (whitelist, appeal link).
		if resp != nil {
			return nil, &DialError{
				URL:        wsURL,
				StatusCode: resp.StatusCode,
				Status:     resp.Status,
				Body:       dialBody(resp),
				Err:        err,
			}
		}
		return nil, fmt.Errorf("protocol: dialing %s: %w", wsURL, err)
	}
	ws.SetReadLimit(maxIncomingBytes)

	// Registered once only; see registerCodecsOnce.
	registerCodecsOnce.Do(registerCodecs)

	backlogCap := readBacklogCap
	if len(opts) > 0 && opts[0].ReadBacklogCap > 0 {
		backlogCap = opts[0].ReadBacklogCap
	}
	c := &Conn{
		ws:       ws,
		incoming: make(chan Packet, incomingQueueCap),
		backlog:  make(chan sizedPacket, backlogCap),
		writes:   make(chan []byte, writeQueueCap),
		closed:   make(chan struct{}),
	}
	c.sess = aolib.NewServer(aolib.SessionConfig{
		Send: func(wire []byte) { _ = c.queueWrite(wire) },
		// Headers aolib doesn't model (MU, UM, SD, CASEA, checkconnection) are framed positionally from the Fanta wire.
		OnUnknownHeader: func(header string, wire []byte) {
			pkt, err := aolib.NewPacket(strings.TrimSuffix(string(wire), "%"))
			if err != nil {
				return
			}
			c.enqueue(Packet{Header: header, Fields: unescapeArgs(pkt.Body)})
		},
		// Undecodable frames are dropped, matching AO2-Client's tolerance of a bad server frame.
	})
	c.registerInbound()
	if len(opts) > 0 && opts[0].KeepaliveInterval > 0 {
		c.keepaliveEvery = opts[0].KeepaliveInterval
	}
	go c.readLoop()
	go c.writeLoop()
	go c.deliverLoop()   // backlog → Incoming hand-off; exits when backlog closes or on Close (§17.4)
	go c.keepaliveLoop() // fires the CH ping off the render loop; exits on Close (no per-conn leak, §17.4)
	return c, nil
}

// Incoming returns the channel of parsed server packets; it closes when the connection dies, and Err then reports why.
func (c *Conn) Incoming() <-chan Packet {
	return c.incoming
}

// Err reports the terminal read error after Incoming closes (nil after Close).
func (c *Conn) Err() error {
	if p := c.readErr.Load(); p != nil {
		return *p
	}
	return nil
}

// SetJSONMode forces the outbound wire format, for a server that advertises JSON before sending it.
func (c *Conn) SetJSONMode(enabled bool) { c.jsonMode.Store(enabled) }

func (c *Conn) JSONMode() bool { return c.jsonMode.Load() }

// Send queues a packet and never waits on the network; a failed write is reported by later sends and by Err.
func (c *Conn) Send(p Packet) error {
	mode := aolib.WireFanta
	if c.jsonMode.Load() {
		mode = aolib.WireJSON
	}
	raw, err := c.encode(p, mode)
	if err != nil {
		// A packet failing aolib validation falls back to positional framing, accepted in either wire mode.
		raw = []byte(p.String())
	}
	if err := c.queueWrite(raw); err != nil {
		return fmt.Errorf("protocol: sending %s: %w", p.Header, err)
	}
	return nil
}

// encode serializes typed forms through aolib, custom headers (GP) by codec, and the rest as Fanta.
func (c *Conn) encode(p Packet, mode aolib.WireMode) ([]byte, error) {
	if p.typed != nil {
		if packetutil.IsCustom(p.Header) {
			return packetutil.EncodeCustom(p.Header, p.typed, mode)
		}
		raw, err := aolib.Encode(p.typed, mode)
		if err != nil {
			// JSON validation failed; the server accepts Fanta per frame.
			return aolib.Encode(p.typed, aolib.WireFanta)
		}
		return raw, nil
	}
	return []byte(p.String()), nil
}

// queueWrite hands one encoded frame to writeLoop without blocking; a full queue ends the connection.
func (c *Conn) queueWrite(raw []byte) error {
	if p := c.writeErr.Load(); p != nil {
		return *p
	}
	select {
	case <-c.closed:
		return net.ErrClosed
	default:
	}
	select {
	case c.writes <- raw:
		return nil
	default:
		c.readErr.CompareAndSwap(nil, &errWriteQueueFull)
		c.Close()
		return errWriteQueueFull
	}
}

// writeLoop keeps a stalled socket off every other thread. A failed write leaves the socket unusable: closing it ends
// readLoop with parked packets intact, so a kick the server sent just before dropping still reaches the UI.
func (c *Conn) writeLoop() {
	defer func() { _ = c.ws.Close(websocket.StatusGoingAway, "client closing") }()
	for {
		select {
		case raw := <-c.writes:
			ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
			err := c.ws.Write(ctx, websocket.MessageText, raw)
			cancel()
			if err != nil {
				e := fmt.Errorf("protocol: write failed: %w", err)
				c.writeErr.Store(&e)
				select {
				case <-c.closed: // deliberate Close: not an error
				default:
					c.readErr.CompareAndSwap(nil, &e)
				}
				return
			}
			c.sent.Add(1)
		case <-c.closed:
			c.drainWrites()
			return
		}
	}
}

// drainWrites sends what was queued before Close, such as a parting packet, within closeDrainTimeout.
func (c *Conn) drainWrites() {
	ctx, cancel := context.WithTimeout(context.Background(), closeDrainTimeout)
	defer cancel()
	for {
		select {
		case raw := <-c.writes:
			if err := c.ws.Write(ctx, websocket.MessageText, raw); err != nil {
				return
			}
			c.sent.Add(1)
		default:
			return
		}
	}
}

// SetKeepalive sets the CH ping payload, or with an empty string stops the ping; safe from any goroutine.
func (c *Conn) SetKeepalive(payload string) {
	if payload == "" {
		c.keepalive.Store(nil)
		return
	}
	c.keepalive.Store(&payload)
}

// keepaliveLoop has its own goroutine because Windows stalls the frame loop of an occluded app.
// It exits on Close or a failed write, so reconnects can't leak it (hard rule 4).
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
		if err := c.queueWrite([]byte(*p)); err != nil {
			return
		}
	}
}

// Close starts the teardown and returns at once; writeLoop closes the socket. Safe to call more than once.
func (c *Conn) Close() {
	c.once.Do(func() { close(c.closed) })
}

// readLoop must always return to ws.Read promptly: the library answers pings only inside Read, and an
// unanswered ping drops a healthy client. So packets go to backlog, and deliverLoop does the blocking hand-off.
// Backlog exhaustion is the deliberate exception:
func (c *Conn) readLoop() {
	// Closing backlog lets deliverLoop finish parked packets and close Incoming. Transport errors below
	// deliberately don't Close: closing c.closed would make deliverLoop abandon parked packets.
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
		// A JSON frame flips the outbound mode so we answer the server in kind.
		if packetutil.IsJSON(data) {
			c.jsonMode.Store(true)
		}
		// The registerInbound handlers enqueue without blocking; the decryptor handler flips jsonMode.
		c.frameBytes = len(data)
		c.sess.Receive(data)
	}
}

// enqueue closes the connection if the bound is exhausted; it runs only on the readLoop goroutine.
func (c *Conn) enqueue(p Packet) {
	c.received.Add(1)
	n := c.frameBytes
	if c.backlogBytes.Load()+int64(n) > readBacklogMaxBytes {
		c.readErr.CompareAndSwap(nil, &errReadBacklogOverflow)
		c.Close()
		return
	}
	select {
	case c.backlog <- sizedPacket{p: p, n: n}:
		c.backlogBytes.Add(int64(n))
	case <-c.closed:
		return
	default:
		// Backlog full: Incoming went undrained for the whole bound, so the app is wedged.
		c.readErr.CompareAndSwap(nil, &errReadBacklogOverflow)
		c.Close()
	}
}

// deliverLoop is Incoming's only closer; blocking here keeps the reader answering pings during a stall.
// After a transport drop every parked packet is delivered before the close; after Close parked packets
// may be abandoned, since a reconnect resyncs full server state.
func (c *Conn) deliverLoop() {
	defer func() {
		close(c.incoming)
		c.wake() // the close wake: a drop must surface as fast as a packet would
	}()
	for {
		// Watches closed while idle too: a stalled socket can keep readLoop from ending for a whole write timeout.
		select {
		case item, ok := <-c.backlog:
			if !ok {
				return
			}
			select {
			case c.incoming <- item.p:
				c.backlogBytes.Add(-int64(item.n))
				c.wake()
			case <-c.closed:
				return // deliberate Close / overflow teardown: stop delivering
			}
		case <-c.closed:
			return
		}
	}
}

// Ping returns a WebSocket ping's round-trip time; coder/websocket allows it alongside reads and Send.
func (c *Conn) Ping(ctx context.Context) (time.Duration, error) {
	start := time.Now()
	if err := c.ws.Ping(ctx); err != nil {
		return 0, err
	}
	return time.Since(start), nil
}

type ConnStats struct {
	Sent     int64
	Received int64
}

func (c *Conn) Stats() ConnStats {
	return ConnStats{Sent: c.sent.Load(), Received: c.received.Load()}
}
