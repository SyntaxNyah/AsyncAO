//go:build cgo && !nocgo_avif

package assets

// Thin CGO binding over libavif (same shape as the libwebp one): native
// dav1d/aom decode straight into pooled RGBA, still images and AV1 image
// sequences (animated AVIF) alike. Dependency justification lives in
// docs/ARCHITECTURE.md; the package comes from MSYS2
// (mingw-w64-ucrt-x86_64-libavif).

/*
#cgo pkg-config: libavif
#include <stdlib.h>
#include <avif/avif.h>
*/
import "C"

import (
	"fmt"
	"image"
	"runtime"
	"time"
	"unsafe"
)

// decodeAVIF decodes a still or animated AVIF payload into RGBA frames.
func decodeAVIF(data []byte, playAnimations bool, maxH int) (*Decoded, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("assets: empty avif payload")
	}
	// libavif reads from the payload across calls; pin it so handing the
	// pointer to C stays legal without copying.
	var pinner runtime.Pinner
	pinner.Pin(&data[0])
	defer pinner.Unpin()

	dec := C.avifDecoderCreate()
	if dec == nil {
		return nil, fmt.Errorf("assets: avif decoder allocation failed")
	}
	defer C.avifDecoderDestroy(dec)
	// Animated AVIF (AV1 image sequences) is a multi-frame decode and dav1d
	// scales across worker threads, so let it use the machine's cores instead
	// of serialising every frame (the animation cold-load cost — see
	// docs/ANIMATION-COLD-LOAD-INVESTIGATION.md gap #1). The decode pool
	// parallelises ACROSS assets and animGate bounds CONCURRENT full animated
	// decodes (default 2), so a per-decoder pool of NumCPU threads cannot
	// multiply into unbounded oversubscription.
	dec.maxThreads = C.int(runtime.NumCPU())

	if res := C.avifDecoderSetIOMemory(dec, (*C.uint8_t)(unsafe.Pointer(&data[0])), C.size_t(len(data))); res != C.AVIF_RESULT_OK {
		return nil, avifError("set io", res)
	}
	if res := C.avifDecoderParse(dec); res != C.AVIF_RESULT_OK {
		return nil, avifError("parse", res)
	}

	width, height := int(dec.image.width), int(dec.image.height)
	frameTotal := int(dec.imageCount)
	if frameTotal <= 0 {
		return nil, fmt.Errorf("assets: avif reports no frames")
	}
	walk := frameTotal // NextImage must advance per frame (each composes onto the last)
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

	for i := 0; i < walk; i++ {
		if res := C.avifDecoderNextImage(dec); res != C.AVIF_RESULT_OK {
			if i == 0 {
				d.Release()
				return nil, avifError("first frame", res)
			}
			break // truncated sequence: keep what decoded
		}

		// imageTiming.duration is this frame's display time in seconds.
		delay := time.Duration(float64(dec.imageTiming.duration) * float64(time.Second))
		if delay <= 0 {
			delay = defaultZeroFrameDelay
		}
		sourceDelays = append(sourceDelays, delay)

		// The decoder is now advanced onto frame i; copy out only the decimated
		// subset (skipped frames still advanced, so kept frames compose right),
		// folding skipped delays into the kept one.
		folded, keepIt := fdec.step(i, delay)
		if !keepIt {
			continue
		}

		var rgb C.avifRGBImage
		C.avifRGBImageSetDefaults(&rgb, dec.image)
		rgb.format = C.AVIF_RGB_FORMAT_RGBA
		rgb.depth = 8

		rgba, token := newPooledRGBA(width, height)
		// The rgb struct carries a Go pointer into C — legal only while
		// the destination buffer is pinned (cgo pointer-passing rules).
		pinner.Pin(&rgba.Pix[0])
		rgb.pixels = (*C.uint8_t)(unsafe.Pointer(&rgba.Pix[0]))
		rgb.rowBytes = C.uint32_t(rgba.Stride)

		if res := C.avifImageYUVToRGB(dec.image, &rgb); res != C.AVIF_RESULT_OK {
			putPixBuf(token)
			d.Release()
			return nil, avifError(fmt.Sprintf("frame %d yuv→rgb", i), res)
		}
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
		return nil, fmt.Errorf("assets: avif yielded no frames")
	}
	spreadLoopDelays(d, sourceDelays)
	return d, nil
}

// decodeAVIFAnimStream is decodeAVIF's incremental form: it walks the same
// avifDecoder and emits each frame from kept index 1 (frame 0 was already
// delivered by the progressive first frame) as a single-frame chunk via emit.
// It streams only when the clip needs no decimation (the common case); when
// decimation is needed it returns streamed=false so the caller falls back to
// the classic full decode. total is the source frame count.
func decodeAVIFAnimStream(data []byte, maxH int, emit func(*Decoded)) (streamed bool, total int, err error) {
	var pinner runtime.Pinner
	pinner.Pin(&data[0])
	defer pinner.Unpin()

	dec := C.avifDecoderCreate()
	if dec == nil {
		return false, 0, fmt.Errorf("assets: avif decoder allocation failed")
	}
	defer C.avifDecoderDestroy(dec)
	dec.maxThreads = C.int(runtime.NumCPU())

	if res := C.avifDecoderSetIOMemory(dec, (*C.uint8_t)(unsafe.Pointer(&data[0])), C.size_t(len(data))); res != C.AVIF_RESULT_OK {
		return false, 0, avifError("set io", res)
	}
	if res := C.avifDecoderParse(dec); res != C.AVIF_RESULT_OK {
		return false, 0, avifError("parse", res)
	}

	width, height := int(dec.image.width), int(dec.image.height)
	walk := int(dec.imageCount)
	if walk <= 0 {
		return false, 0, fmt.Errorf("assets: avif reports no frames")
	}
	tw, th, down := decodeTargetDimsBudgeted(width, height, maxH, walk)
	keep := boundedFrameCount(tw, th, walk)
	if keep < walk {
		return false, walk, nil // decimation needed — fall back to the full decode
	}

	for i := 0; i < walk; i++ {
		if res := C.avifDecoderNextImage(dec); res != C.AVIF_RESULT_OK {
			return true, walk, nil // truncated sequence: keep what decoded
		}
		delay := time.Duration(float64(dec.imageTiming.duration) * float64(time.Second))
		if delay <= 0 {
			delay = defaultZeroFrameDelay
		}
		if i == 0 {
			continue // frame 0 already delivered by the progressive first frame
		}

		var rgb C.avifRGBImage
		C.avifRGBImageSetDefaults(&rgb, dec.image)
		rgb.format = C.AVIF_RGB_FORMAT_RGBA
		rgb.depth = 8

		rgba, token := newPooledRGBA(width, height)
		pinner.Pin(&rgba.Pix[0])
		rgb.pixels = (*C.uint8_t)(unsafe.Pointer(&rgba.Pix[0]))
		rgb.rowBytes = C.uint32_t(rgba.Stride)

		if res := C.avifImageYUVToRGB(dec.image, &rgb); res != C.AVIF_RESULT_OK {
			putPixBuf(token)
			return true, walk, avifError(fmt.Sprintf("frame %d yuv→rgb", i), res)
		}
		out := rgba
		if down {
			small, smallTok := downscaleFrame(rgba, tw, th)
			putPixBuf(token)
			out = small
			token = smallTok
		}
		chunk := &Decoded{
			Frames:       []*image.RGBA{out},
			Delays:       []time.Duration{delay},
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

func avifError(stage string, res C.avifResult) error {
	return fmt.Errorf("assets: avif %s: %s", stage, C.GoString(C.avifResultToString(res)))
}

// peekAVIFAnimDims reads an animated AVIF's canvas size and frame count from a
// header parse only (no frame decode) so the progressive first frame can be
// downscaled to the same budget-fit dimensions the stream's appends use.
func peekAVIFAnimDims(data []byte) (width, height, frames int, ok bool) {
	if len(data) == 0 {
		return 0, 0, 0, false
	}
	var pinner runtime.Pinner
	pinner.Pin(&data[0])
	defer pinner.Unpin()
	dec := C.avifDecoderCreate()
	if dec == nil {
		return 0, 0, 0, false
	}
	defer C.avifDecoderDestroy(dec)
	if res := C.avifDecoderSetIOMemory(dec, (*C.uint8_t)(unsafe.Pointer(&data[0])), C.size_t(len(data))); res != C.AVIF_RESULT_OK {
		return 0, 0, 0, false
	}
	if res := C.avifDecoderParse(dec); res != C.AVIF_RESULT_OK {
		return 0, 0, 0, false
	}
	w, h, n := int(dec.image.width), int(dec.image.height), int(dec.imageCount)
	if w <= 0 || h <= 0 || n <= 0 {
		return 0, 0, 0, false
	}
	return w, h, n, true
}
