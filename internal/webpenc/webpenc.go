//go:build cgo && !nocgo_webp

// Package webpenc encodes RGBA frames into an animated WebP through libwebpmux, compressing each as added, so
// memory stays bounded however long the scene. A no-cgo build gets the stub.
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

	"github.com/SyntaxNyah/AsyncAO/internal/helper/value"
)

// Available reports whether the encoder is compiled in; callers fall back to GIF when not.
func Available() bool { return true }

// Encoder accumulates frames into one animated WebP; drive it from one goroutine.
type Encoder struct {
	enc       *C.WebPAnimEncoder
	config    C.WebPConfig
	w, h      int
	frameMs   int
	timestamp int // cumulative END time of the frames added so far, in ms
	frames    int
	done      bool
}

// New creates an encoder for w×h frames at lossy quality 0..100 and fixed duration frameMs.
func New(w, h, quality, frameMs int, loop bool) (*Encoder, error) {
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
	loopCount := C.int(0) // 0 = loop forever
	if !loop {
		loopCount = 1 // play once
	}
	opts.anim_params.loop_count = loopCount

	enc := C.WebPAnimEncoderNew(C.int(w), C.int(h), &opts)
	if enc == nil {
		return nil, fmt.Errorf("webpenc: encoder allocation failed (%dx%d)", w, h)
	}

	e := &Encoder{enc: enc, w: w, h: h, frameMs: frameMs}
	quality = value.Clamp(quality, 0, 100)
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

// AddFrame encodes one frame of New's size; libwebp copies the pixels, so the caller may reuse the image.
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

	// libwebp copies into its own buffer during this call and doesn't keep the Go pointer.
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

// Assemble returns the finished bytes; the encoder is spent afterwards, so call Close.
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
