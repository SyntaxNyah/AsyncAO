//go:build !cgo || nocgo_webp

// Stub for builds without libwebp: the scene exporter falls back to GIF.
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
func New(w, h, quality, frameMs int, loop bool) (*Encoder, error) {
	return nil, fmt.Errorf("webpenc: built without the libwebp encoder (CGO disabled)")
}

func (e *Encoder) AddFrame(*image.RGBA) error { return fmt.Errorf("webpenc: unavailable") }
func (e *Encoder) Frames() int                { return 0 }
func (e *Encoder) Assemble() ([]byte, error)  { return nil, fmt.Errorf("webpenc: unavailable") }
func (e *Encoder) Close()                     {}
