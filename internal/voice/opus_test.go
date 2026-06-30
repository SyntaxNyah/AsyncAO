//go:build !novoice

package voice

import (
	"math"
	"testing"
)

// TestOpusRoundTrip pins that the encoder + decoder agree on framing: a 20 ms
// frame encodes to a bounded packet and decodes back to exactly FrameSize
// samples (opus is lossy, so we check shape, not sample equality), and the
// packet-loss-concealment path (nil packet) also yields a full frame.
func TestOpusRoundTrip(t *testing.T) {
	enc, err := NewEncoder()
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	defer enc.Close()
	dec, err := NewDecoder()
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	defer dec.Close()

	pcm := make([]int16, FrameSize)
	for i := range pcm {
		pcm[i] = int16(8000 * math.Sin(2*math.Pi*440*float64(i)/SampleRate))
	}

	pkt, err := enc.Encode(pcm)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if len(pkt) == 0 || len(pkt) > maxEncodedBytes {
		t.Fatalf("encoded packet size = %d, want 1..%d", len(pkt), maxEncodedBytes)
	}

	out, err := dec.Decode(pkt)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(out) != FrameSize {
		t.Fatalf("decoded %d samples, want %d", len(out), FrameSize)
	}

	// Packet-loss concealment: a nil packet still produces a full frame.
	lost, err := dec.Decode(nil)
	if err != nil || len(lost) != FrameSize {
		t.Fatalf("PLC decode: err=%v len=%d (want %d)", err, len(lost), FrameSize)
	}
}

func TestOpusTune(t *testing.T) {
	enc, err := NewEncoder()
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	defer enc.Close()
	enc.Tune(voiceTestBitrate, true) // DTX on + a bitrate
	enc.SetBitrate(12000)            // live bitrate change (adaptive path)
	// Still encodes a full frame after tuning.
	pcm := make([]int16, FrameSize)
	for i := range pcm {
		pcm[i] = int16(4000 * math.Sin(2*math.Pi*220*float64(i)/SampleRate))
	}
	if pkt, err := enc.Encode(pcm); err != nil || len(pkt) == 0 {
		t.Fatalf("Encode after Tune: pkt=%d err=%v", len(pkt), err)
	}
}

const voiceTestBitrate = 24000

func TestOpusEncodeWrongFrameSize(t *testing.T) {
	enc, err := NewEncoder()
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	defer enc.Close()
	if _, err := enc.Encode(make([]int16, FrameSize-1)); err == nil {
		t.Error("Encode must reject a frame that isn't exactly FrameSize samples")
	}
}

func TestOpusCloseIdempotent(t *testing.T) {
	enc, _ := NewEncoder()
	enc.Close()
	enc.Close() // must not panic / double-free
	if _, err := enc.Encode(make([]int16, FrameSize)); err == nil {
		t.Error("Encode after Close must error, not crash")
	}
}
