//go:build !nodiscord

// Package presence is an optional stdlib-only Discord Rich Presence client over local IPC; it does nothing
// when Discord isn't running, and -tags nodiscord compiles it out.
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

	"github.com/SyntaxNyah/AsyncAO/internal/platform"
)

const (
	// opHandshake and opFrame are the Discord IPC opcodes we use.
	opHandshake = 0
	opFrame     = 1

	// retryInterval paces reconnects, as a closed Discord must not be probed every frame.
	retryInterval = 30 * time.Second
	// maxFrameLen bounds inbound frames (hard rule 4).
	maxFrameLen = 64 << 10
	// largeImageKey is the art asset name to upload the icon under in the Discord application.
	largeImageKey = "appicon"
)

// Activity is what shows on the profile; empty fields are omitted.
type Activity struct {
	Details string // first line (e.g. the server)
	State   string // second line (e.g. "Nyah as Phoenix — Courtroom 1")
	Start   time.Time
}

// Client owns one worker goroutine that connects lazily; every method is non-blocking and safe from the render thread.
type Client struct {
	appID string

	updates chan Activity // cap 1: newest wins
	clear   chan struct{}
	stop    chan struct{}
	once    sync.Once

	status atomic.Pointer[string]
}

// New starts the worker; an empty appID leaves the client idle with an explanatory status.
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

// Clear asks Discord to drop the activity.
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

// worker holds all I/O, including the blocking reads Discord expects after each write, off callers' goroutines.
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
		dialed, err := platform.DialDiscord()
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
				// A nil activity clears the profile entry.
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

// sendActivity pushes one SET_ACTIVITY (nil clears) and drains the ack.
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

// setActivityCmd builds the SET_ACTIVITY body; the nonce is for Discord's request and reply pairing.
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

// writeFrame emits one [opcode LE][length LE][json] frame.
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

// readFrame consumes one IPC frame; Discord replies to every command.
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
