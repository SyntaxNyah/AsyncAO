package protocol

import (
	"context"
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

	sent     atomic.Int64
	received atomic.Int64
}

// Dial connects to a ws:// or wss:// AO server URL and starts the read loop.
func Dial(ctx context.Context, wsURL string) (*Conn, error) {
	ctx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()

	ws, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"User-Agent": []string{userAgent}},
	})
	if err != nil {
		return nil, fmt.Errorf("protocol: dialing %s: %w", wsURL, err)
	}
	ws.SetReadLimit(maxIncomingBytes)

	c := &Conn{
		ws:       ws,
		incoming: make(chan Packet, incomingQueueCap),
		closed:   make(chan struct{}),
	}
	go c.readLoop()
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
	defer close(c.incoming)
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
		select {
		case c.incoming <- packet:
		case <-c.closed:
			return
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
