// Package voice is AsyncAO's Opus codec for the Nyathena/LemmyAO server-relayed
// voice transport (internal/courtroom owns the VS_* wire; internal/render owns
// mic capture + playback). It is deliberately SDL-FREE (hard rule #1): it speaks
// only PCM int16 in and opus bytes out, so the codec never touches the render
// thread or go-sdl2.
//
// Settings match the Nyathena VS_CAPS defaults: 48 kHz, mono, 20 ms frames.
// Opus is BSD-licensed (AGPL-compatible) — see docs/THIRD-PARTY-LICENSES.md.
package voice

/*
#cgo pkg-config: opus
#include <opus.h>
*/
import "C"

import (
	"errors"
	"fmt"
)

const (
	// SampleRate, Channels and FrameMs mirror the Nyathena VS_CAPS defaults.
	SampleRate = 48000
	Channels   = 1
	FrameMs    = 20
	// FrameSize is one frame's PCM sample count (960 = 20 ms @ 48 kHz mono).
	FrameSize = SampleRate / 1000 * FrameMs

	// opusApplicationVOIP / opusOK mirror opus_defines.h. Hard-coded (not C.*) so
	// the build never depends on cgo exposing those object-like macros.
	opusApplicationVOIP = 2048
	opusOK              = 0
	// maxEncodedBytes caps one encoded packet; an opus frame at our settings is
	// far smaller, but the buffer must be sized for opus_encode's worst case.
	maxEncodedBytes = 4000
)

// Encoder wraps a libopus encoder configured for AsyncAO voice (48 kHz mono VOIP).
// Not safe for concurrent use — one Encoder per capture stream.
type Encoder struct{ enc *C.OpusEncoder }

// NewEncoder creates an Opus VOIP encoder.
func NewEncoder() (*Encoder, error) {
	var errno C.int
	enc := C.opus_encoder_create(C.opus_int32(SampleRate), C.int(Channels), C.int(opusApplicationVOIP), &errno)
	if int(errno) != opusOK || enc == nil {
		return nil, fmt.Errorf("voice: opus_encoder_create failed (%d)", int(errno))
	}
	return &Encoder{enc: enc}, nil
}

// Encode compresses exactly one frame (FrameSize int16 samples) to an opus packet.
func (e *Encoder) Encode(pcm []int16) ([]byte, error) {
	if e.enc == nil {
		return nil, errors.New("voice: encoder closed")
	}
	if len(pcm) != FrameSize {
		return nil, fmt.Errorf("voice: Encode wants %d samples, got %d", FrameSize, len(pcm))
	}
	out := make([]byte, maxEncodedBytes)
	n := C.opus_encode(e.enc, (*C.opus_int16)(&pcm[0]), C.int(FrameSize), (*C.uchar)(&out[0]), C.opus_int32(maxEncodedBytes))
	if int(n) < 0 {
		return nil, fmt.Errorf("voice: opus_encode failed (%d)", int(n))
	}
	return out[:int(n)], nil
}

// Close frees the encoder. Safe to call more than once.
func (e *Encoder) Close() {
	if e.enc != nil {
		C.opus_encoder_destroy(e.enc)
		e.enc = nil
	}
}

// Decoder wraps a libopus decoder (48 kHz mono). One Decoder per remote peer.
type Decoder struct{ dec *C.OpusDecoder }

// NewDecoder creates an Opus decoder.
func NewDecoder() (*Decoder, error) {
	var errno C.int
	dec := C.opus_decoder_create(C.opus_int32(SampleRate), C.int(Channels), &errno)
	if int(errno) != opusOK || dec == nil {
		return nil, fmt.Errorf("voice: opus_decoder_create failed (%d)", int(errno))
	}
	return &Decoder{dec: dec}, nil
}

// Decode expands one opus packet to PCM (FrameSize samples). A nil/empty packet
// asks libopus to conceal a single lost frame (packet-loss concealment).
func (d *Decoder) Decode(packet []byte) ([]int16, error) {
	if d.dec == nil {
		return nil, errors.New("voice: decoder closed")
	}
	out := make([]int16, FrameSize)
	var data *C.uchar
	var n C.opus_int32
	if len(packet) > 0 {
		data = (*C.uchar)(&packet[0])
		n = C.opus_int32(len(packet))
	}
	got := C.opus_decode(d.dec, data, n, (*C.opus_int16)(&out[0]), C.int(FrameSize), 0)
	if int(got) < 0 {
		return nil, fmt.Errorf("voice: opus_decode failed (%d)", int(got))
	}
	return out[:int(got)], nil
}

// Close frees the decoder. Safe to call more than once.
func (d *Decoder) Close() {
	if d.dec != nil {
		C.opus_decoder_destroy(d.dec)
		d.dec = nil
	}
}
