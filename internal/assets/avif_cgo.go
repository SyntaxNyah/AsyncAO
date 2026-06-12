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
func decodeAVIF(data []byte, playAnimations bool) (*Decoded, error) {
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
	dec.maxThreads = 1 // the decode pool already provides parallelism

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
	frameCount := boundedFrameCount(width, height, frameTotal)
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

	for i := 0; i < frameCount; i++ {
		if res := C.avifDecoderNextImage(dec); res != C.AVIF_RESULT_OK {
			if i == 0 {
				d.Release()
				return nil, avifError("first frame", res)
			}
			break // truncated sequence: keep what decoded
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
		d.Frames = append(d.Frames, rgba)
		if token != nil {
			d.pooledPix = append(d.pooledPix, token)
		}

		// imageTiming.duration is this frame's display time in seconds.
		delay := time.Duration(float64(dec.imageTiming.duration) * float64(time.Second))
		if delay <= 0 {
			delay = defaultZeroFrameDelay
		}
		d.Delays = append(d.Delays, delay)
	}

	if len(d.Frames) == 0 {
		return nil, fmt.Errorf("assets: avif yielded no frames")
	}
	return d, nil
}

func avifError(stage string, res C.avifResult) error {
	return fmt.Errorf("assets: avif %s: %s", stage, C.GoString(C.avifResultToString(res)))
}
