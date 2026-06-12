//go:build !nodiscord

package presence

import (
	"bytes"
	"encoding/binary"
	"testing"
	"time"
)

// TestFrameRoundTrip pins the IPC wire shape: [op LE][len LE][json].
func TestFrameRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	payload := []byte(`{"v":1}`)
	if err := writeFrame(&buf, opHandshake, payload); err != nil {
		t.Fatal(err)
	}
	op, got, err := readFrame(&buf)
	if err != nil || op != opHandshake || string(got) != string(payload) {
		t.Fatalf("round trip: op=%d payload=%q err=%v", op, got, err)
	}

	// Oversized frames are rejected, not allocated.
	var huge bytes.Buffer
	var hdr [8]byte
	binary.LittleEndian.PutUint32(hdr[4:], maxFrameLen+1)
	huge.Write(hdr[:])
	if _, _, err := readFrame(&huge); err == nil {
		t.Error("oversized frame accepted")
	}
}

// TestSetActivityCmdShape pins the SET_ACTIVITY body: empty fields are
// omitted, nil activity clears, the icon asset rides every activity.
func TestSetActivityCmdShape(t *testing.T) {
	full := setActivityCmd(123, &Activity{
		Details: "On Skrapegropen",
		State:   "Nyah as Phoenix",
		Start:   time.Unix(1700000000, 0),
	})
	if full["cmd"] != "SET_ACTIVITY" {
		t.Errorf("cmd = %v", full["cmd"])
	}
	args := full["args"].(map[string]any)
	if args["pid"] != 123 {
		t.Errorf("pid = %v", args["pid"])
	}
	activity := args["activity"].(map[string]any)
	if activity["details"] != "On Skrapegropen" || activity["state"] != "Nyah as Phoenix" {
		t.Errorf("activity = %+v", activity)
	}
	assets := activity["assets"].(map[string]any)
	if assets["large_image"] != largeImageKey {
		t.Errorf("large_image = %v", assets["large_image"])
	}
	if _, ok := activity["timestamps"]; !ok {
		t.Error("timestamps missing despite Start being set")
	}

	// Sparse activity omits what's empty (per-field checkboxes off).
	sparse := setActivityCmd(1, &Activity{Details: "On Skrapegropen"})
	sa := sparse["args"].(map[string]any)["activity"].(map[string]any)
	if _, ok := sa["state"]; ok {
		t.Error("empty state serialized")
	}
	if _, ok := sa["timestamps"]; ok {
		t.Error("zero Start serialized")
	}

	// Clear: no activity key at all.
	clear := setActivityCmd(1, nil)
	if _, ok := clear["args"].(map[string]any)["activity"]; ok {
		t.Error("nil activity still carried an activity body")
	}
}
