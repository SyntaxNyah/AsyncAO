//go:build cgo && !nocgo_webp

// Package webpenc is the animated-WebP side of the scene exporter: a streaming
// encoder that turns captured RGBA frames into a single animated WebP. It's the
// higher-quality companion to internal/gifenc — WebP is true-colour and lossy,
// so it has neither GIF's 256-colour banding nor its size, and (unlike GIF, which
// must hold every frame for the final EncodeAll) libwebp compresses each frame as
// it's added, so the encoder's memory stays bounded no matter how long the scene.
//
// Thin CGO binding over libwebpmux's WebPAnimEncoder (mux.h) + the core encoder
// (encode.h). SDL-free and off the render hot path; the capture side that feeds
// it lives in the UI layer. A no-CGO build gets the stub in webpenc_fallback.go.
package webpenc

/*
#cgo pkg-config: libwebpmux libwebp
#include <stdlib.h>
#include <webp/encode.h>
#include <webp/mux.h>
*/
import "C"

import (
	"fmt"
	"image"
	"unsafe"
)

// Available reports whether the animated-WebP encoder is compiled in (it is, in
// the CGO build). The fallback build returns false so callers can degrade to GIF.
func Available() bool { return true }

// Encoder accumulates RGBA frames into one animated WebP. Not safe for concurrent
// use — drive it from a single goroutine (the render thread, during capture).
type Encoder struct {
	enc       *C.WebPAnimEncoder
	config    C.WebPConfig
	w, h      int
	frameMs   int
	timestamp int // cumulative END time of the frames added so far, in ms
	frames    int
	done      bool
}

// New creates an encoder for w×h frames at the given lossy quality (0..100) and a
// fixed per-frame duration frameMs. The animation loops forever.
func New(w, h, quality, frameMs int) (*Encoder, error) {
	if w <= 0 || h <= 0 {
		return nil, fmt.Errorf("webpenc: bad size %dx%d", w, h)
	}
	if frameMs <= 0 {
		frameMs = 1
	}
	var opts C.WebPAnimEncoderOptions
	if C.WebPAnimEncoderOptionsInit(&opts) == 0 {
		return nil, fmt.Errorf("webpenc: anim options init failed (libwebpmux ABI mismatch)")
	}
	opts.anim_params.loop_count = 0 // 0 = loop forever

	enc := C.WebPAnimEncoderNew(C.int(w), C.int(h), &opts)
	if enc == nil {
		return nil, fmt.Errorf("webpenc: encoder allocation failed (%dx%d)", w, h)
	}

	e := &Encoder{enc: enc, w: w, h: h, frameMs: frameMs}
	if quality < 0 {
		quality = 0
	}
	if quality > 100 {
		quality = 100
	}
	if C.WebPConfigPreset(&e.config, C.WEBP_PRESET_DEFAULT, C.float(quality)) == 0 {
		C.WebPAnimEncoderDelete(enc)
		return nil, fmt.Errorf("webpenc: config preset failed (encode ABI mismatch)")
	}
	if C.WebPValidateConfig(&e.config) == 0 {
		C.WebPAnimEncoderDelete(enc)
		return nil, fmt.Errorf("webpenc: invalid encode config")
	}
	return e, nil
}

// AddFrame imports one RGBA frame (its size must match New's) and encodes it at
// the next timestamp. libwebp copies + compresses the pixels synchronously, so
// the caller may reuse/free the image right after.
func (e *Encoder) AddFrame(img *image.RGBA) error {
	if e.done {
		return fmt.Errorf("webpenc: encoder already assembled")
	}
	if img.Rect.Dx() != e.w || img.Rect.Dy() != e.h {
		return fmt.Errorf("webpenc: frame %dx%d != encoder %dx%d", img.Rect.Dx(), img.Rect.Dy(), e.w, e.h)
	}
	if len(img.Pix) == 0 {
		return fmt.Errorf("webpenc: empty frame")
	}

	var pic C.WebPPicture
	if C.WebPPictureInit(&pic) == 0 {
		return fmt.Errorf("webpenc: picture init failed")
	}
	pic.width = C.int(e.w)
	pic.height = C.int(e.h)
	pic.use_argb = 1 // the anim encoder requires ARGB frames

	// ImportRGBA copies into libwebp's own buffer during this call, so passing the
	// Go slice pointer is safe (C doesn't retain it past the call).
	if C.WebPPictureImportRGBA(&pic, (*C.uint8_t)(unsafe.Pointer(&img.Pix[0])), C.int(img.Stride)) == 0 {
		C.WebPPictureFree(&pic)
		return fmt.Errorf("webpenc: RGBA import failed")
	}
	ok := C.WebPAnimEncoderAdd(e.enc, &pic, C.int(e.timestamp), &e.config)
	C.WebPPictureFree(&pic)
	if ok == 0 {
		return fmt.Errorf("webpenc: frame add failed: %s", C.GoString(C.WebPAnimEncoderGetError(e.enc)))
	}
	e.timestamp += e.frameMs
	e.frames++
	return nil
}

// Frames reports how many frames have been added.
func (e *Encoder) Frames() int { return e.frames }

// Assemble flushes the encoder and returns the finished animated-WebP bytes. The
// encoder is spent afterwards; call Close to release it.
func (e *Encoder) Assemble() ([]byte, error) {
	if e.done {
		return nil, fmt.Errorf("webpenc: already assembled")
	}
	if e.frames == 0 {
		return nil, fmt.Errorf("webpenc: no frames to assemble")
	}
	e.done = true
	// A final NULL frame at the end timestamp marks the duration of the last frame.
	C.WebPAnimEncoderAdd(e.enc, nil, C.int(e.timestamp), nil)

	var data C.WebPData
	C.WebPDataInit(&data)
	if C.WebPAnimEncoderAssemble(e.enc, &data) == 0 {
		return nil, fmt.Errorf("webpenc: assemble failed: %s", C.GoString(C.WebPAnimEncoderGetError(e.enc)))
	}
	out := C.GoBytes(unsafe.Pointer(data.bytes), C.int(data.size))
	C.WebPDataClear(&data)
	return out, nil
}

// Close frees the native encoder. Safe to call more than once.
func (e *Encoder) Close() {
	if e.enc != nil {
		C.WebPAnimEncoderDelete(e.enc)
		e.enc = nil
	}
	e.done = true
}
