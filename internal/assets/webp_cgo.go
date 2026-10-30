//go:build cgo && !nocgo_webp

package assets

// CGO binding over libwebp and libwebpdemux: SIMD decode into RGBA through the WebPAnimDecoder API.

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
func decodeWebP(data []byte, playAnimations bool, maxH int) (*Decoded, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("assets: empty webp payload")
	}
	if Sniff(data) == FormatWebPAnim {
		return decodeWebPAnim(data, playAnimations, maxH)
	}
	return decodeWebPStatic(data)
}

// peekWebPAnimDims reads canvas size and frame count from the demuxer header, without decoding pixels.
func peekWebPAnimDims(data []byte) (width, height, frames int, ok bool) {
	if len(data) == 0 {
		return 0, 0, 0, false
	}
	var pinner runtime.Pinner
	pinner.Pin(&data[0])
	defer pinner.Unpin()
	webpData := C.WebPData{
		bytes: (*C.uint8_t)(unsafe.Pointer(&data[0])),
		size:  C.size_t(len(data)),
	}
	dmux := C.WebPDemux(&webpData)
	if dmux == nil {
		return 0, 0, 0, false
	}
	defer C.WebPDemuxDelete(dmux)
	w := int(C.WebPDemuxGetI(dmux, C.WEBP_FF_CANVAS_WIDTH))
	h := int(C.WebPDemuxGetI(dmux, C.WEBP_FF_CANVAS_HEIGHT))
	n := int(C.WebPDemuxGetI(dmux, C.WEBP_FF_FRAME_COUNT))
	if w <= 0 || h <= 0 || n <= 0 {
		return 0, 0, 0, false
	}
	return w, h, n, true
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

// decodeWebPAnim takes delays from ANMF durations, as GetNext's cumulative timestamps shift them by one frame.
func decodeWebPAnim(data []byte, playAnimations bool, maxH int) (*Decoded, error) {
	// The decoder reads the payload across calls; pin it so handing the pointer to C stays legal without a copy.
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
	opts.use_threads = 0 // lossless (VP8L) decode is inherently sequential; threading only helps lossy VP8, and adds overhead here

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

	// Default to the zero-delay fallback, then use the authored duration where the demuxer has one.
	durations := make([]time.Duration, frameTotal)
	for i := range durations {
		durations[i] = defaultZeroFrameDelay
	}
	if dmux := C.WebPDemux(&webpData); dmux != nil {
		defer C.WebPDemuxDelete(dmux)
		for i := 0; i < frameTotal; i++ {
			var iter C.WebPIterator
			if C.WebPDemuxGetFrame(dmux, C.int(i+1), &iter) == 0 {
				continue // keep the default for a frame the demuxer can't reach
			}
			if iter.duration > 0 {
				durations[i] = time.Duration(iter.duration) * time.Millisecond
			}
			C.WebPDemuxReleaseIterator(&iter)
		}
	}

	walk := frameTotal // GetNext must run per frame (each composes onto the last)
	if !playAnimations {
		walk = 1
	}
	tw, th, down := decodeTargetDimsBudgeted(width, height, maxH, walk)
	keep := boundedFrameCount(tw, th, walk) // safety net; == walk after downscale-to-fit
	fdec := newFrameDecimator(walk, keep)
	sourceDelays := make([]time.Duration, 0, walk)

	d := &Decoded{
		Animated:     frameTotal > 1,
		Width:        tw,
		Height:       th,
		SourceFrames: walk, // frame space the sender's networked frame effects index into (#17)
		Frames:       make([]*image.RGBA, 0, keep),
		Delays:       make([]time.Duration, 0, keep),
	}

	canvasBytes := width * height * webpBytesPerPixel
	for i := 0; i < walk; i++ {
		if C.WebPAnimDecoderHasMoreFrames(dec) == 0 {
			break
		}
		var frameRGBA *C.uint8_t
		var timestamp C.int // read but ignored: delays come from the demuxer durations
		if C.WebPAnimDecoderGetNext(dec, &frameRGBA, &timestamp) == 0 {
			d.Release()
			return nil, fmt.Errorf("assets: webp anim frame %d decode failed", i)
		}

		delay := durations[i]
		sourceDelays = append(sourceDelays, delay)

		// Every GetNext composited the canvas; copy out only the kept frames, folding skipped delays.
		folded, keepIt := fdec.step(i, delay)
		if !keepIt {
			continue
		}
		rgba, token := newPooledRGBA(width, height)
		src := unsafe.Slice((*byte)(unsafe.Pointer(frameRGBA)), canvasBytes)
		copy(rgba.Pix, src)
		if down {
			small, smallTok := downscaleFrame(rgba, tw, th)
			putPixBuf(token)
			d.Frames = append(d.Frames, small)
			if smallTok != nil {
				d.pooledPix = append(d.pooledPix, smallTok)
			}
		} else {
			d.Frames = append(d.Frames, rgba)
			if token != nil {
				d.pooledPix = append(d.pooledPix, token)
			}
		}
		d.Delays = append(d.Delays, folded)
	}

	if len(d.Frames) == 0 {
		return nil, fmt.Errorf("assets: webp anim yielded no frames")
	}
	spreadLoopDelays(d, sourceDelays)
	return d, nil
}

// decodeWebPAnimStream emits each frame from kept index 1 as a chunk; decimation returns streamed=false.
func decodeWebPAnimStream(data []byte, maxH int, emit func(*Decoded)) (streamed bool, total int, err error) {
	var pinner runtime.Pinner
	pinner.Pin(&data[0])
	defer pinner.Unpin()

	webpData := C.WebPData{
		bytes: (*C.uint8_t)(unsafe.Pointer(&data[0])),
		size:  C.size_t(len(data)),
	}

	var opts C.WebPAnimDecoderOptions
	if C.WebPAnimDecoderOptionsInit(&opts) == 0 {
		return false, 0, fmt.Errorf("assets: webp anim options init failed")
	}
	opts.color_mode = C.MODE_RGBA
	opts.use_threads = 0

	dec := C.WebPAnimDecoderNew(&webpData, &opts)
	if dec == nil {
		return false, 0, fmt.Errorf("assets: webp anim decoder rejected payload")
	}
	defer C.WebPAnimDecoderDelete(dec)

	var info C.WebPAnimInfo
	if C.WebPAnimDecoderGetInfo(dec, &info) == 0 {
		return false, 0, fmt.Errorf("assets: webp anim info unavailable")
	}
	width, height := int(info.canvas_width), int(info.canvas_height)
	walk := int(info.frame_count)
	if walk == 0 {
		return false, 0, fmt.Errorf("assets: webp anim reports zero frames")
	}

	durations := make([]time.Duration, walk)
	for i := range durations {
		durations[i] = defaultZeroFrameDelay
	}
	if dmux := C.WebPDemux(&webpData); dmux != nil {
		defer C.WebPDemuxDelete(dmux)
		for i := 0; i < walk; i++ {
			var iter C.WebPIterator
			if C.WebPDemuxGetFrame(dmux, C.int(i+1), &iter) == 0 {
				continue
			}
			if iter.duration > 0 {
				durations[i] = time.Duration(iter.duration) * time.Millisecond
			}
			C.WebPDemuxReleaseIterator(&iter)
		}
	}

	tw, th, down := decodeTargetDimsBudgeted(width, height, maxH, walk)
	keep := boundedFrameCount(tw, th, walk)
	if keep < walk {
		return false, walk, nil // decimation needed — fall back to the full decode
	}

	canvasBytes := width * height * webpBytesPerPixel

	// When downscaling, reuse one native buffer instead of churning the pool; else it is the delivered frame.
	var reuse *image.RGBA
	var reuseToken *[]byte
	if down {
		reuse, reuseToken = newPooledRGBA(width, height)
		defer func() {
			if reuseToken != nil {
				putPixBuf(reuseToken)
			}
		}()
	}

	for i := 0; i < walk; i++ {
		if C.WebPAnimDecoderHasMoreFrames(dec) == 0 {
			return true, walk, nil // truncated payload: keep what decoded
		}
		var frameRGBA *C.uint8_t
		var timestamp C.int // read but ignored: delays come from the demuxer durations
		if C.WebPAnimDecoderGetNext(dec, &frameRGBA, &timestamp) == 0 {
			return true, walk, nil // mid-stream frame failure: keep the prefix
		}
		if i == 0 {
			continue // frame 0 already delivered by the progressive first frame
		}
		var rgba *image.RGBA
		var token *[]byte
		if down {
			rgba = reuse
		} else {
			rgba, token = newPooledRGBA(width, height)
		}
		src := unsafe.Slice((*byte)(unsafe.Pointer(frameRGBA)), canvasBytes)
		copy(rgba.Pix, src)
		out := rgba
		if down {
			small, smallTok := downscaleFrame(rgba, tw, th)
			out = small
			token = smallTok
		}
		chunk := &Decoded{
			Frames:       []*image.RGBA{out},
			Delays:       []time.Duration{durations[i]},
			Animated:     true,
			SourceFrames: walk,
			Width:        tw,
			Height:       th,
			Stream:       true,
			FrameOffset:  i,
			Partial:      true,
		}
		if token != nil {
			chunk.pooledPix = append(chunk.pooledPix, token)
		}
		emit(chunk)
	}
	return true, walk, nil
}
