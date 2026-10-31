//go:build !nodiscord

package presence

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

const (
	testAppID   = "123"
	testWait    = 5 * time.Second
	testPollGap = 5 * time.Millisecond
)

// waitForStatus polls, as Status is the only view of the worker from outside.
func waitForStatus(t *testing.T, c *Client, want string) {
	t.Helper()
	deadline := time.Now().Add(testWait)
	for time.Now().Before(deadline) {
		if strings.Contains(c.Status(), want) {
			return
		}
		time.Sleep(testPollGap)
	}
	t.Fatalf("status = %q, want it to contain %q", c.Status(), want)
}

func TestClientSendsTheActivityThroughTheInjectedDial(t *testing.T) {
	const details = "On Skrapegropen"
	ours, discord := net.Pipe()
	defer discord.Close()
	client := New(testAppID, func() (io.ReadWriteCloser, error) { return ours, nil })
	defer client.Close()

	received := make(chan map[string]any, 1)
	go func() {
		if _, _, err := readFrame(discord); err != nil { // handshake
			return
		}
		if err := writeFrame(discord, opFrame, []byte(`{"evt":"READY"}`)); err != nil {
			return
		}
		_, payload, err := readFrame(discord)
		if err != nil {
			return
		}
		var command map[string]any
		if json.Unmarshal(payload, &command) == nil {
			received <- command
		}
		_ = writeFrame(discord, opFrame, []byte(`{}`))
	}()

	client.Set(Activity{Details: details})
	select {
	case command := <-received:
		if command["cmd"] != "SET_ACTIVITY" {
			t.Errorf("cmd = %v", command["cmd"])
		}
		args, _ := command["args"].(map[string]any)
		activity, _ := args["activity"].(map[string]any)
		if activity["details"] != details {
			t.Errorf("activity = %v", activity)
		}
	case <-time.After(testWait):
		t.Fatal("no activity reached Discord")
	}
	waitForStatus(t, client, "presence live")
}

func TestClientReportsDiscordNotRunning(t *testing.T) {
	client := New(testAppID, func() (io.ReadWriteCloser, error) { return nil, errors.New("no pipe") })
	defer client.Close()
	client.Set(Activity{Details: "x"})
	waitForStatus(t, client, "not detected")
}

func TestClientWithNoApplicationIDNeverDials(t *testing.T) {
	dialed := make(chan struct{}, 1)
	client := New("", func() (io.ReadWriteCloser, error) {
		dialed <- struct{}{}
		return nil, errors.New("no pipe")
	})
	defer client.Close()
	client.Set(Activity{Details: "x"})
	select {
	case <-dialed:
		t.Error("dialed with no application ID")
	case <-time.After(testPollGap):
	}
}
