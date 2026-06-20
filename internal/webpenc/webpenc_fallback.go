//go:build !cgo || nocgo_webp

// Stub for builds without the libwebp CGO encoder: the scene exporter calls
// Available() and falls back to GIF when animated WebP isn't compiled in.
package webpenc

import (
	"fmt"
	"image"
)

// Available reports the encoder is absent in this build.
func Available() bool { return false }

// Encoder is the no-op stand-in; New always errors.
type Encoder struct{}

// New always fails in the fallback build.
func New(w, h, quality, frameMs int) (*Encoder, error) {
	return nil, fmt.Errorf("webpenc: built without the libwebp encoder (CGO disabled)")
}

func (e *Encoder) AddFrame(*image.RGBA) error { return fmt.Errorf("webpenc: unavailable") }
func (e *Encoder) Frames() int                { return 0 }
func (e *Encoder) Assemble() ([]byte, error)  { return nil, fmt.Errorf("webpenc: unavailable") }
func (e *Encoder) Close()                     {}
