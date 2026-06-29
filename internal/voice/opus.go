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

// opus_encoder_ctl is variadic, which cgo can't call directly — wrap the few
// request macros we use in fixed-signature helpers.
static int asyncao_opus_set_bitrate(OpusEncoder *e, int v)   { return opus_encoder_ctl(e, OPUS_SET_BITRATE(v)); }
static int asyncao_opus_set_dtx(OpusEncoder *e, int v)       { return opus_encoder_ctl(e, OPUS_SET_DTX(v)); }
static int asyncao_opus_set_complexity(OpusEncoder *e, int v){ return opus_encoder_ctl(e, OPUS_SET_COMPLEXITY(v)); }
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

// Tune applies AsyncAO's voice encoder settings: a target bitrate (bits/sec), DTX
// (discontinuous transmission — don't send during silence, which cuts bandwidth
// and is the main resilience lever over a reliable TCP/WebSocket transport), and a
// middling complexity (good quality, low CPU). Best-effort; a failed ctl is
// non-fatal (voice still works at the encoder defaults).
func (e *Encoder) Tune(bitrate int, dtx bool) {
	if e.enc == nil {
		return
	}
	C.asyncao_opus_set_bitrate(e.enc, C.int(bitrate))
	d := C.int(0)
	if dtx {
		d = 1
	}
	C.asyncao_opus_set_dtx(e.enc, d)
	C.asyncao_opus_set_complexity(e.enc, 5)
}

// SetBitrate adjusts the encoder's target bitrate (bits/sec) live — used for the
// adaptive-bitrate path (drop it when the jitter buffer backs up). Best-effort.
func (e *Encoder) SetBitrate(bps int) {
	if e.enc != nil {
		C.asyncao_opus_set_bitrate(e.enc, C.int(bps))
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
