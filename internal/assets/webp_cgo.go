//go:build cgo && !nocgo_webp

package assets

// Thin CGO binding over libwebp + libwebpdemux (spec §2): SIMD decode
// straight into RGBA, animated and static alike, via the WebPAnimDecoder
// API. Roughly a hundred lines — no third-party wrapper needed.

/*
#cgo pkg-config: libwebpdemux libwebp
#include <stdlib.h>
#include <webp/decode.h>
#include <webp/demux.h>
*/
import "C"

import (
	"fmt"
	"image"
	"runtime"
	"time"
	"unsafe"
)

const webpBytesPerPixel = 4

// decodeWebP decodes a static or animated WebP payload into RGBA frames.
func decodeWebP(data []byte, playAnimations bool) (*Decoded, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("assets: empty webp payload")
	}
	if Sniff(data) == FormatWebPAnim {
		return decodeWebPAnim(data, playAnimations)
	}
	return decodeWebPStatic(data)
}

// decodeWebPStatic decodes a still WebP directly into a pooled RGBA buffer.
func decodeWebPStatic(data []byte) (*Decoded, error) {
	var w, h C.int
	if C.WebPGetInfo((*C.uint8_t)(unsafe.Pointer(&data[0])), C.size_t(len(data)), &w, &h) == 0 {
		return nil, fmt.Errorf("assets: invalid webp header")
	}
	width, height := int(w), int(h)
	rgba, token := newPooledRGBA(width, height)

	out := C.WebPDecodeRGBAInto(
		(*C.uint8_t)(unsafe.Pointer(&data[0])), C.size_t(len(data)),
		(*C.uint8_t)(unsafe.Pointer(&rgba.Pix[0])), C.size_t(len(rgba.Pix)),
		C.int(rgba.Stride),
	)
	if out == nil {
		putPixBuf(token)
		return nil, fmt.Errorf("assets: webp decode failed (%dx%d payload)", width, height)
	}

	d := &Decoded{
		Frames:   []*image.RGBA{rgba},
		Delays:   []time.Duration{0},
		Animated: false,
		Width:    width,
		Height:   height,
	}
	if token != nil {
		d.pooledPix = append(d.pooledPix, token)
	}
	return d, nil
}

// decodeWebPAnim walks the WebPAnimDecoder, copying each composed canvas
// into pooled RGBA frames. Timestamps arrive as cumulative end-times in
// milliseconds; per-frame delays are their deltas.
func decodeWebPAnim(data []byte, playAnimations bool) (*Decoded, error) {
	// The decoder reads from the payload across calls; pin it so handing
	// the pointer to C stays legal without copying the payload.
	var pinner runtime.Pinner
	pinner.Pin(&data[0])
	defer pinner.Unpin()

	webpData := C.WebPData{
		bytes: (*C.uint8_t)(unsafe.Pointer(&data[0])),
		size:  C.size_t(len(data)),
	}

	var opts C.WebPAnimDecoderOptions
	if C.WebPAnimDecoderOptionsInit(&opts) == 0 {
		return nil, fmt.Errorf("assets: webp anim options init failed")
	}
	opts.color_mode = C.MODE_RGBA
	opts.use_threads = 0 // the decode pool already provides parallelism

	dec := C.WebPAnimDecoderNew(&webpData, &opts)
	if dec == nil {
		return nil, fmt.Errorf("assets: webp anim decoder rejected payload")
	}
	defer C.WebPAnimDecoderDelete(dec)

	var info C.WebPAnimInfo
	if C.WebPAnimDecoderGetInfo(dec, &info) == 0 {
		return nil, fmt.Errorf("assets: webp anim info unavailable")
	}
	width, height := int(info.canvas_width), int(info.canvas_height)
	frameTotal := int(info.frame_count)
	if frameTotal == 0 {
		return nil, fmt.Errorf("assets: webp anim reports zero frames")
	}

	frameCount := frameTotal
	if !playAnimations {
		frameCount = 1
	}

	d := &Decoded{
		Animated: frameTotal > 1,
		Width:    width,
		Height:   height,
		Frames:   make([]*image.RGBA, 0, frameCount),
		Delays:   make([]time.Duration, 0, frameCount),
	}

	canvasBytes := width * height * webpBytesPerPixel
	prevTimestamp := 0
	for i := 0; i < frameCount; i++ {
		if C.WebPAnimDecoderHasMoreFrames(dec) == 0 {
			break
		}
		var frameRGBA *C.uint8_t
		var timestamp C.int
		if C.WebPAnimDecoderGetNext(dec, &frameRGBA, &timestamp) == 0 {
			d.Release()
			return nil, fmt.Errorf("assets: webp anim frame %d decode failed", i)
		}

		rgba, token := newPooledRGBA(width, height)
		src := unsafe.Slice((*byte)(unsafe.Pointer(frameRGBA)), canvasBytes)
		copy(rgba.Pix, src)
		d.Frames = append(d.Frames, rgba)
		if token != nil {
			d.pooledPix = append(d.pooledPix, token)
		}

		delay := time.Duration(int(timestamp)-prevTimestamp) * time.Millisecond
		if delay <= 0 {
			delay = defaultZeroFrameDelay
		}
		d.Delays = append(d.Delays, delay)
		prevTimestamp = int(timestamp)
	}

	if len(d.Frames) == 0 {
		return nil, fmt.Errorf("assets: webp anim yielded no frames")
	}
	return d, nil
}
