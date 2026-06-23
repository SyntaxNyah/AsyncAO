//go:build !nodiscord

// Package presence is an OPTIONAL Discord Rich Presence client speaking
// the local IPC protocol directly — no Discord SDK, no third-party
// module, nothing linked: pure stdlib. Discord is never required to
// build OR run AsyncAO; when Discord isn't running (or the feature is
// off) this package quietly does nothing. Build with -tags nodiscord to
// compile even this file out (see BUILDING.md).
package presence

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// opHandshake / opFrame are the Discord IPC opcodes we use.
	opHandshake = 0
	opFrame     = 1

	// retryInterval paces reconnect attempts while updates are pending —
	// a closed Discord must not be probed every frame.
	retryInterval = 30 * time.Second
	// maxFrameLen bounds inbound frames (rule §17.4 — Discord's replies
	// are small JSON; anything bigger is garbage).
	maxFrameLen = 64 << 10
	// pipeCandidates: Discord listens on discord-ipc-0..9.
	pipeCandidates = 10
	// largeImageKey is the art asset key the activity references — upload
	// the AsyncAO icon under this name in the Discord application.
	largeImageKey = "appicon"
)

// Activity is what shows on the profile. Empty fields are omitted; the
// UI layer composes them from the user's per-field checkboxes.
type Activity struct {
	Details string // first line (e.g. the server)
	State   string // second line (e.g. "Nyah as Phoenix — Courtroom 1")
	Start   time.Time
}

// Client owns one worker goroutine that lazily connects to the local
// Discord IPC socket and pushes the newest activity. Every method is
// non-blocking and safe from the render thread.
type Client struct {
	appID string

	updates chan Activity // cap 1: newest wins
	clear   chan struct{}
	stop    chan struct{}
	once    sync.Once

	status atomic.Pointer[string]
}

// New starts the worker. An empty appID leaves the client idle with an
// explanatory status (create a Discord application named AsyncAO, upload
// the icon as "appicon", paste its ID in Settings).
func New(appID string) *Client {
	c := &Client{
		appID:   appID,
		updates: make(chan Activity, 1),
		clear:   make(chan struct{}, 1),
		stop:    make(chan struct{}),
	}
	c.setStatus("idle")
	if appID == "" {
		c.setStatus("no application ID set (Settings → Discord)")
	}
	go c.worker()
	return c
}

// Set replaces the pending activity (newest wins, never blocks).
func (c *Client) Set(act Activity) {
	select {
	case <-c.updates:
	default:
	}
	select {
	case c.updates <- act:
	default:
	}
}

// Clear asks Discord to drop the activity (presence toggled off /
// disconnected from the server).
func (c *Client) Clear() {
	select {
	case c.clear <- struct{}{}:
	default:
	}
}

// Close stops the worker.
func (c *Client) Close() {
	c.once.Do(func() { close(c.stop) })
}

// Status is a one-line connection state for the Settings screen.
func (c *Client) Status() string {
	if s := c.status.Load(); s != nil {
		return *s
	}
	return "idle"
}

func (c *Client) setStatus(s string) { c.status.Store(&s) }

// worker drains updates, (re)connecting lazily with backoff. All I/O —
// including the blocking reads Discord's protocol expects after each
// write — lives here, never on a caller's goroutine.
func (c *Client) worker() {
	var conn io.ReadWriteCloser
	var lastTry time.Time
	var pending *Activity
	defer func() {
		if conn != nil {
			conn.Close()
		}
	}()

	retry := time.NewTicker(retryInterval)
	defer retry.Stop()

	ensure := func() bool {
		if c.appID == "" {
			return false
		}
		if conn != nil {
			return true
		}
		if time.Since(lastTry) < retryInterval {
			return false
		}
		lastTry = time.Now()
		dialed, err := dialDiscord()
		if err != nil {
			c.setStatus("Discord not detected (retrying)")
			return false
		}
		if err := c.handshake(dialed); err != nil {
			dialed.Close()
			c.setStatus("handshake failed: " + err.Error())
			return false
		}
		conn = dialed
		c.setStatus("connected")
		return true
	}
	push := func(act *Activity) {
		if act == nil || !ensure() {
			return
		}
		if err := c.sendActivity(conn, act); err != nil {
			conn.Close()
			conn = nil
			c.setStatus("connection lost (retrying)")
			return
		}
		pending = nil
		c.setStatus("connected — presence live")
	}

	for {
		select {
		case act := <-c.updates:
			pending = &act
			push(pending)
		case <-c.clear:
			pending = nil
			if conn != nil {
				// nil activity clears the profile entry.
				if err := c.sendActivity(conn, nil); err != nil {
					conn.Close()
					conn = nil
				} else {
					c.setStatus("connected — presence cleared")
				}
			}
		case <-retry.C:
			if pending != nil {
				push(pending)
			}
		case <-c.stop:
			return
		}
	}
}

// handshake performs op 0 and reads Discord's READY reply.
func (c *Client) handshake(conn io.ReadWriter) error {
	payload, err := json.Marshal(map[string]any{"v": 1, "client_id": c.appID})
	if err != nil {
		return err
	}
	if err := writeFrame(conn, opHandshake, payload); err != nil {
		return err
	}
	_, _, err = readFrame(conn)
	return err
}

// sendActivity pushes one SET_ACTIVITY (nil = clear) and drains the ack.
func (c *Client) sendActivity(conn io.ReadWriter, act *Activity) error {
	payload, err := json.Marshal(setActivityCmd(os.Getpid(), act))
	if err != nil {
		return err
	}
	if err := writeFrame(conn, opFrame, payload); err != nil {
		return err
	}
	_, _, err = readFrame(conn)
	return err
}

// setActivityCmd builds the SET_ACTIVITY body (exported shape pinned by
// tests; the nonce keeps Discord's request/reply pairing happy).
func setActivityCmd(pid int, act *Activity) map[string]any {
	args := map[string]any{"pid": pid}
	if act != nil {
		activity := map[string]any{
			"assets": map[string]any{
				"large_image": largeImageKey,
				"large_text":  "AsyncAO",
			},
		}
		if act.Details != "" {
			activity["details"] = act.Details
		}
		if act.State != "" {
			activity["state"] = act.State
		}
		if !act.Start.IsZero() {
			activity["timestamps"] = map[string]any{"start": act.Start.Unix()}
		}
		args["activity"] = activity
	}
	return map[string]any{
		"cmd":   "SET_ACTIVITY",
		"args":  args,
		"nonce": fmt.Sprintf("asyncao-%d", time.Now().UnixNano()),
	}
}

// writeFrame emits one [opcode LE][length LE][json] IPC frame.
func writeFrame(w io.Writer, op uint32, payload []byte) error {
	var hdr [8]byte
	binary.LittleEndian.PutUint32(hdr[0:], op)
	binary.LittleEndian.PutUint32(hdr[4:], uint32(len(payload)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}

// readFrame consumes one IPC frame (Discord replies to every command).
func readFrame(r io.Reader) (op uint32, payload []byte, err error) {
	var hdr [8]byte
	if _, err = io.ReadFull(r, hdr[:]); err != nil {
		return 0, nil, err
	}
	op = binary.LittleEndian.Uint32(hdr[0:])
	n := binary.LittleEndian.Uint32(hdr[4:])
	if n > maxFrameLen {
		return 0, nil, fmt.Errorf("presence: oversized frame (%d bytes)", n)
	}
	payload = make([]byte, n)
	_, err = io.ReadFull(r, payload)
	return op, payload, err
}
